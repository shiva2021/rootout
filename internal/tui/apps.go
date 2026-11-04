package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/output"
)

const (
	nothingTickedApps = "tick apps with space, then l"
	inspectingLabel   = "inspecting…"
	// inspectWorkers bounds the cursor-triggered fact probes in flight:
	// spctl may consult Apple and codesign walks the bundle.
	inspectWorkers = 2
)

// AppAssessFunc runs the configured model on one app's facts.
type AppAssessFunc func(ctx context.Context, f app.Facts) (llm.AppAssessment, error)

// AppAssessor is the AI the apps screen and the picker can ask about an
// app, plus how to gather the facts it needs and how to swap its model. It
// mirrors Assessor for extensions; see that type for the field semantics.
type AppAssessor struct {
	Provider string // "ollama" | "anthropic" | "openai"; "" = none configured
	Label    string // "anthropic · claude-sonnet-5-5"; "" = none configured
	// Inspect gathers the facts for an app (code signature, Gatekeeper,
	// origin, launch items); nil means Facts{App: a}, for tests.
	Inspect  func(ctx context.Context, a app.App) app.Facts
	Assess   AppAssessFunc // nil = none
	Reassess AppAssessFunc // asks ignoring saved answers; nil falls back to Assess
	// Cached answers from the saved assessments without a model call; nil
	// = nothing saved (local models, --no-cache).
	Cached       func(f app.Facts) (llm.AppAssessment, bool)
	Models       *ModelDeps
	HostedModels func(ctx context.Context) ([]llm.ModelInfo, error)
	OnModel      func(ctx context.Context, provider, model string) (AppAssessor, error)
}

// inspect runs Inspect or the test fallback.
func (a AppAssessor) inspect(ctx context.Context, x app.App) app.Facts {
	if a.Inspect == nil {
		return app.Facts{App: x}
	}
	return a.Inspect(ctx, x)
}

// AppsResult is what the user decided.
type AppsResult struct {
	Uninstall []app.App // ticked for removal, in display order
}

// factsMsg carries one app's gathered facts back to the view.
type factsMsg struct {
	app int
	f   app.Facts
}

// appAssessMsg carries one finished assessment (and the facts it was made
// from) back to the view; gen is the assessor generation the call was
// made under, so an answer from a model the user has since replaced is
// kept only where nothing newer exists.
type appAssessMsg struct {
	app int
	gen int
	f   app.Facts
	a   llm.AppAssessment
	err error
}

// appAssessorMsg carries the AppAssessor rebuilt after a model pick.
type appAssessorMsg struct {
	a   AppAssessor
	err error
}

// ReviewApps shows every installed app with its facts and the AI's answer
// and returns the apps the user ticked for removal; the caller runs the
// usual per-app plan and confirm for each. ok=false means the user
// cancelled; ctrl+x and ctrl+c return ErrQuit. Probes and model calls
// started here stop when the screen closes.
func ReviewApps(ctx context.Context, apps []app.App, a AppAssessor) (AppsResult, bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	v := NewAppsView(apps, a)
	v.ctx = ctx
	if _, err := tea.NewProgram(v, tea.WithContext(ctx)).Run(); err != nil {
		return AppsResult{}, false, runErr(err, "review apps")
	}
	if v.quitAll {
		return AppsResult{}, false, ErrQuit
	}
	res, ok := v.Result()
	return res, ok, nil
}

// AppsView is the apps review model. It implements tea.Model and is
// exported so tests can drive it without a terminal. Its queue fields and
// methods carry the ScanView names so the two can later share code.
type AppsView struct {
	ctx      context.Context
	apps     []app.App
	assessor AppAssessor
	styles   scanStyles
	spin     spinner.Model

	ticked     map[int]bool
	facts      map[int]app.Facts
	inspecting map[int]bool // cursor-triggered probes in flight
	results    map[int]llm.AppAssessment
	errs       map[int]error
	pending    map[int]bool
	queue      []int
	force      map[int]bool
	gen        int
	summary    bool
	detailsF   bool

	width, height int
	cursor, top   int
	detailTop     int
	flash         string
	errLine       string
	picker        embeddedPicker
	help          bool
	accepted      bool
	quitAll       bool
}

