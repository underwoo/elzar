package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"slurmtui/internal/config"
	"slurmtui/internal/model"
	"slurmtui/internal/registry"
)

func navigationApp(options Options, count int) *App {
	app := NewWithOptions(nil, options)
	app.width, app.height = 100, 12
	app.queueJobs = make([]model.Job, count)
	for i := range app.queueJobs {
		app.queueJobs[i].ID = int64(i + 1)
	}
	return app
}

func pressRune(app *App, r rune) {
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

func TestCountedListMotionsAndJumpBounds(t *testing.T) {
	app := navigationApp(Options{}, 20)
	pressRune(app, '5')
	if got := app.navigationStatus(); got != "5" {
		t.Fatalf("pending count status = %q", got)
	}
	pressRune(app, 'j')
	if app.queueCursor != 5 {
		t.Fatalf("5j cursor = %d, want 5", app.queueCursor)
	}
	pressRune(app, '2')
	pressRune(app, 'k')
	if app.queueCursor != 3 {
		t.Fatalf("2k cursor = %d, want 3", app.queueCursor)
	}
	pressRune(app, '1')
	pressRune(app, 'G')
	if app.queueCursor != 0 {
		t.Fatalf("1G cursor = %d, want 0", app.queueCursor)
	}
	pressRune(app, '9')
	pressRune(app, '9')
	pressRune(app, '9')
	pressRune(app, 'G')
	if app.queueCursor != len(app.queueJobs)-1 {
		t.Fatalf("clamped G cursor = %d, want %d", app.queueCursor, len(app.queueJobs)-1)
	}
	pressRune(app, '5')
	pressRune(app, 'g')
	if app.queueCursor == 4 {
		t.Fatal("single g jumped before second g")
	}
	pressRune(app, 'g')
	if app.queueCursor != 4 {
		t.Fatalf("5gg cursor = %d, want 4", app.queueCursor)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyHome})
	if app.queueCursor != 0 {
		t.Fatalf("Home cursor = %d, want 0", app.queueCursor)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if app.queueCursor != len(app.queueJobs)-1 {
		t.Fatalf("End cursor = %d, want bottom", app.queueCursor)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'<'}, Alt: true})
	if app.queueCursor != 0 {
		t.Fatalf("M-< cursor = %d, want top", app.queueCursor)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'>'}, Alt: true})
	if app.queueCursor != len(app.queueJobs)-1 {
		t.Fatalf("M-> cursor = %d, want bottom", app.queueCursor)
	}
	pressRune(app, '0')
	if app.navCountActive {
		t.Fatal("leading zero started a count")
	}
}

func TestCountedPageMotionsAndEscape(t *testing.T) {
	app := navigationApp(Options{}, 100)
	page := app.pageSize()
	pressRune(app, '3')
	app.Update(tea.KeyMsg{Type: tea.KeySpace})
	if app.queueCursor != 3*page {
		t.Fatalf("3 space cursor = %d, want %d", app.queueCursor, 3*page)
	}
	pressRune(app, '2')
	app.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if app.queueCursor != page {
		t.Fatalf("2 PgUp cursor = %d, want %d", app.queueCursor, page)
	}
	pressRune(app, '2')
	app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if app.navCountActive || app.queueCursor != page {
		t.Fatalf("escape count state: active=%v cursor=%d", app.navCountActive, app.queueCursor)
	}
	pressRune(app, '2')
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if app.navCountActive || app.mode != modeConfirm {
		t.Fatalf("non-motion did not clear count and run normally: active=%v mode=%v", app.navCountActive, app.mode)
	}
}

func TestDigitsDoNotSwitchTabsAndTabPrefixSwitches(t *testing.T) {
	app := navigationApp(Options{}, 5)
	pressRune(app, '2')
	if app.tab != registry.QueueTab || !app.navCountActive {
		t.Fatalf("plain 2 changed tab or was not count: tab=%s active=%v", app.tab, app.navCountActive)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	app.Update(tea.KeyMsg{Type: tea.KeyTab})
	if app.tab != registry.RecentTab {
		t.Fatalf("Tab selected %s, want recent", app.tab)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if app.tab != registry.QueueTab {
		t.Fatalf("Shift+Tab selected %s, want queue", app.tab)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !strings.Contains(app.navigationStatus(), "^T-") {
		t.Fatalf("prefix status = %q", app.navigationStatus())
	}
	pressRune(app, '2')
	if app.tab != registry.RecentTab || app.tabPrefixPending {
		t.Fatalf("prefix 2 state: tab=%s pending=%v", app.tab, app.tabPrefixPending)
	}
	app.tab = registry.QueueTab
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	pressRune(app, '2')
	if app.tab != registry.QueueTab || !app.navCountActive {
		t.Fatalf("prefix passthrough then 2 should count, tab=%s count=%v", app.tab, app.navCountActive)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	pressRune(app, 'x')
	if app.tabPrefixPending || app.mode != modeList {
		t.Fatalf("prefix cancel state: pending=%v mode=%v", app.tabPrefixPending, app.mode)
	}
}

func TestCustomTabPrefixAndConflictWarning(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[keys]\ntab_prefix = \"alt+x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := config.LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	app := navigationApp(Options{TabPrefix: settings.TabPrefix}, 5)
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}, Alt: true})
	pressRune(app, '2')
	if app.tab != registry.RecentTab {
		t.Fatalf("custom prefix selected %s, want recent", app.tab)
	}
	app.showHelp = true
	app.height = 40
	if !strings.Contains(app.View(), "Alt+X 1") {
		t.Fatalf("custom tab prefix missing from help: %q", app.View())
	}

	conflict := navigationApp(Options{TabPrefix: "ctrl+b"}, 20)
	conflict.queueCursor = 10
	conflict.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	if !conflict.tabPrefixPending || conflict.queueCursor != 10 || !strings.Contains(conflict.navigationWarning, "page up") {
		t.Fatalf("conflicting prefix state: pending=%v cursor=%d warning=%q", conflict.tabPrefixPending, conflict.queueCursor, conflict.navigationWarning)
	}
	if !strings.Contains(conflict.View(), "shadows page up") {
		t.Fatalf("conflict warning absent from status: %q", conflict.View())
	}
	conflict.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	if conflict.queueCursor != 5 {
		t.Fatalf("repeated conflicting prefix should pass through as page up; cursor=%d", conflict.queueCursor)
	}
}

func TestDetailCountMotionsAndViewportBounds(t *testing.T) {
	app := navigationApp(Options{}, 0)
	app.mode = modeDetail
	app.detail = &model.JobDetail{Steps: make([]model.JobStep, 15)}
	app.detailJob.ID = 10
	pressRune(app, '4')
	pressRune(app, 'j')
	if app.detailScroll != 4 {
		t.Fatalf("detail 4j scroll = %d, want 4", app.detailScroll)
	}
	pressRune(app, '1')
	pressRune(app, 'G')
	if app.detailScroll != 0 {
		t.Fatalf("detail 1G scroll = %d, want 0", app.detailScroll)
	}
	pressRune(app, 'G')
	if app.detailScroll != app.detailMaxScroll() {
		t.Fatalf("detail G scroll = %d, want max %d", app.detailScroll, app.detailMaxScroll())
	}
	pressRune(app, 'g')
	pressRune(app, 'g')
	if app.detailScroll != 0 {
		t.Fatalf("detail gg scroll = %d, want 0", app.detailScroll)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if app.detailScroll != app.detailMaxScroll() {
		t.Fatalf("detail End scroll = %d, want max %d", app.detailScroll, app.detailMaxScroll())
	}
}
