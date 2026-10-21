package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/output"
)

const (
	homeHelp       = "↑/↓ move · enter select · ctrl+x quit · ? help"
	homeChromeRows = 4 // title, blank row, flash row, footer row
)

// homeKeys is the ? overlay of the home screen.
var homeKeys = []keyHelp{
	{"↑/↓", "move"},
	{"enter", "select"},
	{"ctrl+x", "quit rootout"},
	{"?", "this help"},
}

// Flash is the one-line outcome of the previous action shown on the home screen.
type Flash struct {
	Text string
	Err  bool // render with the danger style
}

// Action is what the user chose on the home screen.
type Action int

const (
	ActionNone Action = iota
	ActionUninstall
	ActionOrphans
	ActionScan
	ActionModel
	ActionQuit // never returned by PickAction: the Quit row yields ErrQuit
)

type homeRow struct {
	action Action
	title  string
	blurb  string
}

var homeRows = []homeRow{
	{ActionUninstall, "Uninstall an app", "pick an installed app, review every file it left behind, remove it"},
	{ActionOrphans, "Remove orphaned leftovers", "find files left by apps that are already gone"},
	{ActionScan, "Scan editor extensions", "score VS Code-family extensions for malicious behaviour"},
	{ActionModel, "Choose local AI model", "pick or download an Ollama model that fits this Mac"},
	{ActionQuit, "Quit", "leave rootout (ctrl+x works on every screen)"},
}

// PickAction shows the home screen and returns the chosen action. It
// returns ErrQuit when the user quits (ctrl+x, ctrl+c or the Quit row).
func PickAction(ctx context.Context, flash Flash) (Action, error) {
	h := NewHome(flash)
	if _, err := tea.NewProgram(h, tea.WithContext(ctx)).Run(); err != nil {
		return ActionNone, fmt.Errorf("tui: home: %w", err)
	}
	if h.quitAll {
		return ActionNone, ErrQuit
	}
	a, _ := h.Chosen()
	return a, nil
}

// Home is the home screen model. It implements tea.Model.
type Home struct {
	rows          []homeRow
	styles        styles
	width, height int
	cursor        int
	flash         Flash
	showHelp      bool
	chosen        Action
	ok            bool
	quitAll       bool
}

// NewHome builds the home screen showing flash under the list.
func NewHome(flash Flash) *Home {
	return &Home{
		rows:   homeRows,
		styles: defaultStyles(),
		width:  defaultWidth,
		height: defaultHeight,
		flash:  flash,
	}
}

// Chosen returns the picked action once the program has quit; ok is false
// when nothing was chosen.
func (h *Home) Chosen() (Action, bool) { return h.chosen, h.ok }

// Init has nothing to start.
func (h *Home) Init() tea.Cmd { return nil }

// Update handles window resizes and keys.
func (h *Home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h.width, h.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		k := msg.String()
		if isQuitKey(k) {
			h.quitAll = true
			return h, tea.Quit
		}
		if h.showHelp {
			h.showHelp = false
			return h, nil
		}
		if msg.Text == "?" {
			k = "?"
		}
		switch k {
		case "?":
			h.showHelp = true
		case "up", "ctrl+p":
			h.cursor = max(h.cursor-1, 0)
		case "down", "ctrl+n":
			h.cursor = min(h.cursor+1, len(h.rows)-1)
		case "enter":
			row := h.rows[h.cursor]
			if row.action == ActionQuit {
				h.quitAll = true
			} else {
				h.chosen, h.ok = row.action, true
			}
			return h, tea.Quit
		}
	}
	return h, nil
}

// View renders the whole screen in the alternate buffer.
func (h *Home) View() tea.View {
	v := tea.NewView(h.render())
	v.AltScreen = true
	return v
}

func (h *Home) render() string {
	if h.showHelp {
		return renderHelp(h.width, h.height, "Keys", homeKeys)
	}
	width := max(h.width, 1)
	lines := make([]string, 0, len(h.rows))
	for i, r := range h.rows {
		lines = append(lines, h.renderRow(r, i == h.cursor, width))
	}

	flash := ""
	if h.flash.Text != "" {
		st := h.styles.flash
		if h.flash.Err {
			st = h.styles.danger
		}
		flash = st.Render(output.Truncate(h.flash.Text, width))
	}

	var b strings.Builder
	b.WriteString(h.styles.title.Render("rootout"))
	b.WriteString("\n\n")
	b.WriteString(fillRows(lines, width, max(h.height-homeChromeRows, 1)))
	b.WriteString("\n")
	b.WriteString(flash)
	b.WriteString("\n")
	b.WriteString(h.styles.dim.Render(output.Truncate(homeHelp, width)))
	return b.String()
}

// renderRow draws one action: marker, title and a dim blurb truncated to fit.
// The cursor row is drawn in the inverted cursor style across the full width.
func (h *Home) renderRow(r homeRow, selected bool, width int) string {
	base, dim := lipgloss.NewStyle(), h.styles.dim
	marker := "  "
	if selected {
		base = h.styles.cursor
		dim = base.Faint(true)
		marker = "› "
	}
	avail := max(width-lipgloss.Width(marker), 4)
	title := output.Truncate(r.title, avail)

	var b strings.Builder
	b.WriteString(base.Render(marker + title))
	if room := avail - lipgloss.Width(title) - 2; room >= 4 {
		b.WriteString(base.Render("  "))
		b.WriteString(dim.Render(output.Truncate(r.blurb, room)))
	}
	return padWith(b.String(), width, base)
}
