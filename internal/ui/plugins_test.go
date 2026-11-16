package ui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"github.com/underwoo/elzar/internal/config"
	"github.com/underwoo/elzar/internal/model"
	"github.com/underwoo/elzar/internal/plugin"
	"github.com/underwoo/elzar/internal/registry"
	"github.com/underwoo/elzar/internal/slurm"
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

func TestAccountDetailPluginScope(t *testing.T) {
	runner := &pluginTestRunner{}
	app := NewWithOptions(runner, Options{})
	app.tab = registry.AccountsTab
	app.mode = modeDetail
	app.accounts.detailRow = model.AccountRow{Share: model.Share{Cluster: "cluster", Account: "account", Parent: "parent"}}
	account := commandPlugin()
	account.Scopes = []string{"accounts"}
	account.Args = []string{"{{.Account}}"}
	options := plugin.DefaultOptions()
	options.UserDir = t.TempDir()
	options.SiteDir = t.TempDir()
	t.Setenv("PAGER", "echo")
	builtins, errs := plugin.Discover(options)
	if len(errs) != 0 || len(builtins) != 2 {
		t.Fatalf("builtins=%v errors=%v", builtins, errs)
	}
	app.installPlugins(append(builtins, account))
	for _, key := range []string{"o", "O"} {
		if cmd := pluginKey(app, key); cmd != nil || app.actionStatus != "" {
			t.Fatalf("job detail plugin %s dispatched in Account Detail", key)
		}
	}
	groups := app.helpGroups()
	plugins := groups[len(groups)-1]
	if plugins.title != "Plugins" || len(plugins.bindings) != 1 || plugins.bindings[0].Help().Key != "z" {
		t.Fatalf("Account Detail help=%+v", plugins)
	}
	cmd := pluginKey(app, "z")
	if cmd == nil {
		t.Fatal("account plugin unavailable in Account Detail")
	}
	app.Update(cmd())
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, []string{"account"}) {
		t.Fatalf("calls=%+v", runner.calls)
	}
}

const uiExecDescribeJSON = `{"protocol":1,"columns":[{"id":"gpu","header":"GPU","width":10,"tabs":["queue","recent"]}],"sections":[{"id":"notes","title":"Plugin Notes"}]}`

type uiFakeExecutor struct {
	mu        sync.Mutex
	calls     []string
	inputs    [][]byte
	responses []struct {
		stdout, stderr string
		err            error
	}
	run func(context.Context, []string, []byte) ([]byte, []byte, error)
}

func (f *uiFakeExecutor) Exec(ctx context.Context, argv []string, stdin []byte) ([]byte, []byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(argv, " "))
	f.inputs = append(f.inputs, append([]byte(nil), stdin...))
	run := f.run
	if run == nil {
		if len(f.responses) == 0 {
			f.mu.Unlock()
			return nil, nil, errors.New("unexpected executor call")
		}
		resp := f.responses[0]
		f.responses = f.responses[1:]
		f.mu.Unlock()
		return []byte(resp.stdout), []byte(resp.stderr), resp.err
	}
	f.mu.Unlock()
	return run(ctx, argv, stdin)
}

func uiExecPlugin() plugin.Plugin {
	return plugin.Plugin{
		Manifest: plugin.Manifest{
			Name:        "metrics",
			Description: "GPU metrics and job notes",
			Type:        "exec",
			Command:     "echo",
			Provides:    []string{"columns", "detail"},
		},
	}
}

func TestPluginColumnGolden(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	clock := func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) }
	exec := &uiFakeExecutor{
		responses: []struct {
			stdout, stderr string
			err            error
		}{
			{stdout: uiExecDescribeJSON},
			{stdout: `{"values":{"101":{"gpu":"4x H100"},"102":{"gpu":"2x A100"}}}`},
		},
	}
	app := NewWithOptions(nil, Options{
		Scope:          slurm.ScopeMe,
		Clock:          clock,
		PluginExecutor: exec,
		QueueColumns:   []string{"job_id", "name", "user", "state", "plugin.metrics.gpu"},
	})
	app.width, app.height = 96, 12
	app.installPlugins([]plugin.Plugin{uiExecPlugin()})

	jobs := []model.Job{
		{ID: 101, Name: "train-llm", UserName: "alice", States: []model.State{model.StateRunning}},
		{ID: 102, Name: "eval-vision", UserName: "bob", States: []model.State{model.StatePending}},
	}
	_, cmd := app.Update(queueResult{scope: slurm.ScopeMe, at: clock(), result: slurm.ListResult{Jobs: jobs}})
	if cmd == nil {
		t.Fatal("refresh did not start async plugin columns call")
	}
	if !strings.Contains(app.View(), "…") {
		t.Fatalf("expected loading ellipsis before plugin columns call returns:\n%s", app.View())
	}
	app.Update(cmd())
	teatest.RequireEqualOutput(t, []byte(app.View()))
}

