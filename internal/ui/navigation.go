package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/underwoo/elzar/internal/registry"
)

const maxNavigationCount = 1_000_000

// handleNavigationKey handles count prefixes, list/detail motions, and the
// configurable two-key tab switch. It returns false for ordinary app keys.
func (a *App) handleNavigationKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	keyString := msg.String()
	prefixPassthrough := false
	if a.tabPrefixPending {
		a.tabPrefixPending = false
		a.navigationWarning = ""
		if keyString == a.options.TabPrefix {
			prefixPassthrough = true
		} else if a.runTabPrefixCommand(keyString) {
			return nil, true
		} else {
			return nil, true // any other key cancels the prefix sequence
		}
	}
	if keyString == a.options.TabPrefix && !prefixPassthrough {
		a.clearNavigationPrefix()
		a.tabPrefixPending = true
		if !a.prefixWarned {
			if conflict := prefixConflict(a.options.TabPrefix); conflict != "" {
				a.navigationWarning = "tab prefix shadows " + conflict
				a.addMessage(a.navigationWarning, messageError)
				a.prefixWarned = true
			}
		}
		return nil, true
	}
	if key.Matches(msg, appKeys.back) && (a.navCountActive || a.navG) {
		a.clearNavigationPrefix()
		return nil, true
	}
	if digit, ok := countDigit(msg); ok {
		if digit == 0 && !a.navCountActive {
			if a.navG {
				a.clearNavigationPrefix()
			}
			return nil, false
		}
		if !a.navCountActive {
			a.navCount = int(digit)
		} else if a.navCount < maxNavigationCount {
			a.navCount = min(maxNavigationCount, a.navCount*10+int(digit))
		}
		a.navCountActive = true
		a.navG = false
		return nil, true
	}
	if a.navG && keyString != "g" {
		a.navG = false
	}
	if keyString == "g" {
		if a.navG {
			a.jumpToRow(a.navigationCount())
			a.clearNavigationPrefix()
		} else {
			a.navG = true
		}
		return nil, true
	}
	if isNavigationMotion(msg, keyString) {
		count := a.navigationCount()
		switch {
		case keyString == "G":
			if a.navCountActive {
				a.jumpToRow(count)
			} else {
				a.jumpToBottom()
			}
		case keyString == "home" || keyString == "<" || keyString == "alt+<":
			a.jumpToTop()
		case keyString == "end" || keyString == ">" || keyString == "alt+>":
			a.jumpToBottom()
		case key.Matches(msg, appKeys.up):
			if a.mode == modeDetail {
				a.detailScroll = max(0, a.detailScroll-count)
			} else {
				a.moveCursor(-count)
			}
		case key.Matches(msg, appKeys.down):
			if a.mode == modeDetail {
				a.detailScroll += count
			} else {
				a.moveCursor(count)
			}
		case key.Matches(msg, appKeys.pageUp):
			a.scrollPage(-count)
		case key.Matches(msg, appKeys.pageDown):
			a.scrollPage(count)
		}
		a.clearNavigationPrefix()
		return nil, true
	}
	if a.navCountActive || a.navG {
		a.clearNavigationPrefix()
	}
	return nil, false
}

// runTabPrefixCommand dispatches commands that follow the configured tab
// prefix. Keeping this as a small table makes adding the Accounts tab later a
// single entry rather than another special case in key handling.
func (a *App) runTabPrefixCommand(command string) bool {
	for _, item := range a.tabPrefixCommands() {
		if item.key == command {
			item.run()
			return true
		}
	}
	return false
}

type tabPrefixCommand struct {
	key         string
	description string
	run         func()
}

func (a *App) tabPrefixCommands() []tabPrefixCommand {
	return []tabPrefixCommand{
		{key: "1", description: "queue view", run: func() {
			a.tab = registry.QueueTab
			a.leaveDetailForTabSwitch()
		}},
		{key: "2", description: "recent view", run: func() {
			a.tab = registry.RecentTab
			a.leaveDetailForTabSwitch()
		}},
		{key: "3", description: "accounts view", run: func() { a.tab = registry.AccountsTab; a.leaveDetailForTabSwitch() }},
		{key: "s", description: "messages", run: func() {
			a.showMessages = true
			a.messageScroll = 0
		}},
	}
}

func countDigit(msg tea.KeyMsg) (rune, bool) {
	if msg.Type != tea.KeyRunes || msg.Alt || msg.Paste || len(msg.Runes) != 1 {
		return 0, false
	}
	r := msg.Runes[0]
	if r < '0' || r > '9' {
		return 0, false
	}
	return r - '0', true
}

