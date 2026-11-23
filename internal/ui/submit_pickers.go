package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/underwoo/elzar/internal/submit"
)

func (a *App) submitHints() submit.HintData {
	cluster := ""
	if len(a.accounts.clusters) == 1 {
		for c := range a.accounts.clusters {
			cluster = c
		}
	}
	return submit.Hints(a.options.Username, cluster, a.accounts.associations, a.accounts.partitions, a.accounts.qos)
}

func (a *App) effectiveSubmitFieldValue(id submit.FieldID) string {
	for _, rf := range a.submitForm.plan.Fields {
		if rf.Spec.ID == id {
			return strings.TrimSpace(rf.Value)
		}
	}
	for i, spec := range a.submitForm.specs {
		if spec.ID == id {
			return strings.TrimSpace(a.submitForm.inputs[i].Value())
		}
	}
	return ""
}

func (a *App) submitAllWarnings() []submit.Warning {
	values := make(map[submit.FieldID]string, len(a.submitForm.plan.Fields))
	for _, rf := range a.submitForm.plan.Fields {
		values[rf.Spec.ID] = rf.Value
	}
	return submit.Check(values, a.submitHints())
}

func (a *App) submitWarningsForField(id submit.FieldID) []submit.Warning {
	all := a.submitAllWarnings()
	var out []submit.Warning
	for _, w := range all {
		if w.Field == id {
			out = append(out, w)
		}
	}
	return out
}

func (a *App) openSubmitPicker() bool {
	idx := a.submitForm.focus
	if idx < 0 || idx >= len(a.submitForm.specs) {
		return false
	}
	spec := a.submitForm.specs[idx]
	hints := a.submitHints()

	var title string
	var items []string
	switch spec.ID {
	case submit.FieldAccount:
		title = "Pick account"
		items = hints.Accounts
	case submit.FieldPartition:
		title = "Pick partition"
		items = hints.PartitionsForAccount(a.effectiveSubmitFieldValue(submit.FieldAccount))
	case submit.FieldQOS:
		title = "Pick QOS"
		items = hints.QOSFor(
			a.effectiveSubmitFieldValue(submit.FieldAccount),
			a.effectiveSubmitFieldValue(submit.FieldPartition),
		)
	default:
		return false
	}

	if len(items) == 0 {
		a.setActionStatus("no " + strings.ToLower(spec.Label) + " hints available")
		return false
	}

	current := a.effectiveSubmitFieldValue(spec.ID)
	cursor := 0
	if pos := slices.Index(items, current); pos >= 0 {
		cursor = pos
	}

	a.submitForm.picker = newListPicker(title, items, cursor)
	a.submitForm.pickerField = spec.ID
	a.submitForm.pickerActive = true
	return true
}

func (a *App) handleSubmitPickerKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case key.Matches(msg, appKeys.back):
		a.submitForm.pickerActive = false
	case key.Matches(msg, appKeys.up):
		a.submitForm.picker.Move(-1)
	case key.Matches(msg, appKeys.down):
		a.submitForm.picker.Move(1)
	case key.Matches(msg, appKeys.pageUp):
		a.submitForm.picker.Move(-a.pageSize())
	case key.Matches(msg, appKeys.pageDown):
		a.submitForm.picker.Move(a.pageSize())
	case key.Matches(msg, appKeys.enter):
		items := a.submitForm.picker.Items()
		cursor := a.submitForm.picker.Cursor()
		if cursor >= 0 && cursor < len(items) {
			chosen := items[cursor]
			for i, spec := range a.submitForm.specs {
				if spec.ID == a.submitForm.pickerField {
					a.submitForm.inputs[i].SetValue(chosen)
					a.submitForm.inputs[i].CursorEnd()
					a.submitForm.edited[spec.ID] = true
					delete(a.submitForm.clearHint, spec.ID)
					a.recomputeSubmitPlan()
					break
				}
			}
		}
		a.submitForm.pickerActive = false
	}
	return nil
}

func (a *App) renderSubmitPicker(out *strings.Builder, width int) {
	fmt.Fprintf(out, "%s  %s\n\n", titleStyle.Render("Submit batch script"), mutedStyle.Render(a.submitForm.scriptPath))
	fmt.Fprintln(out, a.submitForm.picker.View(width, a.height-2))
	fmt.Fprint(out, mutedStyle.Render(fitFooter(width, []string{"↑/↓ or j/k select", "pgup/pgdn page", "enter choose", "esc cancel"})))
}

func (a *App) renderSubmitConfirmWarnings(out *strings.Builder) {
	warnings := a.submitAllWarnings()
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, warningStyle.Render("Warnings (non-blocking):"))
	for _, w := range warnings {
		fmt.Fprintln(out, warningStyle.Render("  • "+w.Message))
	}
}
