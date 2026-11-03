package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/plan"
)

const (
	scanChromeRows = 5 // title row, AI row, blank row, flash row, help row
	scanNoAI       = "AI: none"
	scanNoAIPick   = "AI: none · m to pick a local model · rootout setup for a key"
	scanProvider   = "ollama"
	labelSep       = " · "
	lowScore       = 30 // reports under this are hidden by `f`
	highScore      = 70
	nothingTicked  = "nothing ticked for uninstall"
	scoreWidth     = 3

	// assessWorkers bounds the model calls in flight: hosted calls cost
	// money and are rate-limited, local ones share one Mac.
	assessWorkers       = 2
	nothingTickedAssess = "tick rows with space, then l"
	summaryHidden       = "(hidden)"

	// Verdict icons shown after the editor tag; iconW is the slot width
	// (⚠ is drawn 1 or 2 cells wide depending on the terminal).
	iconBenign    = "✓"
	iconMalicious = "✗"
	iconUnsure    = "⚠"
	iconError     = "!"
	iconW         = 2
)

// ScanResult is what the user decided.
type ScanResult struct {
	Uninstall []heur.Report // ticked for removal, in display order
}

// AssessFunc runs the configured LLM on one report; nil hides the "assess" action.
type AssessFunc func(ctx context.Context, r heur.Report) (llm.Assessment, error)

// Assessor is the AI the scan screen can ask, plus how to swap its model.
type Assessor struct {
	Provider string     // "ollama" | "anthropic" | "openai"; "" = none configured
	Label    string     // "ollama · qwen3.5:4b", "anthropic · claude-sonnet-5-5"; "" = none configured
	Assess   AssessFunc // nil = none
	Models   *ModelDeps // nil = local model picking unavailable
	// HostedModels lists the configured hosted provider's models for the m
	// key; nil = unavailable (no key). Built in cmd/ with the key resolved.
	HostedModels func(ctx context.Context) ([]llm.ModelInfo, error)
	// OnModel is called after the user picks a model for provider and
	// returns the replacement Assessor (Provider, Label, Assess, Models and
	// HostedModels are taken from it).
	OnModel func(ctx context.Context, provider, model string) (Assessor, error)
}

// ReviewScan shows the scan results full screen and returns the extensions
// the user ticked for removal. ok=false means the user cancelled; ctrl+x and
// ctrl+c return ErrQuit. Assessments started here stop when the screen closes.
func ReviewScan(ctx context.Context, reports []heur.Report, a Assessor) (ScanResult, bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	v := NewScanView(reports, a)
	v.ctx = ctx
	if _, err := tea.NewProgram(v, tea.WithContext(ctx)).Run(); err != nil {
		return ScanResult{}, false, runErr(err, "review scan")
	}
	if v.quitAll {
		return ScanResult{}, false, ErrQuit
	}
	res, ok := v.Result()
	return res, ok, nil
}

// assessMsg carries one finished LLM assessment back to the view.
type assessMsg struct {
	report int
	a      llm.Assessment
	err    error
}

// assessorMsg carries the Assessor rebuilt after a model pick.
type assessorMsg struct {
	a   Assessor
	err error
}

// scanStyles extends the shared styles with the ones only this screen uses.
type scanStyles struct {
	styles
	high, mid, low lipgloss.Style // score bands
	heading        lipgloss.Style
	box            lipgloss.Style
	verdict        map[string]lipgloss.Style
	severity       map[heur.Severity]lipgloss.Style
	focused        lipgloss.Style // details pane border when focused
}

