// Package tui holds the interactive terminal screens: a searchable app picker
// now, confirmation and progress screens later. It is built on Bubble Tea v2.
package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/purge/internal/app"
	"github.com/shiva2021/purge/internal/output"
)

const (
	searchPrompt   = "Search apps › "
	helpText       = "↑/↓ move · enter select · esc quit"
	protectedFlash = "system apps cannot be removed"
	systemSuffix   = " (system)"
	sizeWidth      = 8 // "123.4 GB"
	defaultWidth   = 80
	defaultHeight  = 24
	chromeRows     = 4 // search row, blank row, status row, footer row
	minPreviewCols = 60
)

// PickApp shows a full-screen searchable list of apps and returns the one the
// user chose. ok=false means the user cancelled (esc / q / ctrl+c).
func PickApp(ctx context.Context, apps []app.App) (chosen app.App, ok bool, err error) {
	p := NewPicker(apps)
	if _, err := tea.NewProgram(p, tea.WithContext(ctx)).Run(); err != nil {
		return app.App{}, false, fmt.Errorf("tui: pick app: %w", err)
	}
	chosen, ok = p.Selected()
	return chosen, ok, nil
}

// Picker is the app picker model. It implements tea.Model and is exported so
// tests and later screens can drive it without a terminal.
type Picker struct {
	apps    []app.App
	matches []app.Match
	input   textinput.Model
	styles  styles

	width, height int
	cursor, top   int // cursor index into matches; top is the first visible row
	flash         string
	chosen        app.App
	ok            bool
}

// NewPicker builds the model with the search box focused and every app listed.
func NewPicker(apps []app.App) *Picker {
	s := defaultStyles()
	in := textinput.New()
	in.Prompt = searchPrompt
	in.Placeholder = "type to filter"
	st := in.Styles()
	st.Focused.Prompt = s.prompt
	st.Focused.Placeholder = s.dim
	st.Focused.Text = lipgloss.NewStyle()
	in.SetStyles(st)
	in.Focus()

	p := &Picker{
		apps:   apps,
		input:  in,
		styles: s,
		width:  defaultWidth,
		height: defaultHeight,
	}
	p.matches = app.Fuzzy(apps, "")
	p.layout()
	return p
}

// Selected returns the chosen app once the program has quit; ok is false when
// the user cancelled.
func (p *Picker) Selected() (app.App, bool) { return p.chosen, p.ok }

// Matches returns the current filtered list, best first.
func (p *Picker) Matches() []app.Match { return p.matches }

// Cursor returns the index into Matches of the highlighted row.
func (p *Picker) Cursor() int { return p.cursor }

// Init starts the search cursor blinking.
func (p *Picker) Init() tea.Cmd { return textinput.Blink }

// Update handles window resizes and keys; everything else is forwarded to the
// search box.
func (p *Picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
		p.layout()
		return p, nil

	case tea.KeyPressMsg:
		p.flash = ""
		switch msg.String() {
		case "ctrl+c", "esc":
			return p, tea.Quit
		case "q":
			if p.input.Value() == "" {
				return p, tea.Quit
			}
		case "up", "ctrl+p":
			p.move(-1)
			return p, nil
		case "down", "ctrl+n":
			p.move(1)
			return p, nil
		case "pgup":
			p.move(-p.listRows())
			return p, nil
		case "pgdown":
			p.move(p.listRows())
			return p, nil
		case "enter":
			return p.choose()
		}
	}

	before := p.input.Value()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	if q := p.input.Value(); q != before {
		p.matches = app.Fuzzy(p.apps, q)
		p.cursor, p.top = 0, 0
	}
	return p, cmd
}

// View renders the whole screen in the alternate buffer.
func (p *Picker) View() tea.View {
	v := tea.NewView(p.render())
	v.AltScreen = true
	return v
}

// choose selects the cursor app, or flashes a message when it is protected.
func (p *Picker) choose() (tea.Model, tea.Cmd) {
	if len(p.matches) == 0 {
		return p, nil
	}
	a := p.matches[p.cursor].App
	if a.Protected {
		p.flash = protectedFlash
		return p, nil
	}
	p.chosen, p.ok = a, true
	return p, tea.Quit
}

// move shifts the cursor by delta, clamped, and keeps it on screen.
func (p *Picker) move(delta int) {
	p.cursor += delta
	if p.cursor >= len(p.matches) {
		p.cursor = len(p.matches) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	p.scroll()
}

// scroll adjusts top so the cursor row is visible.
func (p *Picker) scroll() {
	rows := p.listRows()
	if p.cursor < p.top {
		p.top = p.cursor
	}
	if p.cursor >= p.top+rows {
		p.top = p.cursor - rows + 1
	}
	if p.top < 0 {
		p.top = 0
	}
}

// layout recomputes derived sizes after a resize.
func (p *Picker) layout() {
	w := p.width - lipgloss.Width(searchPrompt) - 1
	if w < 10 {
		w = 10
	}
	p.input.SetWidth(w)
	p.scroll()
}

// listRows is the number of list lines that fit between header and footer.
func (p *Picker) listRows() int {
	rows := p.height - chromeRows
	if rows < 1 {
		rows = 1
	}
	return rows
}

// columns returns the width of the list and of the preview pane. Narrow
// terminals drop the preview entirely.
func (p *Picker) columns() (left, right int) {
	if p.width < minPreviewCols {
		return p.width, 0
	}
	left = p.width * 55 / 100
	return left, p.width - left
}

func (p *Picker) render() string {
	rows := p.listRows()
	leftW, rightW := p.columns()

	var b strings.Builder
	b.WriteString(p.input.View())
	b.WriteString("\n\n")

	body := p.renderList(leftW, rows)
	if rightW > 0 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, p.renderPreview(rightW, rows))
	}
	b.WriteString(body)
	b.WriteString("\n")

	if p.flash != "" {
		b.WriteString(p.styles.flash.Render(p.flash))
	}
	b.WriteString("\n")

	pos := 0
	if len(p.matches) > 0 {
		pos = p.cursor + 1
	}
	b.WriteString(p.styles.dim.Render(fmt.Sprintf("%d of %d  %s", pos, len(p.matches), helpText)))
	return b.String()
}

