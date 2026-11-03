package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/output"
)

const (
	setupMenuHelp = "↑/↓ move · enter select · esc skip · ctrl+x quit · ? help"
	setupKeyHelp  = "enter check and save · esc back · ctrl+x quit"
	setupFailHelp = "r retry · s save anyway · esc back · ? help"
	setupTitle    = "Welcome to rootout"
	setupIntro    = "rootout removes macOS apps completely and scores editor extensions (VS Code, Cursor, Windsurf…) for malicious behaviour. Scores come from built-in rules. Optionally, an AI model reads each finding and recommends keep or uninstall: press l on an extension in the scan screen."
	setupNoKey    = "paste a key first"
	setupChecking = "Checking the key…"
)

// setupKeys is the ? overlay of the setup screen.
var setupKeys = []keyHelp{
	{"↑/↓", "move"},
	{"enter", "select / check the key"},
	{"esc", "skip (menu) or back (key screen)"},
	{"r", "retry after a failed check"},
	{"s", "save the key without a successful check"},
	{"ctrl+x", "quit rootout"},
	{"?", "this help"},
}

// SetupDeps is what the setup screen needs from the caller. The screen never
// touches config or the Keychain itself; the caller persists SetupResult.
type SetupDeps struct {
	Models    *ModelDeps // nil = local model picking unavailable
	ModelsErr string     // why, shown on the Ollama row
	// Verify checks a hosted provider's key with one request, the models
	// list, and returns it so the model step needs no second call. An empty
	// key means "keep the stored one": the caller resolves and checks that.
	Verify func(ctx context.Context, provider, key string) ([]llm.ModelInfo, error)
	// Existing reports a stored key for provider as its masked form and
	// source ("keychain" or "env"); masked "" with a nil error means none.
	Existing func(provider string) (masked, source string, err error)
	Current  string // current Ollama model, pre-selected in the picker
	// CurrentHosted is the configured model for a hosted provider,
	// pre-selected in its picker; nil means none known.
	CurrentHosted func(provider string) string
}

// SetupResult is what the user chose.
type SetupResult struct {
	Provider    string // "" when skipped
	Model       string // model for Provider; "" keeps the configured one
	Key         string // hosted key; "" keeps the stored one
	KeyVerified bool
	Warning     string // the check error when the key was saved anyway
	Skipped     bool
}

// RunSetup shows the setup screen and returns the choice. It returns ErrQuit
// when the user quits rootout (ctrl+x, ctrl+c).
func RunSetup(ctx context.Context, d SetupDeps) (SetupResult, error) {
	s := NewSetup(ctx, d)
	if _, err := tea.NewProgram(s, tea.WithContext(ctx)).Run(); err != nil {
		return SetupResult{}, runErr(err, "setup")
	}
	if s.quitAll {
		return SetupResult{}, ErrQuit
	}
	res, _ := s.Result()
	return res, nil
}

type setupStep int

const (
	stepMenu setupStep = iota
	stepKey
	stepPicker // Ollama picker over the menu
	stepModel  // hosted picker after a verified key
)

type setupRow struct {
	provider string // "ollama" | "anthropic" | "openai" | "" (skip)
	title    string
	blurb    string
}

var setupRows = []setupRow{
	{"ollama", "Local model (Ollama)", "free, private, runs on this Mac (2–5 GB download)"},
	{"anthropic", "Claude (Anthropic)", "paste an API key from console.anthropic.com"},
	{"openai", "OpenAI", "paste an API key from platform.openai.com"},
	{"", "Not now", "heuristics only · rootout setup any time"},
}

// hosted names the providers for the key screen and the keys' environment variables.
var hosted = map[string]struct{ name, site, envVar string }{
	"anthropic": {"Claude (Anthropic)", "console.anthropic.com", "ANTHROPIC_API_KEY"},
	"openai":    {"OpenAI", "platform.openai.com", "OPENAI_API_KEY"},
}

// verifiedMsg is the outcome of a key check, with the models the key can
// use; id matches the check it belongs to.
type verifiedMsg struct {
	id     int
	models []llm.ModelInfo
	err    error
}

