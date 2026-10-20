package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	helpClose      = "any key to close"
	helpFixedLines = 4 // title, blank, blank, close hint
)

// keyHelp is one row of the ? overlay.
type keyHelp struct{ key, what string }

// renderHelp draws a centred box titled title over a blank area of
// width×height, listing keys aligned in two columns and closing with
// "any key to close". Keys that do not fit the height are dropped.
func renderHelp(width, height int, title string, keys []keyHelp) string {
	s := defaultStyles()
	width, height = max(width, 1), max(height, 1)

	// Drop rows from the end when the box would be taller than the screen.
	if room := height - 2 - helpFixedLines; len(keys) > room {
		keys = keys[:max(room, 0)]
	}

	keyW := 0
	for _, k := range keys {
		keyW = max(keyW, lipgloss.Width(k.key))
	}
	lines := make([]string, 0, len(keys)+helpFixedLines)
	lines = append(lines, s.prompt.Render(title), "")
	for _, k := range keys {
		lines = append(lines, s.match.Render(fmt.Sprintf("%-*s", keyW, k.key))+"  "+k.what)
	}
	lines = append(lines, "", s.dim.Render(helpClose))

	body := strings.Join(lines, "\n")
	boxW := min(lipgloss.Width(body)+4, width)
	box := s.pane.Width(boxW).MaxHeight(height).Render(body)

	blank := strings.TrimSuffix(strings.Repeat("\n", height), "\n")
	return overlay(blank, box, width, height)
}

// overlay composites box over base, centred. Lines of base behind the box
// are replaced; the others are kept. The result is exactly height lines,
// each at most width cells wide.
func overlay(base, box string, width, height int) string {
	width, height = max(width, 1), max(height, 1)
	lines := strings.Split(base, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	lines = lines[:height]

	boxLines := strings.Split(box, "\n")
	if len(boxLines) > height {
		boxLines = boxLines[:height]
	}
	top := (height - len(boxLines)) / 2
	pad := strings.Repeat(" ", max((width-lipgloss.Width(box))/2, 0))
	clip := lipgloss.NewStyle().MaxWidth(width)
	for i, l := range boxLines {
		lines[top+i] = pad + l
	}
	for i, l := range lines {
		if lipgloss.Width(l) > width {
			lines[i] = clip.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}
