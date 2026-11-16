package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const messageLogLimit = 200

type messageLevel uint8

const (
	messageInfo messageLevel = iota
	messageError
)

type statusMessage struct {
	Text  string
	Level messageLevel
	At    time.Time
	Count int
}

func (a *App) addMessage(text string, level messageLevel) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	now := a.clock().In(time.Local)
	if n := len(a.messageLog); n > 0 {
		last := &a.messageLog[n-1]
		if last.Text == text && last.Level == level {
			last.Count++
			last.At = now
			return
		}
	}
	if len(a.messageLog) == messageLogLimit {
		copy(a.messageLog, a.messageLog[1:])
		a.messageLog = a.messageLog[:messageLogLimit-1]
	}
	a.messageLog = append(a.messageLog, statusMessage{Text: text, Level: level, At: now, Count: 1})
}

func (a *App) removeMessage(text string) {
	text = strings.TrimSpace(text)
	for i := len(a.messageLog) - 1; i >= 0; i-- {
		if a.messageLog[i].Text == text {
			a.messageLog = append(a.messageLog[:i], a.messageLog[i+1:]...)
			return
		}
	}
}

func (a *App) setActionStatus(text string) {
	a.actionStatus = text
	level := messageInfo
	if strings.Contains(text, "failed:") || strings.HasPrefix(text, "could not load") {
		level = messageError
	}
	a.addMessage(text, level)
}

func (a *App) messagesPageSize() int {
	return max(1, a.height-6)
}

func (a *App) messageMaxScroll() int {
	width := a.width
	if width <= 0 {
		width = 80
	}
	panelWidth := min(max(1, width-4), 72)
	innerWidth := max(1, panelWidth-4)
	height := a.height
	if height <= 0 {
		height = 24
	}
	return max(0, len(a.messageLines(innerWidth))-max(1, height-6))
}

func (a *App) handleMessagesKey(msg tea.KeyMsg) {
	keyString := msg.String()
	if a.tabPrefixPending {
		a.tabPrefixPending = false
		if keyString == "s" {
			a.closeMessages()
			return
		}
	}
	if keyString == a.options.TabPrefix {
		a.tabPrefixPending = true
		return
	}
	switch {
	case keyString == "q" || key.Matches(msg, appKeys.back):
		a.closeMessages()
		return
	}
	if digit, ok := countDigit(msg); ok {
		if digit == 0 && !a.navCountActive {
			return
		}
		if !a.navCountActive {
			a.navCount = int(digit)
		} else if a.navCount < maxNavigationCount {
			a.navCount = min(maxNavigationCount, a.navCount*10+int(digit))
		}
		a.navCountActive = true
		a.messagePrefixG = false
		return
	}
	if a.messagePrefixG && keyString != "g" {
		a.messagePrefixG = false
	}
	if keyString == "g" {
		if a.messagePrefixG {
			a.messageScroll = min(a.messageMaxScroll(), max(0, a.navigationCount()-1))
			a.clearNavigationPrefix()
		} else {
			a.messagePrefixG = true
		}
		return
	}
	count := a.navigationCount()
	switch {
	case keyString == "G" || keyString == "end" || keyString == ">" || keyString == "alt+>":
		if a.navCountActive {
			a.messageScroll = min(a.messageMaxScroll(), max(0, count-1))
		} else {
			a.messageScroll = a.messageMaxScroll()
		}
	case keyString == "home" || keyString == "<" || keyString == "alt+<":
		a.messageScroll = 0
	case key.Matches(msg, appKeys.up):
		a.messageScroll = max(0, a.messageScroll-count)
	case key.Matches(msg, appKeys.down):
		a.messageScroll = min(a.messageMaxScroll(), a.messageScroll+count)
	case key.Matches(msg, appKeys.pageUp):
		a.messageScroll = max(0, a.messageScroll-count*a.messagesPageSize())
	case key.Matches(msg, appKeys.pageDown):
		a.messageScroll = min(a.messageMaxScroll(), a.messageScroll+count*a.messagesPageSize())
	default:
		a.clearNavigationPrefix()
		return
	}
	a.clearNavigationPrefix()
}

func (a *App) closeMessages() {
	a.showMessages = false
	a.messageScroll = 0
	a.messagePrefixG = false
	a.clearNavigationPrefix()
	a.tabPrefixPending = false
}

func (a *App) statusMessageHint() string {
	return a.tabPrefixLabel() + " s"
}

func (a *App) tabPrefixLabel() string {
	if a.options.TabPrefix == "ctrl+t" {
		return "^T"
	}
	return formatKeyLabel(a.options.TabPrefix)
}

func fitStatusLine(text string, width int, hint string) string {
	if width < 1 || lipgloss.Width(text) <= width {
		return text
	}
	suffix := "… " + hint
	if lipgloss.Width(suffix) >= width {
		return ansi.Truncate(suffix, width, "")
	}
	return ansi.Truncate(text, width-lipgloss.Width(suffix), "") + suffix
}
