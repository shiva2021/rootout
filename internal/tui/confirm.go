package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/purge/internal/leftover"
	"github.com/shiva2021/purge/internal/output"
	"github.com/shiva2021/purge/internal/plan"
)

const (
	confirmHelp       = "space toggle · a all · n none · enter confirm · esc cancel"
	nothingFlash      = "nothing selected"
	confirmChromeRows = 5 // header row, blank row, flash row, totals row, help row
	rowIndent         = "  "
	checkboxWidth     = 4 // "[x] "
)

// Confirmer implements plan.Confirmer with a full-screen review of the plan.
// It edits p.Items[i].Selected and p.BrewZap in place and never runs anything.
type Confirmer struct{}

var _ plan.Confirmer = Confirmer{}

// Confirm shows the review screen and reports whether the user accepted.
func (Confirmer) Confirm(ctx context.Context, p *plan.Plan) (bool, error) {
	c := NewConfirm(p)
	if _, err := tea.NewProgram(c, tea.WithContext(ctx)).Run(); err != nil {
		return false, fmt.Errorf("tui: confirm: %w", err)
	}
	return c.Accepted(), nil
}

// rowKind says what a screen row shows.
type rowKind int

const (
	rowBrew    rowKind = iota // "[x] brew uninstall --zap <cask> first"
	rowHeading                // Kind.String() group title
	rowItem                   // one plan item
	rowNote                   // Note under a login item
)

// row is one line of the scrolling list. item indexes plan.Items for
// rowItem and rowNote.
type row struct {
	kind rowKind
	item int
	text string
}

// confirmStyles extends the shared styles with the ones only this screen uses.
type confirmStyles struct {
	styles
	heading   lipgloss.Style // kind group title
	admin     lipgloss.Style // "[admin]" tag
	tag       lipgloss.Style // "[name]" / "[manual]" tags
	trash     lipgloss.Style // "→ Trash" badge
	permanent lipgloss.Style // "PERMANENT" badge
	dryRun    lipgloss.Style // "DRY RUN" badge
}

func defaultConfirmStyles() confirmStyles {
	return confirmStyles{
		styles:    defaultStyles(),
		heading:   lipgloss.NewStyle().Bold(true),
		admin:     lipgloss.NewStyle().Foreground(lipgloss.Yellow),
		tag:       lipgloss.NewStyle().Foreground(lipgloss.Magenta),
		trash:     lipgloss.NewStyle().Foreground(lipgloss.Black).Background(lipgloss.Cyan).Bold(true).Padding(0, 1),
		permanent: lipgloss.NewStyle().Foreground(lipgloss.White).Background(lipgloss.Red).Bold(true).Padding(0, 1),
		dryRun:    lipgloss.NewStyle().Faint(true).Padding(0, 1),
	}
}

// Confirm is the plan review model. It implements tea.Model and is exported
// so tests can drive it without a terminal.
type Confirm struct {
	plan   *plan.Plan
	rows   []row
	styles confirmStyles

	width, height int
	cursor, top   int // cursor indexes rows and is always a toggleable row (-1 when none)
	flash         string
	accepted      bool
}

// NewConfirm builds the review screen for p with the cursor on the first
// toggleable row.
func NewConfirm(p *plan.Plan) *Confirm {
	c := &Confirm{
		plan:   p,
		rows:   buildRows(p),
		styles: defaultConfirmStyles(),
		width:  defaultWidth,
		height: defaultHeight,
		cursor: -1,
	}
	for i := range c.rows {
		if c.toggleable(i) {
			c.cursor = i
			break
		}
	}
	c.scroll()
	return c
}

// Accepted reports whether the user confirmed the plan.
func (c *Confirm) Accepted() bool { return c.accepted }

// buildRows lays the plan out as rows: the brew toggle, then each kind's
// heading followed by its items, with a note line under login items.
func buildRows(p *plan.Plan) []row {
	var rows []row
	if p.App.Cask != "" {
		rows = append(rows, row{kind: rowBrew})
	}
	var cur leftover.Kind = -1
	for i, it := range p.Items {
		if it.Kind != cur {
			cur = it.Kind
			rows = append(rows, row{kind: rowHeading, text: cur.String()})
		}
		rows = append(rows, row{kind: rowItem, item: i})
		if it.Kind == leftover.LoginItem && it.Note != "" {
			rows = append(rows, row{kind: rowNote, item: i, text: it.Note})
		}
	}
	return rows
}

