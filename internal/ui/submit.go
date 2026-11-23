package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/underwoo/elzar/internal/files"
	"github.com/underwoo/elzar/internal/registry"
	"github.com/underwoo/elzar/internal/slurm"
	"github.com/underwoo/elzar/internal/submit"
)

const (
	submitExtraFieldIndex = 14 // Extra sbatch options
	submitScriptArgsIndex = 15 // Script arguments
	submitActionButtonRow = 16 // [ Submit ] / [ Dry run (t) ] row
	submitInputFieldCount = 16
	submitTotalFocusable  = 17
)

type submitKeyMap struct {
	up     key.Binding
	down   key.Binding
	next   key.Binding
	prev   key.Binding
	reset  key.Binding
	dryRun key.Binding
	submit key.Binding
	cancel key.Binding
}

// submitKeys lives in its own keymap and is never added to allAppBindings(),
// preserving users' plugin key bindings across the application.
var submitKeys = submitKeyMap{
	up:     key.NewBinding(key.WithKeys("up"), key.WithHelp("↑/shift+tab", "previous field")),
	down:   key.NewBinding(key.WithKeys("down"), key.WithHelp("↓/tab", "next field")),
	next:   key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next field")),
	prev:   key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "previous field")),
	reset:  key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "reset field")),
	dryRun: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "dry run (--test-only)")),
	submit: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next / confirm submit")),
	cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
}

type submitFormState struct {
	active        bool
	confirming    bool
	returnMode    mode
	scriptPath    string
	parsed        submit.ParsedScript
	env           submit.EnvLookup
	specs         []submit.FieldSpec
	inputs        [submitInputFieldCount]textinput.Model
	edited        map[submit.FieldID]bool
	clearHint     map[submit.FieldID]bool
	focus         int
	scroll        int
	plan          submit.Plan
	planErr       error
	dryRunRunning bool
	submitting    bool
	dryRunOutput  string
	dryRunError   string
	submitError   string
}

type submitJobResult struct {
	testOnly bool
	result   slurm.SubmitResult
	err      error
}

// openSubmitForm reads and parses the batch script at path (using files.Preview
// so FIFOs/devices are rejected and the script is never opened for writing),
// resolves initial field values and provenance from #SBATCH and SBATCH_* env
// variables, and enters the submit form.
func (a *App) openSubmitForm(path string) error {
	absPath := path
	if absPath != "" && !filepath.IsAbs(absPath) {
		if resolved, err := filepath.Abs(absPath); err == nil {
			absPath = resolved
		}
	}
	preview, err := files.Preview(absPath)
	if err != nil {
		a.setActionStatus("submit failed: " + err.Error())
		return err
	}
	if !preview.IsScript {
		err := fmt.Errorf("%s is not a batch script", filepath.Base(absPath))
		a.setActionStatus("submit failed: " + err.Error())
		return err
	}
	parsed, err := submit.ParseScript(preview.Text)
	if err != nil {
		a.setActionStatus("submit failed: " + err.Error())
		return err
	}
	a.beginSubmitFormWithParsed(absPath, parsed, a.mode)
	return nil
}

func (a *App) beginSubmitFormWithParsed(scriptPath string, parsed submit.ParsedScript, returnMode mode) {
	env := a.options.Env
	if env == nil {
		env = os.LookupEnv
	}
	specs := submit.FixedFields()
	state := submitFormState{
		active:     true,
		returnMode: returnMode,
		scriptPath: scriptPath,
		parsed:     parsed,
		env:        env,
		specs:      specs,
		edited:     make(map[submit.FieldID]bool),
		clearHint:  make(map[submit.FieldID]bool),
	}

	inputWidth := min(44, max(20, a.width-58))
	for i, spec := range specs {
		val, _ := submit.BaseValue(spec.ID, parsed, env)
		in := textinput.New()
		in.Prompt = ""
		in.Width = inputWidth
		in.CharLimit = 1024
		in.SetValue(val)
		in.CursorEnd()
		state.inputs[i] = in
	}
	for _, idx := range []int{submitExtraFieldIndex, submitScriptArgsIndex} {
		in := textinput.New()
		in.Prompt = ""
		in.Width = inputWidth
		in.CharLimit = 1024
		state.inputs[idx] = in
	}

	a.submitForm = state
	a.focusSubmitField(0)
	a.recomputeSubmitPlan()
	a.editConfirm = false
	a.pendingPlugin = nil
	a.mode = modeForm
}

