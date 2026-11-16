package ui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/underwoo/elzar/internal/model"
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

type execPlugin struct {
	plugin      plugin.Plugin
	session     *plugin.Session
	description plugin.Description
	columns     map[registry.Tab]*pluginTabState
	detail      pluginDetailState
	mu          sync.Mutex
	breakerErrs []error
}

func (e *execPlugin) recordBreakerError(err error) {
	e.mu.Lock()
	e.breakerErrs = append(e.breakerErrs, err)
	e.mu.Unlock()
}

func (e *execPlugin) takeBreakerErrors() []error {
	e.mu.Lock()
	defer e.mu.Unlock()
	errs := e.breakerErrs
	e.breakerErrs = nil
	return errs
}

func (e *execPlugin) hasColumnsForTab(tab registry.Tab) bool {
	for _, col := range e.description.Columns {
		if slices.Contains(col.Tabs, string(tab)) {
			return true
		}
	}
	return false
}

type pluginTabState struct {
	request uint64
	loading bool
	loaded  bool
	err     error
	values  plugin.ColumnValues
}

type pluginDetailState struct {
	request  uint64
	loading  bool
	loaded   bool
	err      error
	sections plugin.DetailSections
}

type pluginColumnsResult struct {
	plugin  string
	tab     registry.Tab
	request uint64
	values  plugin.ColumnValues
	err     error
	breaker []error
}

