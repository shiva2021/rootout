package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/output"
)

const (
	barFull       = "▰"
	barEmpty      = "▱"
	countBarCells = 10 // bar width inside renderCount
)

// renderBar returns a cells-wide bar of ▰/▱ followed by " NN%". pct is
// clamped to 0..1. (bubbles' progress package needs harmonica, which go.sum
// does not carry.)
func renderBar(pct float64, cells int) string {
	pct = max(0, min(1, pct))
	return bar(pct, cells) + fmt.Sprintf(" %3.0f%%", pct*100)
}

// renderCount renders "label ▰▰▱▱ 12/47 detail" for list-loading states.
// detail is dimmed and truncated so the whole line fits width; it is
// dropped when there is no room.
func renderCount(width int, label string, done, total int, detail string) string {
	pct := 0.0
	if total > 0 {
		pct = float64(done) / float64(total)
	}
	line := fmt.Sprintf("%s %s %d/%d", label, bar(pct, countBarCells), done, total)
	if detail == "" {
		return line
	}
	room := width - lipgloss.Width(line) - 1
	if room < 2 {
		return line
	}
	return line + " " + defaultStyles().dim.Render(output.Truncate(detail, room))
}

// bar draws the filled and empty cells without a percentage.
func bar(pct float64, cells int) string {
	pct = max(0, min(1, pct))
	cells = max(cells, 0)
	full := int(pct*float64(cells) + 0.5)
	var b strings.Builder
	if full > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Cyan).Render(strings.Repeat(barFull, full)))
	}
	if cells-full > 0 {
		b.WriteString(defaultStyles().dim.Render(strings.Repeat(barEmpty, cells-full)))
	}
	return b.String()
}
