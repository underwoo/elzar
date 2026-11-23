package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/underwoo/elzar/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"
)

func detailAccountTestApp() *App {
	a := accountTestApp()
	a.width, a.height = 100, 60
	a.accounts.shares = append(a.accounts.shares,
		model.Share{Cluster: "c1", Account: "root", Type: "ASSOCIATION", RawShares: model.NoValNumber{Set: true, Number: 1}},
		model.Share{Cluster: "c2", Account: "root", Parent: "wrong-cluster", Type: "ASSOCIATION"})
	a.accounts.qos = []model.QOS{{Name: "normal", MaxWall: model.NoValNumber{Set: true, Number: 120}, MaxJobsPerUser: model.NoValNumber{Set: true, Number: 10}, MaxSubmitPerUser: model.NoValNumber{Infinite: true}, Priority: model.NoValNumber{Set: true, Number: 0}, UsageFactor: model.OptionalFloat{Set: true, Number: 1}, MaxTRESPerJob: []model.TRES{{Type: "cpu", Count: 32}}, MaxTRESPerUser: []model.TRES{{Type: "gres", Name: "gpu", Count: 4}}}}
	a.accounts.partitionClusters = map[string]bool{"c1": true, "c2": true}
	a.accounts.partitions = []model.Partition{
		{Cluster: "c1", Name: "compute", MaxTime: model.NoValNumber{Infinite: true}, DefaultTime: model.NoValNumber{Set: true, Number: 60}, MaxNodes: model.NoValNumber{Set: true, Number: 8}, State: []string{"UP"}},
		{Cluster: "c2", Name: "wrong-cluster", State: []string{"DOWN"}},
	}
	return a
}

func TestAccountDetailGoldens(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	for _, scenario := range []string{"lineage", "non_member", "no_limits"} {
		t.Run(scenario, func(t *testing.T) {
			a := detailAccountTestApp()
			if scenario == "non_member" {
				a.accounts.cursor = 2
			}
			if scenario == "no_limits" {
				a.accounts.qos = []model.QOS{{Name: "normal"}}
			}
			a.Update(tea.KeyMsg{Type: tea.KeyEnter})
			teatest.RequireEqualOutput(t, []byte(a.View()))
		})
	}
}

func TestAccountDetailNavigationAndIsolation(t *testing.T) {
	a := detailAccountTestApp()
	a.height = 10
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.mode != modeDetail {
		t.Fatal("enter did not open detail")
	}
	for _, k := range []rune{'x', 'h', 'R', 'e', 'a', 'r'} {
		_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{k}})
		if cmd != nil || a.mode != modeDetail {
			t.Fatalf("job action %c leaked into account detail", k)
		}
	}
	pressRune(a, 'G')
	if a.detailScroll == 0 {
		t.Fatal("bottom did not scroll")
	}
	pressRune(a, 'g')
	pressRune(a, 'g')
	if a.detailScroll != 0 {
		t.Fatal("top did not scroll")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.mode != modeList || a.accounts.cursor != 0 {
		t.Fatal("back lost selection")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	if a.mode != modeList || a.tab != filesTab {
		t.Fatal("tab did not leave detail")
	}
}

func TestAccountDetailMissingAndCyclicData(t *testing.T) {
	a := detailAccountTestApp()
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	content := strings.Join(a.accountDetailLines(), "\n")
	if strings.Contains(content, "wrong-cluster") || !strings.Contains(content, "root\n") {
		t.Fatal(content)
	}
	a.accounts.shares = nil
	a.accounts.qos = nil
	content = strings.Join(a.accountDetailLines(), "\n")
	if !strings.Contains(content, "Ancestor data unavailable: root") || !strings.Contains(content, "QOS limits unavailable") {
		t.Fatal(content)
	}
	a.accounts.shares = []model.Share{{Cluster: "c1", Account: "root", Parent: "account-0", Type: "ASSOCIATION"}}
	if !strings.Contains(strings.Join(a.accountDetailLines(), "\n"), "cycle") {
		t.Fatal("cycle not reported")
	}
	a.accounts.visible = nil
	a.mode = modeList
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.mode != modeList {
		t.Fatal("empty list opened detail")
	}
}

func TestAccountDetailMissingUserShares(t *testing.T) {
	a := detailAccountTestApp()
	shares := []model.Share{}
	for _, share := range a.accounts.shares {
		if share.Type != "USER" {
			shares = append(shares, share)
		}
	}
	a.accounts.shares = shares
	a.accounts.rows = buildAccountRows(shares, a.accounts.associations, "me")
	a.recomputeAccounts()
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(strings.Join(a.accountDetailLines(), "\n"), "FairShare: —  Level: —") {
		t.Fatal("missing user shares must not show account factors")
	}
}

func TestAccountDetailTerminalWorkflow(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	a := detailAccountTestApp()
	tm := teatest.NewTestModel(t, a, teatest.WithInitialTermSize(100, 16))
	t.Cleanup(func() { _ = tm.Quit() })
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return strings.Contains(string(out), "Accounts: 6 of 6") }, teatest.WithDuration(time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return strings.Contains(string(out), "Account detail: c1/account-0") }, teatest.WithDuration(time.Second))
	tm.Type("G")
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return strings.Contains(string(out), "MaxNodes: 8") }, teatest.WithDuration(time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	tm.Type("q")
	final := tm.FinalModel(t, teatest.WithFinalTimeout(time.Second)).(*App)
	if final.mode != modeList || final.accounts.cursor != 0 {
		t.Fatal("detail did not return to selected account")
	}
}
