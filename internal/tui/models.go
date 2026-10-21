package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/llm/ollama"
	"github.com/shiva2021/rootout/internal/output"
)

const (
	modelsHelp        = "↑/↓ move · enter choose · d uninstall · esc back · ctrl+x quit · ? help"
	modelsPullHelp    = "esc cancel download · ctrl+x quit · ? help"
	modelsInstallHelp = "↑/↓ move · enter run · esc back · ctrl+x quit · ? help"
	modelsStartHelp   = "enter start Ollama · esc back · ctrl+x quit · ? help"
	modelsChromeRows  = 5 // title row, blank row, detail row, flash row, help row
	pullBarWidth      = 40

	// Column widths of the model grid; cmd/rootout/config.go prints the same.
	modelNameW = 26 // fits "qwen2.5-coder:7b (current)"
	modelFitW  = 11 // "comfortable"

	tooLargeHeading = "Need more memory than this Mac has"
)

// ModelDeps are the model picker's dependencies, injectable for tests.
type ModelDeps struct {
	Client     *ollama.Client
	Hardware   ollama.Hardware
	HasBrew    bool
	RunInstall func(ctx context.Context) error // runs the brew install route; nil hides it
	// ClearDefault forgets the configured default model. The picker calls it
	// after uninstalling that model; nil means nothing to clear.
	ClearDefault func() error
}

// PickModel returns the chosen (and, if needed, pulled) model name.
// ok=false means the user cancelled; ctrl+x and ctrl+c return ErrQuit.
func PickModel(ctx context.Context, d ModelDeps, current string) (model string, ok bool, err error) {
	m := NewModelPicker(ctx, d, current)
	_, err = tea.NewProgram(m, tea.WithContext(ctx)).Run()
	m.cancelPull()
	if err != nil {
		return "", false, runErr(err, "pick model")
	}
	if m.quitAll {
		return "", false, ErrQuit
	}
	model, ok = m.Chosen()
	return model, ok, nil
}

// modelState is the screen the picker shows.
type modelState int

const (
	stateDetecting    modelState = iota // spinner while Client.Detect runs
	stateNotInstalled                   // install guide
	stateNotRunning                     // offer to start Ollama
	stateInstalling                     // spinner while RunInstall runs
	stateStarting                       // spinner while Client.Start runs
	stateListing                        // spinner while ListInstalled runs
	stateReady                          // installed + recommended list
	statePulling                        // progress bar
	stateDeleting                       // spinner while Client.Delete runs
)

func (s modelState) busy() bool {
	switch s {
	case stateDetecting, stateInstalling, stateStarting, stateListing, stateDeleting:
		return true
	}
	return false
}

// Messages the picker's Cmds send back.
type (
	detectedMsg struct{ err error }
	listedMsg   struct {
		installed []ollama.Installed
		err       error
	}
	installedOllamaMsg struct{ err error }
	startedMsg         struct{ err error }
	pullProgressMsg    struct {
		id               int
		status           string
		completed, total int64
	}
	pullDoneMsg struct {
		id  int
		err error
	}
	deletedMsg struct {
		name string
		err  error
	}
)

// modelRowKind says what a list row shows.
type modelRowKind int

const (
	mrHeading    modelRowKind = iota // bold section title
	mrSubheading                     // dim note or blank separator
	mrColumns                        // the grid's column titles
	mrInstalled
	mrRecommended
	mrRoute
)

// selectable reports whether the cursor can rest on rows of this kind.
func (k modelRowKind) selectable() bool {
	return k == mrInstalled || k == mrRecommended || k == mrRoute
}

type modelRow struct {
	kind modelRowKind
	idx  int // into installed, recs or routes
	text string
}

// modelStyles extends the shared styles with this screen's badges.
type modelStyles struct {
	styles
	heading      lipgloss.Style
	comfortable  lipgloss.Style
	tight        lipgloss.Style
	tooLarge     lipgloss.Style
	installed    lipgloss.Style
	notInstalled lipgloss.Style
	errLine      lipgloss.Style
}

