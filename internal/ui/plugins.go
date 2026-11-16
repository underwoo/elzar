package ui

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/underwoo/elzar/internal/plugin"
	"github.com/underwoo/elzar/internal/registry"
)

type pluginInvocation struct {
	manifest   plugin.Manifest
	name       string
	args       []string
	returnMode mode
}

type pluginResult struct {
	name, output string
	err          error
}

func (a *App) loadPlugins() {
	plugins, errs := plugin.Discover(a.options.Plugins)
	for _, err := range errs {
		a.addMessage("Plugin load failed: "+err.Error(), messageError)
	}
	a.installPlugins(plugins)
}

func (a *App) installPlugins(plugins []plugin.Plugin) {
	a.plugins = nil
	for _, p := range plugins {
		if a.pluginKeyReserved(p.Key) {
			a.addMessage(fmt.Sprintf("Plugin %s disabled: key %q conflicts with a built-in binding", p.Name, p.Key), messageError)
			continue
		}
		conflict := false
		for _, other := range a.plugins {
			if other.Key != p.Key {
				continue
			}
			for _, scope := range p.Scopes {
				if slices.Contains(other.Scopes, scope) {
					conflict = true
				}
			}
		}
		if conflict {
			a.addMessage(fmt.Sprintf("Plugin %s disabled: key %q conflicts with another plugin", p.Name, p.Key), messageError)
			continue
		}
		a.plugins = append(a.plugins, p)
	}
}

func (a *App) pluginKeyReserved(value string) bool {
	if value == a.options.TabPrefix || value == "M" {
		return true
	}
	for _, binding := range allAppBindings() {
		if slices.Contains(binding.Keys(), value) {
			return true
		}
	}
	return false
}

func allAppBindings() []key.Binding {
	return []key.Binding{
		appKeys.quit, appKeys.help, appKeys.filter, appKeys.sort, appKeys.columns,
		appKeys.cancel, appKeys.hold, appKeys.requeue, appKeys.edit, appKeys.toggle,
		appKeys.moveUp, appKeys.moveDown, appKeys.save, appKeys.tab, appKeys.shiftTab,
		appKeys.up, appKeys.down, appKeys.pageUp, appKeys.pageDown, appKeys.scroll,
		appKeys.refresh, appKeys.scope, appKeys.enter, appKeys.back, appKeys.allFields,
		appKeys.top, appKeys.bottom, appKeys.count,
	}
}

func (a *App) pluginScope() string {
	if a.mode != modeList && a.mode != modeDetail {
		return ""
	}
	if a.tab == registry.AccountsTab {
		return "accounts"
	}
	if a.mode == modeDetail {
		return "detail"
	}
	if a.tab == registry.RecentTab {
		return "recent"
	}
	return "queue"
}

func (a *App) pluginFields() (plugin.Fields, error) {
	if a.tab == registry.AccountsTab {
		if a.mode == modeDetail {
			return accountPluginFields(a.accounts.detailRow), nil
		}
		if a.accounts.cursor < 0 || a.accounts.cursor >= len(a.accounts.visible) {
			return nil, fmt.Errorf("no account selected")
		}
		return accountPluginFields(a.accounts.visible[a.accounts.cursor]), nil
	}
	if a.mode == modeDetail {
		if a.detail == nil {
			return nil, fmt.Errorf("job detail is not available yet")
		}
		fields := jobPluginFields(a.detail.Job)
		fields["Comment"] = a.detail.Comment
		return fields, nil
	}
	_, jobs, cursor, _, _ := a.activeData()
	if cursor < 0 || cursor >= len(jobs) {
		return nil, fmt.Errorf("no job selected")
	}
	return jobPluginFields(jobs[cursor]), nil
}

