package tui

import "charm.land/lipgloss/v2"

// styles holds every lipgloss style the picker uses. Colours are the 16 ANSI
// palette entries so the terminal's own theme decides the exact shade and the
// screen reads well on both dark and light backgrounds.
type styles struct {
	prompt  lipgloss.Style // search prompt
	match   lipgloss.Style // highlighted runes in a name
	dim     lipgloss.Style // bundle ids, "(system)" suffix, labels, help
	cursor  lipgloss.Style // the selected list row (inverted)
	flash   lipgloss.Style // one-line transient message
	danger  lipgloss.Style // protected warning in the preview
	pane    lipgloss.Style // preview border
	title   lipgloss.Style // app name in the preview
	noMatch lipgloss.Style // "no matching apps"
}

func defaultStyles() styles {
	accent := lipgloss.Cyan
	return styles{
		prompt:  lipgloss.NewStyle().Foreground(accent).Bold(true),
		match:   lipgloss.NewStyle().Foreground(accent).Bold(true),
		dim:     lipgloss.NewStyle().Faint(true),
		cursor:  lipgloss.NewStyle().Reverse(true),
		flash:   lipgloss.NewStyle().Foreground(lipgloss.Yellow).Bold(true),
		danger:  lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true),
		pane:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.BrightBlack).Padding(0, 1),
		title:   lipgloss.NewStyle().Bold(true),
		noMatch: lipgloss.NewStyle().Faint(true).Italic(true),
	}
}
