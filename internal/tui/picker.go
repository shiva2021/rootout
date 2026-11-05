// Package tui holds the interactive terminal screens: the searchable app
// picker, the plan review, the scan review and the model picker. It is built
// on Bubble Tea v2.
package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/progress"
)

const (
	searchPrompt   = "Search apps › "
	helpText       = "↑/↓ move · enter select · esc back · ctrl+x quit"
	helpAsk        = " · ctrl+l ask AI"
	helpHint       = " · ? help"
	protectedFlash = "system apps cannot be removed"
	protectedAsk   = "system app: nothing to assess"
	askingLabel    = "asking the AI…"
	systemSuffix   = " (system)"
	loadLabel      = "Scanning applications"
	sizeWidth      = 8 // "123.4 GB"
	defaultWidth   = 80
	defaultHeight  = 24
	chromeRows     = 4 // search row, blank row, status row, footer row
	minPreviewCols = 60
	// previewLabelW fits the longest fact label ("Could not run") so the
	// basic lines and the fact lines share one value column.
	previewLabelW = 14
)

// pickerKeys is the ? overlay of the app picker.
var pickerKeys = []keyHelp{
	{"type", "filter the list"},
	{"↑/↓", "move"},
	{"pgup/pgdn", "page"},
	{"enter", "select"},
	{"esc", "back to the home screen"},
	{"ctrl+x", "quit rootout"},
	{"?", "this help (when the search is empty)"},
}

// pickerAskKey is the ? row added when an AI model is configured.
var pickerAskKey = keyHelp{"ctrl+l", "ask the configured AI what the highlighted app is for, whether its source is legitimate and whether to keep it (shown in the preview)"}

// pickerFactsMsg carries one app's gathered facts back to the picker,
// keyed by bundle path; the picker then asks the model about them.
type pickerFactsMsg struct {
	path string
	name string
	f    app.Facts
}