func isNavigationMotion(msg tea.KeyMsg, keyString string) bool {
	return keyString == "G" || keyString == "home" || keyString == "end" || keyString == "<" || keyString == ">" || keyString == "alt+<" || keyString == "alt+>" || key.Matches(msg, appKeys.up) || key.Matches(msg, appKeys.down) || key.Matches(msg, appKeys.pageUp) || key.Matches(msg, appKeys.pageDown)
}

func (a *App) navigationCount() int {
	if !a.navCountActive || a.navCount < 1 {
		return 1
	}
	return a.navCount
}

func (a *App) clearNavigationPrefix() {
	a.navCount, a.navCountActive, a.navG = 0, false, false
}

func (a *App) jumpToRow(oneBased int) {
	if a.mode == modeDetail {
		a.detailScroll = min(a.detailMaxScroll(), max(0, oneBased-1))
		return
	}
	count, cursor := a.activeRowCountAndCursor()
	if count == 0 {
		*cursor = 0
		*a.activeScrollPointer() = 0
		return
	}
	*cursor = max(0, min(count-1, oneBased-1))
	a.keepActiveCursorVisible()
}

func (a *App) detailMaxScroll() int {
	if a.detail == nil && a.tab != registry.AccountsTab {
		return 0
	}
	width := a.width
	if width <= 0 {
		width = 120
	}
	available := a.detailPageSize()
	return max(0, len(a.detailContentLines(width))-available)
}

func (a *App) jumpToTop() {
	if a.mode == modeDetail {
		a.detailScroll = 0
		return
	}
	a.jumpToRow(1)
}

func (a *App) jumpToBottom() {
	if a.mode == modeDetail {
		a.detailScroll = a.detailMaxScroll()
		return
	}
	count, cursor := a.activeRowCountAndCursor()
	*cursor = max(0, count-1)
	a.keepActiveCursorVisible()
}

func (a *App) scrollPage(pages int) {
	if a.mode == modeDetail {
		a.detailScroll = max(0, a.detailScroll+pages*a.detailPageSize())
		return
	}
	a.pageCursor(pages * a.pageSize())
}

func (a *App) navigationStatus() string {
	var parts []string
	if a.navCountActive {
		parts = append(parts, strconv.Itoa(a.navCount))
	}
	if a.navG {
		parts = append(parts, "g-")
	}
	if a.tabPrefixPending {
		prefix := "^T"
		if a.options.TabPrefix != "ctrl+t" {
			prefix = formatKeyLabel(a.options.TabPrefix)
		}
		parts = append(parts, prefix+"-")
	}
	if a.navigationWarning != "" {
		parts = append(parts, a.navigationWarning)
	}
	return strings.Join(parts, "  ")
}

func formatKeyLabel(value string) string {
	if value == " " {
		return "Space"
	}
	parts := strings.Split(value, "+")
	for i := range parts {
		switch parts[i] {
		case "ctrl":
			parts[i] = "Ctrl"
		case "alt":
			parts[i] = "Alt"
		case "shift":
			parts[i] = "Shift"
		default:
			parts[i] = strings.ToUpper(parts[i])
		}
	}
	return strings.Join(parts, "+")
}

func prefixConflict(prefix string) string {
	if prefix == "M" {
		return "cluster selector"
	}
	bindings := []struct {
		binding key.Binding
		name    string
	}{
		{appKeys.quit, "quit"}, {appKeys.help, "help"}, {appKeys.filter, "filter"}, {appKeys.sort, "sort"},
		{appKeys.columns, "columns"}, {appKeys.cancel, "cancel"}, {appKeys.hold, "hold/release"},
		{appKeys.requeue, "requeue"}, {appKeys.edit, "edit"}, {appKeys.pageUp, "page up"},
		{appKeys.pageDown, "page down"}, {appKeys.refresh, "refresh"}, {appKeys.scope, "scope"},
		{appKeys.up, "move up"}, {appKeys.down, "move down"}, {appKeys.enter, "details"},
		{appKeys.tab, "next view"}, {appKeys.shiftTab, "previous view"}, {appKeys.back, "back"},
		{appKeys.allFields, "all fields"}, {appKeys.top, "top"}, {appKeys.bottom, "bottom"}, {appKeys.count, "motion count"},
	}
	for _, item := range bindings {
		for _, bindingKey := range item.binding.Keys() {
			if bindingKey == prefix {
				return item.name
			}
		}
	}
	return ""
}

func (a *App) leaveDetailForTabSwitch() {
	if a.mode == modeDetail {
		a.mode = modeList
		a.detail, a.detailError = nil, nil
		a.detailRequest++
	}
}