func TestPluginDetailSectionGolden(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	clock := func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) }
	location := time.FixedZone("EST", -5*60*60)
	exec := &uiFakeExecutor{
		responses: []struct {
			stdout, stderr string
			err            error
		}{
			{stdout: uiExecDescribeJSON},
			{stdout: `{"sections":{"notes":["Efficiency: 94%","Bottleneck: none"]}}`},
		},
	}
	job := model.Job{
		ID: 101, Cluster: "es", Name: "train-llm", UserName: "alice", Account: "ml",
		Partition: "gpu", QOS: "normal", States: []model.State{model.StateRunning},
		NodeList: "gpu-01", NodeCount: model.NoValNumber{Set: true, Number: 1},
		SubmitTime:       model.NoValNumber{Set: true, Number: 1794398000},
		StartTime:        model.NoValNumber{Set: true, Number: 1794398100},
		TimeLimit:        model.NoValNumber{Set: true, Number: 120},
		WorkingDirectory: "/scratch/alice/train", Command: "/scratch/alice/train/run.sh",
		Stdout: "/scratch/alice/train/slurm-101.out", Stderr: "/scratch/alice/train/slurm-101.err",
	}
	runner := &detailTestRunner{
		command:  "scontrol show job 101 --json",
		response: []byte(`{"jobs":[{"job_id":101,"name":"train-llm","user_name":"alice","account":"ml","partition":"gpu","qos":"normal","job_state":["RUNNING"],"nodes":"gpu-01","node_count":{"set":true,"number":1},"submit_time":{"set":true,"number":1794398000},"start_time":{"set":true,"number":1794398100},"time_limit":{"set":true,"number":120},"current_working_directory":"/scratch/alice/train","command":"/scratch/alice/train/run.sh","standard_output":"/scratch/alice/train/slurm-101.out","standard_error":"/scratch/alice/train/slurm-101.err"}]}`),
	}
	app := NewWithOptions(runner, Options{
		Scope:          slurm.ScopeMe,
		Clock:          clock,
		Location:       location,
		PluginExecutor: exec,
	})
	app.width, app.height = 100, 45
	app.installPlugins([]plugin.Plugin{uiExecPlugin()})
	app.queueJobs = []model.Job{job}

	_, detailCmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if detailCmd == nil {
		t.Fatal("enter did not start detail fetch")
	}
	_, pluginCmd := app.Update(detailCmd())
	if pluginCmd == nil {
		t.Fatal("detailResult did not start async plugin detail fetch")
	}
	beforeView := app.View()
	if !strings.Contains(beforeView, "Plugin Notes\nloading…") {
		t.Fatalf("expected loading… in plugin section before completion:\n%s", beforeView)
	}
	app.Update(pluginCmd())
	afterView := app.View()
	if strings.Contains(afterView, "All fields") {
		t.Fatalf("All fields should be hidden by default:\n%s", afterView)
	}
	teatest.RequireEqualOutput(t, []byte(afterView))

	// Verify plugin sections appear before "All fields" when expanded.
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	expanded := app.View()
	notesIdx := strings.Index(expanded, "Plugin Notes")
	allIdx := strings.Index(expanded, "All fields")
	if notesIdx < 0 || allIdx < 0 || notesIdx > allIdx {
		t.Fatalf("plugin section did not render before All fields: notes=%d all=%d\n%s", notesIdx, allIdx, expanded)
	}
}