// pickerAssessMsg carries the model's answer about one app back to the
// picker, keyed by bundle path.
type pickerAssessMsg struct {
	path string
	name string
	a    llm.AppAssessment
	err  error
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
// user chose. ok=false means the user cancelled (esc / q); ctrl+x / ctrl+c return ErrQuit.
func PickApp(ctx context.Context, apps []app.App) (chosen app.App, ok bool, err error) {
	p := NewPicker(apps)
	if _, err := tea.NewProgram(p, tea.WithContext(ctx)).Run(); err != nil {
		return app.App{}, false, runErr(err, "pick app")
	}
	if p.quitAll {
		return app.App{}, false, ErrQuit
	}
	chosen, ok = p.Selected()
	return chosen, ok, nil
}

// PickAppLoad starts the picker immediately and runs load inside the program,
// showing a progress bar until the apps arrive. a, when not nil, lets
// ctrl+l ask the AI about the highlighted app. ok=false means the user
// cancelled (ctrl+x and ctrl+c return ErrQuit); a load failure is shown on
// screen and returned once the user leaves.
func PickAppLoad(ctx context.Context, load LoadFunc, a *AppAssessor) (app.App, bool, error) {
	// Cancelled on return so a ctrl+l call still in flight stops with the
	// screen instead of finishing (and paying) for nobody.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := NewPickerLoading(load)
	p.ctx = ctx
	p.SetAssessor(a)
	if _, err := tea.NewProgram(p, tea.WithContext(ctx)).Run(); err != nil {
		return app.App{}, false, runErr(err, "pick app")
	}
	if p.quitAll {
		return app.App{}, false, ErrQuit
	}
	if p.loadErr != nil {
		return app.App{}, false, fmt.Errorf("tui: pick app: %w", p.loadErr)
	}
	chosen, ok := p.Selected()
	return chosen, ok, nil
}

// Picker is the app picker model. It implements tea.Model and is exported so
// tests and later screens can drive it without a terminal.
type Picker struct {
	apps    []app.App
	matches []app.Match
	input   textinput.Model
	styles  styles

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

	// AI: nil assessor hides ctrl+l; state is keyed by bundle path so it
	// survives re-filtering.
	assessor *AppAssessor
	rec      map[string]lipgloss.Style // recommendation colours
	spin     spinner.Model
	facts    map[string]app.Facts // gathered on ctrl+l, shown under the answer
	results  map[string]llm.AppAssessment
	errs     map[string]error
	asking   map[string]bool

	width, height int
	cursor, top   int // cursor indexes matches; top is the first visible row
	flash         string
	showHelp      bool
	quitAll       bool // ctrl+x / ctrl+c: leave rootout, not just this screen
	picked        app.App
	ok            bool
}

// SetAssessor enables ctrl+l with a (nil disables it). An assessor without
// a model (facts only) also disables it: the picker shows no facts.
func (p *Picker) SetAssessor(a *AppAssessor) {
	if a == nil || a.Assess == nil {
		p.assessor = nil
		return
	}
	p.assessor = a
}

// NewPicker builds the model with the search box focused and every app listed.
func NewPicker(apps []app.App) *Picker {
	p := newPicker()
	p.setApps(apps)
	return p
}

// NewPickerLoading builds the model in its loading state; Init starts load.
func NewPickerLoading(load LoadFunc) *Picker {
	p := newPicker()
	p.load = load
	p.loading = true
	return p
}

func newPicker() *Picker {
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

	sc := defaultScanStyles()
	p := &Picker{
		input:     in,
		styles:    s,
		ctx:       context.Background(),
		loadLabel: loadLabel,
		width:     defaultWidth,
		height:    defaultHeight,
		rec:       map[string]lipgloss.Style{llm.Keep: sc.verdict[llm.Benign], llm.Drop: sc.verdict[llm.Malicious], llm.Review: sc.verdict[llm.Suspicious]},
		spin:      spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		facts:     map[string]app.Facts{},
		results:   map[string]llm.AppAssessment{},
		errs:      map[string]error{},
		asking:    map[string]bool{},
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
// the user cancelled.
func (p *Picker) Selected() (app.App, bool) { return p.picked, p.ok }

// Matches returns the current filtered list, best first.
func (p *Picker) Matches() []app.Match { return p.matches }

// Cursor returns the index into Matches of the highlighted row.
func (p *Picker) Cursor() int { return p.cursor }

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

	case pickerFactsMsg:
		p.facts[msg.path] = msg.f
		return p, p.askModel(msg.path, msg.name, msg.f)

	case pickerAssessMsg:
		delete(p.asking, msg.path)
		if msg.err != nil {
			p.errs[msg.path] = msg.err
		} else {
			delete(p.errs, msg.path)
			p.results[msg.path] = msg.a
		}
		if p.narrow() {
			// No preview pane: the outcome goes on the flash line, named,
			// since the cursor may have moved on.
			if msg.err != nil {
				p.flash = msg.name + ": AI error: " + strings.TrimPrefix(msg.err.Error(), "llm: ")
			} else {
				p.flash = msg.name + ": " + answerLine(msg.a)
			}
		}
		return p, nil

	case spinner.TickMsg:
		if len(p.asking) == 0 {
			return p, nil
		}
		var cmd tea.Cmd
		p.spin, cmd = p.spin.Update(msg)
		return p, cmd

	case tea.KeyPressMsg:
		if isQuitKey(msg.String()) {
			p.quitAll = true
			return p, p.quit()
		}
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
		case "esc":
			return p, p.quit()
		case "ctrl+l":
			// Before the search box, so it works with a filter typed; the
			// box has no binding on it.
			return p, p.ask()
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

// ask asks the AI about the highlighted app in two steps: the facts first
// (through the assessor's Inspect, shown as soon as they arrive), then the
// saved answer or the model. Nothing happens without an assessor, without
// a row, for a protected app, or for an app already answered or being
// asked; an errored app is asked again.
func (p *Picker) ask() tea.Cmd {
	if p.assessor == nil || p.loading || p.rowCount() == 0 {
		return nil
	}
	a := p.matches[p.cursor].App
	if a.Protected {
		p.flash = protectedAsk
		return nil
	}
	path := a.Path
	if _, done := p.results[path]; done || p.asking[path] {
		return nil
	}
	idle := len(p.asking) == 0
	p.asking[path] = true
	delete(p.errs, path)
	if p.narrow() {
		p.flash = a.Name + ": " + askingLabel
	}
	var cmd tea.Cmd
	if f, ok := p.facts[path]; ok {
		// A retry after an error: the facts are at hand, and probing again
		// would cost seconds and possibly a call to Apple.
		cmd = p.askModel(path, a.Name, f)
	} else {
		ctx, as := p.ctx, *p.assessor
		cmd = func() tea.Msg {
			return pickerFactsMsg{path: path, name: a.Name, f: as.inspect(ctx, a)}
		}
	}
	if idle {
		return tea.Batch(cmd, p.spin.Tick)
	}
	return cmd
}

// askModel is ask's second step: the saved answer for the facts when there
// is one, else the model.
func (p *Picker) askModel(path, name string, f app.Facts) tea.Cmd {
	ctx, as := p.ctx, *p.assessor
	return func() tea.Msg {
		if as.Cached != nil {
			if r, ok := as.Cached(f); ok {
				return pickerAssessMsg{path: path, name: name, a: r}
			}
		}
		r, err := as.Assess(ctx, f)
		return pickerAssessMsg{path: path, name: name, a: r, err: err}
	}
}

// narrow reports whether the terminal is too narrow for the preview pane.
func (p *Picker) narrow() bool {
	_, right := p.columns()
	return right == 0
}

// answerLine is the one-line form of an answer: "KEEP 85% · purpose".
func answerLine(a llm.AppAssessment) string {
	line := fmt.Sprintf("%s %.0f%%", strings.ToUpper(a.Recommendation), a.Confidence*100)
	if a.Purpose != "" {
		line += labelSep + a.Purpose
	}
	return line
}

// helpLine is the footer text, with ctrl+l when an AI is at hand.
func (p *Picker) helpLine() string {
	if p.assessor != nil {
		return helpText + helpAsk
	}
	return helpText
}

// helpKeys is the ? overlay table, with ctrl+l when an AI is at hand.
func (p *Picker) helpKeys() []keyHelp {
	if p.assessor == nil {
		return pickerKeys
	}
	return append(slices.Clone(pickerKeys), pickerAskKey)
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

// rowCount is the number of list rows: the matches.
func (p *Picker) rowCount() int { return len(p.matches) }

// choose selects the cursor row, or flashes a message when it is protected.
func (p *Picker) choose() (tea.Model, tea.Cmd) {
	if p.loading || p.loadErr != nil || p.rowCount() == 0 {
		return p, nil
	}
	a := p.matches[p.cursor].App
	if a.Protected {
		p.flash = protectedFlash
		return p, nil
	}
	p.picked, p.ok = a, true
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
		return renderHelp(p.width, p.height, "Keys", p.helpKeys())
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
		b.WriteString(p.styles.flash.Render(output.Truncate(p.flash, max(p.width, 1))))
	}
	b.WriteString("\n")

	pos := 0
	if p.rowCount() > 0 {
		pos = p.cursor + 1
	}
	help := p.helpLine()
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
	n := p.rowCount()
	if n == 0 {
		lines = append(lines, padTo(p.styles.noMatch.Render("no matching apps"), width))
	}
	for i := p.top; i < n && len(lines) < rows; i++ {
		lines = append(lines, p.renderRow(p.matches[i], i == p.cursor, width))
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
	a := p.matches[p.cursor].App
	label := func(k, v string) string {
		return p.styles.dim.Render(fmt.Sprintf("%-*s", previewLabelW, k)) + v
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
	// The answer before the facts: the pane does not scroll, so on a short
	// terminal it is the facts that get clipped, never the verdict.
	if ai := p.aiLines(a.Path, width-4); len(ai) > 0 {
		lines = append(lines, "")
		lines = append(lines, ai...)
	}
	if f, ok := p.facts[a.Path]; ok {
		lines = append(lines, "")
		lines = append(lines, factLines(f, label, p.styles, width-4-previewLabelW)...)
	}
	return pane.Render(strings.Join(lines, "\n"))
}

// factLines renders the gathered facts, one label per line, styled by
// tone: warnings in the flash colour, missing evidence dim. Values wrap at
// valW and continue under the value column, so the pane never wraps them
// at its border.
func factLines(f app.Facts, label func(k, val string) string, s styles, valW int) []string {
	var lines []string
	for _, l := range f.Summary() {
		val := wrapLines(l.Value, valW)
		switch l.Tone {
		case app.Warn:
			val = s.flash.Render(val)
		case app.Dim:
			val = s.dim.Render(val)
		}
		first, rest, more := strings.Cut(val, "\n")
		lines = append(lines, label(l.Label, first))
		if more {
			lines = append(lines, indent(rest, strings.Repeat(" ", previewLabelW)))
		}
	}
	return lines
}

// aiLines renders the AI state for the app at path: the spinner while
// asking, the error, or the answer; nothing when not asked.
func (p *Picker) aiLines(path string, width int) []string {
	width = max(width, 10)
	switch {
	case p.asking[path]:
		return []string{p.spin.View() + p.styles.dim.Render(" "+askingLabel)}
	case p.errs[path] != nil:
		return []string{p.styles.danger.Render(wrapLines("AI error: "+strings.TrimPrefix(p.errs[path].Error(), "llm: "), width))}
	}
	a, ok := p.results[path]
	if !ok {
		return nil
	}
	rec, ok := p.rec[a.Recommendation]
	if !ok {
		rec = p.styles.dim
	}
	head := rec.Render(strings.ToUpper(a.Recommendation)) + p.styles.dim.Render(fmt.Sprintf("  %.0f%% confidence", a.Confidence*100))
	if a.Model != "" {
		head += p.styles.dim.Render(labelSep + a.Model)
	}
	if a.Cached {
		head += p.styles.dim.Render(labelSep + savedTag)
	}
	lines := []string{head}
	if a.Purpose != "" {
		lines = append(lines, wrapLines(a.Purpose, width))
	}
	lines = append(lines, p.styles.dim.Render("Source: ")+a.Source)
	if a.Reasoning != "" {
		lines = append(lines, wrapLines(a.Reasoning, width))
	}
	return lines
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
