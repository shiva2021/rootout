package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/output"
)

const (
	hostedHelp       = "↑/↓ move · enter choose · t type a model id · esc back · ctrl+x quit · ? help"
	hostedTypeHelp   = "enter choose · esc back to the list · ctrl+x quit · ? help"
	hostedChromeRows = 4 // title row, blank row, status row, help row
	hostedTypeRow    = "Type a model id…"
	hostedNoID       = "type a model id first"
	hostedIDW        = 34 // "claude-sonnet-5-5 (current)" with room
	hostedNameW      = 28
)

// hostedNames titles the picker per provider.
var hostedNames = map[string]string{"anthropic": "Claude (Anthropic)", "openai": "OpenAI"}

// hostedKeys is the ? overlay of the hosted model picker.
var hostedKeys = []keyHelp{
	{"↑/↓", "move"},
	{"pgup/pgdn", "page"},
	{"enter", "choose the model under the cursor"},
	{"t", "type a model id by hand (any id the provider accepts)"},
	{"esc", "back without choosing (or back to the list while typing)"},
	{"ctrl+x", "quit rootout"},
	{"?", "this help"},
}

// hostedFetchSeq numbers fetches across every picker in the process, so a
// message from a picker that was closed cannot pass a later picker's guard.
var hostedFetchSeq atomic.Int64

// hostedModelsMsg is the fetched list; id matches the fetch it belongs to.
type hostedModelsMsg struct {
	id     int
	models []llm.ModelInfo
	err    error
}

// HostedPicker lists a hosted provider's models (Claude, OpenAI) and lets
// the user choose one or type an id. It implements tea.Model and
// embeddedPicker. The list is fetched by the caller's fetch func, built in
// cmd/ with the key already resolved, so this package never sees a key.
type HostedPicker struct {
	ctx      context.Context
	provider string
	current  string
	fetch    func(ctx context.Context) ([]llm.ModelInfo, error)
	styles   styles
	spin     spinner.Model

	models   []llm.ModelInfo
	loading  bool
	fetchErr string
	fetchID  int
	cancelFn context.CancelFunc

	cursor, top   int
	typing        bool
	input         textinput.Model
	hint          string
	width, height int
	help          bool

	embedded bool
	finished bool
	chosen   string
	ok       bool
	quitAll  bool
}

// NewHostedPicker builds a picker that fetches the list in Init.
func NewHostedPicker(ctx context.Context, provider string, fetch func(context.Context) ([]llm.ModelInfo, error), current string) *HostedPicker {
	return &HostedPicker{
		ctx:      ctx,
		provider: provider,
		current:  current,
		fetch:    fetch,
		styles:   defaultStyles(),
		spin:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		width:    defaultWidth,
		height:   defaultHeight,
	}
}

// NewHostedPickerWith builds a picker over an already fetched list (the
// setup wizard has it from the key check).
func NewHostedPickerWith(models []llm.ModelInfo, provider, current string) *HostedPicker {
	p := NewHostedPicker(context.Background(), provider, nil, current)
	p.models = models
	p.cursor = p.currentRow()
	p.scroll()
	return p
}

// NewHostedPickerFailed builds a picker for a list that could not be
// fetched: the error in red, the current model as its own row, and the
// cursor on the typing row.
func NewHostedPickerFailed(provider, current, fetchErr string) *HostedPicker {
	p := NewHostedPickerWith([]llm.ModelInfo{}, provider, current)
	p.fetchErr = firstLine(fetchErr)
	p.cursor = p.typeRow()
	p.scroll()
	return p
}

// PickHostedModel shows the picker full screen over models and returns the
// choice; fetchErr != "" means the list could not be fetched and only the
// current model and a typed id are offered. ok=false means the user
// cancelled; ctrl+x and ctrl+c return ErrQuit.
func PickHostedModel(ctx context.Context, provider string, models []llm.ModelInfo, current, fetchErr string) (model string, ok bool, err error) {
	p := NewHostedPickerWith(models, provider, current)
	if fetchErr != "" {
		p = NewHostedPickerFailed(provider, current, fetchErr)
	}
	p.ctx = ctx
	if _, err := tea.NewProgram(p, tea.WithContext(ctx)).Run(); err != nil {
		return "", false, runErr(err, "pick model")
	}
	if p.quitAll {
		return "", false, ErrQuit
	}
	model, ok = p.Chosen()
	return model, ok, nil
}

// Chosen returns the model once the program has quit; ok is false when the
// user cancelled.
func (p *HostedPicker) Chosen() (string, bool) { return p.chosen, p.ok }

func (p *HostedPicker) setEmbedded(on bool) { p.embedded = on }

func (p *HostedPicker) done() (model string, chosen, finished bool) {
	return p.chosen, p.ok, p.finished
}