func (a *App) focusSubmitField(index int) {
	prev := a.submitForm.focus
	a.submitForm.focus = max(0, min(submitTotalFocusable-1, index))
	if prev != a.submitForm.focus && prev >= 0 && prev < len(a.submitForm.plan.Fields) && a.submitForm.plan.Fields[prev].ClearRequested {
		spec := a.submitForm.specs[prev]
		delete(a.submitForm.edited, spec.ID)
		a.submitForm.clearHint[spec.ID] = true
		a.submitForm.inputs[prev].SetValue(a.submitForm.plan.Fields[prev].Value)
		a.submitForm.inputs[prev].CursorEnd()
		a.recomputeSubmitPlan()
	}
	for i := range a.submitForm.inputs {
		if i == a.submitForm.focus {
			a.submitForm.inputs[i].Focus()
		} else {
			a.submitForm.inputs[i].Blur()
		}
	}
}

func (a *App) currentSubmitEdits() submit.Edits {
	values := make(map[submit.FieldID]string, len(a.submitForm.specs))
	edited := make(map[submit.FieldID]bool, len(a.submitForm.edited))
	for i, spec := range a.submitForm.specs {
		values[spec.ID] = a.submitForm.inputs[i].Value()
		if a.submitForm.edited[spec.ID] {
			edited[spec.ID] = true
		}
	}
	return submit.Edits{
		Values:     values,
		Edited:     edited,
		ExtraArgs:  a.submitForm.inputs[submitExtraFieldIndex].Value(),
		ScriptArgs: a.submitForm.inputs[submitScriptArgsIndex].Value(),
	}
}

func (a *App) recomputeSubmitPlan() {
	plan, err := submit.Resolve(a.submitForm.scriptPath, a.submitForm.parsed, a.submitForm.env, a.currentSubmitEdits())
	a.submitForm.plan = plan
	a.submitForm.planErr = err
}

func (a *App) resetFocusedSubmitField() {
	idx := a.submitForm.focus
	if idx < 0 || idx >= submitInputFieldCount {
		return
	}
	if idx < len(a.submitForm.specs) {
		spec := a.submitForm.specs[idx]
		delete(a.submitForm.edited, spec.ID)
		delete(a.submitForm.clearHint, spec.ID)
		baseVal, _ := submit.BaseValue(spec.ID, a.submitForm.parsed, a.submitForm.env)
		a.submitForm.inputs[idx].SetValue(baseVal)
		a.submitForm.inputs[idx].CursorEnd()
	} else {
		a.submitForm.inputs[idx].SetValue("")
		a.submitForm.inputs[idx].CursorEnd()
	}
	a.recomputeSubmitPlan()
}

func (a *App) handleSubmitFormKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case key.Matches(msg, submitKeys.cancel):
		a.submitForm.active = false
		a.submitForm.confirming = false
		a.mode = a.submitForm.returnMode
		return nil
	case key.Matches(msg, submitKeys.reset):
		a.resetFocusedSubmitField()
		return nil
	case key.Matches(msg, submitKeys.up), key.Matches(msg, submitKeys.prev):
		a.focusSubmitField(a.submitForm.focus - 1)
		return nil
	case key.Matches(msg, submitKeys.down), key.Matches(msg, submitKeys.next):
		a.focusSubmitField(a.submitForm.focus + 1)
		return nil
	case msg.String() == "ctrl+t":
		return a.startSubmitDryRun()
	case key.Matches(msg, submitKeys.dryRun) && a.submitForm.focus == submitActionButtonRow:
		return a.startSubmitDryRun()
	case key.Matches(msg, submitKeys.submit):
		if a.submitForm.focus < submitActionButtonRow {
			a.focusSubmitField(a.submitForm.focus + 1)
			return nil
		}
		return a.beginSubmitConfirm()
	}

	if a.submitForm.focus >= 0 && a.submitForm.focus < submitInputFieldCount {
		idx := a.submitForm.focus
		before := a.submitForm.inputs[idx].Value()
		var cmd tea.Cmd
		a.submitForm.inputs[idx], cmd = a.submitForm.inputs[idx].Update(msg)
		after := a.submitForm.inputs[idx].Value()
		if after != before {
			if idx < len(a.submitForm.specs) {
				spec := a.submitForm.specs[idx]
				a.submitForm.edited[spec.ID] = true
				delete(a.submitForm.clearHint, spec.ID)
			}
			a.recomputeSubmitPlan()
		}
		return cmd
	}
	return nil
}

