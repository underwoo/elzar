package ui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"github.com/underwoo/elzar/internal/model"
	"github.com/underwoo/elzar/internal/plugin"
	"github.com/underwoo/elzar/internal/registry"
)

type pluginTestRunner struct {
	calls  []actionCall
	output string
	err    error
}

func (r *pluginTestRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, actionCall{name: name, args: append([]string(nil), args...)})
	return []byte(r.output), r.err
}
func commandPlugin() plugin.Plugin {
	return plugin.Plugin{Manifest: plugin.Manifest{Name: "inspect", Description: "Inspect selected item", Type: "command", Key: "z", Scopes: []string{"queue", "recent", "accounts", "detail"}, Command: "echo", Args: []string{"{{.JobID}}", "{{.Name}}"}, Mode: "background"}}
}
func pluginKey(app *App, value string) tea.Cmd {
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)})
	return cmd
}
func TestPluginExactArgvAndScopes(t *testing.T) {
	for _, tab := range []registry.Tab{registry.QueueTab, registry.RecentTab, registry.AccountsTab} {
		t.Run(string(tab), func(t *testing.T) {
			runner := &pluginTestRunner{output: "first line\nfull output"}
			app := NewWithOptions(runner, Options{})
			app.tab = tab
			job := model.Job{ID: 123, Name: "a b'c"}
			app.queueJobs = []model.Job{job}
			app.recentJobs = []model.Job{job}
			p := commandPlugin()
			want := []string{"123", "a b'c"}
			if tab == registry.AccountsTab {
				p.Args = []string{"{{.Cluster}}", "{{.Account}}", "{{.Parent}}"}
				app.accounts.visible = []model.AccountRow{{Share: model.Share{Cluster: "cluster", Account: "account name", Parent: "parent"}}}
				want = []string{"cluster", "account name", "parent"}
			}
			app.installPlugins([]plugin.Plugin{p})
			cmd := pluginKey(app, "z")
			if cmd == nil || len(runner.calls) != 0 {
				t.Fatal("plugin did not defer execution")
			}
			app.Update(cmd())
			executable, err := plugin.CheckExecutable("echo")
			if err != nil {
				t.Fatal(err)
			}
			if len(runner.calls) != 1 || runner.calls[0].name != executable || !reflect.DeepEqual(runner.calls[0].args, want) {
				t.Fatalf("calls=%+v want=%v", runner.calls, want)
			}
			if app.actionStatus != "first line" || !strings.Contains(app.messageLog[len(app.messageLog)-1].Text, "full output") {
				t.Fatalf("status=%q log=%+v", app.actionStatus, app.messageLog)
			}
		})
	}
	app := NewWithOptions(nil, Options{})
	p := commandPlugin()
	p.Scopes = []string{"queue"}
	app.installPlugins([]plugin.Plugin{p})
	app.tab = registry.AccountsTab
	if cmd := pluginKey(app, "z"); cmd != nil || app.actionStatus != "" {
		t.Fatal("queue-only plugin ran in Accounts")
	}
}

func TestPluginCollisions(t *testing.T) {
	for _, value := range []string{"q", "1", "0", "g", "M", "ctrl+t", "ctrl+y", "w"} {
		app := NewWithOptions(nil, Options{TabPrefix: "ctrl+y"})
		p := commandPlugin()
		p.Key = value
		// ctrl+t is reserved only when it is the actual configured prefix.
		if value == "ctrl+t" {
			app.options.TabPrefix = "ctrl+t"
		}
		app.installPlugins([]plugin.Plugin{p})
		if len(app.plugins) != 0 || len(app.messageLog) != 1 {
			t.Fatalf("key %s not rejected", value)
		}
	}
	app := NewWithOptions(nil, Options{})
	p := commandPlugin()
	other := p
	other.Name = "duplicate"
	app.installPlugins([]plugin.Plugin{p, other})
	if len(app.plugins) != 1 || len(app.messageLog) != 1 {
		t.Fatal("overlapping keys not rejected")
	}
	other.Scopes = []string{"recent"}
	p.Scopes = []string{"queue"}
	app.installPlugins([]plugin.Plugin{p, other})
	if len(app.plugins) != 2 {
		t.Fatal("disjoint scopes rejected")
	}
}

func TestPluginConfirmAbortAndRecheck(t *testing.T) {
	runner := &pluginTestRunner{}
	app := NewWithOptions(runner, Options{})
	app.queueJobs = []model.Job{{ID: 7, Name: "quoted name"}}
	p := commandPlugin()
	p.Confirm = true
	app.installPlugins([]plugin.Plugin{p})
	if cmd := pluginKey(app, "z"); cmd != nil || app.mode != modeConfirm {
		t.Fatal("confirmation missing")
	}
	if !strings.Contains(app.View(), "\"quoted name\"") {
		t.Fatalf("argv missing: %s", app.View())
	}
	pluginKey(app, "n")
	if len(runner.calls) != 0 || app.pendingPlugin != nil || app.mode != modeList {
		t.Fatal("abort executed plugin")
	}
	// Permissions can change while the confirmation is visible.
	path := filepath.Join(t.TempDir(), "command")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	p.Command = path
	app.installPlugins([]plugin.Plugin{p})
	pluginKey(app, "z")
	if err := os.Chmod(path, 0o702); err != nil {
		t.Fatal(err)
	}
	cmd := pluginKey(app, "y")
	if cmd == nil {
		t.Fatal("confirmation did not return command")
	}
	app.Update(cmd())
	if len(runner.calls) != 0 || !strings.Contains(app.actionStatus, "writable") {
		t.Fatalf("permission check bypassed: %+v %s", runner.calls, app.actionStatus)
	}
}

