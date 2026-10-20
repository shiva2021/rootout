// Package tui holds the interactive terminal screens: the searchable app
// picker, the plan review, the scan review and the model picker. It is built
// on Bubble Tea v2.
package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/progress"
)

const (
	searchPrompt   = "Search apps › "
	helpText       = "↑/↓ move · enter select · esc quit"
	helpHint       = " · ? help"
	protectedFlash = "system apps cannot be removed"
	systemSuffix   = " (system)"
	loadLabel      = "Scanning applications"
	orphansTitle   = "Orphaned leftovers…"
	orphansBlurb   = "find files left by apps that are already gone"
	sizeWidth      = 8 // "123.4 GB"
	defaultWidth   = 80
	defaultHeight  = 24
	chromeRows     = 4 // search row, blank row, status row, footer row
	minPreviewCols = 60
)

// pickerKeys is the ? overlay of the app picker.
var pickerKeys = []keyHelp{
	{"type", "filter the list"},
	{"↑/↓", "move"},
	{"pgup/pgdn", "page"},
	{"enter", "select"},
	{"esc", "quit"},
	{"?", "this help (when the search is empty)"},
}

// Pick is what the picker returns.
type Pick struct {
	App     app.App
	Orphans bool // the user chose the "Orphaned leftovers…" row
}

// PickOptions configures the picker.
type PickOptions struct {
	OrphansRow bool // pin an "Orphaned leftovers…" row above the apps
}

// LoadFunc discovers the apps while the picker is already on screen. It
// receives a Reporter whose Start/Step drive the progress bar.
type LoadFunc func(progress.Reporter) ([]app.App, error)

type (
	// loadProgressMsg mirrors one Reporter call from the load goroutine.
	loadProgressMsg struct {
		id          int
		label       string
		done, total int
		detail      string
	}
	// loadedMsg ends the load.
	loadedMsg struct {
		id   int
		apps []app.App
		err  error
	}
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

// PickAppLoad starts the picker immediately and runs load inside the program,
// showing a progress bar until the apps arrive. ok=false means the user
// cancelled; a load failure is shown on screen and returned once the user
// leaves.
func PickAppLoad(ctx context.Context, load LoadFunc, opts PickOptions) (Pick, bool, error) {
	p := NewPickerLoading(load, opts)
	p.ctx = ctx
	if _, err := tea.NewProgram(p, tea.WithContext(ctx)).Run(); err != nil {
		return Pick{}, false, fmt.Errorf("tui: pick app: %w", err)
	}
	if p.loadErr != nil {
		return Pick{}, false, fmt.Errorf("tui: pick app: %w", p.loadErr)
	}
	pick, ok := p.Picked()
	return pick, ok, nil
}

// Picker is the app picker model. It implements tea.Model and is exported so
// tests and later screens can drive it without a terminal.
type Picker struct {
	apps    []app.App
	matches []app.Match
	input   textinput.Model
	styles  styles
	opts    PickOptions

	// Loading state; load is nil when the apps were given up front.
	ctx        context.Context
	load       LoadFunc
	loading    bool
	loadID     int
	loadCancel context.CancelFunc
	loadWait   tea.Cmd
	loadLabel  string
	loadDone   int
	loadTotal  int
	loadDetail string
	loadErr    error

	width, height int
	cursor, top   int // cursor indexes list rows (orphans row first, then matches); top is the first visible row
	flash         string
	showHelp      bool
	picked        Pick
	ok            bool
}

// NewPicker builds the model with the search box focused and every app listed.
func NewPicker(apps []app.App) *Picker {
	p := newPicker(PickOptions{})
	p.setApps(apps)
	return p
}

// NewPickerLoading builds the model in its loading state; Init starts load.
func NewPickerLoading(load LoadFunc, opts PickOptions) *Picker {
	p := newPicker(opts)
	p.load = load
	p.loading = true
	return p
}

func newPicker(opts PickOptions) *Picker {
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
		input:     in,
		styles:    s,
		opts:      opts,
		ctx:       context.Background(),
		loadLabel: loadLabel,
		width:     defaultWidth,
		height:    defaultHeight,
	}
	p.layout()
	return p
}

// setApps installs the loaded apps and applies whatever query was typed so far.
func (p *Picker) setApps(apps []app.App) {
	p.apps = apps
	p.matches = app.Fuzzy(apps, p.input.Value())
	p.cursor, p.top = 0, 0
	p.layout()
}

// Selected returns the chosen app once the program has quit; ok is false when
// the user cancelled or chose the orphans row (see Picked).
func (p *Picker) Selected() (app.App, bool) { return p.picked.App, p.ok && !p.picked.Orphans }

// Picked returns the full result once the program has quit; ok is false when
// the user cancelled.
func (p *Picker) Picked() (Pick, bool) { return p.picked, p.ok }