func defaultModelStyles() modelStyles {
	s := defaultStyles()
	return modelStyles{
		styles:       s,
		heading:      lipgloss.NewStyle().Bold(true),
		comfortable:  lipgloss.NewStyle().Foreground(lipgloss.Green),
		tight:        lipgloss.NewStyle().Foreground(lipgloss.Yellow),
		tooLarge:     s.dim,
		installed:    lipgloss.NewStyle().Foreground(lipgloss.Cyan),
		notInstalled: s.dim,
		errLine:      lipgloss.NewStyle().Foreground(lipgloss.Red),
	}
}

// ModelPicker is the local model picker. It implements tea.Model and is
// exported so tests can drive it without a terminal.
type ModelPicker struct {
	ctx     context.Context
	deps    ModelDeps
	current string
	styles  modelStyles
	spin    spinner.Model

	state     modelState
	installed []ollama.Installed
	recs      []ollama.Recommendation
	routes    []ollama.InstallRoute
	rows      []modelRow

	width, height int
	cursor, top   int // cursor indexes rows; -1 when nothing is selectable
	flash         string
	errText       string
	ask           string         // pending y/n question
	onYes         func() tea.Cmd // what y does

	pullID     int
	pullName   string
	pullStatus string
	pullDone   int64
	pullTotal  int64
	pullCancel context.CancelFunc
	pullWait   tea.Cmd // reads the next pull message
	chosen     string
	ok         bool
	quitAll    bool   // ctrl+x / ctrl+c: leave rootout, not just this screen
	deleting   string // model being uninstalled

	help     bool // ? overlay open
	embedded bool // hosted by another screen: finish instead of quitting
	finished bool // embedded mode: the picker would have quit
}

// NewModelPicker builds the picker in the detecting state; Init starts
// detection.
func NewModelPicker(ctx context.Context, d ModelDeps, current string) *ModelPicker {
	return &ModelPicker{
		ctx:     ctx,
		deps:    d,
		current: current,
		styles:  defaultModelStyles(),
		spin:    spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		state:   stateDetecting,
		width:   defaultWidth,
		height:  defaultHeight,
		cursor:  -1,
	}
}

// Chosen returns the model name once the program has quit; ok is false
// when the user cancelled.
func (m *ModelPicker) Chosen() (string, bool) { return m.chosen, m.ok }

// setEmbedded makes the picker a sub-model: where it would quit the program
// it sets the done state instead, and the host reads it with done().
func (m *ModelPicker) setEmbedded(on bool) { m.embedded = on }

// done reports whether an embedded picker has finished and what it chose.
func (m *ModelPicker) done() (model string, chosen, finished bool) {
	return m.chosen, m.ok, m.finished
}

// quit ends the picker: tea.Quit on its own, the done flag when embedded.
func (m *ModelPicker) quit() tea.Cmd {
	if m.embedded {
		m.finished = true
		return nil
	}
	return tea.Quit
}

// Init starts detection with a spinner.
func (m *ModelPicker) Init() tea.Cmd {
	return tea.Batch(m.detect(), m.spin.Tick)
}