type pluginDetailResult struct {
	plugin   string
	request  uint64
	sections plugin.DetailSections
	err      error
	breaker  []error
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
	a.execPlugins = nil
	for _, p := range plugins {
		if p.Type == "exec" {
			ep := &execPlugin{
				plugin: p,
				columns: map[registry.Tab]*pluginTabState{
					registry.QueueTab:  {},
					registry.RecentTab: {},
				},
			}
			session, err := plugin.LoadExec(context.Background(), p, a.options.PluginExecutor, a.options.Plugins, ep.recordBreakerError)
			if err != nil {
				a.addMessage("Plugin load failed: "+err.Error(), messageError)
				continue
			}
			ep.session = session
			ep.description = session.Description()
			a.execPlugins = append(a.execPlugins, ep)
			continue
		}
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
	a.pluginTabColumns = map[registry.Tab][]registry.Column{
		registry.QueueTab:  a.buildPluginColumnsFor(registry.QueueTab),
		registry.RecentTab: a.buildPluginColumnsFor(registry.RecentTab),
	}
	a.pluginSections = a.buildPluginDetailSections()
	a.refreshColumnWarnings()
}

func (a *App) findExecPlugin(name string) *execPlugin {
	for _, ep := range a.execPlugins {
		if ep.plugin.Name == name {
			return ep
		}
	}
	return nil
}

func (a *App) pluginColumnsFor(tab registry.Tab) []registry.Column {
	return a.pluginTabColumns[tab]
}

func (a *App) buildPluginColumnsFor(tab registry.Tab) []registry.Column {
	if len(a.execPlugins) == 0 {
		return nil
	}
	var columns []registry.Column
	for _, ep := range a.execPlugins {
		state := ep.columns[tab]
		for _, col := range ep.description.Columns {
			tabs := make([]registry.Tab, 0, len(col.Tabs))
			for _, t := range col.Tabs {
				tabs = append(tabs, registry.Tab(t))
			}
			if !slices.Contains(tabs, tab) {
				continue
			}
			ep := ep
			state := state
			colID := col.ID
			columns = append(columns, registry.NewPluginColumn(ep.plugin.Name, colID, col.Header, col.Width, tabs, func(job model.Job) string {
				return ep.cellValue(state, colID, job.DisplayID())
			}))
		}
	}
	return columns
}

func (e *execPlugin) cellValue(state *pluginTabState, colID, jobID string) string {
	if state == nil {
		return "…"
	}
	if byCol, ok := state.values[jobID]; ok {
		if val, ok := byCol[colID]; ok {
			return val
		}
	}
	if state.loading || (!state.loaded && state.err == nil && !e.session.Disabled()) {
		return "…"
	}
	return "—"
}

func (a *App) startPluginColumnsFetch(tab registry.Tab, jobs []model.Job) tea.Cmd {
	if len(a.execPlugins) == 0 {
		return nil
	}
	var jobFields []plugin.Fields
	var cmds []tea.Cmd
	for _, ep := range a.execPlugins {
		if !ep.hasColumnsForTab(tab) {
			continue
		}
		if jobFields == nil {
			jobFields = make([]plugin.Fields, len(jobs))
			for i, job := range jobs {
				jobFields[i] = jobPluginFields(job)
			}
		}
		state := ep.columns[tab]
		state.request++
		state.loading = !ep.session.Disabled()
		req := state.request
		ep := ep
		fields := jobFields
		cmds = append(cmds, func() tea.Msg {
			values, err := ep.session.Columns(context.Background(), string(tab), fields)
			return pluginColumnsResult{
				plugin:  ep.plugin.Name,
				tab:     tab,
				request: req,
				values:  values,
				err:     err,
				breaker: ep.takeBreakerErrors(),
			}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (a *App) acceptPluginColumnsResult(msg pluginColumnsResult) {
	for _, err := range msg.breaker {
		a.addMessage(err.Error(), messageError)
	}
	ep := a.findExecPlugin(msg.plugin)
	if ep == nil {
		return
	}
	state := ep.columns[msg.tab]
	if state == nil || msg.request != state.request {
		return
	}
	state.loading = false
	state.err = msg.err
	if msg.err == nil {
		state.loaded = true
		state.values = msg.values
	}
	if msg.tab == registry.RecentTab {
		oldRecent := a.visibleRecent()
		sortJobs(a.recentJobs, a.columnsFor(registry.RecentTab), a.recentSort)
		a.recomputeRecentFilter()
		a.recentCursor = restoreCursor(oldRecent, a.visibleRecent(), a.recentCursor)
	} else {
		oldQueue := a.visibleQueue()
		sortJobs(a.queueJobs, a.columnsFor(registry.QueueTab), a.queueSort)
		a.recomputeQueueFilter()
		a.queueCursor = restoreCursor(oldQueue, a.visibleQueue(), a.queueCursor)
	}
	a.keepAllCursorsVisible()
}

func (a *App) pluginDetailSections() []registry.DetailSection {
	return a.pluginSections
}

func (a *App) buildPluginDetailSections() []registry.DetailSection {
	if len(a.execPlugins) == 0 {
		return nil
	}
	var sections []registry.DetailSection
	for _, ep := range a.execPlugins {
		for _, sec := range ep.description.Sections {
			ep := ep
			sec := sec
			sections = append(sections, registry.DetailSection{
				ID:    "plugin." + ep.plugin.Name + "." + sec.ID,
				Title: sec.Title,
				Render: func(_ model.JobDetail, _ time.Time, _ *time.Location) []string {
					return a.renderPluginDetailSection(ep, sec.ID)
				},
			})
		}
	}
	return sections
}

func (a *App) renderPluginDetailSection(ep *execPlugin, sectionID string) []string {
	state := ep.detail
	if state.request == a.detailRequest {
		if state.err != nil {
			return []string{errorStyle.Render(oneLinePluginError(state.err))}
		}
		if state.loaded {
			lines := state.sections[sectionID]
			if len(lines) == 0 {
				return []string{"—"}
			}
			return lines
		}
	}
	if ep.session.Disabled() {
		return []string{errorStyle.Render(oneLinePluginError(plugin.ErrDisabled))}
	}
	return []string{"loading…"}
}

func oneLinePluginError(err error) string {
	text := strings.Join(strings.Fields(err.Error()), " ")
	if strings.HasPrefix(strings.ToLower(text), "error:") {
		return text
	}
	return "error: " + text
}

func (a *App) startPluginDetailFetch(detail model.JobDetail, request uint64) tea.Cmd {
	if len(a.execPlugins) == 0 {
		return nil
	}
	var fields plugin.Fields
	var cmds []tea.Cmd
	for _, ep := range a.execPlugins {
		if len(ep.description.Sections) == 0 {
			continue
		}
		if fields == nil {
			fields = jobPluginFields(detail.Job)
			if detail.Comment != "" {
				fields["Comment"] = detail.Comment
			}
		}
		ep.detail = pluginDetailState{
			request: request,
			loading: !ep.session.Disabled(),
		}
		ep := ep
		jobFields := fields
		raw := detail.RawJSON
		cmds = append(cmds, func() tea.Msg {
			sections, err := ep.session.Detail(context.Background(), jobFields, raw)
			return pluginDetailResult{
				plugin:   ep.plugin.Name,
				request:  request,
				sections: sections,
				err:      err,
				breaker:  ep.takeBreakerErrors(),
			}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (a *App) acceptPluginDetailResult(msg pluginDetailResult) {
	for _, err := range msg.breaker {
		a.addMessage(err.Error(), messageError)
	}
	if msg.err != nil && len(msg.breaker) == 0 && !errors.Is(msg.err, context.Canceled) && !errors.Is(msg.err, plugin.ErrDisabled) {
		a.addMessage("Plugin detail failed: "+msg.err.Error(), messageError)
	}
	ep := a.findExecPlugin(msg.plugin)
	if ep == nil || msg.request != a.detailRequest {
		return
	}
	ep.detail.loading = false
	ep.detail.err = msg.err
	if msg.err == nil {
		ep.detail.loaded = true
		ep.detail.sections = msg.sections
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