func TestExecPluginSlowCallDoesNotBlockRefreshOrRender(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	described := false
	exec := &uiFakeExecutor{
		run: func(_ context.Context, argv []string, _ []byte) ([]byte, []byte, error) {
			if !described && len(argv) > 1 && argv[len(argv)-1] == "describe" {
				described = true
				return []byte(uiExecDescribeJSON), nil, nil
			}
			started <- struct{}{}
			<-release
			return []byte(`{"values":{"1":{"gpu":"8x H100"}},"sections":{"notes":["ready"]}}`), nil, nil
		},
	}
	app := NewWithOptions(nil, Options{
		Scope:          slurm.ScopeMe,
		PluginExecutor: exec,
		QueueColumns:   []string{"job_id", "name", "plugin.metrics.gpu"},
	})
	app.width, app.height = 90, 12
	app.installPlugins([]plugin.Plugin{uiExecPlugin()})

	jobs := make([]model.Job, 15)
	for i := range jobs {
		jobs[i] = model.Job{ID: int64(i + 1), Name: "job"}
	}
	_, cmd := app.Update(queueResult{scope: slurm.ScopeMe, result: slurm.ListResult{Jobs: jobs}})
	if cmd == nil || app.queueLoading {
		t.Fatalf("refresh blocked or failed to return async plugin cmd: loading=%v cmd=%v", app.queueLoading, cmd)
	}
	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- cmd() }()
	<-started

	// While the slow plugin is blocked in Exec, rendering, selection, and paging work immediately.
	renderStart := time.Now()
	view := app.View()
	if elapsed := time.Since(renderStart); elapsed > 16*time.Millisecond {
		t.Fatalf("render took %v while slow plugin was in flight (budget 16ms)", elapsed)
	}
	if !strings.Contains(view, "…") {
		t.Fatalf("expected … while plugin call is in flight:\n%s", view)
	}
	app.moveCursor(2)
	if app.queueCursor != 2 {
		t.Fatalf("selection blocked by slow plugin: cursor=%d", app.queueCursor)
	}
	app.pageCursor(app.pageSize())
	if app.queueCursor <= 2 {
		t.Fatalf("paging blocked by slow plugin: cursor=%d", app.queueCursor)
	}

	close(release)
	app.Update(<-msgCh)
	app.moveCursor(-100)
	if !strings.Contains(app.View(), "8x H100") {
		t.Fatalf("completed slow plugin value missing:\n%s", app.View())
	}
}

func TestExecPluginColumnsErrorPreservesOldValuesAndBreaker(t *testing.T) {
	exec := &uiFakeExecutor{
		responses: []struct {
			stdout, stderr string
			err            error
		}{
			{stdout: uiExecDescribeJSON},
			{stderr: "first error", err: errors.New("exit status 1")},
			{stdout: `{"values":{"1":{"gpu":"4x A100"}}}`},
			{stderr: " fail 1 ", err: errors.New("exit status 1")},
			{stderr: " fail 2 ", err: errors.New("exit status 1")},
			{stderr: " fail 3 ", err: errors.New("exit status 1")},
		},
	}
	app := NewWithOptions(nil, Options{
		Scope:          slurm.ScopeMe,
		PluginExecutor: exec,
		QueueColumns:   []string{"job_id", "plugin.metrics.gpu"},
	})
	app.width, app.height = 80, 12
	app.installPlugins([]plugin.Plugin{uiExecPlugin()})
	jobs := []model.Job{{ID: 1, Name: "job-1"}}

	// 1. First refresh fails with no prior values -> cell shows "—".
	_, cmd := app.Update(queueResult{scope: slurm.ScopeMe, result: slurm.ListResult{Jobs: jobs}})
	app.Update(cmd())
	if got := app.columnsFor(registry.QueueTab)[1].Value(jobs[0], time.Time{}, time.UTC); got != "—" {
		t.Fatalf("cell after initial error = %q, want —", got)
	}

	// 2. Second refresh succeeds -> cell shows "4x A100".
	_, cmd = app.Update(queueResult{scope: slurm.ScopeMe, result: slurm.ListResult{Jobs: jobs}})
	app.Update(cmd())
	if got := app.columnsFor(registry.QueueTab)[1].Value(jobs[0], time.Time{}, time.UTC); got != "4x A100" {
		t.Fatalf("cell after success = %q, want 4x A100", got)
	}

	// 3. Failed Slurm refresh does not call plugin and preserves old values.
	callsBefore := len(exec.calls)
	_, cmd = app.Update(queueResult{scope: slurm.ScopeMe, err: errors.New("squeue down")})
	if cmd != nil || len(exec.calls) != callsBefore {
		t.Fatalf("failed refresh invoked plugin: cmd=%v calls=%d", cmd, len(exec.calls))
	}
	app.messageLog = nil

	// 4. Three consecutive plugin failures keep the old value "4x A100" and trip the breaker on the 3rd failure.
	for i := 1; i <= 3; i++ {
		_, cmd = app.Update(queueResult{scope: slurm.ScopeMe, result: slurm.ListResult{Jobs: jobs}})
		app.Update(cmd())
		if got := app.columnsFor(registry.QueueTab)[1].Value(jobs[0], time.Time{}, time.UTC); got != "4x A100" {
			t.Fatalf("failure %d overwrote prior cell value: got %q, want 4x A100", i, got)
		}
		if i < 3 && len(app.messageLog) != 0 {
			t.Fatalf("logged before breaker tripped at failure %d: %+v", i, app.messageLog)
		}
	}
	if len(app.messageLog) != 1 || !strings.Contains(app.messageLog[0].Text, "disabled after 3 consecutive failures") {
		t.Fatalf("breaker log = %+v", app.messageLog)
	}

	// 5. Subsequent refresh makes zero new process calls and logs no extra errors.
	callsAfterBreaker := len(exec.calls)
	_, cmd = app.Update(queueResult{scope: slurm.ScopeMe, result: slurm.ListResult{Jobs: jobs}})
	if cmd != nil {
		app.Update(cmd())
	}
	if len(exec.calls) != callsAfterBreaker || len(app.messageLog) != 1 {
		t.Fatalf("disabled plugin called again or logged twice: calls=%d->%d log=%+v", callsAfterBreaker, len(exec.calls), app.messageLog)
	}
}