// Update handles resizes, keys and the results of the picker's Cmds.
func (m *ModelPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll()
		return m, nil

	case spinner.TickMsg:
		if !m.state.busy() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case detectedMsg:
		switch {
		case msg.err == nil:
			m.errText = ""
			m.state = stateListing
			return m, m.list()
		case errors.Is(msg.err, ollama.ErrNotInstalled):
			m.routes = ollama.InstallGuide(m.deps.HasBrew)
			m.setState(stateNotInstalled)
		default:
			if !errors.Is(msg.err, ollama.ErrNotRunning) {
				m.errText = msg.err.Error()
			}
			m.setState(stateNotRunning)
		}
		return m, nil

	case listedMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
		}
		m.installed = msg.installed
		m.recs = ollama.Recommend(m.deps.Hardware, m.installed)
		m.setState(stateReady)
		return m, nil

	case installedOllamaMsg:
		if msg.err != nil {
			m.errText = "install failed: " + msg.err.Error()
			m.setState(stateNotInstalled)
			return m, nil
		}
		return m, m.redetect()

	case startedMsg:
		if msg.err != nil {
			m.errText = "could not start Ollama: " + msg.err.Error()
			m.setState(stateNotRunning)
			return m, nil
		}
		return m, m.redetect()

	case pullProgressMsg:
		if msg.id != m.pullID || m.state != statePulling {
			return m, nil
		}
		m.pullStatus = msg.status
		if msg.total > 0 {
			m.pullDone, m.pullTotal = msg.completed, msg.total
		}
		return m, m.pullWait

	case pullDoneMsg:
		if msg.id != m.pullID || m.state != statePulling {
			return m, nil
		}
		m.cancelPull()
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.setState(stateReady)
			return m, nil
		}
		m.chosen, m.ok = m.pullName, true
		return m, m.quit()

	case deletedMsg:
		if m.state != stateDeleting {
			return m, nil
		}
		m.deleting = ""
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.setState(stateReady)
			return m, nil
		}
		m.flash = msg.name + " uninstalled"
		if m.isCurrent(msg.name) {
			m.current = ""
			m.flash += "; it was the default, choose another"
			if m.deps.ClearDefault != nil {
				if err := m.deps.ClearDefault(); err != nil {
					m.errText = err.Error()
				}
			}
		}
		m.state = stateListing
		return m, tea.Batch(m.list(), m.spin.Tick)

	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *ModelPicker) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	m.flash = ""
	if isQuitKey(k) {
		m.cancelPull()
		m.quitAll = true
		m.ok = false
		return m, m.quit()
	}
	if m.help {
		m.help = false
		return m, nil
	}
	if k == "?" {
		m.help = true
		return m, nil
	}
	if m.ask != "" {
		switch k {
		case "y", "Y":
			m.ask = ""
			return m, m.onYes()
		case "n", "N", "esc":
			m.ask = ""
		}
		return m, nil
	}
	if m.state == statePulling {
		if k == "esc" {
			m.cancelPull()
			m.flash = "download cancelled"
			m.setState(stateReady)
		}
		return m, nil
	}

	switch k {
	case "esc", "q":
		m.ok = false
		return m, m.quit()
	case "up", "ctrl+p":
		m.move(-1)
	case "down", "ctrl+n":
		m.move(1)
	case "pgup":
		m.move(-m.listRows())
	case "pgdown":
		m.move(m.listRows())
	case "enter":
		return m, m.choose()
	case "d":
		m.askUninstall()
	}
	return m, nil
}

// askUninstall asks to uninstall the model on the cursor row when it is in
// the local store; other rows just flash.
func (m *ModelPicker) askUninstall() {
	if m.state != stateReady || m.cursor < 0 {
		return
	}
	r := m.rows[m.cursor]
	var name string
	switch r.kind {
	case mrInstalled:
		name = m.installed[r.idx].Name
	case mrRecommended:
		name = m.installedName(m.recs[r.idx].Name)
	}
	in, ok := m.installedEntry(name)
	if !ok {
		m.flash = "not installed"
		return
	}
	m.ask = fmt.Sprintf("Uninstall %s (%s)? y/n", in.Name, output.Bytes(in.SizeBytes))
	m.onYes = func() tea.Cmd { return m.startDelete(in.Name) }
}

// installedEntry finds name in the local store.
func (m *ModelPicker) installedEntry(name string) (ollama.Installed, bool) {
	for _, in := range m.installed {
		if in.Name == name {
			return in, true
		}
	}
	return ollama.Installed{}, false
}

// startDelete runs Client.Delete with a spinner; deletedMsg re-lists.
func (m *ModelPicker) startDelete(name string) tea.Cmd {
	m.errText = ""
	m.deleting = name
	m.state = stateDeleting
	c, ctx := m.deps.Client, m.ctx
	return tea.Batch(func() tea.Msg { return deletedMsg{name: name, err: c.Delete(ctx, name)} }, m.spin.Tick)
}