// cancel stops a running fetch.
func (p *HostedPicker) cancel() {
	if p.cancelFn != nil {
		p.cancelFn()
		p.cancelFn = nil
	}
	p.loading = false
}

// Init starts the fetch when there is one to run.
func (p *HostedPicker) Init() tea.Cmd {
	if p.fetch == nil || p.models != nil {
		return nil
	}
	p.loading, p.fetchErr = true, ""
	p.fetchID = int(hostedFetchSeq.Add(1))
	id, fetch := p.fetchID, p.fetch
	ctx, cancel := context.WithCancel(p.ctx)
	p.cancelFn = cancel
	return tea.Batch(func() tea.Msg {
		defer cancel()
		models, err := fetch(ctx)
		if errors.Is(ctx.Err(), context.Canceled) {
			// Cancelled by esc or ctrl+x: nobody is waiting, and a picker
			// opened later must not mistake this for its own fetch.
			return nil
		}
		return hostedModelsMsg{id: id, models: models, err: err}
	}, p.spin.Tick)
}

// Update handles resizes, keys, the fetched list and spinner ticks.
func (p *HostedPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
		p.scroll()
		return p, nil

	case hostedModelsMsg:
		if msg.id != p.fetchID || !p.loading {
			return p, nil
		}
		p.loading, p.cancelFn = false, nil
		if msg.err != nil {
			p.fetchErr = firstLine(msg.err.Error())
			p.models = []llm.ModelInfo{}
			p.cursor = p.typeRow()
		} else {
			p.models = msg.models
			p.cursor = p.currentRow()
		}
		p.scroll()
		return p, nil

	case spinner.TickMsg:
		if !p.loading {
			return p, nil
		}
		var cmd tea.Cmd
		p.spin, cmd = p.spin.Update(msg)
		return p, cmd

	case tea.PasteMsg:
		if p.typing {
			msg.Content = strings.NewReplacer("\r", "", "\n", "").Replace(msg.Content)
			var cmd tea.Cmd
			p.input, cmd = p.input.Update(msg)
			return p, cmd
		}
		return p, nil

	case tea.KeyPressMsg:
		return p, p.key(msg)
	}
	return p, nil
}

func (p *HostedPicker) key(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if msg.Text == "?" {
		k = "?"
	}
	if isQuitKey(k) {
		p.cancel()
		p.quitAll, p.ok = true, false
		return p.quit()
	}
	if p.help {
		p.help = false
		return nil
	}
	if k == "?" {
		// Also while typing: a model id never contains "?".
		p.help = true
		return nil
	}
	if p.typing {
		return p.typeKey(msg, k)
	}
	p.hint = ""
	switch k {
	case "esc", "q":
		p.cancel()
		p.ok = false
		return p.quit()
	case "up", "ctrl+p":
		p.move(-1)
	case "down", "ctrl+n":
		p.move(1)
	case "pgup":
		p.move(-p.listRows())
	case "pgdown":
		p.move(p.listRows())
	case "t":
		return p.openInput()
	case "enter":
		if p.loading {
			return nil
		}
		if p.cursor == p.typeRow() {
			return p.openInput()
		}
		if id := p.rowID(p.cursor); id != "" {
			return p.finish(id)
		}
	}
	return nil
}

// typeKey handles the input: enter accepts a non-empty id, esc returns to
// the list, everything else edits the text.
func (p *HostedPicker) typeKey(msg tea.KeyPressMsg, k string) tea.Cmd {
	switch k {
	case "esc":
		p.typing, p.hint = false, ""
		p.input.Blur()
		return nil
	case "enter":
		id := strings.TrimSpace(p.input.Value())
		if id == "" {
			p.hint = hostedNoID
			return nil
		}
		return p.finish(id)
	}
	p.hint = ""
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return cmd
}

// openInput switches to the typing screen with the current model prefilled.
func (p *HostedPicker) openInput() tea.Cmd {
	in := textinput.New()
	in.Prompt = typedPrompt
	in.CharLimit = 0
	st := in.Styles()
	st.Focused.Prompt = p.styles.prompt
	st.Focused.Text = lipgloss.NewStyle()
	in.SetStyles(st)
	in.SetValue(p.current)
	p.input = in
	p.typing, p.hint = true, ""
	return p.input.Focus()
}

func (p *HostedPicker) finish(id string) tea.Cmd {
	p.cancel()
	p.chosen, p.ok = id, true
	return p.quit()
}

// quit ends the picker: tea.Quit on its own, the done flag when embedded.
func (p *HostedPicker) quit() tea.Cmd {
	if p.embedded {
		p.finished = true
		return nil
	}
	return tea.Quit
}

