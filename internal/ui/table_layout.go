package ui

import (
	"fmt"
	"slurmtui/internal/registry"
	"sort"
	"strings"
)

// tableLayout returns render-only column indices. Configured columns remain
// unchanged so filtering and column preferences do not depend on terminal size.
func tableLayout(columns []registry.Column, width int, pinned string, contentWidth int) ([]int, []int, int) {
	priority := make([]int, 0, len(columns))
	pin := -1
	for i, c := range columns {
		if c.ID == pinned {
			pin = i
			priority = append(priority, i)
			break
		}
	}
	for i := range columns {
		if i != pin {
			priority = append(priority, i)
		}
	}
	// Pick by retention priority, then restore configured order for rendering.
	for keep := len(priority); ; keep-- {
		hidden := len(priority) - keep
		available := width
		if hidden > 0 && width > 1+len(fmt.Sprintf("+%d cols", hidden)) {
			available = width - 1 - len(fmt.Sprintf("+%d cols", hidden))
		}
		indices := append([]int(nil), priority[:keep]...)
		sort.Ints(indices)
		selected := make([]registry.Column, keep)
		minimum := max(0, keep-1)
		for j, i := range indices {
			selected[j] = columns[i]
			if i == pin {
				selected[j].MinWidth = max(1, min(contentWidth, available))
				selected[j].Width = max(selected[j].Width, selected[j].MinWidth)
			}
			minimum += max(1, selected[j].MinWidth)
		}
		if minimum <= available || keep <= 1 {
			return indices, fitWidths(selected, available), hidden
		}
	}
}

func overflowHeader(header string, hidden, width int) string {
	if hidden == 0 {
		return header
	}
	badge := fmt.Sprintf("+%d cols", hidden)
	// Very small terminals prioritize the retained identifier over the badge.
	if width <= len(badge)+1 {
		return truncateLine(header, width)
	}
	room := width - len(badge) - 1
	header = truncateLine(header, room)
	return header + strings.Repeat(" ", max(1, width-len([]rune(header))-len(badge))) + badge
}