// View renders the whole screen in the alternate buffer.
func (m *ModelPicker) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// choose acts on the cursor row for the current state.
func (m *ModelPicker) choose() tea.Cmd {
	switch m.state {
	case stateNotRunning:
		m.errText = ""
		m.state = stateStarting
		c, ctx := m.deps.Client, m.ctx
		return tea.Batch(func() tea.Msg { return startedMsg{err: c.Start(ctx)} }, m.spin.Tick)
	case stateNotInstalled, stateReady:
	default:
		return nil
	}
	if m.cursor < 0 {
		return nil
	}
	r := m.rows[m.cursor]
	switch r.kind {
	case mrRoute:
		route := m.routes[r.idx]
		if !route.Runnable || m.deps.RunInstall == nil {
			m.flash = "install Ollama as shown above, then run rootout again"
			return nil
		}
		m.ask = fmt.Sprintf("Run `%s`? y/n", route.Command)
		m.onYes = m.install
	case mrInstalled:
		m.chosen, m.ok = m.installed[r.idx].Name, true
		return m.quit()
	case mrRecommended:
		rec := m.recs[r.idx]
		switch {
		case rec.Installed:
			m.chosen, m.ok = m.installedName(rec.Name), true
			return m.quit()
		case rec.Fit == ollama.FitTooLarge:
			m.flash = fmt.Sprintf("%s needs about %s of memory; this Mac has %d GB",
				rec.Name, output.Bytes(rec.DownloadBytes*12/10), m.deps.Hardware.RAMBytes>>30)
			return nil
		}
		m.ask = fmt.Sprintf("Download %s (%s)? y/n", rec.Name, output.Bytes(rec.DownloadBytes))
		m.onYes = func() tea.Cmd { return m.startPull(rec) }
	}
	return nil
}

// installedName maps a catalogue name to the store's spelling ("x" may be
// listed as "x:latest").
func (m *ModelPicker) installedName(name string) string {
	for _, in := range m.installed {
		if strings.TrimSuffix(in.Name, ":latest") == strings.TrimSuffix(name, ":latest") {
			return in.Name
		}
	}
	return name
}

func (m *ModelPicker) detect() tea.Cmd {
	c, ctx := m.deps.Client, m.ctx
	return func() tea.Msg {
		_, err := c.Detect(ctx)
		return detectedMsg{err: err}
	}
}

func (m *ModelPicker) redetect() tea.Cmd {
	m.errText = ""
	m.state = stateDetecting
	return tea.Batch(m.detect(), m.spin.Tick)
}

func (m *ModelPicker) list() tea.Cmd {
	c, ctx, hw := m.deps.Client, m.ctx, m.deps.Hardware
	return func() tea.Msg {
		in, err := c.ListInstalled(ctx, hw)
		return listedMsg{installed: in, err: err}
	}
}

func (m *ModelPicker) install() tea.Cmd {
	m.errText = ""
	m.state = stateInstalling
	run, ctx := m.deps.RunInstall, m.ctx
	return tea.Batch(func() tea.Msg { return installedOllamaMsg{err: run(ctx)} }, m.spin.Tick)
}

// startPull runs Client.Pull in a goroutine and bridges its progress
// callback to tea messages through a channel read by a re-arming Cmd.
func (m *ModelPicker) startPull(rec ollama.Recommendation) tea.Cmd {
	m.cancelPull()
	m.pullID++
	id := m.pullID
	ctx, cancel := context.WithCancel(m.ctx)
	m.pullCancel = cancel
	m.pullName, m.pullStatus = rec.Name, "starting"
	m.pullDone, m.pullTotal = 0, 0
	m.errText = ""
	m.state = statePulling

	ch := make(chan tea.Msg)
	send := func(msg tea.Msg) {
		select {
		case ch <- msg:
		case <-ctx.Done():
		}
	}
	c := m.deps.Client
	go func() {
		err := c.Pull(ctx, rec.Name, func(status string, completed, total int64) {
			send(pullProgressMsg{id: id, status: status, completed: completed, total: total})
		})
		send(pullDoneMsg{id: id, err: err})
	}()
	m.pullWait = waitPull(ctx, ch)
	return m.pullWait
}

// waitPull reads the next message from the pull goroutine. Update re-arms
// it after every progress message.
func waitPull(ctx context.Context, ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-ch:
			return msg
		case <-ctx.Done():
			return nil
		}
	}
}