// Init implements tea.Model.
func (c *Confirm) Init() tea.Cmd { return nil }

// Update handles window resizes and keys.
func (c *Confirm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.width, c.height = msg.Width, msg.Height
		c.scroll()
		return c, nil

	case tea.KeyPressMsg:
		c.flash = ""
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			c.accepted = false
			return c, tea.Quit
		case "up", "ctrl+p":
			c.move(-1)
		case "down", "ctrl+n":
			c.move(1)
		case "pgup":
			c.move(-c.listRows())
		case "pgdown":
			c.move(c.listRows())
		case "space":
			c.toggle()
		case "a":
			c.selectAll(true)
		case "n":
			c.selectAll(false)
		case "enter":
			return c.confirm()
		}
	}
	return c, nil
}

// View renders the whole screen in the alternate buffer.
func (c *Confirm) View() tea.View {
	v := tea.NewView(c.render())
	v.AltScreen = true
	return v
}

// confirm accepts the plan unless nothing would happen.
func (c *Confirm) confirm() (tea.Model, tea.Cmd) {
	if c.selectedItems() == 0 && !c.plan.BrewZap {
		c.flash = nothingFlash
		return c, nil
	}
	c.accepted = true
	return c, tea.Quit
}

// selectedItems counts selected items the user could have toggled.
func (c *Confirm) selectedItems() int {
	n := 0
	for _, it := range c.plan.Items {
		if it.Selected && it.Kind != leftover.LoginItem {
			n++
		}
	}
	return n
}

// toggleable reports whether the cursor may rest on row i.
func (c *Confirm) toggleable(i int) bool {
	if i < 0 || i >= len(c.rows) {
		return false
	}
	switch r := c.rows[i]; r.kind {
	case rowBrew:
		return true
	case rowItem:
		return c.plan.Items[r.item].Kind != leftover.LoginItem
	}
	return false
}

// cursorItem returns the plan item index under the cursor, or -1 when the
// cursor is on the brew row or there is nothing to select.
func (c *Confirm) cursorItem() int {
	if c.cursor < 0 || c.rows[c.cursor].kind != rowItem {
		return -1
	}
	return c.rows[c.cursor].item
}

// toggle flips the row under the cursor.
func (c *Confirm) toggle() {
	if c.cursor < 0 {
		return
	}
	switch r := c.rows[c.cursor]; r.kind {
	case rowBrew:
		c.plan.BrewZap = !c.plan.BrewZap
	case rowItem:
		c.plan.Items[r.item].Selected = !c.plan.Items[r.item].Selected
	}
}

// selectAll sets Selected on every toggleable item; login items are left alone.
func (c *Confirm) selectAll(on bool) {
	for i := range c.plan.Items {
		if c.plan.Items[i].Kind != leftover.LoginItem {
			c.plan.Items[i].Selected = on
		}
	}
}

// move steps the cursor delta toggleable rows, skipping headings, notes and
// login items, and keeps it on screen.
func (c *Confirm) move(delta int) {
	if c.cursor < 0 {
		return
	}
	step := 1
	if delta < 0 {
		step, delta = -1, -delta
	}
	for ; delta > 0; delta-- {
		j := c.cursor + step
		for j >= 0 && j < len(c.rows) && !c.toggleable(j) {
			j += step
		}
		if j < 0 || j >= len(c.rows) {
			break
		}
		c.cursor = j
	}
	c.scroll()
}

// scroll adjusts top so the cursor row is visible.
func (c *Confirm) scroll() {
	rows := c.listRows()
	if c.cursor < c.top {
		c.top = c.cursor
	}
	if c.cursor >= c.top+rows {
		c.top = c.cursor - rows + 1
	}
	if c.top < 0 {
		c.top = 0
	}
}