func defaultScanStyles() scanStyles {
	s := defaultStyles()
	red := lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true)
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	green := lipgloss.NewStyle().Foreground(lipgloss.Green)
	return scanStyles{
		styles:  s,
		high:    red,
		mid:     yellow,
		low:     s.dim,
		heading: lipgloss.NewStyle().Bold(true).Underline(true),
		box:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.BrightBlack).Padding(0, 1),
		verdict: map[string]lipgloss.Style{
			llm.Malicious:  red,
			llm.Suspicious: yellow.Bold(true),
			llm.Benign:     green.Bold(true),
			llm.Unknown:    s.dim,
		},
		severity: map[heur.Severity]lipgloss.Style{
			heur.Critical: red,
			heur.High:     lipgloss.NewStyle().Foreground(lipgloss.Red),
			heur.Medium:   yellow,
			heur.Low:      s.dim,
			heur.Info:     s.dim,
		},
		focused: s.pane.BorderForeground(lipgloss.Cyan),
	}
}

// ScanView is the scan results model. It implements tea.Model and is
// exported so tests can drive it without a terminal.
type ScanView struct {
	ctx      context.Context
	reports  []heur.Report
	assessor Assessor
	styles   scanStyles
	spin     spinner.Model

	visible  []int // indexes into reports, in display order
	ticked   map[int]bool
	results  map[int]llm.Assessment
	errs     map[int]error
	pending  map[int]bool
	queue    []int // reports waiting for a worker, in ask order
	hideLow  bool
	detailsF bool // details pane has focus

	width, height int
	cursor, top   int // cursor indexes visible
	detailTop     int // first visible line of the details pane
	flash         string
	errLine       string         // red one-liner (model swap failed)
	confirm       *typedConfirm  // typed "delete" box; nil when closed
	picker        embeddedPicker // embedded model picker; nil when closed
	help          bool           // ? overlay open
	accepted      bool
	quitAll       bool // ctrl+x / ctrl+c: leave rootout, not just this screen
}

// NewScanView builds the screen with every report listed and the cursor on
// the first one.
func NewScanView(reports []heur.Report, a Assessor) *ScanView {
	v := &ScanView{
		ctx:      context.Background(),
		reports:  reports,
		assessor: a,
		styles:   defaultScanStyles(),
		spin:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		ticked:   map[int]bool{},
		results:  map[int]llm.Assessment{},
		errs:     map[int]error{},
		pending:  map[int]bool{},
		width:    defaultWidth,
		height:   defaultHeight,
	}
	v.filter()
	return v
}

// Result returns the reports ticked for removal once the program has quit;
// ok is false when the user cancelled.
func (v *ScanView) Result() (ScanResult, bool) {
	if !v.accepted {
		return ScanResult{}, false
	}
	var res ScanResult
	for i, r := range v.reports {
		if v.ticked[i] {
			res.Uninstall = append(res.Uninstall, r)
		}
	}
	return res, true
}

// Init implements tea.Model.
func (v *ScanView) Init() tea.Cmd { return nil }

// Update handles resizes, keys, assessment results and spinner ticks. While
// the model picker or the typed confirm is open, other messages go to it.
func (v *ScanView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.scroll()
		v.clampDetails()
		if v.picker != nil {
			v.picker.Update(msg)
		}
		return v, nil

	case assessorMsg:
		if msg.err != nil {
			v.errLine = "model: " + msg.err.Error()
			return v, nil
		}
		v.assessor.Provider, v.assessor.Label, v.assessor.Assess = msg.a.Provider, msg.a.Label, msg.a.Assess
		v.assessor.Models, v.assessor.HostedModels = msg.a.Models, msg.a.HostedModels
		if msg.a.OnModel != nil {
			v.assessor.OnModel = msg.a.OnModel
		}
		return v, nil

	case assessMsg:
		delete(v.pending, msg.report)
		if msg.err != nil {
			v.errs[msg.report] = msg.err
		} else {
			delete(v.errs, msg.report)
			v.results[msg.report] = msg.a
		}
		return v, v.startNext()

	case spinner.TickMsg:
		// Both spinners may be ticking; each ignores the other's ID.
		var cmds []tea.Cmd
		if len(v.pending) > 0 {
			var cmd tea.Cmd
			v.spin, cmd = v.spin.Update(msg)
			cmds = append(cmds, cmd)
		}
		if v.picker != nil {
			cmds = append(cmds, v.delegate(msg))
		}
		return v, tea.Batch(cmds...)

	case tea.KeyPressMsg:
		if isQuitKey(msg.String()) {
			v.closePicker()
			v.quitAll = true
			v.accepted = false
			return v, tea.Quit
		}
		if v.help {
			v.help = false
			return v, nil
		}
	}

	switch {
	case v.picker != nil:
		return v, v.delegate(msg)
	case v.confirm != nil:
		return v, v.confirmUpdate(msg)
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		return v.key(k)
	}
	return v, nil
}

