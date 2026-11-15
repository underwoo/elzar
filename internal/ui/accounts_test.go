package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"slurmtui/internal/config"
	"slurmtui/internal/model"
	"slurmtui/internal/registry"
	"slurmtui/internal/slurm"
)

func accountTestApp() *App {
	a := NewWithOptions(nil, Options{Username: "me"})
	a.tab = registry.AccountsTab
	a.width, a.height = 150, 16
	shares := []model.Share{}
	associations := []model.Association{}
	for _, cluster := range []string{"c1", "c2"} {
		for i := 0; i < 3; i++ {
			name := fmt.Sprintf("account-%d", i)
			shares = append(shares, model.Share{Cluster: cluster, Account: name, Parent: "root", Type: "ASSOCIATION", RawShares: model.NoValNumber{Set: true, Number: int64(i + 1)}, NormShares: model.OptionalFloat{Set: true, Number: 0.2}, RawUsage: uint64(i * 100), Level: model.OptionalFloat{Set: true, Number: 0.4}})
			if i < 2 {
				associations = append(associations, model.Association{Cluster: cluster, Account: name, User: "me", IsDefault: i == 0, QOS: []string{"normal"}})
				shares = append(shares, model.Share{Cluster: cluster, Account: name, User: "me", Type: "USER", FairShare: model.OptionalFloat{Set: true, Number: 0.6}, Level: model.OptionalFloat{Set: true, Number: 0.8}})
			}
		}
	}
	a.acceptAccounts(accountsResult{shares: slurm.SharesResult{Shares: shares}, associations: slurm.AssociationsResult{Associations: associations}, username: "me"})
	return a
}
func TestAccountMembership(t *testing.T) {
	a := accountTestApp()
	if len(a.accounts.rows) != 6 {
		t.Fatal(len(a.accounts.rows))
	}
	for _, r := range a.accounts.rows {
		switch r.Account {
		case "account-0":
			if r.You != "default" || r.FairShare.Number != 0.6 || r.RawShares.Number != 1 || r.Level.Number != 0.8 {
				t.Fatalf("%+v", r)
			}
		case "account-1":
			if r.You != "member" {
				t.Fatalf("%+v", r)
			}
		case "account-2":
			if r.You != "" || r.FairShare.Set {
				t.Fatalf("%+v", r)
			}
		}
	}
}
func TestAccountsClusterSelectorAndRegex(t *testing.T) {
	a := accountTestApp()
	pressRune(a, 'M')
	if a.mode != modePicker || !a.picker.checked[0] {
		t.Fatal("All not initially selected")
	}
	// Toggle All off, select c2, then apply.
	pressRune(a, ' ')
	a.picker.Move(2)
	pressRune(a, ' ')
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(a.accounts.visible) != 3 || a.clusterStatus() != "c2" {
		t.Fatalf("%s %d", a.clusterStatus(), len(a.accounts.visible))
	}
	a.applyFilter("member|default")
	if len(a.accounts.visible) != 2 {
		t.Fatal(len(a.accounts.visible))
	}
	for _, r := range a.accounts.visible {
		if r.Cluster != "c2" || r.You == "" {
			t.Fatal(r)
		}
	}
	a.applyFilter("[")
	if a.filterError == nil || len(a.accounts.visible) != 2 {
		t.Fatal("invalid regex discarded valid filter")
	}
	a.clearFilter()
	if len(a.accounts.visible) != 3 || a.clusterStatus() != "c2" {
		t.Fatal("clearing regex changed clusters")
	}
	pressRune(a, 'M')
	a.picker.Move(2)
	pressRune(a, ' ')
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(a.accounts.visible) != 6 || a.clusterStatus() != "all" {
		t.Fatal("empty selection must mean All")
	}
	pressRune(a, 'M')
	pressRune(a, ' ')
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(a.accounts.visible) != 6 {
		t.Fatal("cancel changed selection")
	}
}
func TestAccountsRefreshSelectionAndFailures(t *testing.T) {
	a := accountTestApp()
	a.accounts.cursor = 4
	selected := a.accounts.visible[4].Key()
	shares := append([]model.Share(nil), a.accounts.shares...)
	for i, j := 0, len(shares)-1; i < j; i, j = i+1, j-1 {
		shares[i], shares[j] = shares[j], shares[i]
	}
	a.acceptAccounts(accountsResult{shares: slurm.SharesResult{Shares: shares}, associations: slurm.AssociationsResult{Associations: a.accounts.associations}, username: "me"})
	if a.accounts.visible[a.accounts.cursor].Key() != selected {
		t.Fatal("selection changed on reorder")
	}
	a.acceptAccounts(accountsResult{shareErr: errors.New("offline"), assocErr: errors.New("denied"), username: "me"})
	if len(a.accounts.visible) != 6 || a.accounts.err == nil || len(a.messageLog) < 2 {
		t.Fatal("failure discarded rows or diagnostics")
	}
	c1 := []model.Share{}
	for _, s := range shares {
		if s.Cluster == "c1" {
			c1 = append(c1, s)
		}
	}
	a.acceptAccounts(accountsResult{shares: slurm.SharesResult{Shares: c1, Errors: []slurm.ClusterError{{Cluster: "c2", Err: errors.New("retired")}}}, assocErr: errors.New("denied"), username: "me"})
	if len(a.accounts.visible) != 6 || a.accounts.visible[a.accounts.cursor].Key() != selected {
		t.Fatal("failed cluster snapshot lost")
	}
	a.acceptAccounts(accountsResult{shares: slurm.SharesResult{}, username: "me"})
	if len(a.accounts.visible) != 0 || a.accounts.cursor != 0 {
		t.Fatal("successful empty result should clear rows")
	}
}
func TestAccountsNavigationAndActionIsolation(t *testing.T) {
	a := accountTestApp()
	a.tab = registry.QueueTab
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	pressRune(a, '3')
	if a.tab != registry.AccountsTab {
		t.Fatal(a.tab)
	}
	pressRune(a, '3')
	pressRune(a, 'j')
	if a.accounts.cursor != 3 {
		t.Fatal(a.accounts.cursor)
	}
	pressRune(a, 'G')
	if a.accounts.cursor != 5 {
		t.Fatal(a.accounts.cursor)
	}
	pressRune(a, 'g')
	pressRune(a, 'g')
	if a.accounts.cursor != 0 {
		t.Fatal(a.accounts.cursor)
	}
	a.Update(tea.KeyMsg{Type: tea.KeySpace})
	if a.accounts.cursor == 0 {
		t.Fatal("page failed")
	}
	for _, r := range "xhReu" {
		pressRune(a, r)
		if a.mode != modeList {
			t.Fatalf("job action leaked: %c", r)
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	if a.tab != registry.QueueTab {
		t.Fatal(a.tab)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if a.tab != registry.AccountsTab {
		t.Fatal(a.tab)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	pressRune(a, 's')
	if !a.showMessages {
		t.Fatal("message shortcut missing")
	}
}
func TestAccountsSortAndColumns(t *testing.T) {
	a := accountTestApp()
	a.options.ConfigPath = filepath.Join(t.TempDir(), "config.toml")
	a.accounts.cursor = 1
	selected := a.accounts.visible[1].Key()
	a.accounts.sort = columnSort{active: true, columnID: "raw_shares", descending: true}
	a.recomputeAccounts()
	if a.accounts.visible[0].RawShares.Number != 3 || a.accounts.visible[a.accounts.cursor].Key() != selected {
		t.Fatal("sort value/selection")
	}
	a.beginAccountsPicker("columns")
	for i := range a.picker.checked {
		a.picker.checked[i] = strings.HasPrefix(a.picker.items[i], "account  ")
	}
	pressRune(a, 'w')
	if a.mode != modeList || len(a.accountColumns()) != 1 {
		t.Fatal("columns not saved")
	}
	settings, err := config.LoadFile(a.options.ConfigPath)
	if err != nil || fmt.Sprint(settings.AccountColumns) != "[account]" || len(settings.QueueColumns) == 0 {
		t.Fatalf("%+v %v", settings, err)
	}
}

type accountFixtureRunner struct{ calls []string }

func (r *accountFixtureRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	file := ""
	switch call {
	case "sshare --json -l -U -M all":
		file = "shares_all"
	case "sacctmgr --json show assoc user=user-01":
		file = "site-01_associations"
	case "sacctmgr --json show qos":
		file = "site-01_qos"
	default:
		return nil, fmt.Errorf("unexpected command %s", call)
	}
	return os.ReadFile("../../testdata/accounts/" + file + ".json")
}
func TestAccountsFetchAndIndependentTimer(t *testing.T) {
	r := &accountFixtureRunner{}
	a := NewWithOptions(r, Options{Username: "user-01"})
	a.tab = registry.AccountsTab
	if a.options.AccountRefreshInterval != 5*time.Minute {
		t.Fatal(a.options.AccountRefreshInterval)
	}
	cmd := a.startAccountsFetch()
	if cmd == nil || a.startAccountsFetch() != nil {
		t.Fatal("fetch overlap")
	}
	a.Update(cmd())
	if len(a.accounts.rows) == 0 || len(r.calls) != 3 || a.accounts.loading {
		t.Fatal("fetch failed")
	}
	before := len(r.calls)
	a.Update(tickMsg{})
	if a.accounts.loading || len(r.calls) != before {
		t.Fatal("job tick triggered accounts")
	}
	a.Update(accountsTick{})
	if !a.accounts.loading {
		t.Fatal("account tick did not start fetch")
	}
}
func TestAccountsGoldens(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	for _, scenario := range []string{"multi_cluster", "single_cluster"} {
		t.Run(scenario, func(t *testing.T) {
			a := accountTestApp()
			if scenario == "single_cluster" {
				rows := []model.AccountRow{}
				for _, r := range a.accounts.rows {
					if r.Cluster == "c1" {
						rows = append(rows, r)
					}
				}
				a.accounts.rows = rows
				a.recomputeAccounts()
			}
			teatest.RequireEqualOutput(t, []byte(a.View()))
		})
	}
}

func TestAccountsTerminalWorkflow(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	a := accountTestApp()
	tm := teatest.NewTestModel(t, a, teatest.WithInitialTermSize(150, 16))
	t.Cleanup(func() { _ = tm.Quit() })
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return strings.Contains(string(out), "account-0") }, teatest.WithDuration(time.Second))
	tm.Type("M")
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return strings.Contains(string(out), "Choose clusters") }, teatest.WithDuration(time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	tm.Type("/member")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	tm.Type("q")
	final := tm.FinalModel(t, teatest.WithFinalTimeout(time.Second)).(*App)
	if final.filterPattern != "member" || len(final.accounts.visible) != 2 || final.mode != modeList {
		t.Fatalf("filter=%q rows=%d mode=%v", final.filterPattern, len(final.accounts.visible), final.mode)
	}
}

func TestAccountsInitialClustersAndSortMissing(t *testing.T) {
	a := accountTestApp()
	a.accounts.clusters = clusterSelection("c2")
	a.recomputeAccounts()
	if len(a.accounts.visible) != 3 {
		t.Fatal("configured subset ignored")
	}
	for _, desc := range []bool{false, true} {
		a.accounts.sort = columnSort{active: true, columnID: "fairshare", descending: desc}
		a.recomputeAccounts()
		if a.accounts.visible[len(a.accounts.visible)-1].You != "" {
			t.Fatal("missing values should sort last")
		}
	}
	if len(clusterSelection("")) != 0 || len(clusterSelection("c1, all")) != 0 {
		t.Fatal("All/empty config")
	}
}