func TestExecPluginDetailLoadingAndInlineOneLineError(t *testing.T) {
	exec := &uiFakeExecutor{
		responses: []struct {
			stdout, stderr string
			err            error
		}{
			{stdout: uiExecDescribeJSON},
			{stderr: "line one\nline two", err: errors.New("exit status 2")},
		},
	}
	app := NewWithOptions(nil, Options{Scope: slurm.ScopeMe, PluginExecutor: exec})
	app.width, app.height = 100, 45
	app.installPlugins([]plugin.Plugin{uiExecPlugin()})

	detail := model.JobDetail{Job: model.Job{ID: 42, Name: "broken"}, RawJSON: []byte(`{"job_id":42}`)}
	app.mode = modeDetail
	app.detailJob = detail.Job
	app.detailRequest = 1
	_, cmd := app.Update(detailResult{request: 1, detail: detail})
	if cmd == nil {
		t.Fatal("detailResult did not start plugin detail fetch")
	}
	if !strings.Contains(app.View(), "Plugin Notes\nloading…") {
		t.Fatalf("missing loading… state in detail view:\n%s", app.View())
	}
	app.Update(cmd())
	view := app.View()
	if !strings.Contains(view, "Plugin Notes\nerror: plugin metrics: exit status 2; stderr: line one line two") {
		t.Fatalf("expected inline one-liner error in detail view:\n%s", view)
	}
	if len(app.messageLog) != 1 || app.messageLog[0].Level != messageError || !strings.Contains(app.messageLog[0].Text, "line one") {
		t.Fatalf("detail error not logged to message log: %+v", app.messageLog)
	}
}

func TestExecPluginSortAndFilterUseCellText(t *testing.T) {
	exec := &uiFakeExecutor{
		responses: []struct {
			stdout, stderr string
			err            error
		}{
			{stdout: uiExecDescribeJSON},
			{stdout: `{"values":{"1":{"gpu":"b-mid"},"2":{"gpu":"a-low"},"3":{"gpu":"c-high"}}}`},
		},
	}
	app := NewWithOptions(nil, Options{
		Scope:          slurm.ScopeMe,
		PluginExecutor: exec,
		QueueColumns:   []string{"job_id", "name", "plugin.metrics.gpu"},
	})
	app.width, app.height = 90, 14
	app.installPlugins([]plugin.Plugin{uiExecPlugin()})
	jobs := []model.Job{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}, {ID: 3, Name: "three"}}
	_, cmd := app.Update(queueResult{scope: slurm.ScopeMe, result: slurm.ListResult{Jobs: jobs}})
	app.Update(cmd())

	// Filter by plugin cell text.
	app.applyFilter("c-high")
	if len(app.visibleQueue()) != 1 || app.visibleQueue()[0].ID != 3 {
		t.Fatalf("filter by plugin cell text = %+v, want job 3", app.visibleQueue())
	}
	app.clearFilter()

	// Sort by plugin column via the sort picker.
	app.queueCursor = 0 // job 1 ("b-mid")
	app.beginSort()
	app.picker.Move(2) // GPU
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	gotAsc := []int64{app.queueJobs[0].ID, app.queueJobs[1].ID, app.queueJobs[2].ID}
	if !reflect.DeepEqual(gotAsc, []int64{2, 1, 3}) {
		t.Fatalf("ascending sort by plugin column = %v, want [2 1 3]", gotAsc)
	}
	if app.queueJobs[app.queueCursor].ID != 1 {
		t.Fatalf("selection did not stay on job 1 after sort: got %d", app.queueJobs[app.queueCursor].ID)
	}

	// Flip sort to descending.
	app.beginSort()
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	gotDesc := []int64{app.queueJobs[0].ID, app.queueJobs[1].ID, app.queueJobs[2].ID}
	if !reflect.DeepEqual(gotDesc, []int64{3, 1, 2}) {
		t.Fatalf("descending sort by plugin column = %v, want [3 1 2]", gotDesc)
	}
}