// NewAppsView builds the screen with every app listed and the cursor on
// the first one.
func NewAppsView(apps []app.App, a AppAssessor) *AppsView {
	return &AppsView{
		ctx:        context.Background(),
		apps:       apps,
		assessor:   a,
		styles:     defaultScanStyles(),
		spin:       spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		ticked:     map[int]bool{},
		facts:      map[int]app.Facts{},
		inspecting: map[int]bool{},
		results:    map[int]llm.AppAssessment{},
		errs:       map[int]error{},
		pending:    map[int]bool{},
		force:      map[int]bool{},
		width:      defaultWidth,
		height:     defaultHeight,
	}
}

// Result returns the apps ticked for removal once the program has quit;
// ok is false when the user cancelled.
func (v *AppsView) Result() (AppsResult, bool) {
	if !v.accepted {
		return AppsResult{}, false
	}
	var res AppsResult
	for i, a := range v.apps {
		if v.ticked[i] {
			res.Uninstall = append(res.Uninstall, a)
		}
	}
	return res, true
}

// Init implements tea.Model: the cursor row is inspected straight away.
func (v *AppsView) Init() tea.Cmd { return v.inspectCursor() }

// Update implements tea.Model.
func (v *AppsView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
		v.scroll()
		v.clampDetails()
		if v.picker != nil {
			v.picker.Update(msg)
		}
		return v, nil

	case factsMsg:
		delete(v.inspecting, msg.app)
		if _, have := v.facts[msg.app]; !have {
			v.facts[msg.app] = msg.f
		}
		v.fillCached(msg.app)
		v.syncPane()
		return v, tea.Batch(v.startNext(), v.inspectCursor())

	case appAssessMsg:
		delete(v.pending, msg.app)
		if _, have := v.facts[msg.app]; !have {
			v.facts[msg.app] = msg.f
		}
		if msg.gen != v.gen {
			// From a model the user has since replaced: a real answer, kept
			// (labelled with its model) unless the new model already has one.
			_, has := v.results[msg.app]
			if msg.err != nil || has {
				v.syncPane()
				return v, tea.Batch(v.startNext(), v.inspectCursor())
			}
		}
		if msg.err != nil {
			v.errs[msg.app] = msg.err
		} else {
			delete(v.errs, msg.app)
			v.results[msg.app] = msg.a
		}
		v.syncPane()
		return v, tea.Batch(v.startNext(), v.inspectCursor())

	case appAssessorMsg:
		if msg.err != nil {
			v.errLine = "model: " + msg.err.Error()
			return v, nil
		}
		v.gen++ // calls in flight keep running; their answers are kept only where nothing newer exists
		v.assessor.Provider, v.assessor.Label, v.assessor.Assess = msg.a.Provider, msg.a.Label, msg.a.Assess
		v.assessor.Models, v.assessor.HostedModels = msg.a.Models, msg.a.HostedModels
		v.assessor.Cached, v.assessor.Reassess = msg.a.Cached, msg.a.Reassess
		if msg.a.Inspect != nil {
			v.assessor.Inspect = msg.a.Inspect
		}
		if msg.a.OnModel != nil {
			v.assessor.OnModel = msg.a.OnModel
		}
		// Saved answers belong to the model that gave them; this screen's
		// own answers stay, labelled with their model. Facts are the
		// app's, not the model's, and stay.
		for i, a := range v.results {
			if a.Cached {
				delete(v.results, i)
			}
		}
		for i := range v.facts {
			v.fillCached(i)
		}
		v.syncPane()
		return v, nil

	case spinner.TickMsg:
		var cmds []tea.Cmd
		if len(v.pending)+len(v.inspecting) > 0 {
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

	if v.picker != nil {
		return v, v.delegate(msg)
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		m, cmd := v.key(k)
		v.syncPane()
		return m, cmd
	}
	return v, nil
}

// summaryShown reports whether the details pane is the AI summary.
func (v *AppsView) summaryShown() bool { return v.tickedCount() > 1 && v.anyTickedAsked() }

// syncPane resets the details scroll when the pane switches between the
// summary and a single app.
func (v *AppsView) syncPane() {
	if s := v.summaryShown(); s != v.summary {
		v.summary = s
		v.detailTop = 0
	}
}

func (v *AppsView) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	v.flash, v.errLine = "", ""
	switch k := msg.String(); k {
	case "esc", "q":
		v.accepted = false
		return v, tea.Quit
	case "tab", "shift+tab":
		v.detailsF = !v.detailsF
	case "up", "ctrl+p":
		v.step(-1)
		return v, v.inspectCursor()
	case "down", "ctrl+n":
		v.step(1)
		return v, v.inspectCursor()
	case "pgup":
		v.step(-v.paneRows())
		return v, v.inspectCursor()
	case "pgdown":
		v.step(v.paneRows())
		return v, v.inspectCursor()
	case "l":
		return v, v.enqueueAssess(false)
	case "L":
		return v, v.enqueueAssess(true)
	case "space":
		if i, ok := v.current(); ok {
			if v.apps[i].Protected {
				v.flash = protectedFlash
			} else {
				v.ticked[i] = !v.ticked[i]
			}
		}
	case "ctrl+a":
		v.tickAll()
	case "m":
		return v, v.openPicker()
	case "?":
		v.help = true
	case "enter":
		if v.tickedCount() == 0 {
			v.flash = nothingTicked
			return v, nil
		}
		v.accepted = true
		return v, tea.Quit
	}
	return v, nil
}

