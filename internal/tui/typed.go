package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	typedBoxWidth = 60
	typedPrompt   = "› "
	typedHelp     = "enter confirm · esc back"
	typedWrongErr = "that is not the word"
)

// typedConfirm asks the user to type an exact word before a destructive step.
type typedConfirm struct {
	input  textinput.Model
	word   string // expected word, e.g. "delete"
	prompt string // one line shown above the input
	danger bool   // red styling (permanent deletes)
	err    string // "that is not the word" flash after a wrong Enter
}

// newTypedConfirm builds the sub-model with the input focused. The returned
// Cmd starts the cursor blinking and should be batched by the caller.
func newTypedConfirm(word, prompt string, danger bool) (typedConfirm, tea.Cmd) {
	s := defaultStyles()
	in := textinput.New()
	in.Prompt = typedPrompt
	in.CharLimit = 64
	st := in.Styles()
	st.Focused.Prompt = s.prompt
	if danger {
		st.Focused.Prompt = s.danger
	}
	st.Focused.Text = lipgloss.NewStyle()
	in.SetStyles(st)
	cmd := in.Focus()
	return typedConfirm{input: in, word: word, prompt: prompt, danger: danger}, cmd
}

// Update handles keys. accepted is true when Enter was pressed with the
// exact word (case-insensitive, trimmed); cancelled is true on esc or
// ctrl+c. Any other Enter sets err and keeps the input. Pasted newlines are
// stripped so a pasted "delete\n" still needs an explicit Enter.
func (t typedConfirm) Update(msg tea.Msg) (model typedConfirm, cmd tea.Cmd, accepted, cancelled bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			return t, nil, false, true
		case "enter":
			if t.matches() {
				return t, nil, true, false
			}
			t.err = typedWrongErr
			return t, nil, false, false
		}
		t.err = ""
	case tea.PasteMsg:
		msg.Content = strings.NewReplacer("\r", "", "\n", "").Replace(msg.Content)
		t.err = ""
		t.input, cmd = t.input.Update(msg)
		return t, cmd, false, false
	}
	t.input, cmd = t.input.Update(msg)
	return t, cmd, false, false
}

// View renders a bordered box (width ≤ typedBoxWidth; the caller centres it)
// with the prompt, the input, the error flash and the key hint.
func (t typedConfirm) View(width int) string {
	s := defaultStyles()
	w := max(min(width, typedBoxWidth), 12)
	inner := w - 4 // border and padding on both sides
	box := s.pane.Width(w)
	promptStyle := s.title
	if t.danger {
		box = box.BorderForeground(lipgloss.Red)
		promptStyle = s.danger
	}

	in := t.input
	in.SetWidth(max(inner-lipgloss.Width(typedPrompt), 1))

	errLine := ""
	if t.err != "" {
		errLine = s.danger.Render(t.err)
	}
	lines := []string{
		promptStyle.Render(wrapLines(t.prompt, inner)),
		"",
		in.View(),
		errLine,
		s.dim.Render(typedHelp),
	}
	return box.Render(strings.Join(lines, "\n"))
}

// Focused reports whether the input has focus.
func (t typedConfirm) Focused() bool { return t.input.Focused() }

func (t typedConfirm) matches() bool {
	return strings.EqualFold(strings.TrimSpace(t.input.Value()), t.word)
}