func (a *App) handlePluginKey(msg tea.KeyMsg) tea.Cmd {
	scope := a.pluginScope()
	for _, p := range a.plugins {
		if p.Key != msg.String() || !slices.Contains(p.Scopes, scope) {
			continue
		}
		fields, err := a.pluginFields()
		if err != nil {
			a.setActionStatus("plugin " + p.Name + " failed: " + err.Error())
			return nil
		}
		name, args, err := plugin.Render(p.Manifest, fields)
		if err != nil {
			if strings.Contains(err.Error(), "map has no entry for key") {
				err = fmt.Errorf("field not available here: %w", err)
			}
			a.setActionStatus("plugin " + p.Name + " failed: " + err.Error())
			return nil
		}
		invocation := pluginInvocation{manifest: p.Manifest, name: name, args: args, returnMode: a.mode}
		if p.Confirm {
			a.pendingPlugin = &invocation
			a.editConfirm = false
			a.mode = modeConfirm
			return nil
		}
		return a.runPlugin(invocation)
	}
	return nil
}

func (a *App) confirmPlugin(msg tea.KeyMsg) tea.Cmd {
	invocation := *a.pendingPlugin
	a.pendingPlugin = nil
	a.mode = invocation.returnMode
	if msg.String() != "y" {
		a.setActionStatus("plugin " + invocation.manifest.Name + " aborted")
		return nil
	}
	return a.runPlugin(invocation)
}

func (a *App) renderPluginConfirm(out *strings.Builder) {
	invocation := a.pendingPlugin
	fmt.Fprintf(out, "%s\n\n", titleStyle.Render("Run plugin "+invocation.manifest.Name+"?"))
	argv := append([]string{invocation.name}, invocation.args...)
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = strconv.Quote(arg)
	}
	fmt.Fprintln(out, strings.Join(quoted, " "))
	fmt.Fprintf(out, "\n%s\n", mutedStyle.Render("Press y to confirm; any other key aborts."))
}

func (a *App) runPlugin(invocation pluginInvocation) tea.Cmd {
	name := invocation.manifest.Name
	if invocation.manifest.Mode == "foreground" {
		// Bubble Tea suspends its terminal handling while ExecProcess runs.
		executable, err := plugin.CheckExecutable(invocation.name)
		if err != nil {
			a.setActionStatus("plugin " + name + " failed: " + err.Error())
			return nil
		}
		return tea.ExecProcess(exec.Command(executable, invocation.args...), func(err error) tea.Msg {
			return pluginResult{name: name, err: err}
		})
	}
	runner := a.runner
	timeout := a.options.Plugins.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return func() tea.Msg {
		// Re-check inside the async command, after any confirmation delay.
		executable, err := plugin.CheckExecutable(invocation.name)
		if err != nil {
			return pluginResult{name: name, err: err}
		}
		if runner == nil {
			return pluginResult{name: name, err: fmt.Errorf("command runner is unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		output, err := runner.Run(ctx, executable, invocation.args...)
		return pluginResult{name: name, output: string(output), err: err}
	}
}

func (a *App) acceptPluginResult(result pluginResult) {
	if result.err != nil {
		a.setActionStatus("plugin " + result.name + " failed: " + result.err.Error())
		return
	}
	output := strings.TrimSpace(result.output)
	if output == "" {
		a.setActionStatus("plugin " + result.name + " completed")
		return
	}
	a.actionStatus = strings.SplitN(output, "\n", 2)[0]
	a.addMessage("plugin "+result.name+": "+output, messageInfo)
}

func (a *App) helpGroups() []helpGroup {
	groups := a.builtinHelpGroups()
	var bindings []key.Binding
	scope := a.pluginScope()
	for _, p := range a.plugins {
		if slices.Contains(p.Scopes, scope) {
			bindings = append(bindings, key.NewBinding(key.WithKeys(p.Key), key.WithHelp(p.Key, p.Description)))
		}
	}
	if len(bindings) > 0 {
		groups = append(groups, helpGroup{title: "Plugins", bindings: bindings})
	}
	return groups
}