func (m *ModelPicker) cancelPull() {
	if m.pullCancel != nil {
		m.pullCancel()
		m.pullCancel = nil
	}
	m.pullWait = nil
}

// setState switches to a list state and rebuilds its rows.
func (m *ModelPicker) setState(s modelState) {
	m.state = s
	m.rows = m.buildRows()
	m.cursor, m.top = -1, 0
	for i, r := range m.rows {
		if !r.kind.selectable() {
			continue
		}
		if m.cursor < 0 {
			m.cursor = i
		}
		if r.kind == mrInstalled && m.isCurrent(m.installed[r.idx].Name) {
			m.cursor = i
			break
		}
	}
	m.scroll()
}

func (m *ModelPicker) isCurrent(name string) bool {
	return m.current != "" && strings.TrimSuffix(name, ":latest") == strings.TrimSuffix(m.current, ":latest")
}

func (m *ModelPicker) buildRows() []modelRow {
	var rows []modelRow
	switch m.state {
	case stateNotInstalled:
		for i := range m.routes {
			rows = append(rows, modelRow{kind: mrRoute, idx: i})
		}
	case stateReady:
		rows = append(rows, modelRow{kind: mrColumns}, modelRow{kind: mrHeading, text: "Installed"})
		for i := range m.installed {
			rows = append(rows, modelRow{kind: mrInstalled, idx: i})
		}
		if len(m.installed) == 0 {
			rows = append(rows, modelRow{kind: mrSubheading, text: "none yet"})
		}
		hw := m.deps.Hardware
		rows = append(rows, modelRow{kind: mrSubheading},
			modelRow{kind: mrHeading, text: fmt.Sprintf("Recommended for this Mac — %s, %d GB", orDash(hw.Chip), hw.RAMBytes>>30)})
		var large []modelRow
		for i, rec := range m.recs {
			r := modelRow{kind: mrRecommended, idx: i}
			if rec.Fit == ollama.FitTooLarge {
				large = append(large, r)
				continue
			}
			rows = append(rows, r)
		}
		if len(large) > 0 {
			rows = append(rows, modelRow{kind: mrSubheading, text: tooLargeHeading})
			rows = append(rows, large...)
		}
	}
	return rows
}

// move steps the cursor delta selectable rows, skipping headings.
func (m *ModelPicker) move(delta int) {
	if m.cursor < 0 {
		return
	}
	step := 1
	if delta < 0 {
		step, delta = -1, -delta
	}
	for ; delta > 0; delta-- {
		j := m.cursor + step
		for j >= 0 && j < len(m.rows) && !m.rows[j].kind.selectable() {
			j += step
		}
		if j < 0 || j >= len(m.rows) {
			break
		}
		m.cursor = j
	}
	m.scroll()
}

// scroll adjusts top so the cursor row is visible, showing the headings
// right above it when scrolling up.
func (m *ModelPicker) scroll() {
	rows := m.listRows()
	if m.cursor < 0 {
		m.top = 0
		return
	}
	if m.cursor < m.top {
		m.top = m.cursor
		for m.top > 0 && !m.rows[m.top-1].kind.selectable() {
			m.top--
		}
	}
	if m.cursor >= m.top+rows {
		m.top = m.cursor - rows + 1
	}
	m.top = max(m.top, 0)
}

func (m *ModelPicker) listRows() int {
	return max(m.height-modelsChromeRows, 1)
}