// tickAll ticks every removable app, or unticks everything when every
// removable app is already ticked.
func (v *AppsView) tickAll() {
	all := true
	for i, a := range v.apps {
		if !a.Protected && !v.ticked[i] {
			all = false
			break
		}
	}
	for i, a := range v.apps {
		if a.Protected {
			continue
		}
		v.ticked[i] = !all
	}
}

// openPicker embeds a model picker over the screen: the configured hosted
// provider's list when it can be fetched, else the Ollama picker; nil when
// neither is available.
func (v *AppsView) openPicker() tea.Cmd {
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

// pickerProvider is the provider a picked model belongs to.
func (v *AppsView) pickerProvider() string {
	if _, ok := v.picker.(*HostedPicker); ok {
		return v.assessor.Provider
	}
	return scanProvider
}

// currentModel is the model part of the "<provider> · <model>" label.
func (v *AppsView) currentModel() string {
	_, model, _ := strings.Cut(v.assessor.Label, labelSep)
	return model
}

// currentOllamaModel is the current model when the provider is Ollama.
func (v *AppsView) currentOllamaModel() string {
	if v.assessor.Provider != scanProvider {
		return ""
	}
	return v.currentModel()
}

// delegate forwards msg to the embedded picker and, once it has finished,
// closes it and rebuilds the AppAssessor for the chosen model.
func (v *AppsView) delegate(msg tea.Msg) tea.Cmd {
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
		return appAssessorMsg{a: a, err: err}
	}
}

func (v *AppsView) closePicker() {
	if v.picker != nil {
		v.picker.cancel()
		v.picker = nil
	}
}

// View renders the whole screen in the alternate buffer.
func (v *AppsView) View() tea.View {
	if v.picker != nil {
		return v.picker.View()
	}
	view := tea.NewView(v.render())
	view.AltScreen = true
	return view
}

