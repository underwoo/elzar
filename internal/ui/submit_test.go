package ui

import (
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

	"github.com/underwoo/elzar/internal/files"
	"github.com/underwoo/elzar/internal/model"
	"github.com/underwoo/elzar/internal/registry"
	"github.com/underwoo/elzar/internal/slurm"
	"github.com/underwoo/elzar/internal/submit"
)

func parseSampleForGolden() (submit.ParsedScript, error) {
	return submit.ParseScript(sampleBatchScript)
}

type submitTestRunner struct {
	calls   []string
	dirs    []string
	stdout  string
	stderr  string
	err     error
	runFunc func(dir, name string, args ...string) ([]byte, []byte, error)
}

func (r *submitTestRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	stdout, _, err := r.RunIn(ctx, "", name, args...)
	return stdout, err
}

func (r *submitTestRunner) RunIn(_ context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	cmd := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, cmd)
	r.dirs = append(r.dirs, dir)
	if r.runFunc != nil {
		return r.runFunc(dir, name, args...)
	}
	return []byte(r.stdout), []byte(r.stderr), r.err
}

const sampleBatchScript = `#!/bin/bash
#SBATCH --job-name=climate-sim
#SBATCH --account=gfdl
#SBATCH --partition=batch
#SBATCH --time=02:00:00
#SBATCH --nodes=4
#SBATCH --exclusive --constraint=rome

srun ./simulate
`

func writeBatchScript(t *testing.T, dir, name, content string) (string, []byte, time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	fixedTime := time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC)
	if err := os.Chtimes(path, fixedTime, fixedTime); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, data, info.ModTime()
}

func assertScriptUnchanged(t *testing.T, path string, wantBytes []byte, wantModTime time.Time) {
	t.Helper()
	gotBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotBytes, wantBytes) {
		t.Fatalf("script bytes changed:\ngot:  %q\nwant: %q", gotBytes, wantBytes)
	}
	if !info.ModTime().Equal(wantModTime) {
		t.Fatalf("script mtime changed: got %v, want %v", info.ModTime(), wantModTime)
	}
}