func TestExecPluginColumnPickerRoundTripAndMissingPluginWarning(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "elzar", "config.toml")
	exec := &uiFakeExecutor{
		responses: []struct {
			stdout, stderr string
			err            error
		}{
			{stdout: uiExecDescribeJSON},
		},
	}
	app := NewWithOptions(nil, Options{ConfigPath: configPath, PluginExecutor: exec})
	app.width, app.height = 100, 20
	app.installPlugins([]plugin.Plugin{uiExecPlugin()})

	// Hidden by default.
	if hasColumn(app.columnsFor(registry.QueueTab), "plugin.metrics.gpu") {
		t.Fatal("plugin column should be hidden by default")
	}

	// Open column picker, find plugin.metrics.gpu, toggle it on, and save with 'w'.
	app.beginColumnPicker()
	foundIdx := -1
	for i, item := range app.picker.Items() {
		if strings.HasPrefix(item, "plugin.metrics.gpu  ") {
			foundIdx = i
			break
		}
	}
	if foundIdx < 0 {
		t.Fatalf("plugin.metrics.gpu missing from column picker: %v", app.picker.Items())
	}
	if app.picker.Checked()[foundIdx] {
		t.Fatal("plugin.metrics.gpu was checked by default in picker")
	}
	app.picker.cursor = foundIdx
	app.Update(tea.KeyMsg{Type: tea.KeySpace})
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})

	if !hasColumn(app.columnsFor(registry.QueueTab), "plugin.metrics.gpu") {
		t.Fatalf("saved columns missing plugin.metrics.gpu: %+v", app.columnsFor(registry.QueueTab))
	}
	settings, err := config.LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(settings.QueueColumns[len(settings.QueueColumns)-1], "plugin.metrics.gpu") {
		t.Fatalf("saved QueueColumns in config.toml = %v", settings.QueueColumns)
	}

	// Loading those saved columns when the plugin is missing shows the unknown-ID warning.
	missingApp := NewWithOptions(nil, Options{ConfigPath: configPath, QueueColumns: settings.QueueColumns})
	missingApp.width, missingApp.height = 100, 20
	missingApp.queueJobs = []model.Job{{ID: 1}}
	if !strings.Contains(missingApp.View(), "unknown column IDs skipped: plugin.metrics.gpu") {
		t.Fatalf("missing-plugin column warning not shown:\n%s", missingApp.View())
	}
}

func BenchmarkRender20kWithPluginColumn(b *testing.B) {
	clock := func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) }
	exec := &uiFakeExecutor{
		responses: []struct {
			stdout, stderr string
			err            error
		}{
			{stdout: uiExecDescribeJSON},
		},
	}
	app := NewWithOptions(nil, Options{
		Scope:          slurm.ScopeMe,
		Clock:          clock,
		PluginExecutor: exec,
		QueueColumns:   []string{"job_id", "name", "user", "account", "partition", "qos", "state", "elapsed_limit", "nodes", "node_reason", "plugin.metrics.gpu"},
	})
	app.width, app.height = 140, 30
	app.installPlugins([]plugin.Plugin{uiExecPlugin()})
	app.queueJobs = make([]model.Job, 20_000)
	values := make(plugin.ColumnValues, 20_000)
	for i := range app.queueJobs {
		job := model.Job{
			ID: int64(i + 1), Name: "synthetic-job", UserName: "user", Account: "account",
			Partition: "compute", QOS: "normal", States: []model.State{model.StateRunning},
			NodeCount: model.NoValNumber{Set: true, Number: 1}, NodeList: "node-001",
			StartTime: model.NoValNumber{Set: true, Number: clock().Unix() - 120},
			TimeLimit: model.NoValNumber{Set: true, Number: 60},
		}
		app.queueJobs[i] = job
		values[job.DisplayID()] = map[string]string{"gpu": "4x H100"}
	}
	app.acceptPluginColumnsResult(pluginColumnsResult{
		plugin:  "metrics",
		tab:     registry.QueueTab,
		request: app.execPlugins[0].columns[registry.QueueTab].request,
		values:  values,
	})
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = app.View()
	}
}
