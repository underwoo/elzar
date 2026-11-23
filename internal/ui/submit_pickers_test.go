package ui

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"github.com/underwoo/elzar/internal/model"
	"github.com/underwoo/elzar/internal/slurm"
	"github.com/underwoo/elzar/internal/submit"
)

func loadM3AccountsIntoApp(t *testing.T, app *App) {
	t.Helper()
	assocBytes, err := os.ReadFile("../../testdata/accounts/site-01_associations.json")
	if err != nil {
		t.Fatal(err)
	}
	qosBytes, err := os.ReadFile("../../testdata/accounts/site-01_qos.json")
	if err != nil {
		t.Fatal(err)
	}
	partBytes, err := os.ReadFile("../../testdata/partitions/site-01.json")
	if err != nil {
		t.Fatal(err)
	}
	r := &submitTestRunner{
		runFunc: func(_, name string, args ...string) ([]byte, []byte, error) {
			key := name + " " + strings.Join(args, " ")
			switch key {
			case "sacctmgr --json show assoc user=user-01":
				return assocBytes, nil, nil
			case "sacctmgr --json show qos":
				return qosBytes, nil, nil
			case "scontrol -M cluster-01 show partition --json":
				return partBytes, nil, nil
			default:
				return nil, nil, os.ErrNotExist
			}
		},
	}
	assocRes, err := slurm.ListAssociations(context.Background(), r, "user-01")
	if err != nil {
		t.Fatal(err)
	}
	qosRes, err := slurm.ListQOS(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	partRes := slurm.ListPartitions(context.Background(), r, []model.Association{{Cluster: "cluster-01"}})
	app.accounts.associations = assocRes.Associations
	app.accounts.qos = qosRes.QOS
	app.accounts.partitions = partRes.Partitions
	app.accounts.clusters = map[string]bool{"cluster-01": true}
}

func TestSubmitPickerAccountPartitionQOSAndGolden(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	parsed, err := submit.ParseScript(`#!/bin/bash
#SBATCH --job-name=picker-test
#SBATCH --account=account-05
#SBATCH --partition=partition-02
#SBATCH --qos=qos-02

srun ./run
`)
	if err != nil {
		t.Fatal(err)
	}

	app := NewWithOptions(nil, Options{Username: "user-01"})
	app.width, app.height = 100, 28
	loadM3AccountsIntoApp(t, app)
	app.beginSubmitFormWithParsed("/scratch/user-01/job.sh", parsed, modeList)

	// Pressing ctrl+p on Job name (field 0) does not open a picker.
	app.focusSubmitField(0)
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if app.submitForm.pickerActive {
		t.Fatal("ctrl+p on Job name should not open picker")
	}

	// Focus Account (field 1) and press ctrl+p: default account (account-07) is first,
	// cursor starts on current effective value (account-05).
	app.focusSubmitField(1)
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if !app.submitForm.pickerActive || app.submitForm.pickerField != submit.FieldAccount {
		t.Fatalf("expected Account picker active, got active=%v field=%v", app.submitForm.pickerActive, app.submitForm.pickerField)
	}
	items := app.submitForm.picker.Items()
	if len(items) == 0 || items[0] != "account-07" {
		t.Fatalf("Account picker items = %v, want default account account-07 first", items)
	}
	if app.submitForm.picker.Cursor() != 1 || items[app.submitForm.picker.Cursor()] != "account-05" {
		t.Fatalf("Account picker cursor = %d (%v), want 1 (account-05)", app.submitForm.picker.Cursor(), items)
	}

	// Golden snapshot of the Account list picker inside the submit form.
	teatest.RequireEqualOutput(t, []byte(app.View()))

	// Move up to account-07 and press Enter to choose it.
	app.Update(tea.KeyMsg{Type: tea.KeyUp})
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if app.submitForm.pickerActive {
		t.Fatal("Enter did not close Account picker")
	}
	if got := app.submitForm.inputs[1].Value(); got != "account-07" {
		t.Fatalf("Account input = %q, want account-07", got)
	}
	if !app.submitForm.edited[submit.FieldAccount] || !strings.Contains(app.View(), "--account=account-07") {
		t.Fatalf("picked account not marked edited in plan:\n%s", app.View())
	}

	// Focus Partition (field 2), press ctrl+p, move down, and press Enter.
	app.focusSubmitField(2)
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if !app.submitForm.pickerActive || app.submitForm.pickerField != submit.FieldPartition {
		t.Fatalf("expected Partition picker active, got active=%v field=%v", app.submitForm.pickerActive, app.submitForm.pickerField)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyDown})
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := app.submitForm.inputs[2].Value(); got != "partition-03" {
		t.Fatalf("Partition input = %q, want partition-03", got)
	}

	// Focus QOS (field 3), press ctrl+p, verify default QOS (qos-01) is first, then Esc cancels.
	app.focusSubmitField(3)
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if !app.submitForm.pickerActive || app.submitForm.pickerField != submit.FieldQOS {
		t.Fatalf("expected QOS picker active, got active=%v field=%v", app.submitForm.pickerActive, app.submitForm.pickerField)
	}
	qosItems := app.submitForm.picker.Items()
	if len(qosItems) == 0 || qosItems[0] != "qos-01" {
		t.Fatalf("QOS picker items = %v, want default qos-01 first", qosItems)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyUp})
	app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if app.submitForm.pickerActive {
		t.Fatal("Esc did not close QOS picker")
	}
	if got := app.submitForm.inputs[3].Value(); got != "qos-02" {
		t.Fatalf("Esc modified QOS input: got %q, want qos-02", got)
	}
}