func TestSubmitFormExactArgvUneditedAndEditedAndReset(t *testing.T) {
	dir := t.TempDir()
	scriptPath, origBytes, origMtime := writeBatchScript(t, dir, "job.sh", sampleBatchScript)

	envMap := map[string]string{
		"SBATCH_QOS": "high",
	}
	env := func(k string) (string, bool) {
		v, ok := envMap[k]
		return v, ok
	}

	t.Run("unedited form passes only --parsable and script path", func(t *testing.T) {
		runner := &submitTestRunner{stdout: "65541\n"}
		app := NewWithOptions(runner, Options{Env: env})
		app.width, app.height = 120, 32

		if err := app.openSubmitForm(scriptPath); err != nil {
			t.Fatal(err)
		}
		if app.mode != modeForm || !app.submitForm.active {
			t.Fatalf("mode=%v active=%v", app.mode, app.submitForm.active)
		}

		// Jump to submit button and press Enter to open confirmation.
		app.focusSubmitField(submitActionButtonRow)
		app.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if app.mode != modeConfirm || !app.submitForm.confirming {
			t.Fatalf("expected confirm mode, got mode=%v confirming=%v", app.mode, app.submitForm.confirming)
		}
		wantCmd := "sbatch --parsable " + scriptPath
		if !strings.Contains(app.View(), wantCmd) {
			t.Fatalf("confirm dialog missing %q:\n%s", wantCmd, app.View())
		}

		// Confirm with 'y'.
		_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		if cmd == nil {
			t.Fatal("confirm did not return submit command")
		}
		app.Update(cmd())

		if len(runner.calls) != 1 || runner.calls[0] != wantCmd {
			t.Fatalf("runner calls = %v, want [%s]", runner.calls, wantCmd)
		}
		if len(runner.dirs) != 1 || runner.dirs[0] != dir {
			t.Fatalf("runner dirs = %v, want [%s]", runner.dirs, dir)
		}
		assertScriptUnchanged(t, scriptPath, origBytes, origMtime)
	})

	t.Run("edited fields ctrl+r reset and empty-edit ClearRequested hint", func(t *testing.T) {
		runner := &submitTestRunner{stdout: "65542\n"}
		app := NewWithOptions(runner, Options{Env: env})
		app.width, app.height = 120, 32

		if err := app.openSubmitForm(scriptPath); err != nil {
			t.Fatal(err)
		}

		// Field 0 is Job name (script:2 -> "climate-sim"). Edit it, then press ctrl+r to reset.
		app.focusSubmitField(0)
		app.submitForm.inputs[0].SetValue("edited-name")
		app.submitForm.edited[app.submitForm.specs[0].ID] = true
		app.recomputeSubmitPlan()
		if !strings.Contains(app.View(), "--job-name=edited-name") {
			t.Fatalf("expected --job-name=edited-name in live argv preview:\n%s", app.View())
		}
		app.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
		if app.submitForm.inputs[0].Value() != "climate-sim" || strings.Contains(app.View(), "--job-name=") {
			t.Fatalf("ctrl+r did not reset Job name to script value:\n%s", app.View())
		}

		// Field 1 is Account (script:3 -> "gfdl"). Edit it to "override-acct".
		app.focusSubmitField(1)
		app.submitForm.inputs[1].SetValue("override-acct")
		app.submitForm.edited[app.submitForm.specs[1].ID] = true

		// Field 4 is Time (script:5 -> "02:00:00"). Clear it to "" via Backspace keys -> ClearRequested notice, effective value stays "02:00:00", no --time in argv.
		app.focusSubmitField(4)
		for range len("02:00:00") {
			app.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		}

		view := app.View()
		if !strings.Contains(view, "can't unset from the form; edit the script") {
			t.Fatalf("missing ClearRequested hint in form view:\n%s", view)
		}
		if app.submitForm.plan.Fields[4].Value != "02:00:00" || app.submitForm.plan.Fields[4].Source.Label() != "script:5" {
			t.Fatalf("cleared field plan = %+v, want Value='02:00:00' Source='script:5'", app.submitForm.plan.Fields[4])
		}

		// Submit and verify only --account=override-acct is passed.
		app.focusSubmitField(submitActionButtonRow)
		app.Update(tea.KeyMsg{Type: tea.KeyEnter})
		_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		app.Update(cmd())

		wantCmd := "sbatch --parsable --account=override-acct " + scriptPath
		if len(runner.calls) != 1 || runner.calls[0] != wantCmd {
			t.Fatalf("runner calls = %v, want [%s]", runner.calls, wantCmd)
		}
		assertScriptUnchanged(t, scriptPath, origBytes, origMtime)
	})
}

