package ui

import (
	"fmt"
	"reflect"
	"slurmtui/internal/model"
	"slurmtui/internal/registry"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"
)

func TestOverflowLayout(t *testing.T) {
	cols := []registry.Column{{ID: "name", Width: 12, MinWidth: 7}, {ID: "user", Width: 10, MinWidth: 6}, {ID: "job_id", Width: 12, MinWidth: 6}, {ID: "qos", Width: 8, MinWidth: 5}}
	for _, tc := range []struct {
		width  int
		want   []int
		hidden int
	}{{80, []int{0, 1, 2, 3}, 0}, {26, []int{0, 2}, 2}, {20, []int{2}, 3}, {1, []int{2}, 3}} {
		indices, widths, hidden := tableLayout(cols, tc.width, "job_id", 6)
		if !reflect.DeepEqual(indices, tc.want) || hidden != tc.hidden {
			t.Fatalf("width %d: %v hidden %d", tc.width, indices, hidden)
		}
		if sum(widths)+max(0, len(widths)-1) > tc.width {
			t.Fatal("layout exceeds width")
		}
	}
	if cols[0].ID != "name" {
		t.Fatal("mutated configured order")
	}
	indices, _, hidden := tableLayout(cols[:2], 15, "job_id", 6)
	if !reflect.DeepEqual(indices, []int{0, 1}) || hidden != 0 {
		t.Fatal("exact fit should keep configured order")
	}
}
func TestTableOverflowGoldens(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	for _, tab := range []registry.Tab{registry.QueueTab, registry.RecentTab, registry.AccountsTab} {
		for _, width := range []int{40, 80} {
			t.Run(fmt.Sprintf("%s_%d", tab, width), func(t *testing.T) {
				a := accountTestApp()
				a.tab = tab
				a.width = width
				a.height = 16
				a.clock = func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) }
				jobs := []model.Job{{ID: 12345, Name: "sample-job", UserName: "tester", Account: "science", Partition: "compute", QOS: "normal", States: []model.State{model.StateRunning}, NodeList: "node-01"}}
				a.queueJobs = jobs
				a.recentJobs = jobs
				lines := strings.Split(a.View(), "\n")
				for i := range lines {
					lines[i] = strings.TrimRight(lines[i], " ")
				}
				teatest.RequireEqualOutput(t, []byte(strings.Join(lines, "\n")))
			})
		}
	}
}
func TestUserHiddenJobIDStaysHidden(t *testing.T) {
	a := NewWithOptions(nil, Options{QueueColumns: []string{"name", "user", "account", "partition", "qos"}})
	a.width = 20
	a.height = 12
	a.queueJobs = []model.Job{{ID: 12345, Name: "visible-name"}}
	out := a.View()
	if strings.Contains(out, "JobID") || strings.Contains(out, "12345") || !strings.Contains(out, "cols") {
		t.Fatal(out)
	}
}
func TestOverflowFilterStableAcrossResize(t *testing.T) {
	for _, tab := range []registry.Tab{registry.QueueTab, registry.RecentTab, registry.AccountsTab} {
		t.Run(string(tab), func(t *testing.T) {
			a := accountTestApp()
			a.tab = tab
			a.queueJobs = []model.Job{{ID: 12345, NodeList: "unique-match"}, {ID: 2, NodeList: "other"}}
			a.recentJobs = a.queueJobs
			if tab == registry.AccountsTab {
				a.accounts.rows[0].QOS = []string{"unique-match"}
			}
			a.applyFilter("unique-match")
			for _, width := range []int{40, 200, 40} {
				a.Update(tea.WindowSizeMsg{Width: width, Height: 16})
				if tab == registry.AccountsTab {
					if len(a.accounts.visible) != 1 {
						t.Fatal("account matches changed")
					}
				} else {
					_, jobs, _, _, _ := a.activeData()
					if len(jobs) != 1 || jobs[0].ID != 12345 {
						t.Fatal("job matches changed")
					}
				}
				if width == 40 && strings.Contains(strings.Split(a.View(), "\n")[4], "unique-match") {
					t.Fatal("test match column is not overflow-hidden")
				}
			}
		})
	}
}

func TestPinnedContentWidthAndConfiguredOrder(t *testing.T) {
	a := accountTestApp()
	a.width, a.height = 40, 16
	lines := strings.Split(a.View(), "\n")
	if strings.Index(lines[2], "Clu") > strings.Index(lines[2], "Account") || !strings.Contains(lines[2], "Account") {
		t.Fatalf("configured order lost: %s", lines[2])
	}
	if !strings.Contains(lines[4], "account-0") {
		t.Fatalf("identifier truncated: %s", lines[4])
	}
	a.options.AccountColumns = []string{"cluster", "account"}
	a.accounts.visible[0].Account = "long-account-identifier"
	if !strings.Contains(a.View(), "long-account-identifier") {
		t.Fatal("identifier width not reserved without overflow")
	}
	a.accounts.visible[0].Account = strings.Repeat("x", 100)
	for _, line := range strings.Split(a.View(), "\n") {
		if lipgloss.Width(line) > 40 {
			t.Fatal("long identifier exceeds terminal")
		}
	}
	if !strings.HasSuffix(strings.Split(a.View(), "\n")[2], "+1 cols") {
		t.Fatal("missing badge for capped identifier")
	}
}

func TestLongJobIdentifierRetainedInConfiguredOrder(t *testing.T) {
	for _, tab := range []registry.Tab{registry.QueueTab, registry.RecentTab} {
		a := NewWithOptions(nil, Options{QueueColumns: []string{"name", "job_id", "user", "account", "qos"}, RecentColumns: []string{"name", "job_id", "user", "account", "qos"}})
		a.tab = tab
		a.width = 40
		a.height = 12
		jobs := []model.Job{{Name: "sample", ArrayJobID: model.NoValNumber{Set: true, Number: 123456789}, ArrayTaskID: model.NoValNumber{Set: true, Number: 123456}}}
		a.queueJobs = jobs
		a.recentJobs = jobs
		lines := strings.Split(a.View(), "\n")
		if !strings.Contains(lines[4], "123456789_123456") || strings.Index(lines[2], "Name") > strings.Index(lines[2], "JobID") {
			t.Fatalf("%s: %s", tab, a.View())
		}
	}
}