// listRows is the number of list lines between header and footer.
func (c *Confirm) listRows() int {
	rows := c.height - confirmChromeRows
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (c *Confirm) render() string {
	var b strings.Builder
	b.WriteString(c.renderHeader())
	b.WriteString("\n\n")

	rows := c.listRows()
	n := 0
	for i := c.top; i < len(c.rows) && n < rows; i++ {
		b.WriteString(c.renderRow(i, c.width))
		b.WriteString("\n")
		n++
	}
	for ; n < rows; n++ {
		b.WriteString("\n")
	}

	if c.flash != "" {
		b.WriteString(c.styles.flash.Render(c.flash))
	}
	b.WriteString("\n")
	b.WriteString(c.renderTotals())
	b.WriteString("\n")
	b.WriteString(c.styles.dim.Render(confirmHelp))
	return b.String()
}

// renderHeader draws "Remove <Name> (<bundle id>)" and the mode badge.
func (c *Confirm) renderHeader() string {
	a := c.plan.App
	title := c.styles.title.Render(fmt.Sprintf("Remove %s (%s)", a.Name, orDash(a.BundleID)))
	var badge string
	switch c.plan.Mode {
	case plan.Permanent:
		badge = c.styles.permanent.Render("PERMANENT")
	case plan.DryRun:
		badge = c.styles.dryRun.Render("DRY RUN")
	default:
		badge = c.styles.trash.Render("→ Trash")
	}
	return title + "  " + badge
}

// renderTotals draws the live summary from plan.Totals.
func (c *Confirm) renderTotals() string {
	n, bytes, root := c.plan.Totals()
	s := fmt.Sprintf("%d paths · %s", n, output.Bytes(bytes))
	if root > 0 {
		s += fmt.Sprintf(" · %d need admin (deleted permanently)", root)
	}
	return s
}

// renderRow draws row i exactly width cells wide. The cursor row is drawn in
// the inverted cursor style end to end.
func (c *Confirm) renderRow(i, width int) string {
	r := c.rows[i]
	base := lipgloss.NewStyle()
	if i == c.cursor {
		base = c.styles.cursor
	}
	switch r.kind {
	case rowHeading:
		return padWith(c.styles.heading.Render(r.text), width, base)
	case rowNote:
		indent := rowIndent + strings.Repeat(" ", checkboxWidth+sizeWidth+1)
		return padWith(c.styles.dim.Render(indent+output.Truncate(r.text, max(width-len(indent), 1))), width, base)
	case rowBrew:
		text := "brew uninstall --zap " + c.plan.App.Cask + " first"
		return padWith(base.Render(rowIndent+checkbox(c.plan.BrewZap)+text), width, base)
	}
	return c.renderItem(c.plan.Items[r.item], base, i == c.cursor, width)
}

// renderItem draws one plan item: checkbox, right-aligned size, path and tags.
func (c *Confirm) renderItem(it plan.Item, base lipgloss.Style, selected bool, width int) string {
	dim, admin, tag := c.styles.dim, c.styles.admin, c.styles.tag
	if selected {
		dim = base.Faint(true)
		admin = base.Foreground(lipgloss.Yellow)
		tag = base.Foreground(lipgloss.Magenta)
	}
	login := it.Kind == leftover.LoginItem

	box := checkbox(it.Selected)
	if login {
		box = strings.Repeat(" ", checkboxWidth)
	}
	size := ""
	if it.Kind != leftover.Receipt && !login {
		size = output.Bytes(it.SizeBytes)
	}

	var tags strings.Builder
	tagsW := 0
	if it.NeedsRoot {
		tags.WriteString(base.Render(" ") + admin.Render("[admin]"))
		tagsW += 1 + len("[admin]")
	}
	if it.MatchedBy == leftover.ByName || it.MatchedBy == plan.ByManual {
		t := "[" + it.MatchedBy + "]"
		tags.WriteString(base.Render(" ") + tag.Render(t))
		tagsW += 1 + len(t)
	}

	prefix := rowIndent + box + fmt.Sprintf("%*s ", sizeWidth, size)
	avail := width - len([]rune(prefix)) - tagsW
	if avail < 4 {
		avail = 4
	}
	path := output.Truncate(it.Path, avail)

	var b strings.Builder
	b.WriteString(base.Render(prefix))
	if login {
		b.WriteString(dim.Render(path))
	} else {
		b.WriteString(base.Render(path))
	}
	b.WriteString(tags.String())
	return padWith(b.String(), width, base)
}

func checkbox(on bool) string {
	if on {
		return "[x] "
	}
	return "[ ] "
}

// padWith right-pads an already styled string to width cells, rendering the
// padding in base so an inverted cursor bar spans the whole row.
func padWith(s string, width int, base lipgloss.Style) string {
	if n := width - lipgloss.Width(s); n > 0 {
		return s + base.Render(strings.Repeat(" ", n))
	}
	return s
}