func TestSubmitFormDryRunConfirmAbortSuccessJumpAndErrorRetention(t *testing.T) {
	dir := t.TempDir()
	scriptPath, origBytes, origMtime := writeBatchScript(t, dir, "job.sh", sampleBatchScript)

	t.Run("dry run displays --test-only estimate inline and leaves script untouched", func(t *testing.T) {
		estimate := "sbatch: Job 9001 to start at 2026-11-23T04:00:00 using 128 processors on nodes nid00[10-13] in partition batch"
		runner := &submitTestRunner{stderr: estimate + "\n"}
		app := NewWithOptions(runner, Options{})
		app.width, app.height = 120, 34

		if err := app.openSubmitForm(scriptPath); err != nil {
			t.Fatal(err)
		}
		app.focusSubmitField(submitActionButtonRow)
		_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
		if cmd == nil {
			t.Fatal("pressing 't' on action row did not start dry run")
		}
		app.Update(cmd())

		wantDryCmd := "sbatch --parsable --test-only " + scriptPath
		if len(runner.calls) != 1 || runner.calls[0] != wantDryCmd {
			t.Fatalf("dry run calls = %v, want [%s]", runner.calls, wantDryCmd)
		}
		if app.mode != modeForm || !strings.Contains(app.View(), estimate) {
			t.Fatalf("dry run estimate missing from form view:\n%s", app.View())
		}
		assertScriptUnchanged(t, scriptPath, origBytes, origMtime)
	})

	t.Run("confirm abort makes zero sbatch calls and returns to form", func(t *testing.T) {
		runner := &submitTestRunner{stdout: "9002\n"}
		app := NewWithOptions(runner, Options{})
		app.width, app.height = 120, 32

		if err := app.openSubmitForm(scriptPath); err != nil {
			t.Fatal(err)
		}
		app.focusSubmitField(submitActionButtonRow)
		app.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if app.mode != modeConfirm {
			t.Fatalf("mode = %v, want modeConfirm", app.mode)
		}
		_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		if cmd != nil || len(runner.calls) != 0 {
			t.Fatalf("abort invoked sbatch: cmd=%v calls=%v", cmd, runner.calls)
		}
		if app.mode != modeForm || !app.submitForm.active {
			t.Fatalf("abort did not return to open submit form: mode=%v active=%v", app.mode, app.submitForm.active)
		}
	})

	t.Run("submit failure keeps form open with sbatch error", func(t *testing.T) {
		sbatchErr := "sbatch: error: Batch job submission failed: Invalid qos specification"
		runner := &submitTestRunner{
			stderr: sbatchErr,
			err:    errors.New(sbatchErr),
		}
		app := NewWithOptions(runner, Options{})
		app.width, app.height = 120, 34

		if err := app.openSubmitForm(scriptPath); err != nil {
			t.Fatal(err)
		}
		app.submitForm.inputs[3].SetValue("bad-qos")
		app.submitForm.edited[app.submitForm.specs[3].ID] = true
		app.focusSubmitField(submitActionButtonRow)
		app.Update(tea.KeyMsg{Type: tea.KeyEnter})
		_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		app.Update(cmd())

		if app.mode != modeForm || !app.submitForm.active {
			t.Fatalf("failed submit closed form: mode=%v active=%v", app.mode, app.submitForm.active)
		}
		if !strings.Contains(app.View(), "Invalid qos specification") {
			t.Fatalf("form view missing sbatch error:\n%s", app.View())
		}
		if app.submitForm.inputs[3].Value() != "bad-qos" {
			t.Fatalf("form lost user edit after failure: %q", app.submitForm.inputs[3].Value())
		}
	})

	t.Run("submit success on local cluster jumps to Queue refreshes and selects job", func(t *testing.T) {
		runner := &submitTestRunner{stdout: "9005;es\n"}
		app := NewWithOptions(runner, Options{})
		app.width, app.height = 120, 32
		app.tab = filesTab
		app.queueJobs = []model.Job{
			{ID: 9001, Cluster: "es", Name: "existing"},
			{ID: 9005, Cluster: "es", Name: "climate-sim"},
		}

		if err := app.openSubmitForm(scriptPath); err != nil {
			t.Fatal(err)
		}
		app.focusSubmitField(submitActionButtonRow)
		app.Update(tea.KeyMsg{Type: tea.KeyEnter})
		_, submitCmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		_, refreshCmd := app.Update(submitCmd())

		if app.mode != modeList || app.tab != registry.QueueTab {
			t.Fatalf("after local submit: mode=%v tab=%v, want modeList QueueTab", app.mode, app.tab)
		}
		if app.actionStatus != "Submitted job 9005 on es" {
			t.Fatalf("actionStatus = %q, want 'Submitted job 9005 on es'", app.actionStatus)
		}
		if len(app.messageLog) == 0 || app.messageLog[len(app.messageLog)-1].Text != "Submitted job 9005 on es" {
			t.Fatalf("messageLog = %+v", app.messageLog)
		}
		if app.queueCursor != 1 || app.visibleQueue()[app.queueCursor].ID != 9005 {
			t.Fatalf("queueCursor = %d (job %+v), want index 1 (job 9005)", app.queueCursor, app.visibleQueue()[app.queueCursor])
		}
		if refreshCmd == nil {
			t.Fatal("submit success did not trigger queue refresh")
		}

		// Deliver refreshed queue jobs and verify job 9005 on cluster es is selected.
		jobs := []model.Job{
			{ID: 9001, Cluster: "es", Name: "older"},
			{ID: 9005, Cluster: "es", Name: "climate-sim"},
			{ID: 9009, Cluster: "es", Name: "newer"},
		}
		app.Update(queueResult{scope: app.scope, result: slurm.ListResult{Jobs: jobs}})
		if app.queueCursor != 1 || app.visibleQueue()[app.queueCursor].ID != 9005 || app.visibleQueue()[app.queueCursor].Cluster != "es" {
			t.Fatalf("queueCursor = %d (job %+v), want index 1 (job 9005 on es)", app.queueCursor, app.visibleQueue()[app.queueCursor])
		}
		assertScriptUnchanged(t, scriptPath, origBytes, origMtime)
	})

	t.Run("submit to non-local cluster stays in Files and logs Queue shows local only", func(t *testing.T) {
		runner := &submitTestRunner{stdout: "9006;es\n"}
		app := NewWithOptions(runner, Options{})
		app.width, app.height = 120, 32
		app.tab = filesTab
		app.files.dir = dir
		app.queueJobs = []model.Job{
			{ID: 9001, Cluster: "c1", Name: "local-job"},
		}

		if err := app.openSubmitForm(scriptPath); err != nil {
			t.Fatal(err)
		}
		app.submitForm.inputs[14].SetValue("es")
		app.submitForm.edited[submit.FieldClusters] = true
		app.focusSubmitField(submitActionButtonRow)
		app.Update(tea.KeyMsg{Type: tea.KeyEnter})
		_, submitCmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		app.Update(submitCmd())

		wantStatus := "Submitted job 9006 on es. Queue shows c1 only (see #34)"
		if app.mode != modeList || app.tab != filesTab {
			t.Fatalf("after remote submit: mode=%v tab=%v, want modeList filesTab", app.mode, app.tab)
		}
		if app.actionStatus != wantStatus {
			t.Fatalf("actionStatus = %q, want %q", app.actionStatus, wantStatus)
		}
		if len(app.messageLog) == 0 || app.messageLog[len(app.messageLog)-1].Text != wantStatus {
			t.Fatalf("messageLog = %+v, want last message %q", app.messageLog, wantStatus)
		}
		if !strings.Contains(app.View(), wantStatus) {
			t.Fatalf("Files status bar missing %q:\n%s", wantStatus, app.View())
		}
		assertScriptUnchanged(t, scriptPath, origBytes, origMtime)
	})

	t.Run("submit when local cluster is unknown treats parsable cluster as remote and bare jobid as local", func(t *testing.T) {
		// Unknown local cluster + parsable returned a cluster -> treated as remote, stays in Files.
		remoteRunner := &submitTestRunner{stdout: "9007;es\n"}
		remoteApp := NewWithOptions(remoteRunner, Options{})
		remoteApp.width, remoteApp.height = 120, 32
		remoteApp.tab = filesTab
		remoteApp.files.dir = dir

		if err := remoteApp.openSubmitForm(scriptPath); err != nil {
			t.Fatal(err)
		}
		remoteApp.focusSubmitField(submitActionButtonRow)
		remoteApp.Update(tea.KeyMsg{Type: tea.KeyEnter})
		_, submitCmd := remoteApp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		remoteApp.Update(submitCmd())

		wantRemote := "Submitted job 9007 on es. Queue shows local only (see #34)"
		if remoteApp.mode != modeList || remoteApp.tab != filesTab {
			t.Fatalf("after unknown-local parsable-cluster submit: mode=%v tab=%v, want modeList filesTab", remoteApp.mode, remoteApp.tab)
		}
		if remoteApp.actionStatus != wantRemote {
			t.Fatalf("actionStatus = %q, want %q", remoteApp.actionStatus, wantRemote)
		}
		if len(remoteApp.messageLog) == 0 || remoteApp.messageLog[len(remoteApp.messageLog)-1].Text != wantRemote {
			t.Fatalf("messageLog = %+v, want last message %q", remoteApp.messageLog, wantRemote)
		}

		// Unknown local cluster + bare jobid -> treated as local, jumps to Queue.
		localRunner := &submitTestRunner{stdout: "9008\n"}
		localApp := NewWithOptions(localRunner, Options{})
		localApp.width, localApp.height = 120, 32
		localApp.tab = filesTab

		if err := localApp.openSubmitForm(scriptPath); err != nil {
			t.Fatal(err)
		}
		localApp.focusSubmitField(submitActionButtonRow)
		localApp.Update(tea.KeyMsg{Type: tea.KeyEnter})
		_, submitCmd = localApp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		localApp.Update(submitCmd())

		if localApp.mode != modeList || localApp.tab != registry.QueueTab {
			t.Fatalf("after unknown-local bare-jobid submit: mode=%v tab=%v, want modeList QueueTab", localApp.mode, localApp.tab)
		}
		if localApp.actionStatus != "Submitted job 9008" {
			t.Fatalf("actionStatus = %q, want 'Submitted job 9008'", localApp.actionStatus)
		}
	})
}