func TestSubmitClusterPickerAndTwoClusterScoping(t *testing.T) {
	parsed, err := submit.ParseScript(`#!/bin/bash
#SBATCH --job-name=multi-cluster
#SBATCH --partition=part-c2

srun ./run
`)
	if err != nil {
		t.Fatal(err)
	}

	app := NewWithOptions(nil, Options{Username: "user-01"})
	app.width, app.height = 110, 34
	app.accounts.clusters = map[string]bool{"c1": true, "c2": true}
	app.accounts.associations = []model.Association{
		{Cluster: "c1", Account: "acct-c1", User: "user-01", IsDefault: true, DefaultQOS: "qos-01", QOS: []string{"qos-01"}},
		{Cluster: "c2", Account: "acct-c2", User: "user-01", IsDefault: true, DefaultQOS: "qos-02", QOS: []string{"qos-02"}},
	}
	app.accounts.partitions = []model.Partition{
		{Cluster: "c1", Name: "part-c1", State: []string{"UP"}},
		{Cluster: "c2", Name: "part-c2", State: []string{"UP"}},
	}
	app.accounts.qos = []model.QOS{
		{Name: "qos-01"},
		{Name: "qos-02"},
	}

	app.beginSubmitFormWithParsed("/scratch/user-01/job.sh", parsed, modeList)

	// With no cluster set in the script/env/form, the local cluster is "c1",
	// where "part-c2" does not exist -> warning on Partition.
	if !strings.Contains(app.View(), `warning: partition "part-c2" is not in cluster partitions`) {
		t.Fatalf("expected warning on c1 for part-c2:\n%s", app.View())
	}

	// Focus Clusters (field 14), press ctrl+p, verify known clusters [c1, c2], and pick c2.
	app.focusSubmitField(14)
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if !app.submitForm.pickerActive || app.submitForm.pickerField != submit.FieldClusters {
		t.Fatalf("expected Clusters picker active, got active=%v field=%v", app.submitForm.pickerActive, app.submitForm.pickerField)
	}
	if got := app.submitForm.picker.Items(); len(got) != 2 || got[0] != "c1" || got[1] != "c2" {
		t.Fatalf("Clusters picker items = %v, want [c1 c2]", got)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyDown})
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if app.submitForm.pickerActive {
		t.Fatal("Enter did not close Clusters picker")
	}
	if got := app.submitForm.inputs[14].Value(); got != "c2" {
		t.Fatalf("Clusters input = %q, want c2", got)
	}
	if !strings.Contains(app.View(), "--clusters=c2") {
		t.Fatalf("expected --clusters=c2 in argv preview:\n%s", app.View())
	}

	// Now that effective cluster is c2, "part-c2" exists on c2 -> warning disappears!
	if strings.Contains(app.View(), `warning: partition "part-c2" is not in cluster partitions`) {
		t.Fatalf("unexpected partition warning after switching to c2:\n%s", app.View())
	}

	// Opening Partition picker (field 2) now shows only c2's partition ("part-c2").
	app.focusSubmitField(2)
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if got := app.submitForm.picker.Items(); len(got) != 1 || got[0] != "part-c2" {
		t.Fatalf("c2 Partition picker items = %v, want [part-c2]", got)
	}
}