// inspectCursor starts a facts probe for the cursor row when it has no
// facts, none is on its way (a probe or an ask) and fewer than
// inspectWorkers probes are in flight. Called after every cursor move and
// every finished probe, so a row skipped while busy is picked up when the
// cursor rests on it.
func (v *AppsView) inspectCursor() tea.Cmd {
	i, ok := v.current()
	if !ok {
		return nil
	}
	if _, have := v.facts[i]; have || v.inspecting[i] || v.pending[i] || len(v.inspecting) >= inspectWorkers {
		return nil
	}
	idle := len(v.pending)+len(v.inspecting) == 0
	v.inspecting[i] = true
	ctx, a, inspect := v.ctx, v.apps[i], v.assessor.inspect
	cmd := func() tea.Msg { return factsMsg{app: i, f: inspect(ctx, a)} }
	if idle {
		return tea.Batch(cmd, v.spin.Tick)
	}
	return cmd
}

// fillCached gives row i its saved answer when it has facts, no answer yet
// and the assessor has one.
func (v *AppsView) fillCached(i int) {
	if v.assessor.Cached == nil {
		return
	}
	if _, has := v.results[i]; has {
		return
	}
	f, ok := v.facts[i]
	if !ok {
		return
	}
	if a, ok := v.assessor.Cached(f); ok {
		v.results[i] = a
	}
}

// enqueueAssess queues the ticked apps for the model, skipping the ones
// already answered or in flight (an errored one is asked again), and
// starts as many as the worker bound allows. With force (the L key) the
// answered ones are asked again, ignoring saved answers.
func (v *AppsView) enqueueAssess(force bool) tea.Cmd {
	if v.assessor.Assess == nil {
		return nil
	}
	order := v.tickedOrder()
	if len(order) == 0 {
		v.flash = nothingTickedApps
		return nil
	}
	answered, asking, asked := 0, 0, 0
	for _, i := range order {
		if v.pending[i] || slices.Contains(v.queue, i) {
			asking++
			continue
		}
		if _, ok := v.results[i]; ok && !force {
			answered++
			continue
		}
		v.queue = append(v.queue, i)
		if force {
			v.force[i] = true
		}
		asked++
	}
	var parts []string
	if asked > 0 {
		parts = append(parts, fmt.Sprintf("asking %d", asked))
	}
	if answered > 0 {
		parts = append(parts, fmt.Sprintf("%d already answered", answered))
	}
	if asking > 0 {
		parts = append(parts, fmt.Sprintf("%d already asking", asking))
	}
	v.flash = strings.Join(parts, labelSep)
	return v.startNext()
}