func TestSubmitFormGolden(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	dir := "/scratch/alice/climate"
	parsed, err := parseSampleForGolden()
	if err != nil {
		t.Fatal(err)
	}
	env := func(k string) (string, bool) {
		if k == "SBATCH_QOS" {
			return "high", true
		}
		return "", false
	}
	app := NewWithOptions(nil, Options{Env: env})
	app.width, app.height = 110, 32
	app.beginSubmitFormWithParsed(dir+"/run.sh", parsed, modeList)

	// Mark partition as edited in the form so all four source labels
	// (script:<line>, env SBATCH_QOS, form, and default) appear in the golden.
	app.submitForm.inputs[2].SetValue("gpu")
	app.submitForm.edited[app.submitForm.specs[2].ID] = true
	app.recomputeSubmitPlan()
	app.submitForm.dryRunOutput = "sbatch: Job 9001 to start at 2026-11-23T04:00:00 using 128 processors on nodes nid00[10-13] in partition gpu"

	view := app.View()
	for _, want := range []string{"[script:2]", "[script:3]", "[form]", "[env SBATCH_QOS]", "[default]", "Passed through by sbatch:", "Working directory: /scratch/alice/climate", "Command: sbatch --parsable --partition=gpu /scratch/alice/climate/run.sh"} {
		if !strings.Contains(view, want) {
			t.Fatalf("form view missing %q:\n%s", want, view)
		}
	}
	teatest.RequireEqualOutput(t, []byte(view))
}