func (m *ModelPicker) render() string {
	if m.help {
		return renderHelp(m.width, m.height, "Keys", m.helpKeys())
	}
	s := m.styles
	var body []string
	help := modelsHelp
	title := "Local models (Ollama)"

	switch m.state {
	case stateDetecting:
		body = []string{m.spin.View() + " looking for Ollama…"}
	case stateListing:
		body = []string{m.spin.View() + " listing installed models…"}
	case stateInstalling:
		body = []string{m.spin.View() + " installing Ollama with Homebrew…"}
	case stateStarting:
		body = []string{m.spin.View() + " starting Ollama…"}
	case stateDeleting:
		body = []string{m.spin.View() + " uninstalling " + m.deleting + "…"}
	case stateNotRunning:
		help = modelsStartHelp
		body = []string{
			"Ollama is installed but not running.",
			"",
			s.cursor.Render("› Start Ollama "),
		}
	case stateNotInstalled:
		help = modelsInstallHelp
		body = append([]string{"Ollama is not installed. Install it one of these ways:", ""}, m.renderRows(m.listRows()-2)...)
	case statePulling:
		help = modelsPullHelp
		title = "Downloading " + m.pullName
		pct := 0.0
		if m.pullTotal > 0 {
			pct = float64(m.pullDone) / float64(m.pullTotal)
		}
		status := m.pullStatus
		if m.pullTotal > 0 {
			status += fmt.Sprintf("  %s / %s", output.Bytes(m.pullDone), output.Bytes(m.pullTotal))
		}
		body = []string{renderBar(pct, pullBarWidth), "", s.dim.Render(status)}
	case stateReady:
		body = m.renderRows(m.listRows())
	}

	rows := m.listRows()
	for len(body) < rows {
		body = append(body, "")
	}

	var b strings.Builder
	b.WriteString(s.title.Render(title))
	b.WriteString("\n\n")
	b.WriteString(strings.Join(body[:rows], "\n"))
	b.WriteString("\n")
	b.WriteString(m.renderDetail())
	b.WriteString("\n")
	switch {
	case m.ask != "":
		b.WriteString(s.flash.Render(m.ask))
	case m.flash != "":
		b.WriteString(s.flash.Render(m.flash))
	case m.errText != "":
		b.WriteString(s.errLine.Render(output.Truncate(m.errText, m.width)))
	}
	b.WriteString("\n")
	b.WriteString(s.dim.Render(help))
	return b.String()
}

// helpKeys lists the keys for the ? overlay; y/n appears where a question
// can be asked (install routes, downloads, starting Ollama).
func (m *ModelPicker) helpKeys() []keyHelp {
	keys := []keyHelp{{"↑/↓", "move"}}
	switch m.state {
	case stateNotInstalled:
		keys = append(keys, keyHelp{"enter", "run the selected install route"})
	case stateNotRunning:
		keys = append(keys, keyHelp{"enter", "start Ollama"})
	case statePulling:
		keys = append(keys, keyHelp{"esc", "cancel the download"})
	default:
		keys = append(keys, keyHelp{"enter", "choose the model (downloads it if needed)"})
		keys = append(keys, keyHelp{"d", "uninstall the selected model from Ollama"})
	}
	if m.ask != "" || m.state == stateNotInstalled || m.state == stateNotRunning || m.state == stateReady {
		keys = append(keys, keyHelp{"y / n", "answer a run, download or uninstall question"})
	}
	if m.state != statePulling {
		keys = append(keys, keyHelp{"esc", "back without choosing"})
	}
	keys = append(keys, keyHelp{"ctrl+x", "quit rootout"})
	return append(keys, keyHelp{"?", "this help"})
}

// renderRows draws up to n rows of the list starting at top.
func (m *ModelPicker) renderRows(n int) []string {
	var out []string
	for i := m.top; i < len(m.rows) && len(out) < n; i++ {
		out = append(out, m.renderRow(i))
	}
	return out
}

// detail is "<name> — <why>" for the cursor row when it is a catalogue
// model (recommended, or installed and in the catalogue); "" otherwise.
func (m *ModelPicker) detail() string {
	if m.state != stateReady || m.cursor < 0 {
		return ""
	}
	r := m.rows[m.cursor]
	switch r.kind {
	case mrRecommended:
		rec := m.recs[r.idx]
		return rec.Name + " — " + rec.Why
	case mrInstalled:
		name := strings.TrimSuffix(m.installed[r.idx].Name, ":latest")
		for _, rec := range m.recs {
			if strings.TrimSuffix(rec.Name, ":latest") == name {
				return rec.Name + " — " + rec.Why
			}
		}
	}
	return ""
}