// Setup is the setup screen model. It implements tea.Model.
type Setup struct {
	ctx    context.Context
	deps   SetupDeps
	styles styles
	spin   spinner.Model

	step          setupStep
	cursor        int
	width, height int
	help          bool

	// key screen
	provider   string
	input      textinput.Model
	note       string // stored-key / env / Keychain line
	hasStored  bool   // enter with nothing typed keeps the stored key
	hint       string // one-line hint or error under the input
	checking   bool
	failed     bool // last check failed: r / s / esc
	verifyID   int
	cancelFunc context.CancelFunc

	picker embeddedPicker

	result  SetupResult
	ok      bool
	quitAll bool
}

// NewSetup builds the screen on the menu step.
func NewSetup(ctx context.Context, d SetupDeps) *Setup {
	return &Setup{
		ctx:    ctx,
		deps:   d,
		styles: defaultStyles(),
		spin:   spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		width:  defaultWidth,
		height: defaultHeight,
	}
}

// Result returns the choice once the program has quit; ok is false when the
// user quit rootout instead.
func (s *Setup) Result() (SetupResult, bool) { return s.result, s.ok }

// Init has nothing to start.
func (s *Setup) Init() tea.Cmd { return nil }

// Update handles resizes, keys, check results and spinner ticks. While the
// model picker is open, other messages go to it.
func (s *Setup) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
		if s.picker != nil {
			s.picker.Update(msg)
		}
		return s, nil

	case verifiedMsg:
		if msg.id != s.verifyID || !s.checking {
			return s, nil
		}
		s.checking = false
		s.cancelFunc = nil
		if msg.err == nil {
			return s, s.openHosted(msg.models)
		}
		s.failed = true
		s.hint = firstLine(msg.err.Error())
		return s, nil

	case spinner.TickMsg:
		var cmds []tea.Cmd
		if s.checking {
			var cmd tea.Cmd
			s.spin, cmd = s.spin.Update(msg)
			cmds = append(cmds, cmd)
		}
		if s.picker != nil {
			cmds = append(cmds, s.delegate(msg))
		}
		return s, tea.Batch(cmds...)

	case tea.KeyPressMsg:
		if isQuitKey(msg.String()) {
			s.closePicker()
			s.cancelCheck()
			s.quitAll = true
			return s, tea.Quit
		}
		if s.help {
			s.help = false
			return s, nil
		}
	}

	if s.picker != nil {
		return s, s.delegate(msg)
	}
	switch s.step {
	case stepKey:
		return s, s.keyScreen(msg)
	default:
		if k, ok := msg.(tea.KeyPressMsg); ok {
			return s, s.menuKey(k)
		}
	}
	return s, nil
}

func (s *Setup) menuKey(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if msg.Text == "?" {
		k = "?"
	}
	switch k {
	case "?":
		s.help = true
	case "esc", "q":
		return s.finish(SetupResult{Skipped: true})
	case "up", "ctrl+p":
		s.cursor = max(s.cursor-1, 0)
	case "down", "ctrl+n":
		s.cursor = min(s.cursor+1, len(setupRows)-1)
	case "enter":
		row := setupRows[s.cursor]
		switch row.provider {
		case "":
			return s.finish(SetupResult{Skipped: true})
		case "ollama":
			return s.openPicker()
		default:
			s.openKey(row.provider)
			return s.input.Focus()
		}
	}
	return nil
}

// openKey switches to the key screen for a hosted provider.
func (s *Setup) openKey(provider string) {
	s.step, s.provider = stepKey, provider
	s.hint, s.failed, s.checking = "", false, false
	in := textinput.New()
	in.Prompt = typedPrompt
	in.EchoMode = textinput.EchoPassword
	in.CharLimit = 0
	st := in.Styles()
	st.Focused.Prompt = s.styles.prompt
	st.Focused.Text = lipgloss.NewStyle()
	in.SetStyles(st)
	s.input = in

	s.note, s.hasStored = "", false
	if s.deps.Existing == nil {
		return
	}
	masked, source, err := s.deps.Existing(provider)
	switch {
	case err != nil:
		s.note = "Keychain unavailable: " + firstLine(err.Error())
	case masked == "":
	case source == "env":
		s.note = "Using " + hosted[provider].envVar + " from the environment; a saved key is ignored while it is set · enter checks it"
		s.hasStored = true
	default:
		s.note = "Stored key " + masked + " (" + source + ") · enter keeps it · paste a new key to replace it"
		s.hasStored = true
	}
}

