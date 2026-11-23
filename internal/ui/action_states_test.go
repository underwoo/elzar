package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/underwoo/elzar/internal/model"
	"github.com/underwoo/elzar/internal/registry"
)

func TestStateActionMatrix(t *testing.T) {
	for _, tc := range []struct {
		state   model.State
		allowed string
	}{
		{model.StatePending, "xhe"}, {model.StateRunning, "xeR"}, {model.StateSuspended, "xR"}, {model.StateConfiguring, "x"},
		{model.StateCompleted, "R"}, {model.StateFailed, "R"}, {model.StateCancelled, "R"}, {model.StateTimeout, "R"}, {model.StateNodeFail, "R"}, {model.StatePreempted, "R"}, {model.StateCompleting, ""}, {"UNKNOWN", ""},
	} {
		for _, entry := range []struct {
			key    rune
			action jobAction
		}{{'x', actionCancel}, {'h', actionHold}, {'e', actionEdit}, {'R', actionRequeue}} {
			t.Run(fmt.Sprintf("%s_%c", tc.state, entry.key), func(t *testing.T) {
				job := model.Job{ID: 1, States: []model.State{tc.state}}
				want := strings.ContainsRune(tc.allowed, entry.key)
				if jobActionAllowed(entry.action, job) != want {
					t.Fatal("availability mismatch")
				}
				runner := &actionTestRunner{}
				a := NewWithOptions(runner, Options{})
				a.tab = registry.RecentTab
				a.recentJobs = []model.Job{job}
				shown := false
				for _, b := range a.jobActionBindings() {
					for _, k := range b.Keys() {
						if k == string(entry.key) {
							shown = true
						}
					}
				}
				if shown != want {
					t.Fatal("help mismatch")
				}
				footer := strings.Join(a.jobActionFooter([]string{"x cancel", "h hold", "e edit", "R requeue"}), " ")
				if strings.Contains(footer, "R requeue") {
					t.Fatal("requeue in footer")
				}
				if !want {
					_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{entry.key}})
					if cmd != nil || len(runner.calls) != 0 || !strings.Contains(a.actionStatus, "can't") {
						t.Fatalf("invalid action dispatched: %s", a.actionStatus)
					}
				}
			})
		}
	}
	no := false
	yes := true
	for _, batch := range []*bool{nil, &yes, &no} {
		job := model.Job{States: []model.State{model.StateCompleted}, Batch: batch}
		if jobActionAllowed(actionRequeue, job) != (batch == nil || *batch) {
			t.Fatal("batch eligibility")
		}
	}
}
func TestStateActionGoldens(t *testing.T) {
	for _, state := range []model.State{model.StateRunning, model.StateCompleted} {
		for _, help := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_help_%t", state, help), func(t *testing.T) {
				a := NewWithOptions(nil, Options{})
				a.width = 120
				a.height = 24
				a.tab = registry.RecentTab
				a.recentJobs = []model.Job{{ID: 1, Name: "sample", States: []model.State{state}}}
				a.showHelp = help
				teatest.RequireEqualOutput(t, []byte(a.View()))
			})
		}
	}
}
func TestNumberedTabGoldens(t *testing.T) {
	for _, width := range []int{20, 40, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := NewWithOptions(nil, Options{TabPrefix: "ctrl+b"})
			a.width = width
			teatest.RequireEqualOutput(t, []byte(a.tabLine()))
		})
	}
}