func TestPluginMissingFieldsAndFailure(t *testing.T) {
	runner := &pluginTestRunner{err: errors.New("exit status 1: stderr message")}
	app := NewWithOptions(runner, Options{})
	app.queueJobs = []model.Job{{ID: 9}}
	p := commandPlugin()
	p.Args = []string{"{{.StdOut}}"}
	app.installPlugins([]plugin.Plugin{p})
	if cmd := pluginKey(app, "z"); cmd != nil || !strings.Contains(app.actionStatus, "field not available here") {
		t.Fatalf("missing field status=%s", app.actionStatus)
	}
	p.Args = []string{"{{.JobID}}"}
	app.installPlugins([]plugin.Plugin{p})
	app.Update(pluginKey(app, "z")())
	if !strings.Contains(app.actionStatus, "stderr message") || app.messageLog[len(app.messageLog)-1].Level != messageError || app.mode != modeList {
		t.Fatal("failure not isolated and logged")
	}
}

func TestPluginDetailFieldsAndModalGating(t *testing.T) {
	app := NewWithOptions(nil, Options{})
	app.queueJobs = []model.Job{{ID: 1, Name: "list name"}}
	app.mode = modeDetail
	app.detail = &model.JobDetail{Job: model.Job{ID: 1, Name: "detail name"}, Comment: "detail comment"}
	fields, err := app.pluginFields()
	if err != nil || fields["Name"] != "detail name" || fields["Comment"] != "detail comment" {
		t.Fatalf("fields=%v err=%v", fields, err)
	}
	p := commandPlugin()
	app.installPlugins([]plugin.Plugin{p})
	for _, mode := range []mode{modeFilter, modePicker, modeForm} {
		app.mode = mode
		if cmd := app.handlePluginKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}}); cmd != nil {
			t.Fatalf("plugin ran in mode %v", mode)
		}
	}
	app.mode = modeList
	app.showHelp = true
	if cmd := pluginKey(app, "z"); cmd != nil {
		t.Fatal("plugin leaked through help")
	}
}

func TestPluginStartupDiscovery(t *testing.T) {
	options := plugin.DefaultOptions()
	options.SiteDir = t.TempDir()
	options.UserDir = t.TempDir()
	t.Setenv("PAGER", "echo")
	app := NewWithOptions(nil, Options{Plugins: options})
	if len(app.plugins) != 2 {
		t.Fatalf("plugins=%+v log=%+v", app.plugins, app.messageLog)
	}
	options.Enabled = false
	app = NewWithOptions(nil, Options{Plugins: options})
	if len(app.plugins) != 0 {
		t.Fatal("disabled plugins loaded")
	}
}

func TestPluginHelpGolden(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	app := NewWithOptions(nil, Options{})
	app.installPlugins([]plugin.Plugin{commandPlugin()})
	panel, _, _ := app.renderHelpOverlay(80, 50)
	if !strings.Contains(panel, "Plugins") {
		t.Fatal("plugin help missing")
	}
	teatest.RequireEqualOutput(t, []byte(panel))
}

func TestForegroundPluginReturnsToUI(t *testing.T) {
	app := NewWithOptions(nil, Options{})
	app.queueJobs = []model.Job{{ID: 1, Name: "foreground"}}
	p := commandPlugin()
	p.Mode = "foreground"
	app.installPlugins([]plugin.Plugin{p})
	tm := teatest.NewTestModel(t, app, teatest.WithInitialTermSize(100, 20))
	t.Cleanup(func() { _ = tm.Quit() })
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	teatest.WaitFor(t, tm.Output(), func(output []byte) bool {
		return bytes.Contains(output, []byte("plugin inspect completed"))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	tm.Type("q")
	final := tm.FinalModel(t, teatest.WithFinalTimeout(time.Second)).(*App)
	if final.mode != modeList || final.actionStatus != "plugin inspect completed" {
		t.Fatalf("foreground did not restore UI: %s", final.actionStatus)
	}
}

func TestFirstNode(t *testing.T) {
	for source, want := range map[string]string{"node01": "node01", "node[001-020,025],other": "node001", "rack[1-3]n[02-09]": "rack1n02", "node1,node2": "node1", "": "", "node[": ""} {
		if got := firstNode(source); got != want {
			t.Errorf("%s => %s want %s", source, got, want)
		}
	}
}