// keyScreen handles the key step: typing goes to the input; enter checks;
// after a failed check r retries and s saves anyway; esc goes back.
func (s *Setup) keyScreen(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.PasteMsg:
		m.Content = strings.NewReplacer("\r", "", "\n", "").Replace(m.Content)
		msg = m
	case tea.KeyPressMsg:
		k := m.String()
		switch {
		case k == "esc":
			s.cancelCheck()
			s.step = stepMenu
			return nil
		case s.checking:
			return nil
		case k == "enter":
			return s.startCheck()
		case s.failed:
			switch k {
			case "r":
				s.failed, s.hint = false, ""
				return tea.Batch(s.input.Focus(), s.startCheck())
			case "s":
				return s.finish(SetupResult{Provider: s.provider, Key: s.typedKey(), Warning: s.hint})
			case "?":
				s.help = true
			}
			return nil
		}
		s.hint = ""
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd
}

// typedKey is the trimmed input; "" keeps the stored key.
func (s *Setup) typedKey() string { return strings.TrimSpace(s.input.Value()) }

// startCheck runs Deps.Verify as a Cmd with the spinner ticking.
func (s *Setup) startCheck() tea.Cmd {
	key := s.typedKey()
	if key == "" && !s.hasStored {
		s.hint = setupNoKey
		return nil
	}
	if s.deps.Verify == nil {
		return s.finish(SetupResult{Provider: s.provider, Key: key, Warning: "key not checked"})
	}
	s.checking, s.failed, s.hint = true, false, ""
	s.input.Blur()
	s.verifyID++
	id, provider, verify := s.verifyID, s.provider, s.deps.Verify
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancelFunc = cancel
	return tea.Batch(func() tea.Msg {
		defer cancel()
		models, err := verify(ctx, provider, key)
		return verifiedMsg{id: id, models: models, err: err}
	}, s.spin.Tick)
}

func (s *Setup) cancelCheck() {
	if s.cancelFunc != nil {
		s.cancelFunc()
		s.cancelFunc = nil
	}
	s.checking = false
}

// openPicker embeds the model picker; nil when local picking is unavailable.
func (s *Setup) openPicker() tea.Cmd {
	if s.deps.Models == nil {
		return nil
	}
	p := NewModelPicker(s.ctx, *s.deps.Models, s.deps.Current)
	p.setEmbedded(true)
	p.Update(tea.WindowSizeMsg{Width: s.width, Height: s.height})
	s.picker = p
	s.step = stepPicker
	return p.Init()
}

// openHosted embeds the hosted model picker over the verified key screen.
func (s *Setup) openHosted(models []llm.ModelInfo) tea.Cmd {
	current := ""
	if s.deps.CurrentHosted != nil {
		current = s.deps.CurrentHosted(s.provider)
	}
	p := NewHostedPickerWith(models, s.provider, current)
	p.setEmbedded(true)
	p.Update(tea.WindowSizeMsg{Width: s.width, Height: s.height})
	s.picker = p
	s.step = stepModel
	return p.Init()
}

// delegate forwards msg to the embedded picker and, once it has finished,
// closes it: a chosen model ends the setup; otherwise the Ollama picker
// goes back to the menu and the hosted one to the key screen.
func (s *Setup) delegate(msg tea.Msg) tea.Cmd {
	_, cmd := s.picker.Update(msg)
	model, chosen, finished := s.picker.done()
	if !finished {
		return cmd
	}
	hosted := s.step == stepModel
	s.closePicker()
	if !chosen {
		if hosted {
			return tea.Batch(cmd, s.input.Focus())
		}
		return cmd
	}
	if hosted {
		return tea.Batch(cmd, s.finish(SetupResult{Provider: s.provider, Key: s.typedKey(), KeyVerified: true, Model: model}))
	}
	return tea.Batch(cmd, s.finish(SetupResult{Provider: "ollama", Model: model}))
}

