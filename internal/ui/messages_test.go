package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestMessageLogDeduplicatesAndKeepsLastTwoHundred(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	app := NewWithOptions(nil, Options{Clock: func() time.Time { now = now.Add(time.Second); return now }})
	app.addMessage("refresh failed", messageError)
	app.addMessage("refresh failed", messageError)
	if len(app.messageLog) != 1 || app.messageLog[0].Count != 2 {
		t.Fatalf("duplicate log entries not collapsed: %+v", app.messageLog)
	}
	if got := app.messageLog[0].At; !got.Equal(now.In(time.Local)) {
		t.Fatalf("deduplicated timestamp = %s, want latest %s", got, now)
	}
	for i := 0; i < messageLogLimit+5; i++ {
		app.addMessage(fmt.Sprintf("status %03d", i), messageInfo)
	}
	if len(app.messageLog) != messageLogLimit {
		t.Fatalf("message log has %d entries, want %d", len(app.messageLog), messageLogLimit)
	}
	if got := app.messageLog[0].Text; got != "status 005" {
		t.Fatalf("oldest retained entry = %q, want status 005", got)
	}
}

func TestMessageOverlayNewestFirstAndWraps(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	app := NewWithOptions(nil, Options{})
	app.width, app.height = 40, 12
	app.addMessage("older status", messageInfo)
	app.addMessage("permission denied "+strings.Repeat("because the account is not permitted ", 3), messageError)
	lines := app.messageLines(32)
	if len(lines) < 3 {
		t.Fatalf("long message did not wrap: %q", lines)
	}
	joined := strings.Join(lines, "\n")
	errorIndex, olderIndex := strings.Index(joined, "ERROR"), strings.Index(joined, "older status")
	if errorIndex < 0 || olderIndex < 0 || errorIndex > olderIndex {
		t.Fatalf("messages are not newest first: %q", lines)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > 32 {
			t.Fatalf("wrapped message line %d is %d columns wide: %q", i+1, got, line)
		}
	}
	app.showMessages = true
	view := app.View()
	if !strings.Contains(view, "because") || !strings.Contains(view, "messages") {
		t.Fatalf("overlay omitted wrapped error or title: %q", view)
	}
}

func TestMessageOverlayOpensAndClosesWithConfiguredPrefix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix tea.KeyType
		label  string
	}{
		{name: "default", prefix: tea.KeyCtrlT, label: "^T s"},
		{name: "custom", prefix: tea.KeyCtrlY, label: "Ctrl+Y s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := Options{}
			if tc.name == "custom" {
				options.TabPrefix = "ctrl+y"
			}
			app := NewWithOptions(nil, options)
			app.width, app.height = 80, 16
			app.Update(tea.KeyMsg{Type: tc.prefix})
			if !app.tabPrefixPending {
				t.Fatal("configured prefix was not accepted")
			}
			pressRune(app, 's')
			if !app.showMessages {
				t.Fatal("prefix+s did not open the message overlay")
			}
			app.Update(tea.KeyMsg{Type: tc.prefix})
			pressRune(app, 's')
			if app.showMessages {
				t.Fatal("prefix+s did not close the message overlay")
			}
			if got := app.statusMessageHint(); got != tc.label {
				t.Fatalf("message shortcut hint = %q, want %q", got, tc.label)
			}
		})
	}
}

func TestMessageOverlaySupportsCountedNavigationMotions(t *testing.T) {
	app := NewWithOptions(nil, Options{})
	app.width, app.height = 40, 12
	app.showMessages = true
	for i := 0; i < 20; i++ {
		app.addMessage(fmt.Sprintf("entry %02d %s", i, strings.Repeat("wrapped text ", 5)), messageInfo)
	}
	maxScroll := app.messageMaxScroll()
	if maxScroll < 10 {
		t.Fatalf("test messages did not produce scrollable content: max=%d", maxScroll)
	}
	pressRune(app, '5')
	pressRune(app, 'j')
	if app.messageScroll != 5 {
		t.Fatalf("5j scrolled to %d, want 5", app.messageScroll)
	}
	pressRune(app, '1')
	pressRune(app, 'G')
	if app.messageScroll != 0 {
		t.Fatalf("1G scrolled to %d, want top", app.messageScroll)
	}
	pressRune(app, '3')
	app.Update(tea.KeyMsg{Type: tea.KeySpace})
	if app.messageScroll != min(maxScroll, 3*app.messagesPageSize()) {
		t.Fatalf("3 space scrolled to %d, want %d", app.messageScroll, min(maxScroll, 3*app.messagesPageSize()))
	}
	pressRune(app, 'g')
	pressRune(app, 'g')
	if app.messageScroll != 0 {
		t.Fatalf("gg scrolled to %d, want top", app.messageScroll)
	}
	app.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if app.messageScroll != maxScroll {
		t.Fatalf("End scrolled to %d, want bottom %d", app.messageScroll, maxScroll)
	}
	for _, up := range []tea.KeyMsg{
		{Type: tea.KeyPgUp},
		{Type: tea.KeyRunes, Runes: []rune{'b'}},
		{Type: tea.KeyCtrlB},
		{Type: tea.KeyRunes, Runes: []rune{'v'}, Alt: true},
	} {
		app.messageScroll = maxScroll
		app.Update(up)
		if app.messageScroll >= maxScroll {
			t.Errorf("page-up alias %q did not scroll message log", up.String())
		}
	}
	for _, down := range []tea.KeyMsg{
		{Type: tea.KeyPgDown},
		{Type: tea.KeySpace},
		{Type: tea.KeyCtrlV},
		{Type: tea.KeyCtrlF},
	} {
		app.messageScroll = 0
		app.Update(down)
		if app.messageScroll == 0 {
			t.Errorf("page-down alias %q did not scroll message log", down.String())
		}
	}
}

func TestTruncatedStatusPointsToMessageLog(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	app := NewWithOptions(nil, Options{})
	app.width, app.height = 40, 12
	app.queueError = fmt.Errorf("queue refresh failed: %s", strings.Repeat("details ", 12))
	lines := strings.Split(app.View(), "\n")
	status := lines[len(lines)-2]
	if !strings.HasSuffix(status, "… ^T s") {
		t.Fatalf("truncated status does not point to full message log: %q", status)
	}
	if lipgloss.Width(status) > 40 {
		t.Fatalf("truncated status is %d columns, want <= 40", lipgloss.Width(status))
	}
}