// renderDetail draws detail() with the name plain and the blurb dim,
// truncated to the screen width.
func (m *ModelPicker) renderDetail() string {
	d := output.Truncate(m.detail(), m.width)
	name, why, ok := strings.Cut(d, " — ")
	if !ok {
		return d
	}
	return name + m.styles.dim.Render(" — "+why)
}

// gridRow formats one line of the model grid after the two-cell marker.
func gridRow(name, size, fit string) string {
	return fmt.Sprintf("%-*s  %*s  %-*s  ", modelNameW, output.Truncate(name, modelNameW), sizeWidth, size, modelFitW, fit)
}

func (m *ModelPicker) renderRow(i int) string {
	r, s := m.rows[i], m.styles
	switch r.kind {
	case mrHeading:
		return s.heading.Render(r.text)
	case mrSubheading:
		return s.dim.Render("  " + r.text)
	case mrColumns:
		return s.dim.Render("  " + gridRow("MODEL", "SIZE", "FIT") + "NOTES")
	}
	selected := i == m.cursor
	base, dim := lipgloss.NewStyle(), s.dim
	if selected {
		base = s.cursor
		dim = base.Faint(true)
	}
	marker := "  "
	if selected {
		marker = "› "
	}

	var b strings.Builder
	b.WriteString(base.Render(marker))
	switch r.kind {
	case mrRoute:
		route := m.routes[r.idx]
		b.WriteString(base.Render(fmt.Sprintf("%-11s %s", route.Label, route.Command)))
		if route.Runnable && m.deps.RunInstall != nil {
			b.WriteString(base.Render(" ") + dim.Render("(enter to run)"))
		}
	case mrInstalled:
		in := m.installed[r.idx]
		name := in.Name
		if m.isCurrent(in.Name) {
			name += " (current)"
		}
		fit, fitStyle := "fits", s.comfortable
		if !in.FitsRAM {
			fit, fitStyle = "too large", s.tooLarge
		}
		b.WriteString(base.Render(fmt.Sprintf("%-*s  ", modelNameW, output.Truncate(name, modelNameW))))
		b.WriteString(dim.Render(fmt.Sprintf("%*s  ", sizeWidth, output.Bytes(in.SizeBytes))))
		b.WriteString(fitStyle.Inherit(base).Render(fmt.Sprintf("%-*s", modelFitW, fit)))
		b.WriteString(base.Render("  ") + dim.Render(m.notesCell(installedNotes(in), b.String())))
	case mrRecommended:
		rec := m.recs[r.idx]
		nameStyle := base
		if rec.Fit == ollama.FitTooLarge {
			nameStyle = dim
		}
		b.WriteString(nameStyle.Render(fmt.Sprintf("%-*s  ", modelNameW, output.Truncate(rec.Name, modelNameW))))
		b.WriteString(dim.Render(fmt.Sprintf("%*s  ", sizeWidth, output.Bytes(rec.DownloadBytes))))
		b.WriteString(m.fitStyle(rec.Fit).Inherit(base).Render(fmt.Sprintf("%-*s", modelFitW, rec.Fit)))
		status, statusStyle := "not installed", s.notInstalled
		if rec.Installed {
			status, statusStyle = "installed", s.installed
		}
		b.WriteString(base.Render("  ") + statusStyle.Inherit(base).Render(m.notesCell(status, b.String())))
	}
	return padWith(b.String(), m.width, base)
}

// notesCell truncates the NOTES text to what is left of the row after
// the cells already in row and the two-space gap.
func (m *ModelPicker) notesCell(text, row string) string {
	return output.Truncate(text, max(m.width-lipgloss.Width(row)-2, 2))
}

// installedNotes is the NOTES cell of an installed row: "3.2B · Q4_K_M".
func installedNotes(in ollama.Installed) string {
	var parts []string
	for _, p := range []string{in.ParameterSize, in.Quantization} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
}

func (m *ModelPicker) fitStyle(fit string) lipgloss.Style {
	switch fit {
	case ollama.FitComfortable:
		return m.styles.comfortable
	case ollama.FitTight:
		return m.styles.tight
	}
	return m.styles.tooLarge
}