// startNext hands queued apps to free workers. A row whose facts are still
// being gathered by a cursor probe stays queued (the probe's factsMsg calls
// startNext again); a row without facts and without a probe gathers them
// inside the call. Each call captures the Assess in force when it starts,
// so a model swap applies to the rest of the queue.
func (v *AppsView) startNext() tea.Cmd {
	var cmds []tea.Cmd
	idle := len(v.pending)+len(v.inspecting) == 0
	for qi := 0; qi < len(v.queue) && len(v.pending) < assessWorkers; {
		i := v.queue[qi]
		if !v.ticked[i] {
			// Unticked while waiting: the user changed their mind before it
			// cost anything.
			v.queue = slices.Delete(v.queue, qi, qi+1)
			delete(v.force, i)
			continue
		}
		if v.inspecting[i] {
			qi++
			continue
		}
		v.queue = slices.Delete(v.queue, qi, qi+1)
		v.pending[i] = true
		delete(v.errs, i)
		assess := v.assessor.Assess
		if v.force[i] {
			delete(v.force, i)
			if v.assessor.Reassess != nil {
				assess = v.assessor.Reassess
			}
		}
		ctx, a, gen, inspect := v.ctx, v.apps[i], v.gen, v.assessor.inspect
		facts, have := v.facts[i]
		cmds = append(cmds, func() tea.Msg {
			f := facts
			if !have {
				f = inspect(ctx, a)
			}
			r, err := assess(ctx, f)
			return appAssessMsg{app: i, gen: gen, f: f, a: r, err: err}
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

// tickedOrder is the ticked apps in display order.
func (v *AppsView) tickedOrder() []int {
	var out []int
	for i := range v.apps {
		if v.ticked[i] {
			out = append(out, i)
		}
	}
	return out
}

// anyTickedAsked reports whether a ticked row has a result, an error or a
// call in flight: only then does the summary replace the single detail.
func (v *AppsView) anyTickedAsked() bool {
	for i, on := range v.ticked {
		if !on {
			continue
		}
		if _, ok := v.results[i]; ok || v.pending[i] || v.errs[i] != nil {
			return true
		}
	}
	return false
}

// current returns the apps index under the cursor.
func (v *AppsView) current() (int, bool) {
	if v.cursor < 0 || v.cursor >= len(v.apps) {
		return 0, false
	}
	return v.cursor, true
}

func (v *AppsView) tickedCount() int {
	n := 0
	for _, on := range v.ticked {
		if on {
			n++
		}
	}
	return n
}

// step moves the cursor, or scrolls the details when they have focus.
func (v *AppsView) step(delta int) {
	if v.detailsF {
		v.detailTop += delta
		v.clampDetails()
		return
	}
	v.cursor = max(0, min(len(v.apps)-1, v.cursor+delta))
	v.detailTop = 0
	v.scroll()
}

// scroll adjusts top so the cursor row is visible.
func (v *AppsView) scroll() {
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

func (v *AppsView) clampDetails() {
	_, rightW := v.columns()
	limit := len(v.detailLines(rightW-4)) - (v.paneRows() - 2)
	v.detailTop = max(0, min(v.detailTop, limit))
}

// paneRows is the number of lines between header and footer.
func (v *AppsView) paneRows() int {
	return max(v.height-scanChromeRows, 3)
}

// columns splits the width roughly 45/55 between list and details.
func (v *AppsView) columns() (left, right int) {
	left = v.width * 45 / 100
	return left, max(v.width-left, 10)
}

func (v *AppsView) render() string {
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
	return b.String()
}

// appsFooterDropOrder is the order in which footer entries give way on a
// narrow terminal (they all remain in the ? overlay).
var appsFooterDropOrder = []string{"tab focus", "ctrl+a tick all", "ctrl+x quit", "esc back", "space tick", "enter uninstall", "L re-ask", "m model", "l ask AI (ticked)", "↑/↓ move"}

// footerHelp lists the keys that currently do something.
func (v *AppsView) footerHelp() string {
	parts := []string{"↑/↓ move", "space tick", "ctrl+a tick all", "tab focus"}
	if v.assessor.Assess != nil {
		parts = append(parts, "l ask AI (ticked)", "L re-ask")
	}
	if v.assessor.Models != nil || (v.hosted() && v.assessor.HostedModels != nil) {
		parts = append(parts, "m model")
	}
	parts = append(parts, "enter uninstall", "esc back", "ctrl+x quit", "? help")
	return fitFooterWith(parts, v.width, appsFooterDropOrder)
}

// hosted reports whether the configured provider is a hosted one.
func (v *AppsView) hosted() bool {
	return v.assessor.Provider != "" && v.assessor.Provider != scanProvider
}

// helpKeys is the ? overlay table.
func (v *AppsView) helpKeys() []keyHelp {
	return []keyHelp{
		{"↑/↓", "move the cursor, or scroll the details when they have focus; the app under the cursor is inspected (signature, Gatekeeper, origin)"},
		{"space", "tick / untick the app for uninstall (system apps cannot be ticked)"},
		{"ctrl+a", "tick every app, or untick all when all are ticked"},
		{"tab", "switch focus between the list and the details"},
		{"l", "ask the configured AI about every ticked app, " + fmt.Sprint(assessWorkers) + " at a time; rows already answered are skipped, errors are retried"},
		{"L", "ask again for the ticked apps, ignoring saved answers (paid providers keep answers across runs)"},
		{"m", "pick a model: the provider's hosted list (Claude, OpenAI) or a local Ollama model"},
		{iconBenign + " " + iconMalicious + " " + iconUnsure + " " + iconError, fmt.Sprintf("AI recommendation: keep · drop (both at %.0f%% confidence or more) · review or unsure · error", llm.ConfidenceThreshold*100)},
		{"enter", "uninstall the ticked apps: each one gets the usual review of its files before anything is removed"},
		{"esc", "back without changes"},
		{"ctrl+x", "quit rootout"},
		{"?", "this help"},
	}
}

// renderHeader draws the title row and the AI row.
func (v *AppsView) renderHeader() string {
	s := fmt.Sprintf("Installed apps · %d apps · %d ticked", len(v.apps), v.tickedCount())
	ai := scanNoAI
	switch {
	case v.assessor.Label != "":
		ai = "AI: " + v.assessor.Label
	case v.assessor.Models != nil:
		ai = scanNoAIPick
	}
	if len(v.results)+len(v.errs) > 0 {
		ai += labelSep + recLegend()
	}
	return v.styles.title.Render(s) + "\n" + v.styles.dim.Render(output.Truncate(ai, v.width))
}

// recLegend explains the icons; shown once something has been assessed.
func recLegend() string {
	return iconBenign + " keep " + iconMalicious + " drop " + iconUnsure + " unsure"
}

// renderList draws rows list lines, each exactly width cells wide.
func (v *AppsView) renderList(width, rows int) string {
	lines := make([]string, 0, rows)
	if len(v.apps) == 0 {
		lines = append(lines, padTo(v.styles.noMatch.Render("no apps to show"), width))
	}
	for i := v.top; i < len(v.apps) && len(lines) < rows; i++ {
		lines = append(lines, v.renderRow(i, i == v.cursor, width))
	}
	for len(lines) < rows {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// sourceTag is the dim tag after the name: where the app came from.
func (v *AppsView) sourceTag(i int) string {
	a := v.apps[i]
	switch {
	case a.Protected:
		return "[system]"
	case a.Cask != "":
		return "[brew]"
	}
	if f, ok := v.facts[i]; ok && f.AppStore {
		return "[app store]"
	}
	return ""
}

// renderRow draws marker, checkbox, name, dim version and source tag, the
// right-aligned size and the AI icon in a fixed slot. On a narrow pane the
// tag goes first, then the version, then the name is cut.
func (v *AppsView) renderRow(i int, selected bool, width int) string {
	a := v.apps[i]
	base, dim := lipgloss.NewStyle(), v.styles.dim
	if selected {
		base = v.styles.cursor
		dim = base.Faint(true)
		if v.detailsF {
			base, dim = lipgloss.NewStyle().Bold(true), v.styles.dim
		}
	}
	nameStyle := base
	if a.Protected {
		nameStyle = dim
	}
	marker := "  "
	if selected {
		marker = "› "
	}
	prefix := marker + checkbox(v.ticked[i])
	size := fmt.Sprintf("%*s", sizeWidth, output.Bytes(a.SizeBytes))
	icon, iconStyle := v.recIcon(i)
	// Cells left for name, version and tag once the prefix, the size and
	// the icon slot are placed; a pane too narrow for a readable name
	// loses the size column first.
	avail := width - lipgloss.Width(prefix) - 1 - sizeWidth - 1 - iconW
	if avail < 6 {
		size = ""
		avail = width - lipgloss.Width(prefix) - 1 - iconW
	}
	tag, ver := v.sourceTag(i), a.Version
	nameW := lipgloss.Width(a.Name)
	need := func() int {
		n := nameW
		if ver != "" {
			n += 1 + lipgloss.Width(ver)
		}
		if tag != "" {
			n += 1 + lipgloss.Width(tag)
		}
		return n
	}
	if need() > avail {
		tag = ""
	}
	if need() > avail {
		ver = ""
	}
	name := cutTo(a.Name, avail)

	var b strings.Builder
	b.WriteString(base.Render(marker + checkbox(v.ticked[i])))
	b.WriteString(nameStyle.Render(name))
	if ver != "" {
		b.WriteString(base.Render(" ") + dim.Render(ver))
	}
	if tag != "" {
		b.WriteString(base.Render(" ") + dim.Render(tag))
	}
	leftW := width - 1 - iconW
	if size != "" {
		leftW -= 1 + sizeWidth
		size = base.Render(" ") + dim.Render(size)
	}
	left := padWith(b.String(), leftW, base)
	return padWith(left+size+base.Render(" ")+iconStyle.Inherit(base).Render(icon), width, base)
}

// cutTo truncates s to at most n cells with an ellipsis when n allows one;
// n < 1 yields "".
func cutTo(s string, n int) string {
	if n < 1 {
		return ""
	}
	if n == 1 {
		if lipgloss.Width(s) <= 1 {
			return s
		}
		return "…"
	}
	return output.Truncate(s, n)
}

// recIcon is the list icon for an app's AI state: a tick for keep and a
// cross for drop at or above llm.ConfidenceThreshold, the warning sign for
// anything else assessed, "!" for an error, the spinner while asking, and
// nothing when not asked.
func (v *AppsView) recIcon(i int) (string, lipgloss.Style) {
	s := v.styles
	switch {
	case v.pending[i]:
		return v.spin.View(), s.dim
	case v.errs[i] != nil:
		return iconError, s.dim
	}
	a, ok := v.results[i]
	if !ok {
		return "", s.dim
	}
	switch {
	case a.Recommendation == llm.Keep && a.Confident():
		return iconBenign, s.verdict[llm.Benign]
	case a.Recommendation == llm.Drop && a.Confident():
		return iconMalicious, s.verdict[llm.Malicious]
	}
	return iconUnsure, s.verdict[llm.Suspicious]
}

// recStyle colours a recommendation word.
func (v *AppsView) recStyle(rec string) lipgloss.Style {
	switch rec {
	case llm.Keep:
		return v.styles.verdict[llm.Benign]
	case llm.Drop:
		return v.styles.verdict[llm.Malicious]
	}
	return v.styles.verdict[llm.Suspicious]
}

// renderDetails draws the bordered details pane, exactly width × rows.
func (v *AppsView) renderDetails(width, rows int) string {
	pane := v.styles.pane
	if v.detailsF {
		pane = v.styles.focused
	}
	pane = pane.Width(width).Height(rows).MaxHeight(rows)
	lines := v.detailLines(width - 4)
	inner := rows - 2
	from := min(v.detailTop, max(len(lines)-1, 0))
	to := min(from+inner, len(lines))
	return pane.Render(strings.Join(lines[from:to], "\n"))
}

// detailLines lays out the cursor app's details pre-wrapped to width, or
// the AI summary of the ticked rows once more than one is ticked and at
// least one has been asked.
func (v *AppsView) detailLines(width int) []string {
	width = max(width, 10)
	if v.summaryShown() {
		return v.summaryLines(width)
	}
	i, ok := v.current()
	if !ok {
		return []string{v.styles.noMatch.Render("Nothing to show")}
	}
	a := v.apps[i]
	s := v.styles

	head := s.title.Render(output.Truncate(a.Name, width))
	switch {
	case v.pending[i]:
		head += "  " + v.spin.View() + s.dim.Render(" asking the AI…")
	case v.inspecting[i]:
		head += "  " + v.spin.View() + s.dim.Render(" "+inspectingLabel)
	}
	lines := []string{head, ""}
	if box := v.assessBox(i, width); box != "" {
		lines = append(lines, strings.Split(box, "\n")...)
		lines = append(lines, "")
	}
	label := func(k, val string) string {
		return wrapLines(s.dim.Render(fmt.Sprintf("%-11s", k))+val, width)
	}
	source := "—"
	switch {
	case a.Protected:
		source = "system application"
	case a.Cask != "":
		source = "Homebrew cask " + a.Cask
	}
	lines = append(lines,
		label("Bundle ID", orDash(a.BundleID)),
		label("Version", orDash(a.Version)),
		label("Path", a.Path),
		label("Size", output.Bytes(a.SizeBytes)),
		label("Source", source),
	)
	if a.Protected {
		lines = append(lines, s.danger.Render("Protected: system application, cannot be removed"))
	}
	f, ok := v.facts[i]
	if !ok {
		if !v.inspecting[i] && !v.pending[i] {
			lines = append(lines, "", s.dim.Render("Facts not gathered yet"))
		}
	} else {
		lines = append(lines, "", v.styles.heading.Render("Facts"))
		lines = append(lines, v.factLines(f, label)...)
	}
	var out []string
	for _, l := range lines {
		out = append(out, strings.Split(l, "\n")...)
	}
	return out
}

// factLines renders the gathered facts, one label per line, styled by
// tone: warnings in the flash colour, missing evidence dim.
func (v *AppsView) factLines(f app.Facts, label func(k, val string) string) []string {
	s := v.styles
	var lines []string
	for _, l := range f.Summary() {
		val := l.Value
		switch l.Tone {
		case app.Warn:
			val = s.flash.Render(val)
		case app.Dim:
			val = s.dim.Render(val)
		}
		lines = append(lines, label(l.Label, val))
	}
	return lines
}

// assessBox renders the AI answer, the pending error, or "" when neither.
func (v *AppsView) assessBox(i, width int) string {
	s := v.styles
	box := s.box.Width(width)
	inner := max(width-4, 6)
	if err, ok := v.errs[i]; ok {
		msg := "AI unavailable: " + strings.TrimPrefix(err.Error(), "llm: ")
		return box.BorderForeground(lipgloss.Red).Render(s.danger.Render(wrapLines(msg, inner)))
	}
	a, ok := v.results[i]
	if !ok {
		return ""
	}
	head := v.recStyle(a.Recommendation).Render(strings.ToUpper(a.Recommendation)) + s.dim.Render(fmt.Sprintf("  %.0f%% confidence", a.Confidence*100))
	if a.Model != "" {
		head += s.dim.Render(" · " + a.Model)
	}
	if a.Cached {
		head += s.dim.Render(" · " + savedTag)
	}
	parts := []string{head}
	if a.Purpose != "" {
		parts = append(parts, wrapLines(a.Purpose, inner))
	}
	parts = append(parts, s.dim.Render("Source: ")+a.Source)
	if a.Reasoning != "" {
		parts = append(parts, wrapLines(a.Reasoning, inner))
	}
	return box.Render(strings.Join(parts, "\n"))
}

// summaryLines lists every ticked app with its AI status and purpose.
func (v *AppsView) summaryLines(width int) []string {
	s := v.styles
	order := v.tickedOrder()
	answered, asking := 0, 0
	for _, i := range order {
		if _, ok := v.results[i]; ok {
			answered++
		}
		if v.pending[i] {
			asking++
		}
	}
	head := fmt.Sprintf("AI summary · %d ticked · %d answered", len(order), answered)
	if asking > 0 {
		head += fmt.Sprintf(" · %d asking", asking)
	}
	lines := []string{s.title.Render(output.Truncate(head, width)), ""}
	for _, i := range order {
		name := v.apps[i].Name
		var status, purpose string
		switch {
		case v.pending[i]:
			status = v.spin.View() + s.dim.Render(" asking…")
		case v.errs[i] != nil:
			status = s.danger.Render("error: " + firstLine(strings.TrimPrefix(v.errs[i].Error(), "llm: ")))
		default:
			if a, ok := v.results[i]; ok {
				status = v.recStyle(a.Recommendation).Render(fmt.Sprintf("%s %.0f%%", strings.ToUpper(a.Recommendation), a.Confidence*100))
				if a.Cached {
					status += s.dim.Render(" " + savedTag)
				}
				purpose = a.Purpose
			} else {
				status = s.dim.Render("not asked")
			}
		}
		lines = append(lines, wrapLines(name+"  "+status, width))
		if purpose != "" {
			lines = append(lines, indent(wrapLines(purpose, width-2), "  "))
		}
		lines = append(lines, "")
	}
	var out []string
	for _, l := range lines {
		out = append(out, strings.Split(l, "\n")...)
	}
	return out
}