// Rows: the fetched models, then the current model when it is not among
// them, then the typing row.

func (p *HostedPicker) hasSynthetic() bool {
	if p.current == "" {
		return false
	}
	for _, m := range p.models {
		if m.ID == p.current {
			return false
		}
	}
	return true
}

func (p *HostedPicker) rowCount() int {
	n := len(p.models) + 1
	if p.hasSynthetic() {
		n++
	}
	return n
}

func (p *HostedPicker) typeRow() int { return p.rowCount() - 1 }

// currentRow is the row of the current model, or the first row.
func (p *HostedPicker) currentRow() int {
	for i, m := range p.models {
		if m.ID == p.current {
			return i
		}
	}
	if p.hasSynthetic() {
		return len(p.models)
	}
	return 0
}

// rowID is the model id on row i, "" for the typing row.
func (p *HostedPicker) rowID(i int) string {
	switch {
	case i < len(p.models):
		return p.models[i].ID
	case i == len(p.models) && p.hasSynthetic():
		return p.current
	}
	return ""
}

func (p *HostedPicker) move(delta int) {
	p.cursor = max(0, min(p.rowCount()-1, p.cursor+delta))
	p.scroll()
}

func (p *HostedPicker) listRows() int {
	return max(p.height-hostedChromeRows-1, 1) // one row for the column titles
}

func (p *HostedPicker) scroll() {
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

// View renders the picker in the alternate buffer.
func (p *HostedPicker) View() tea.View {
	v := tea.NewView(p.render())
	v.AltScreen = true
	return v
}

func (p *HostedPicker) render() string {
	if p.help {
		return renderHelp(p.width, p.height, "Keys", hostedKeys)
	}
	s := p.styles
	width := max(p.width, 1)
	title := hostedNames[p.provider]
	if title == "" {
		title = p.provider
	}
	lines := []string{s.title.Render(output.Truncate(title+" models", width)), ""}
	help := hostedHelp
	status := ""
	switch {
	case p.typing:
		help = hostedTypeHelp
		in := p.input
		in.SetWidth(max(min(width, typedBoxWidth)-lipgloss.Width(typedPrompt)-1, 1)) // -1: the cursor cell
		lines = append(lines, output.Truncate("Model id (as the provider names it):", width), in.View())
		if p.hint != "" {
			status = s.flash.Render(output.Truncate(p.hint, width))
		}
	case p.loading:
		lines = append(lines, output.Truncate(p.spin.View()+" listing models…", width))
	default:
		lines = append(lines, s.dim.Render(output.Truncate("  "+padTo("MODEL", hostedIDW)+" "+padTo("NAME", hostedNameW)+" RELEASED", width)))
		rows := p.listRows()
		for i := p.top; i < p.rowCount() && i < p.top+rows; i++ {
			lines = append(lines, p.renderRow(i, width))
		}
		if p.fetchErr != "" {
			status = s.danger.Render(output.Truncate(p.fetchErr, width))
		}
	}
	body := fillRows(lines, width, max(p.height-2, 1))
	return body + "\n" + padTo(status, width) + "\n" + s.dim.Render(output.Truncate(help, width))
}

// renderRow draws marker, id (with "(current)"), name, release date and a
// dim note, in the inverted cursor style when selected.
func (p *HostedPicker) renderRow(i, width int) string {
	s := p.styles
	base, dim := lipgloss.NewStyle(), s.dim
	marker := "  "
	if i == p.cursor {
		base, dim, marker = s.cursor, s.cursor.Faint(true), "› "
	}
	avail := max(width-lipgloss.Width(marker), 1)
	var text, note string
	switch {
	case i < len(p.models):
		m := p.models[i]
		id := m.ID
		if id == p.current {
			id += " (current)"
		}
		released := ""
		if !m.Created.IsZero() {
			released = m.Created.Format("2006-01-02")
		}
		text = padTo(output.Truncate(id, hostedIDW), hostedIDW) + " " + padTo(output.Truncate(m.DisplayName, hostedNameW), hostedNameW) + " " + released
		if m.Deprecated {
			note = "deprecated"
		}
	case i == len(p.models) && p.hasSynthetic():
		text, note = padTo(output.Truncate(p.current+" (current)", hostedIDW), hostedIDW), "not in the list"
	default:
		text = hostedTypeRow
	}
	text = output.Truncate(text, avail)
	out := base.Render(marker + text)
	if room := avail - lipgloss.Width(text) - 2; note != "" && room >= 4 {
		out += base.Render("  ") + dim.Render(output.Truncate(note, room))
	}
	return padWith(out, width, base)
}

// hostedLabel is "provider · model" for the scan header.
func hostedLabel(provider, model string) string {
	return fmt.Sprintf("%s%s%s", provider, labelSep, model)
}