func (s *Setup) closePicker() {
	if s.picker != nil {
		s.picker.cancel()
		s.picker = nil
	}
	switch s.step {
	case stepPicker:
		s.step = stepMenu
	case stepModel:
		s.step = stepKey
	}
}

func (s *Setup) finish(res SetupResult) tea.Cmd {
	s.result, s.ok = res, true
	return tea.Quit
}

// View renders the whole screen in the alternate buffer; the embedded
// picker takes it over while open.
func (s *Setup) View() tea.View {
	if s.picker != nil {
		return s.picker.View()
	}
	v := tea.NewView(s.render())
	v.AltScreen = true
	return v
}

func (s *Setup) render() string {
	if s.help {
		return renderHelp(s.width, s.height, "Keys", setupKeys)
	}
	width := max(s.width, 1)
	var body []string
	footer := setupMenuHelp
	switch s.step {
	case stepKey:
		body, footer = s.renderKey(width)
	default:
		body = s.renderMenu(width)
	}
	return fillRows(body, width, max(s.height-1, 1)) + "\n" + s.styles.dim.Render(output.Truncate(footer, width))
}

func (s *Setup) renderMenu(width int) []string {
	lines := []string{s.styles.title.Render(setupTitle), ""}
	lines = append(lines, strings.Split(wrapLines(setupIntro, min(width, 100)), "\n")...)
	lines = append(lines, "")
	for i, r := range setupRows {
		lines = append(lines, s.renderRow(r, i == s.cursor, width))
	}
	return lines
}

// renderRow draws one choice like the home screen: marker, title, dim blurb.
func (s *Setup) renderRow(r setupRow, selected bool, width int) string {
	base, dim := lipgloss.NewStyle(), s.styles.dim
	marker := "  "
	if selected {
		base = s.styles.cursor
		dim = base.Faint(true)
		marker = "› "
	}
	blurb := r.blurb
	if r.provider == "ollama" && s.deps.Models == nil {
		blurb = "unavailable"
		if s.deps.ModelsErr != "" {
			blurb += ": " + s.deps.ModelsErr
		}
	}
	avail := max(width-lipgloss.Width(marker), 4)
	title := output.Truncate(r.title, avail)
	var b strings.Builder
	b.WriteString(base.Render(marker + title))
	if room := avail - lipgloss.Width(title) - 2; room >= 4 {
		b.WriteString(base.Render("  "))
		b.WriteString(dim.Render(output.Truncate(blurb, room)))
	}
	return padWith(b.String(), width, base)
}

func (s *Setup) renderKey(width int) (lines []string, footer string) {
	h := hosted[s.provider]
	in := s.input
	in.SetWidth(max(min(width, typedBoxWidth)-lipgloss.Width(typedPrompt), 1))
	lines = []string{s.styles.title.Render(h.name), ""}
	lines = append(lines, strings.Split(wrapLines("Paste your "+h.name+" API key (input hidden). Keys are kept in the macOS Keychain, never in a file.", min(width, 100)), "\n")...)
	lines = append(lines, s.styles.dim.Render(output.Truncate("Get one at "+h.site, width)))
	if s.note != "" {
		lines = append(lines, s.styles.flash.Render(output.Truncate(s.note, width)))
	}
	lines = append(lines, "", in.View(), "")
	footer = setupKeyHelp
	switch {
	case s.checking:
		lines = append(lines, s.spin.View()+" "+setupChecking)
	case s.failed:
		lines = append(lines, s.styles.danger.Render(output.Truncate(s.hint, width)))
		footer = setupFailHelp
	case s.hint != "":
		lines = append(lines, s.styles.flash.Render(output.Truncate(s.hint, width)))
	}
	return lines, footer
}

// firstLine is the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