// renderList draws rows list lines, each exactly width cells wide.
func (p *Picker) renderList(width, rows int) string {
	lines := make([]string, 0, rows)
	if len(p.matches) == 0 {
		lines = append(lines, padTo(p.styles.noMatch.Render("no matching apps"), width))
	}
	for i := p.top; i < len(p.matches) && len(lines) < rows; i++ {
		lines = append(lines, p.renderRow(p.matches[i], i == p.cursor, width))
	}
	for len(lines) < rows {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// renderRow draws one app: marker, name with highlighted runes, dim bundle id
// truncated to fit, and the size right-aligned. The selected row is drawn
// entirely in the inverted cursor style so the bar spans the full width.
func (p *Picker) renderRow(m app.Match, selected bool, width int) string {
	base, match, dim := lipgloss.NewStyle(), p.styles.match, p.styles.dim
	if selected {
		base = p.styles.cursor
		match = base.Bold(true).Foreground(lipgloss.Cyan)
		dim = base.Faint(true)
	}
	if m.App.Protected {
		match = dim
	}

	marker := "  "
	if selected {
		marker = "› "
	}
	avail := width - lipgloss.Width(marker) - 1 - sizeWidth
	if avail < 4 {
		avail = 4
	}

	name := []rune(m.App.Name)
	suffix := ""
	if m.App.Protected {
		suffix = systemSuffix
	}
	maxName := avail - len([]rune(suffix))
	if maxName < 1 {
		maxName = 1
	}
	if len(name) > maxName {
		name = []rune(output.Truncate(string(name), maxName))
	}
	nameW := len(name) + len([]rune(suffix))

	bundle := ""
	if room := avail - nameW - 1; room >= 4 && m.App.BundleID != "" {
		bundle = output.Truncate(m.App.BundleID, room)
	}
	gap := avail - nameW
	if bundle != "" {
		gap -= 1 + len([]rune(bundle))
	}
	if gap < 0 {
		gap = 0
	}

	var b strings.Builder
	b.WriteString(base.Render(marker))
	if m.App.Protected {
		b.WriteString(dim.Render(string(name) + suffix))
	} else {
		b.WriteString(highlight(name, m.MatchedIndexes, base, match))
	}
	if bundle != "" {
		b.WriteString(base.Render(" "))
		b.WriteString(dim.Render(bundle))
	}
	b.WriteString(base.Render(strings.Repeat(" ", gap) + " "))
	b.WriteString(dim.Render(fmt.Sprintf("%*s", sizeWidth, output.Bytes(m.App.SizeBytes))))
	return padTo(b.String(), width)
}

// highlight renders name with the runes at idx in the match style and the
// rest in base, grouping consecutive runes so escape codes stay sparse.
func highlight(name []rune, idx []int, base, match lipgloss.Style) string {
	hit := make(map[int]bool, len(idx))
	for _, i := range idx {
		hit[i] = true
	}
	var b strings.Builder
	for i := 0; i < len(name); {
		j := i
		for j < len(name) && hit[j] == hit[i] {
			j++
		}
		seg := string(name[i:j])
		if hit[i] {
			b.WriteString(match.Render(seg))
		} else {
			b.WriteString(base.Render(seg))
		}
		i = j
	}
	return b.String()
}

// renderPreview draws the bordered detail pane for the cursor app, exactly
// width cells wide and rows lines tall.
func (p *Picker) renderPreview(width, rows int) string {
	// In lipgloss v2 Width/Height are the final block size, border included.
	pane := p.styles.pane.Width(width).Height(rows).MaxHeight(rows)
	if len(p.matches) == 0 {
		return pane.Render(p.styles.noMatch.Render("Nothing to show"))
	}
	a := p.matches[p.cursor].App
	label := func(k, v string) string {
		return p.styles.dim.Render(fmt.Sprintf("%-10s", k)) + v
	}
	lines := []string{
		p.styles.title.Render(a.Name),
		"",
		label("Bundle ID", orDash(a.BundleID)),
		label("Version", orDash(a.Version)),
		label("Path", a.Path),
		label("Size", output.Bytes(a.SizeBytes)),
	}
	if a.Cask != "" {
		lines = append(lines, "", "Installed via Homebrew cask "+a.Cask)
	}
	if a.Protected {
		lines = append(lines, "", p.styles.danger.Render("Protected: system application, cannot be removed"))
	}
	return pane.Render(strings.Join(lines, "\n"))
}

// padTo right-pads an already styled string with spaces to width cells.
func padTo(s string, width int) string {
	if n := width - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