// Matches returns the current filtered list, best first.
func (p *Picker) Matches() []app.Match { return p.matches }

// Cursor returns the index into Matches of the highlighted row, or -1 when
// the orphans row is highlighted.
func (p *Picker) Cursor() int { return p.cursor - p.orphanRows() }

// Init starts the search cursor blinking and, when loading, the load.
func (p *Picker) Init() tea.Cmd {
	if p.load == nil {
		return textinput.Blink
	}
	return tea.Batch(textinput.Blink, p.startLoad())
}

// Update handles window resizes, load messages and keys; everything else is
// forwarded to the search box.
func (p *Picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
		p.layout()
		return p, nil

	case loadProgressMsg:
		if msg.id != p.loadID || !p.loading {
			return p, nil
		}
		if msg.label != "" {
			p.loadLabel = msg.label
		}
		p.loadDone, p.loadTotal, p.loadDetail = msg.done, msg.total, msg.detail
		return p, p.loadWait

	case loadedMsg:
		if msg.id != p.loadID || !p.loading {
			return p, nil
		}
		p.cancelLoad()
		p.loading = false
		if msg.err != nil {
			p.loadErr = msg.err
			return p, nil
		}
		p.setApps(msg.apps)
		return p, nil

	case tea.KeyPressMsg:
		if p.showHelp {
			p.showHelp = false
			return p, nil
		}
		p.flash = ""
		k := msg.String()
		if msg.Text == "?" {
			k = "?"
		}
		switch k {
		case "ctrl+c", "esc":
			return p, p.quit()
		case "q":
			if p.input.Value() == "" {
				return p, p.quit()
			}
		case "?":
			if p.input.Value() == "" {
				p.showHelp = true
				return p, nil
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

// startLoad runs load in a goroutine and bridges its Reporter calls to tea
// messages through a channel read by a re-arming Cmd.
func (p *Picker) startLoad() tea.Cmd {
	p.cancelLoad()
	p.loadID++
	id := p.loadID
	ctx, cancel := context.WithCancel(p.ctx)
	p.loadCancel = cancel

	ch := make(chan tea.Msg)
	r := &loadReporter{ctx: ctx, ch: ch, id: id}
	load := p.load
	go func() {
		apps, err := load(r)
		r.send(loadedMsg{id: id, apps: apps, err: err})
	}()
	p.loadWait = waitLoad(ctx, ch)
	return p.loadWait
}

// waitLoad reads the next message from the load goroutine. Update re-arms
// it after every progress message.
func waitLoad(ctx context.Context, ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-ch:
			return msg
		case <-ctx.Done():
			return nil
		}
	}
}

func (p *Picker) cancelLoad() {
	if p.loadCancel != nil {
		p.loadCancel()
		p.loadCancel = nil
	}
	p.loadWait = nil
}

// quit stops a running load and ends the program.
func (p *Picker) quit() tea.Cmd {
	p.cancelLoad()
	return tea.Quit
}

// loadReporter implements progress.Reporter by sending loadProgressMsg on a
// channel. Step may be called from several workers at once.
type loadReporter struct {
	ctx context.Context
	ch  chan<- tea.Msg
	id  int

	mu          sync.Mutex
	label       string
	done, total int
}

func (r *loadReporter) Start(label string, total int) {
	r.mu.Lock()
	r.label, r.done, r.total = label, 0, total
	msg := r.progress("")
	r.mu.Unlock()
	r.send(msg)
}

func (r *loadReporter) Step(detail string) {
	r.mu.Lock()
	r.done++
	msg := r.progress(detail)
	r.mu.Unlock()
	r.send(msg)
}

func (r *loadReporter) Done() {}

// progress snapshots the counters; the caller holds r.mu.
func (r *loadReporter) progress(detail string) loadProgressMsg {
	return loadProgressMsg{id: r.id, label: r.label, done: r.done, total: r.total, detail: detail}
}

func (r *loadReporter) send(msg tea.Msg) {
	select {
	case r.ch <- msg:
	case <-r.ctx.Done():
	}
}

// orphanRows is 1 when the pinned orphans row is showing (option on, no
// query, apps loaded), else 0.
func (p *Picker) orphanRows() int {
	if p.opts.OrphansRow && p.input.Value() == "" && !p.loading && p.loadErr == nil {
		return 1
	}
	return 0
}

// rowCount is the number of list rows: the orphans row plus the matches.
func (p *Picker) rowCount() int { return p.orphanRows() + len(p.matches) }

// choose selects the cursor row, or flashes a message when it is protected.
func (p *Picker) choose() (tea.Model, tea.Cmd) {
	if p.loading || p.loadErr != nil || p.rowCount() == 0 {
		return p, nil
	}
	off := p.orphanRows()
	if p.cursor < off {
		p.picked, p.ok = Pick{Orphans: true}, true
		return p, tea.Quit
	}
	a := p.matches[p.cursor-off].App
	if a.Protected {
		p.flash = protectedFlash
		return p, nil
	}
	p.picked, p.ok = Pick{App: a}, true
	return p, tea.Quit
}

// move shifts the cursor by delta, clamped, and keeps it on screen.
func (p *Picker) move(delta int) {
	p.cursor += delta
	if n := p.rowCount(); p.cursor >= n {
		p.cursor = n - 1
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
	if p.showHelp {
		return renderHelp(p.width, p.height, "Keys", pickerKeys)
	}
	rows := p.listRows()
	leftW, rightW := p.columns()

	var b strings.Builder
	b.WriteString(p.input.View())
	b.WriteString("\n\n")

	var body string
	switch {
	case p.loading:
		body = p.renderLoading(leftW, rows)
	case p.loadErr != nil:
		body = p.renderLoadError(leftW, rows)
	default:
		body = p.renderList(leftW, rows)
	}
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
	if p.rowCount() > 0 {
		pos = p.cursor + 1
	}
	help := helpText
	if p.input.Value() == "" {
		help += helpHint
	}
	footer := fmt.Sprintf("%d of %d  %s", pos, p.rowCount(), help)
	b.WriteString(p.styles.dim.Render(output.Truncate(footer, max(p.width, 1))))
	return b.String()
}

// renderLoading fills the list area with the progress line.
func (p *Picker) renderLoading(width, rows int) string {
	lines := []string{padTo(renderCount(width, p.loadLabel, p.loadDone, p.loadTotal, p.loadDetail), width)}
	return fillRows(lines, width, rows)
}

// renderLoadError fills the list area with the load error in red.
func (p *Picker) renderLoadError(width, rows int) string {
	msg := output.Truncate("could not load apps: "+p.loadErr.Error(), max(width, 1))
	lines := []string{padTo(p.styles.danger.Render(msg), width)}
	return fillRows(lines, width, rows)
}

// renderList draws rows list lines, each exactly width cells wide.
func (p *Picker) renderList(width, rows int) string {
	lines := make([]string, 0, rows)
	off, n := p.orphanRows(), p.rowCount()
	if n == 0 {
		lines = append(lines, padTo(p.styles.noMatch.Render("no matching apps"), width))
	}
	for i := p.top; i < n && len(lines) < rows; i++ {
		if i < off {
			lines = append(lines, p.renderOrphansRow(i == p.cursor, width))
			continue
		}
		lines = append(lines, p.renderRow(p.matches[i-off], i == p.cursor, width))
	}
	return fillRows(lines, width, rows)
}

// fillRows pads lines with blank rows to exactly rows lines.
func fillRows(lines []string, width, rows int) string {
	for len(lines) < rows {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// renderOrphansRow draws the pinned "Orphaned leftovers…" row: accent title,
// dim blurb truncated to fit.
func (p *Picker) renderOrphansRow(selected bool, width int) string {
	base, accent, dim := lipgloss.NewStyle(), p.styles.match, p.styles.dim
	marker := "  "
	if selected {
		base = p.styles.cursor
		accent = base.Bold(true).Foreground(lipgloss.Cyan)
		dim = base.Faint(true)
		marker = "› "
	}
	avail := max(width-lipgloss.Width(marker), 4)
	title := output.Truncate(orphansTitle, avail)

	var b strings.Builder
	b.WriteString(base.Render(marker))
	b.WriteString(accent.Render(title))
	if room := avail - lipgloss.Width(title) - 2; room >= 4 {
		b.WriteString(base.Render("  "))
		b.WriteString(dim.Render(output.Truncate(orphansBlurb, room)))
	}
	return padWith(b.String(), width, base)
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

// renderPreview draws the bordered detail pane for the cursor row, exactly
// width cells wide and rows lines tall.
func (p *Picker) renderPreview(width, rows int) string {
	// In lipgloss v2 Width/Height are the final block size, border included.
	pane := p.styles.pane.Width(width).Height(rows).MaxHeight(rows)
	if p.loading || p.loadErr != nil || p.rowCount() == 0 {
		return pane.Render(p.styles.noMatch.Render("Nothing to show"))
	}
	if p.cursor < p.orphanRows() {
		lines := []string{
			p.styles.title.Render(strings.TrimSuffix(orphansTitle, "…")),
			"",
			"Files under ~/Library whose owner app is no longer installed.",
			"Nothing is ticked until you choose.",
		}
		return pane.Render(strings.Join(lines, "\n"))
	}
	a := p.matches[p.cursor-p.orphanRows()].App
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