func TestFilesTabSubmitKey(t *testing.T) {
	dir := t.TempDir()
	scriptPath, origBytes, origMtime := writeBatchScript(t, dir, "job.sh", sampleBatchScript)
	notePath := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notePath, []byte("plain text note\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &submitTestRunner{stdout: "9100\n"}
	a := filesApp(t)
	a.runner = runner
	a.files.dir = dir
	a.files.rows = []fileRow{
		{path: scriptPath, entry: files.Entry{Name: "job.sh", Kind: files.Regular}},
		{path: notePath, entry: files.Entry{Name: "notes.txt", Kind: files.Regular}},
	}
	a.files.scripts[scriptPath] = true

	// Pressing S on a non-script stays on the Files tab and shows an error.
	a.files.cursor = 1
	pressRune(a, 'S')
	if a.mode != modeList || a.submitForm.active {
		t.Fatalf("expected non-script S to stay on Files list, got mode=%v active=%v", a.mode, a.submitForm.active)
	}
	if !strings.Contains(a.files.err, "not a batch script") {
		t.Fatalf("expected non-script error, got %q", a.files.err)
	}

	// Pressing S on a script opens the submit form; Esc returns to Files tab.
	a.files.cursor = 0
	pressRune(a, 'S')
	if a.mode != modeForm || !a.submitForm.active {
		t.Fatalf("expected S on script to open submit form, got mode=%v active=%v", a.mode, a.submitForm.active)
	}
	if !strings.Contains(a.View(), "Submit batch script") {
		t.Fatalf("expected submit form view when opened from Files tab:\n%s", a.View())
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.mode != modeList || a.tab != filesTab || a.submitForm.active {
		t.Fatalf("Esc did not return to Files tab: mode=%v tab=%v active=%v", a.mode, a.tab, a.submitForm.active)
	}
	assertScriptUnchanged(t, scriptPath, origBytes, origMtime)
}