func TestSubmitWarningsFormAndConfirmGoldensAndNonBlocking(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	parsed, err := submit.ParseScript(`#!/bin/bash
#SBATCH --job-name=warn-check
#SBATCH --account=unknown-acct
#SBATCH --partition=partition-04
#SBATCH --qos=qos-01
#SBATCH --time=20-00:00:00
#SBATCH --nodes=999

srun ./simulate
`)
	if err != nil {
		t.Fatal(err)
	}

	runner := &submitTestRunner{stdout: "9201\n"}
	app := NewWithOptions(runner, Options{Username: "user-01"})
	app.width, app.height = 110, 36
	loadM3AccountsIntoApp(t, app)
	app.beginSubmitFormWithParsed("/scratch/user-01/warn.sh", parsed, modeList)

	formView := app.View()
	for _, want := range []string{
		`warning: account "unknown-acct" is not in your associations`,
		`warning: QOS "qos-01" is not allowed on partition "partition-04"`,
		`warning: time 20-00:00:00 (28800 min) exceeds partition "partition-04" MaxTime`,
		`warning: nodes 999 exceeds partition "partition-04" MaxNodes`,
	} {
		if !strings.Contains(formView, want) {
			t.Fatalf("submit form view missing warning %q:\n%s", want, formView)
		}
	}

	t.Run("form_warnings_golden", func(t *testing.T) {
		teatest.RequireEqualOutput(t, []byte(formView))
	})

	// Open confirm dialog and verify warnings appear there too.
	app.focusSubmitField(submitActionButtonRow)
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if app.mode != modeConfirm || !app.submitForm.confirming {
		t.Fatalf("expected confirm dialog, got mode=%v confirming=%v", app.mode, app.submitForm.confirming)
	}
	confirmView := app.View()
	if !strings.Contains(confirmView, "Warnings (non-blocking):") {
		t.Fatalf("confirm view missing warnings header:\n%s", confirmView)
	}

	t.Run("confirm_warnings_golden", func(t *testing.T) {
		teatest.RequireEqualOutput(t, []byte(confirmView))
	})

	// Warnings must never block submission: pressing 'y' executes sbatch cleanly.
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("warnings blocked confirm submission")
	}
	app.Update(cmd())
	if len(runner.calls) != 1 || runner.calls[0] != "sbatch --parsable /scratch/user-01/warn.sh" {
		t.Fatalf("runner calls = %v", runner.calls)
	}
	if app.actionStatus != "Submitted job 9201" {
		t.Fatalf("actionStatus = %q, want 'Submitted job 9201'", app.actionStatus)
	}
}

func TestReadmeMatchesFilesAndSubmitHelpKeys(t *testing.T) {
	readmeBytes, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := strings.ToLower(string(readmeBytes))

	app := NewWithOptions(nil, Options{})
	app.tab = filesTab
	for _, group := range app.builtinHelpGroups() {
		if group.title != "Files" {
			continue
		}
		for _, b := range group.bindings {
			for _, k := range b.Keys() {
				checkKeyInReadme(t, readme, k)
			}
		}
	}

	app.mode = modeForm
	app.submitForm.active = true
	for _, group := range app.builtinHelpGroups() {
		if group.title != "Submit form" {
			continue
		}
		for _, b := range group.bindings {
			for _, k := range b.Keys() {
				checkKeyInReadme(t, readme, k)
			}
		}
	}
}

func checkKeyInReadme(t *testing.T, readmeLower, k string) {
	t.Helper()
	want := strings.ToLower(k)
	switch want {
	case "up":
		want = "↑"
	case "down":
		want = "↓"
	case "left":
		want = "←"
	case "right":
		want = "→"
	}
	if !strings.Contains(readmeLower, want) {
		t.Fatalf("README.md is missing documented key %q (normalized %q) from help.go", k, want)
	}
}