func (v *ScanView) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	v.flash, v.errLine = "", ""
	switch k := msg.String(); k {
	case "esc", "q":
		v.accepted = false
		return v, tea.Quit
	case "tab", "shift+tab":
		v.detailsF = !v.detailsF
	case "up", "ctrl+p":
		v.step(-1)
	case "down", "ctrl+n":
		v.step(1)
	case "pgup":
		v.step(-v.paneRows())
	case "pgdown":
		v.step(v.paneRows())
	case "space":
		if r, ok := v.current(); ok {
			v.ticked[r] = !v.ticked[r]
		}
	case "f":
		v.hideLow = !v.hideLow
		v.filter()
	case "l":
		return v, v.enqueueAssess()
	case "m":
		return v, v.openPicker()
	case "?":
		v.help = true
	case "enter":
		n := v.tickedCount()
		if n == 0 {
			v.flash = nothingTicked
			return v, nil
		}
		tc, cmd := newTypedConfirm(plan.ConfirmWord, fmt.Sprintf("Type %s to uninstall %d extensions", plan.ConfirmWord, n), false)
		v.confirm = &tc
		return v, cmd
	}
	return v, nil
}

// confirmUpdate forwards msg to the typed box: the right word quits with
// the ticks accepted, esc closes the box.
func (v *ScanView) confirmUpdate(msg tea.Msg) tea.Cmd {
	tc, cmd, accepted, cancelled := v.confirm.Update(msg)
	v.confirm = &tc
	switch {
	case accepted:
		v.confirm = nil
		v.accepted = true
		return tea.Quit
	case cancelled:
		v.confirm = nil
	}
	return cmd
}

// openPicker embeds a model picker over the screen: the configured hosted
// provider's list when it can be fetched, else the Ollama picker; nil when
// neither is available.
func (v *ScanView) openPicker() tea.Cmd {
	var p embeddedPicker
	switch {
	case v.hosted() && v.assessor.HostedModels != nil:
		p = NewHostedPicker(v.ctx, v.assessor.Provider, v.assessor.HostedModels, v.currentModel())
	case v.assessor.Models != nil:
		p = NewModelPicker(v.ctx, *v.assessor.Models, v.currentOllamaModel())
	default:
		return nil
	}
	p.setEmbedded(true)
	p.Update(tea.WindowSizeMsg{Width: v.width, Height: v.height})
	v.picker = p
	return p.Init()
}

// hosted reports whether the configured provider is a hosted one.
func (v *ScanView) hosted() bool {
	return v.assessor.Provider != "" && v.assessor.Provider != scanProvider
}

// pickerProvider is the provider a picked model belongs to.
func (v *ScanView) pickerProvider() string {
	if _, ok := v.picker.(*HostedPicker); ok {
		return v.assessor.Provider
	}
	return scanProvider
}

// currentModel is the model part of the "<provider> · <model>" label.
func (v *ScanView) currentModel() string {
	_, model, _ := strings.Cut(v.assessor.Label, labelSep)
	return model
}

// currentOllamaModel is the current model when the provider is Ollama.
func (v *ScanView) currentOllamaModel() string {
	if v.assessor.Provider != scanProvider {
		return ""
	}
	return v.currentModel()
}

// delegate forwards msg to the embedded picker and, once it has finished,
// closes it and rebuilds the Assessor for the chosen model.
func (v *ScanView) delegate(msg tea.Msg) tea.Cmd {
	_, cmd := v.picker.Update(msg)
	model, chosen, finished := v.picker.done()
	if !finished {
		return cmd
	}
	provider := v.pickerProvider()
	v.closePicker()
	if !chosen || v.assessor.OnModel == nil {
		return cmd
	}
	ctx, on := v.ctx, v.assessor.OnModel
	return func() tea.Msg {
		a, err := on(ctx, provider, model)
		return assessorMsg{a: a, err: err}
	}
}