func (a *App) beginSubmitConfirm() tea.Cmd {
	a.recomputeSubmitPlan()
	if a.submitForm.planErr != nil {
		a.submitForm.submitError = a.submitForm.planErr.Error()
		a.addMessage("Submit error: "+a.submitForm.planErr.Error(), messageError)
		return nil
	}
	a.submitForm.submitError = ""
	a.submitForm.confirming = true
	a.editConfirm = false
	a.pendingPlugin = nil
	a.mode = modeConfirm
	return nil
}

func (a *App) handleSubmitConfirmKey(msg tea.KeyMsg) tea.Cmd {
	a.submitForm.confirming = false
	if msg.String() != "y" {
		a.mode = modeForm
		a.setActionStatus("submit aborted; form retained")
		return nil
	}
	a.mode = modeForm
	a.submitForm.submitting = true
	a.submitForm.submitError = ""
	return a.runSubmitCommand(false)
}

func (a *App) startSubmitDryRun() tea.Cmd {
	a.recomputeSubmitPlan()
	if a.submitForm.planErr != nil {
		a.submitForm.dryRunError = a.submitForm.planErr.Error()
		a.submitForm.dryRunOutput = ""
		return nil
	}
	a.submitForm.dryRunRunning = true
	a.submitForm.dryRunError = ""
	a.submitForm.dryRunOutput = ""
	return a.runSubmitCommand(true)
}

func (a *App) runSubmitCommand(testOnly bool) tea.Cmd {
	runner := a.runner
	plan := a.submitForm.plan
	argv := plan.Argv(testOnly)
	dir := plan.WorkingDir
	return func() tea.Msg {
		res, err := slurm.Submit(context.Background(), runner, dir, argv, testOnly)
		return submitJobResult{testOnly: testOnly, result: res, err: err}
	}
}

func (a *App) acceptSubmitJobResult(msg submitJobResult) tea.Cmd {
	if msg.testOnly {
		a.submitForm.dryRunRunning = false
		if msg.err != nil {
			errText := msg.err.Error()
			if msg.result.Stderr != "" && !strings.Contains(errText, msg.result.Stderr) {
				errText = msg.result.Stderr
			}
			a.submitForm.dryRunError = errText
			a.submitForm.dryRunOutput = ""
			return nil
		}
		out := msg.result.Message
		if out == "" {
			out = "dry run succeeded"
		}
		a.submitForm.dryRunOutput = out
		a.submitForm.dryRunError = ""
		return nil
	}

	a.submitForm.submitting = false
	if msg.err != nil {
		errText := msg.err.Error()
		if msg.result.Stderr != "" && !strings.Contains(errText, msg.result.Stderr) {
			errText = msg.result.Stderr
		}
		a.submitForm.submitError = errText
		a.addMessage("Submit failed: "+errText, messageError)
		a.mode = modeForm
		return nil
	}

	jobID := strconv.FormatInt(msg.result.JobID, 10)
	status := "Submitted job " + jobID
	a.submitForm.active = false
	a.submitForm.confirming = false
	a.mode = modeList
	a.tab = registry.QueueTab
	a.pendingSelectJobID = jobID
	for i := range a.visibleQueue() {
		if a.visibleQueue()[i].DisplayID() == jobID {
			a.queueCursor = i
			a.keepActiveCursorVisible()
			break
		}
	}
	a.actionStatus = status
	a.addMessage(status, messageInfo)
	return tea.Batch(a.startQueueFetch(), a.startRecentFetch())
}

