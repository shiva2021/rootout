package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/output"
)

const (
	homeHelp       = "↑/↓ move · enter select · ctrl+x quit · ? help"
	homeChromeRows = 5 // title, AI status row, blank row, flash row, footer row
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
	ActionSetup
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
	{ActionSetup, "Set up AI", "optional: a local model or a Claude/OpenAI key for keep/uninstall recommendations"},
	{ActionQuit, "Quit", "leave rootout (ctrl+x works on every screen)"},
}

// noticeMsg carries the update notice once the notice func has returned.
type noticeMsg string

// PickAction shows the home screen (titled with version, e.g. "v0.3.0",
// with ai as the status line under the title) and returns the chosen
// action. notice, when not nil, runs in the background and its text (an
// update notice; "" for none) joins the title line when it arrives.
// It returns ErrQuit when the user quits (ctrl+x, ctrl+c or the
// Quit row).
func PickAction(ctx context.Context, version string, flash Flash, ai string, notice func() string) (Action, error) {
	h := NewHome(version, flash, ai, notice)
	if _, err := tea.NewProgram(h, tea.WithContext(ctx)).Run(); err != nil {
		return ActionNone, runErr(err, "home")
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
	version       string
	ai            string // status line under the title, e.g. "AI: not set up"
	notice        func() string
	noticeText    string // dim text after the version once notice has answered
	width, height int
	cursor        int
	flash         Flash
	showHelp      bool
	chosen        Action
	ok            bool
	quitAll       bool
}

// NewHome builds the home screen showing flash under the list. version is
// appended to the title when not empty; ai is the one-line AI status shown
// under the title; notice (may be nil) supplies the update notice.
func NewHome(version string, flash Flash, ai string, notice func() string) *Home {
	return &Home{
		rows:    homeRows,
		styles:  defaultStyles(),
		version: version,
		ai:      ai,
		notice:  notice,
		width:   defaultWidth,
		height:  defaultHeight,
		flash:   flash,
	}
}

// Chosen returns the picked action once the program has quit; ok is false
// when nothing was chosen.
func (h *Home) Chosen() (Action, bool) { return h.chosen, h.ok }

// Init starts the notice func, if any, so the screen never waits for it.
func (h *Home) Init() tea.Cmd {
	if h.notice == nil {
		return nil
	}
	notice := h.notice
	return func() tea.Msg { return noticeMsg(notice()) }
}

// Update handles window resizes, keys and the notice arriving.
func (h *Home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case noticeMsg:
		h.noticeText = string(msg)
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
	title := "rootout"
	if h.version != "" {
		title += " " + h.version
	}
	b.WriteString(h.styles.title.Render(title))
	// The notice shares the title line so the layout keeps its row count;
	// it is cut to fit and dropped when there is no room for a word.
	if room := width - lipgloss.Width(title) - 2; h.noticeText != "" && room >= 4 {
		b.WriteString("  ")
		b.WriteString(h.styles.dim.Render(output.Truncate(h.noticeText, room)))
	}
	b.WriteString("\n")
	b.WriteString(h.styles.dim.Render(output.Truncate(h.ai, width)))
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