func (v *ScanView) closePicker() {
	if v.picker != nil {
		v.picker.cancel()
		v.picker = nil
	}
}

// View renders the whole screen in the alternate buffer; the embedded
// picker takes it over while open.
func (v *ScanView) View() tea.View {
	if v.picker != nil {
		return v.picker.View()
	}
	view := tea.NewView(v.render())
	view.AltScreen = true
	return view
}

// enqueueAssess queues the ticked reports for the model, skipping the ones
// already answered or in flight (an errored one is asked again), and
// starts as many as the worker bound allows. Nothing ticked is a hint.
func (v *ScanView) enqueueAssess() tea.Cmd {
	if v.assessor.Assess == nil {
		return nil
	}
	order := v.tickedOrder()
	if len(order) == 0 {
		v.flash = nothingTickedAssess
		return nil
	}
	answered, asked := 0, 0
	for _, r := range order {
		if _, ok := v.results[r]; ok {
			answered++
			continue
		}
		if v.pending[r] || slices.Contains(v.queue, r) {
			continue
		}
		v.queue = append(v.queue, r)
		asked++
	}
	switch {
	case asked == 0:
		v.flash = fmt.Sprintf("%d already answered", answered)
	case answered > 0:
		v.flash = fmt.Sprintf("asking %d · %d already answered", asked, answered)
	default:
		v.flash = fmt.Sprintf("asking %d", asked)
	}
	return v.startNext()
}