func (a *App) renderSubmitForm(out *strings.Builder, width int) {
	plan := a.submitForm.plan
	fmt.Fprintf(out, "%s  %s\n\n", titleStyle.Render("Submit batch script"), mutedStyle.Render(a.submitForm.scriptPath))

	var lines []string
	const labelWidth = 16
	for i, rf := range plan.Fields {
		label := pad(rf.Spec.Label+":", labelWidth)
		valView := a.submitForm.inputs[i].View()
		if a.submitForm.focus == i {
			label = selectedStyle.Render(label)
		}
		srcText := "[" + rf.Source.Label() + "]"
		if rf.ClearRequested || a.submitForm.clearHint[rf.Spec.ID] {
			srcText += "  can't unset from the form; edit the script"
		}
		line := fmt.Sprintf("%s %s  %s", label, valView, mutedStyle.Render(srcText))
		lines = append(lines, line)
	}

	extraRows := []struct {
		index int
		label string
	}{
		{submitExtraFieldIndex, "Extra options:"},
		{submitScriptArgsIndex, "Script args:"},
	}
	for _, row := range extraRows {
		label := pad(row.label, labelWidth)
		if a.submitForm.focus == row.index {
			label = selectedStyle.Render(label)
		}
		src := "[default]"
		if strings.TrimSpace(a.submitForm.inputs[row.index].Value()) != "" {
			src = "[form]"
		}
		lines = append(lines, fmt.Sprintf("%s %s  %s", label, a.submitForm.inputs[row.index].View(), mutedStyle.Render(src)))
	}

	submitBtn := "[ Submit (Enter)  •  Dry run (t) ]"
	if a.submitForm.focus == submitActionButtonRow {
		submitBtn = selectedStyle.Render(submitBtn)
	}
	lines = append(lines, "", "  "+submitBtn)

	// Read-only "Passed through by sbatch" directives.
	lines = append(lines, "")
	if len(plan.Passthrough) == 0 {
		lines = append(lines, mutedStyle.Render("Passed through by sbatch: (none)"))
	} else {
		parts := make([]string, 0, len(plan.Passthrough))
		for _, p := range plan.Passthrough {
			parts = append(parts, fmt.Sprintf("%s (script:%d)", p.String(), p.Line))
		}
		lines = append(lines, mutedStyle.Render("Passed through by sbatch: "+strings.Join(parts, ", ")))
	}

	// Working directory and live argv preview.
	lines = append(lines, mutedStyle.Render("Working directory: "+plan.WorkingDir))
	lines = append(lines, mutedStyle.Render("Command: "+formatArgvPreview(plan.Argv(false))))

	if a.submitForm.planErr != nil {
		lines = append(lines, errorStyle.Render("Error: "+a.submitForm.planErr.Error()))
	}
	if a.submitForm.dryRunRunning {
		lines = append(lines, mutedStyle.Render("Dry run (--test-only): running…"))
	} else if a.submitForm.dryRunError != "" {
		lines = append(lines, errorStyle.Render("Dry run (--test-only): "+a.submitForm.dryRunError))
	} else if a.submitForm.dryRunOutput != "" {
		lines = append(lines, titleStyle.Render("Dry run (--test-only):"))
		lines = append(lines, "  "+a.submitForm.dryRunOutput)
	}
	if a.submitForm.submitting {
		lines = append(lines, mutedStyle.Render("Submitting…"))
	} else if a.submitForm.submitError != "" {
		lines = append(lines, errorStyle.Render("Submit failed: "+a.submitForm.submitError))
	}

	available := max(6, a.height-4)
	if a.height <= 0 {
		available = len(lines)
	}
	if a.submitForm.focus < available/2 {
		a.submitForm.scroll = 0
	} else if a.submitForm.focus >= a.submitForm.scroll+available-6 {
		a.submitForm.scroll = max(0, min(len(lines)-available, a.submitForm.focus-(available-8)))
	}
	maxScroll := max(0, len(lines)-available)
	a.submitForm.scroll = max(0, min(maxScroll, a.submitForm.scroll))
	end := min(len(lines), a.submitForm.scroll+available)
	for _, line := range lines[a.submitForm.scroll:end] {
		fmt.Fprintln(out, truncateLine(line, width))
	}

	fmt.Fprint(out, mutedStyle.Render(fitFooter(width, []string{"↑/↓/tab field", "ctrl+r reset", "t/ctrl+t dry run", "enter next/submit", "esc cancel"})))
}

func (a *App) renderSubmitConfirm(out *strings.Builder) {
	plan := a.submitForm.plan
	fmt.Fprintf(out, "%s\n\n", titleStyle.Render("Submit batch job?"))
	fmt.Fprintf(out, "Script: %s\nWorking directory: %s\n\n", plan.ScriptPath, plan.WorkingDir)
	fmt.Fprintln(out, formatArgvPreview(plan.Argv(false)))
	fmt.Fprintf(out, "\n%s\n", mutedStyle.Render("Press y to confirm; any other key returns to the form."))
}

func formatArgvPreview(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		if arg == "" || strings.ContainsAny(arg, " \t\n\"'\\#") {
			quoted[i] = strconv.Quote(arg)
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}
