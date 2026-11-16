package ui

import (
	"fmt"
	"slurmtui/internal/registry"
	"strings"
)

// tableLayout returns render-only column indices. Configured columns remain
// unchanged so filtering and column preferences do not depend on terminal size.
func tableLayout(columns []registry.Column, width int, pinned string) ([]int, []int, int) {
	indices := make([]int, len(columns))
	minimum := max(0, len(columns)-1)
	for i, c := range columns {
		indices[i] = i
		minimum += max(1, c.MinWidth)
	}
	if minimum <= width {
		return indices, fitWidths(columns, width), 0
	}
	for i, c := range columns {
		if c.ID == pinned {
			indices = append([]int{i}, append(indices[:i:i], indices[i+1:]...)...)
			break
		}
	}
	// Retain the longest prefix that fits, including the overflow indicator.
	keep := len(indices)
	for keep > 1 {
		used := keep - 1
		for _, i := range indices[:keep] {
			used += max(1, columns[i].MinWidth)
		}
		if keep < len(indices) {
			used += 1 + len(fmt.Sprintf("+%d cols", len(indices)-keep))
		}
		if used <= width {
			break
		}
		keep--
	}
	hidden := len(indices) - keep
	indices = indices[:keep]
	selected := make([]registry.Column, keep)
	for j, i := range indices {
		selected[j] = columns[i]
	}
	available := width
	if hidden > 0 && width > 1+len(fmt.Sprintf("+%d cols", hidden)) {
		available = max(1, width-1-len(fmt.Sprintf("+%d cols", hidden)))
	}
	return indices, fitWidths(selected, available), hidden
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