// startNext pops queued reports into free workers. Each call captures the
// Assess in force when it starts, so a model swap applies to the rest of
// the queue. The spinner starts with the first in-flight call.
func (v *ScanView) startNext() tea.Cmd {
	var cmds []tea.Cmd
	idle := len(v.pending) == 0
	for len(v.pending) < assessWorkers && len(v.queue) > 0 {
		r := v.queue[0]
		v.queue = v.queue[1:]
		v.pending[r] = true
		delete(v.errs, r)
		ctx, assess, rep := v.ctx, v.assessor.Assess, v.reports[r]
		cmds = append(cmds, func() tea.Msg {
			a, err := assess(ctx, rep)
			return assessMsg{report: r, a: a, err: err}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	if idle {
		cmds = append(cmds, v.spin.Tick)
	}
	return tea.Batch(cmds...)
}

// tickedOrder is the ticked reports: the visible ones in display order,
// then the ones hidden by the filter in report order.
func (v *ScanView) tickedOrder() []int {
	var out []int
	shown := map[int]bool{}
	for _, r := range v.visible {
		shown[r] = true
		if v.ticked[r] {
			out = append(out, r)
		}
	}
	for r := range v.reports {
		if v.ticked[r] && !shown[r] {
			out = append(out, r)
		}
	}
	return out
}

// anyTickedAsked reports whether a ticked row has a result, an error or a
// call in flight: only then does the summary replace the single detail.
func (v *ScanView) anyTickedAsked() bool {
	for r, on := range v.ticked {
		if !on {
			continue
		}
		if _, ok := v.results[r]; ok || v.pending[r] || v.errs[r] != nil {
			return true
		}
	}
	return false
}

// filter rebuilds the visible list, keeping the cursor on the same report
// when it is still shown.
func (v *ScanView) filter() {
	prev, had := v.current()
	v.visible = v.visible[:0]
	for i, r := range v.reports {
		if v.hideLow && r.Score < lowScore {
			continue
		}
		v.visible = append(v.visible, i)
	}
	v.cursor = 0
	if had {
		for i, r := range v.visible {
			if r >= prev {
				v.cursor = i
				break
			}
		}
	}
	v.detailTop = 0
	v.scroll()
}

// current returns the reports index under the cursor.
func (v *ScanView) current() (int, bool) {
	if v.cursor < 0 || v.cursor >= len(v.visible) {
		return 0, false
	}
	return v.visible[v.cursor], true
}

func (v *ScanView) tickedCount() int {
	n := 0
	for _, on := range v.ticked {
		if on {
			n++
		}
	}
	return n
}

// step moves the cursor, or scrolls the details when they have focus.
func (v *ScanView) step(delta int) {
	if v.detailsF {
		v.detailTop += delta
		v.clampDetails()
		return
	}
	v.cursor = max(0, min(len(v.visible)-1, v.cursor+delta))
	v.detailTop = 0
	v.scroll()
}

// scroll adjusts top so the cursor row is visible.
func (v *ScanView) scroll() {
	rows := v.paneRows()
	if v.cursor < v.top {
		v.top = v.cursor
	}
	if v.cursor >= v.top+rows {
		v.top = v.cursor - rows + 1
	}
	if v.top < 0 {
		v.top = 0
	}
}

func (v *ScanView) clampDetails() {
	_, rightW := v.columns()
	limit := len(v.detailLines(rightW-4)) - (v.paneRows() - 2)
	v.detailTop = max(0, min(v.detailTop, limit))
}

// paneRows is the number of lines between header and footer.
func (v *ScanView) paneRows() int {
	return max(v.height-scanChromeRows, 3)
}

// columns splits the width roughly 45/55 between list and details.
func (v *ScanView) columns() (left, right int) {
	left = v.width * 45 / 100
	return left, max(v.width-left, 10)
}

func (v *ScanView) render() string {
	if v.help {
		return renderHelp(v.width, v.height, "Keys", v.helpKeys())
	}
	rows := v.paneRows()
	leftW, rightW := v.columns()

	var b strings.Builder
	b.WriteString(v.renderHeader())
	b.WriteString("\n\n")
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, v.renderList(leftW, rows), v.renderDetails(rightW, rows)))
	b.WriteString("\n")
	switch {
	case v.errLine != "":
		b.WriteString(v.styles.danger.Render(output.Truncate(v.errLine, v.width)))
	case v.flash != "":
		b.WriteString(v.styles.flash.Render(v.flash))
	}
	b.WriteString("\n")
	b.WriteString(v.styles.dim.Render(v.footerHelp()))
	out := b.String()
	if v.confirm != nil {
		out = overlay(out, v.confirm.View(v.width), v.width, v.height)
	}
	return out
}

// footerHelp lists the keys that currently do something.
func (v *ScanView) footerHelp() string {
	parts := []string{"↑/↓ move", "space tick", "tab focus", "f hide low"}
	if v.assessor.Assess != nil {
		parts = append(parts, "l ask AI (ticked)")
	}
	if v.assessor.Models != nil || (v.hosted() && v.assessor.HostedModels != nil) {
		parts = append(parts, "m model")
	}
	parts = append(parts, "enter uninstall", "esc back", "ctrl+x quit", "? help")
	return strings.Join(parts, " · ")
}

// helpKeys is the ? overlay table.
func (v *ScanView) helpKeys() []keyHelp {
	return []keyHelp{
		{"↑/↓", "move the cursor, or scroll the details when they have focus"},
		{"space", "tick / untick the extension for uninstall"},
		{"tab", "switch focus between the list and the details"},
		{"f", "hide / show extensions scoring under " + fmt.Sprint(lowScore)},
		{"l", "ask the configured AI about every ticked extension, " + fmt.Sprint(assessWorkers) + " at a time; rows already answered are skipped, errors are retried"},
		{"m", "pick a model: the provider's hosted list (Claude, OpenAI) or a local Ollama model"},
		{iconBenign + " " + iconMalicious + " " + iconUnsure + " " + iconError, fmt.Sprintf("AI verdict: benign · malicious (both at %.0f%% confidence or more) · suspicious, unknown or unsure · error", llm.ConfidenceThreshold*100)},
		{"enter", "uninstall the ticked extensions (asks you to type " + plan.ConfirmWord + ")"},
		{"esc", "back without changes"},
		{"ctrl+x", "quit rootout"},
		{"?", "this help"},
	}
}

// renderHeader draws the title row and the AI row.
func (v *ScanView) renderHeader() string {
	s := fmt.Sprintf("Scan results · %d extensions · %d ticked", len(v.reports), v.tickedCount())
	if v.hideLow {
		s += fmt.Sprintf(" · hiding %d under %d", len(v.reports)-len(v.visible), lowScore)
	}
	ai := scanNoAI
	switch {
	case v.assessor.Label != "":
		ai = "AI: " + v.assessor.Label
	case v.assessor.Models != nil:
		ai = scanNoAIPick
	}
	if len(v.results)+len(v.errs) > 0 {
		ai += labelSep + iconLegend()
	}
	return v.styles.title.Render(s) + "\n" + v.styles.dim.Render(output.Truncate(ai, v.width))
}

// renderList draws rows list lines, each exactly width cells wide.
func (v *ScanView) renderList(width, rows int) string {
	lines := make([]string, 0, rows)
	if len(v.visible) == 0 {
		lines = append(lines, padTo(v.styles.noMatch.Render("no extensions to show"), width))
	}
	for i := v.top; i < len(v.visible) && len(lines) < rows; i++ {
		lines = append(lines, v.renderRow(v.visible[i], i == v.cursor, width))
	}
	for len(lines) < rows {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// renderRow draws marker, score badge, checkbox, extension id, editor tag
// and the AI verdict icon in a fixed slot.
func (v *ScanView) renderRow(ri int, selected bool, width int) string {
	r := v.reports[ri]
	base, dim := lipgloss.NewStyle(), v.styles.dim
	if selected {
		base = v.styles.cursor
		dim = base.Faint(true)
		if v.detailsF {
			base, dim = lipgloss.NewStyle().Bold(true), v.styles.dim
		}
	}
	marker := "  "
	if selected {
		marker = "› "
	}
	band := v.band(r.Score).Inherit(base)
	tag := "[" + r.Ext.Editor + "]"
	prefix := marker + strings.Repeat(" ", scoreWidth+1) + checkbox(v.ticked[ri])
	// The id gets what is left after the prefix, the icon slot and the tag;
	// in a pane too narrow for the tag, the tag goes before the id does.
	avail := width - lipgloss.Width(prefix) - 1 - iconW
	if avail-lipgloss.Width(tag)-1 >= 4 {
		avail -= lipgloss.Width(tag) + 1
	} else {
		tag = ""
	}
	id := output.Truncate(r.Ext.ID, max(avail, 1))
	icon, iconStyle := v.verdictIcon(ri)

	var b strings.Builder
	b.WriteString(base.Render(marker))
	b.WriteString(band.Render(fmt.Sprintf("%*d", scoreWidth, r.Score)))
	b.WriteString(base.Render(" " + checkbox(v.ticked[ri]) + id))
	if tag != "" {
		b.WriteString(base.Render(" ") + dim.Render(tag))
	}
	b.WriteString(base.Render(" "))
	b.WriteString(iconStyle.Inherit(base).Render(icon))
	return padWith(b.String(), width, base)
}

// verdictIcon is the list icon for a report's AI state: a tick for benign
// and a cross for malicious at or above llm.ConfidenceThreshold, the
// warning sign for anything else assessed, "!" for an error, the spinner
// while asking, and nothing when not asked.
func (v *ScanView) verdictIcon(ri int) (string, lipgloss.Style) {
	s := v.styles
	switch {
	case v.pending[ri]:
		return v.spin.View(), s.dim
	case v.errs[ri] != nil:
		return iconError, s.dim
	}
	a, ok := v.results[ri]
	if !ok {
		return "", s.dim
	}
	switch {
	case a.Verdict == llm.Benign && a.Confident():
		return iconBenign, s.verdict[llm.Benign]
	case a.Verdict == llm.Malicious && a.Confident():
		return iconMalicious, s.verdict[llm.Malicious]
	}
	return iconUnsure, s.verdict[llm.Suspicious]
}

// iconLegend explains the icons; shown once something has been assessed.
func iconLegend() string {
	return iconBenign + " benign " + iconMalicious + " malicious " + iconUnsure + " unsure"
}

// band returns the score colour: red ≥ 70, yellow 30–69, dim below.
func (v *ScanView) band(score int) lipgloss.Style {
	switch {
	case score >= highScore:
		return v.styles.high
	case score >= lowScore:
		return v.styles.mid
	}
	return v.styles.low
}

// renderDetails draws the bordered details pane, exactly width × rows.
func (v *ScanView) renderDetails(width, rows int) string {
	// In lipgloss v2 Width/Height are the final block size, border included.
	pane := v.styles.pane
	if v.detailsF {
		pane = v.styles.focused
	}
	pane = pane.Width(width).Height(rows).MaxHeight(rows)
	lines := v.detailLines(width - 4) // border and padding
	inner := rows - 2
	from := min(v.detailTop, max(len(lines)-1, 0))
	to := min(from+inner, len(lines))
	return pane.Render(strings.Join(lines[from:to], "\n"))
}

// detailLines lays out the cursor report's details pre-wrapped to width,
// or the AI summary of the ticked rows once more than one is ticked and
// at least one has been asked.
func (v *ScanView) detailLines(width int) []string {
	width = max(width, 10)
	if v.tickedCount() > 1 && v.anyTickedAsked() {
		return v.summaryLines(width)
	}
	ri, ok := v.current()
	if !ok {
		return []string{v.styles.noMatch.Render("Nothing to show")}
	}
	r := v.reports[ri]
	e := r.Ext
	s := v.styles

	title := e.DisplayName
	if title == "" {
		title = e.ID
	}
	head := s.title.Render(output.Truncate(title, width))
	if v.pending[ri] {
		head += "  " + v.spin.View() + s.dim.Render(" asking the LLM…")
	}
	lines := []string{head, ""}

	if box := v.assessBox(ri, width); box != "" {
		lines = append(lines, strings.Split(box, "\n")...)
		lines = append(lines, "")
	}

	label := func(k, val string) string {
		return wrapLines(s.dim.Render(fmt.Sprintf("%-10s", k))+val, width)
	}
	lines = append(lines,
		label("ID", orDash(e.ID)),
		label("Publisher", orDash(e.Publisher)),
		label("Version", orDash(e.Version)),
		label("Editor", orDash(e.Editor)),
		label("Folder", orDash(e.Dir)),
		label("Score", v.band(r.Score).Render(fmt.Sprint(r.Score))),
	)
	if e.Orphaned {
		lines = append(lines, s.flash.Render("Orphaned: folder not registered with the editor"))
	}

	var intent, capability []heur.Finding
	for _, f := range r.Findings {
		if heur.IsCapability(f.Rule) {
			capability = append(capability, f)
		} else {
			intent = append(intent, f)
		}
	}
	if len(r.Findings) == 0 {
		lines = append(lines, "", s.dim.Render("No findings"))
	}
	for _, g := range []struct {
		name string
		fs   []heur.Finding
	}{{"Intent", intent}, {"Capabilities", capability}} {
		if len(g.fs) == 0 {
			continue
		}
		lines = append(lines, "", v.styles.heading.Render(g.name))
		for _, f := range g.fs {
			lines = append(lines, v.findingLines(f, width)...)
		}
	}

	var out []string
	for _, l := range lines {
		out = append(out, strings.Split(l, "\n")...)
	}
	return out
}

// summaryLines lists every ticked report with its AI status: verdict and
// confidence, the spinner while asking, the error, or "not asked", then
// the first sentence of the reasoning.
func (v *ScanView) summaryLines(width int) []string {
	s := v.styles
	order := v.tickedOrder()
	answered, asking := 0, 0
	for _, r := range order {
		if _, ok := v.results[r]; ok {
			answered++
		}
		if v.pending[r] {
			asking++
		}
	}
	head := fmt.Sprintf("AI summary · %d ticked · %d answered", len(order), answered)
	if asking > 0 {
		head += fmt.Sprintf(" · %d asking", asking)
	}
	lines := []string{s.title.Render(output.Truncate(head, width)), ""}
	shown := map[int]bool{}
	for _, r := range v.visible {
		shown[r] = true
	}
	for _, r := range order {
		e := v.reports[r].Ext
		name := e.ID + " " + s.dim.Render("["+e.Editor+"]")
		if !shown[r] {
			name += " " + s.dim.Render(summaryHidden)
		}
		var status, reason string
		switch {
		case v.pending[r]:
			status = v.spin.View() + s.dim.Render(" asking…")
		case v.errs[r] != nil:
			status = s.danger.Render("error: " + firstLine(strings.TrimPrefix(v.errs[r].Error(), "llm: ")))
		default:
			if a, ok := v.results[r]; ok {
				vs, ok := s.verdict[a.Verdict]
				if !ok {
					vs = s.dim
				}
				status = vs.Render(fmt.Sprintf("%s %.0f%%", strings.ToUpper(a.Verdict), a.Confidence*100))
				reason = firstSentence(a.Reasoning)
			} else {
				status = s.dim.Render("not asked")
			}
		}
		lines = append(lines, wrapLines(name+"  "+status, width))
		if reason != "" {
			lines = append(lines, indent(wrapLines(reason, width-2), "  "))
		}
		lines = append(lines, "")
	}
	var out []string
	for _, l := range lines {
		out = append(out, strings.Split(l, "\n")...)
	}
	return out
}

// firstSentence is s up to its first full stop (or the whole of s).
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

// findingLines renders "[severity] rule  file:line", the wrapped detail and
// the dim snippet.
func (v *ScanView) findingLines(f heur.Finding, width int) []string {
	s := v.styles
	sev := s.severity[f.Severity].Render("[" + f.Severity.String() + "]")
	loc := f.File
	if loc != "" && f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	first := sev + " " + f.Rule
	if loc != "" {
		first += "  " + s.dim.Render(loc)
	}
	out := []string{wrapLines(first, width)}
	if f.Detail != "" {
		out = append(out, indent(wrapLines(f.Detail, width-2), "  "))
	}
	if f.Snippet != "" {
		out = append(out, indent(s.dim.Render(wrapLines(f.Snippet, width-2)), "  "))
	}
	return out
}

// assessBox renders the LLM verdict, the pending error, or "" when neither.
func (v *ScanView) assessBox(ri, width int) string {
	s := v.styles
	box := s.box.Width(width)
	inner := max(width-4, 6)
	if err, ok := v.errs[ri]; ok {
		msg := "LLM unavailable: " + strings.TrimPrefix(err.Error(), "llm: ")
		return box.BorderForeground(lipgloss.Red).Render(s.danger.Render(wrapLines(msg, inner)))
	}
	a, ok := v.results[ri]
	if !ok {
		return ""
	}
	vs, ok := s.verdict[a.Verdict]
	if !ok {
		vs = s.dim
	}
	head := vs.Render(strings.ToUpper(a.Verdict)) + s.dim.Render(fmt.Sprintf("  %.0f%% confidence", a.Confidence*100))
	if a.Model != "" {
		head += s.dim.Render(" · " + a.Model)
	}
	parts := []string{head}
	if a.Reasoning != "" {
		parts = append(parts, wrapLines(a.Reasoning, inner))
	}
	if len(a.IOCs) > 0 {
		parts = append(parts, s.dim.Render("Indicators:"))
		for _, ioc := range a.IOCs {
			parts = append(parts, wrapLines("• "+ioc, inner))
		}
	}
	return box.Render(strings.Join(parts, "\n"))
}

// wrapLines word-wraps s to width cells, hard-breaking long words.
func wrapLines(s string, width int) string {
	return lipgloss.Wrap(s, max(width, 4), "/.")
}

// indent prefixes every line of s.
func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}
