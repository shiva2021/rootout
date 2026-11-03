package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/llm"
)

func hostedFixture() []llm.ModelInfo {
	return []llm.ModelInfo{
		{ID: "claude-new", DisplayName: "Claude New", Created: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "claude-sonnet-5-5", DisplayName: "Claude Sonnet 5.5", Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "claude-old", DisplayName: "Claude Old", Created: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Deprecated: true},
	}
}

func newTestHosted(t *testing.T, p *HostedPicker) *HostedPicker {
	t.Helper()
	p.setEmbedded(true)
	p.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return p
}

func pressHP(p *HostedPicker, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = p.Update(k)
	}
	return cmd
}

func viewHP(p *HostedPicker) string { return plain(p.View().Content) }

func typeHP(p *HostedPicker, text string) {
	for _, r := range text {
		p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// pumpHP runs cmd and feeds every non-tick message back into p.
func pumpHP(p *HostedPicker, cmd tea.Cmd) {
	for _, msg := range runCmd(cmd) {
		if _, tick := msg.(spinner.TickMsg); tick || msg == nil {
			continue
		}
		_, next := p.Update(msg)
		pumpHP(p, next)
	}
}

func TestHostedPickerPreselectsCurrent(t *testing.T) {
	p := newTestHosted(t, NewHostedPickerWith(hostedFixture(), "anthropic", "claude-sonnet-5-5"))
	out := viewHP(p)
	for _, want := range []string{"Claude (Anthropic)", "MODEL", "claude-new", "Claude New", "2026-06-01", "claude-sonnet-5-5 (current)", "claude-old", "deprecated", "Type a model id"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "› claude-sonnet-5-5") {
		t.Fatalf("cursor should start on the current model:\n%s", out)
	}
	if cmd := pressHP(p, keyEnter); cmd != nil && isQuit(cmd) {
		t.Fatal("embedded picker must not quit the program")
	}
	if model, chosen, finished := p.done(); !finished || !chosen || model != "claude-sonnet-5-5" {
		t.Errorf("done() = %q %v %v", model, chosen, finished)
	}
}

func TestHostedPickerMoveAndChoose(t *testing.T) {
	p := newTestHosted(t, NewHostedPickerWith(hostedFixture(), "openai", "claude-sonnet-5-5"))
	if !strings.Contains(viewHP(p), "OpenAI") {
		t.Fatalf("title should name the provider:\n%s", viewHP(p))
	}
	pressHP(p, keyUp, keyEnter)
	if model, chosen, _ := p.done(); !chosen || model != "claude-new" {
		t.Errorf("done() = %q %v, want claude-new", model, chosen)
	}
}

func TestHostedPickerSyntheticCurrentRow(t *testing.T) {
	p := newTestHosted(t, NewHostedPickerWith(hostedFixture(), "anthropic", "claude-custom"))
	out := viewHP(p)
	if !strings.Contains(out, "› claude-custom (current)") || !strings.Contains(out, "not in the list") {
		t.Fatalf("synthetic current row missing or not selected:\n%s", out)
	}
	pressHP(p, keyEnter)
	if model, chosen, _ := p.done(); !chosen || model != "claude-custom" {
		t.Errorf("done() = %q %v, want claude-custom", model, chosen)
	}
}

func TestHostedPickerTypeModelID(t *testing.T) {
	p := newTestHosted(t, NewHostedPickerWith(hostedFixture(), "anthropic", "claude-sonnet-5-5"))
	pressHP(p, runeKey('t'))
	out := viewHP(p)
	if !strings.Contains(out, "Model id") || !strings.Contains(out, "claude-sonnet-5-5") {
		t.Fatalf("typing screen should show the input prefilled with the current model:\n%s", out)
	}
	typeHP(p, "-extra")
	pressHP(p, keyEnter)
	if model, chosen, finished := p.done(); !finished || !chosen || model != "claude-sonnet-5-5-extra" {
		t.Errorf("done() = %q %v %v", model, chosen, finished)
	}
}

func TestHostedPickerTypeEmptyAndEsc(t *testing.T) {
	p := newTestHosted(t, NewHostedPickerWith(hostedFixture(), "anthropic", ""))
	// The last row is the typing row; enter on it opens the input.
	pressHP(p, keyDown, keyDown, keyDown, keyEnter)
	if !strings.Contains(viewHP(p), "Model id") {
		t.Fatalf("enter on the last row should open the input:\n%s", viewHP(p))
	}
	pressHP(p, keyEnter)
	if _, _, finished := p.done(); finished || !strings.Contains(viewHP(p), "type a model id") {
		t.Fatalf("empty id should hint, not finish:\n%s", viewHP(p))
	}
	pressHP(p, keyEsc)
	if _, _, finished := p.done(); finished || strings.Contains(viewHP(p), "Model id") {
		t.Fatalf("esc should leave the input and return to the list:\n%s", viewHP(p))
	}
	pressHP(p, keyEsc)
	if _, chosen, finished := p.done(); !finished || chosen {
		t.Errorf("esc on the list should finish without a choice: chosen %v finished %v", chosen, finished)
	}
}

func TestHostedPickerFetchLists(t *testing.T) {
	var got context.Context
	fetch := func(ctx context.Context) ([]llm.ModelInfo, error) { got = ctx; return hostedFixture(), nil }
	p := newTestHosted(t, NewHostedPicker(context.Background(), "anthropic", fetch, "claude-old"))
	cmd := p.Init()
	if cmd == nil || !strings.Contains(viewHP(p), "listing models") {
		t.Fatalf("Init should start the fetch with a spinner:\n%s", viewHP(p))
	}
	pumpHP(p, cmd)
	if got == nil {
		t.Fatal("fetch not called")
	}
	if out := viewHP(p); !strings.Contains(out, "› claude-old (current)") || strings.Contains(out, "listing models") {
		t.Fatalf("list not shown after the fetch:\n%s", out)
	}
}

func TestHostedPickerFetchErrorFallsBackToTyping(t *testing.T) {
	fetch := func(context.Context) ([]llm.ModelInfo, error) {
		return nil, errors.New("anthropic: could not list models: dial tcp")
	}
	p := newTestHosted(t, NewHostedPicker(context.Background(), "anthropic", fetch, "claude-sonnet-5-5"))
	pumpHP(p, p.Init())
	out := viewHP(p)
	if !strings.Contains(out, "could not list models") || !strings.Contains(out, "claude-sonnet-5-5 (current)") || !strings.Contains(out, "› Type a model id") {
		t.Fatalf("error, current row and the typing row expected, cursor on typing:\n%s", out)
	}
	pressHP(p, keyEnter)
	if !strings.Contains(viewHP(p), "Model id") {
		t.Fatalf("enter should open the input:\n%s", viewHP(p))
	}
}

func TestHostedPickerCancelStopsFetch(t *testing.T) {
	started := make(chan context.Context, 1)
	fetch := func(ctx context.Context) ([]llm.ModelInfo, error) {
		started <- ctx
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p := newTestHosted(t, NewHostedPicker(context.Background(), "anthropic", fetch, ""))
	cmds := p.Init()
	go func() { _ = runCmd(cmds) }()
	ctx := <-started
	p.cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel should stop the fetch")
	}
}

func TestHostedPickerHelpFooterAndQuit(t *testing.T) {
	p := newTestHosted(t, NewHostedPickerWith(hostedFixture(), "anthropic", ""))
	lines := strings.Split(viewHP(p), "\n")
	footer := strings.TrimRight(lines[len(lines)-1], " ")
	if !strings.HasSuffix(footer, "? help") || !strings.Contains(footer, "t type a model id") {
		t.Errorf("footer = %q", footer)
	}
	if len(lines) != 30 {
		t.Errorf("view has %d lines, want 30", len(lines))
	}
	pressHP(p, keyQuestion)
	if out := viewHP(p); !strings.Contains(out, "Keys") || !strings.Contains(out, "any key to close") {
		t.Fatalf("help overlay not shown:\n%s", out)
	}
	pressHP(p, keyDown)
	if strings.Contains(viewHP(p), "any key to close") {
		t.Fatal("help should close on any key")
	}
	pressHP(p, keyCtrlX)
	if _, _, finished := p.done(); !finished || !p.quitAll {
		t.Errorf("ctrl+x: finished %v quitAll %v", finished, p.quitAll)
	}
}

func TestHostedPickerStandaloneQuits(t *testing.T) {
	p := NewHostedPickerWith(hostedFixture(), "anthropic", "")
	p.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	assertQuit(t, pressHP(p, keyEnter))
	if model, ok := p.Chosen(); !ok || model != "claude-new" {
		t.Errorf("Chosen() = %q %v", model, ok)
	}
}

func TestHostedPickerRowsExactWidth(t *testing.T) {
	for _, w := range []int{40, 80, 140} {
		p := NewHostedPickerWith(hostedFixture(), "anthropic", "claude-custom-with-a-very-long-model-identifier")
		p.Update(tea.WindowSizeMsg{Width: w, Height: 12})
		lines := strings.Split(plain(p.View().Content), "\n")
		if len(lines) != 12 {
			t.Errorf("width %d: %d lines, want 12", w, len(lines))
		}
		for i, l := range lines {
			if got := lipgloss.Width(l); got > w {
				t.Errorf("width %d: line %d is %d cells: %q", w, i, got, l)
			}
		}
	}
}

// A fetch cancelled by esc must not post its result: the next picker opened
// with m would mistake it for its own (both number fetches from 1).
func TestHostedPickerCancelledFetchSendsNothing(t *testing.T) {
	fetch := func(ctx context.Context) ([]llm.ModelInfo, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	a := newTestHosted(t, NewHostedPicker(context.Background(), "anthropic", fetch, ""))
	cmd := a.Init()
	pressHP(a, keyEsc) // cancels the fetch
	msgs := runCmd(cmd)
	for _, m := range msgs {
		if _, ok := m.(hostedModelsMsg); ok {
			t.Fatalf("cancelled fetch posted %v", m)
		}
	}
	// Even if it had, a second picker shows its own list.
	b := newTestHosted(t, NewHostedPicker(context.Background(), "anthropic", func(context.Context) ([]llm.ModelInfo, error) { return hostedFixture(), nil }, ""))
	bcmd := b.Init()
	b.Update(hostedModelsMsg{id: 1, err: context.Canceled})
	pumpHP(b, bcmd)
	if out := viewHP(b); !strings.Contains(out, "claude-new") || strings.Contains(out, "canceled") {
		t.Fatalf("second picker should show its own list:\n%s", out)
	}
}

func TestHostedPickerWithFetchError(t *testing.T) {
	p := NewHostedPickerFailed("openai", "gpt-5.2", "openai: could not list models: offline")
	p.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	out := viewHP(p)
	if !strings.Contains(out, "could not list models") || !strings.Contains(out, "gpt-5.2 (current)") || !strings.Contains(out, "› Type a model id") {
		t.Fatalf("failed-fetch picker should show the error, the current row and the typing row selected:\n%s", out)
	}
}

func TestHostedPickerHelpWhileTyping(t *testing.T) {
	p := newTestHosted(t, NewHostedPickerWith(hostedFixture(), "anthropic", "claude-sonnet-5-5"))
	pressHP(p, runeKey('t'))
	pressHP(p, keyQuestion)
	if out := viewHP(p); !strings.Contains(out, "any key to close") {
		t.Fatalf("? while typing should open the help, not insert a character:\n%s", out)
	}
	pressHP(p, keyDown)
	out := viewHP(p)
	if strings.Contains(out, "any key to close") || !strings.Contains(out, "Model id") || strings.Contains(out, "claude-sonnet-5-5?") {
		t.Fatalf("help should close back to the input with the text untouched:\n%s", out)
	}
	pressHP(p, keyEnter)
	if model, chosen, _ := p.done(); !chosen || model != "claude-sonnet-5-5" {
		t.Errorf("done() = %q %v", model, chosen)
	}
}

func TestHostedPickerLongIDKeepsColumns(t *testing.T) {
	long := "claude-" + strings.Repeat("x", 60)
	models := []llm.ModelInfo{{ID: long, DisplayName: "Long One"}, {ID: "claude-new", DisplayName: "Short One"}}
	p := newTestHosted(t, NewHostedPickerWith(models, "anthropic", long))
	lines := strings.Split(viewHP(p), "\n")
	col := func(name string) int {
		for _, l := range lines {
			if i := strings.Index(l, name); i >= 0 {
				return lipgloss.Width(l[:i])
			}
		}
		return -1
	}
	if a, b := col("Long One"), col("Short One"); a != b || a < 0 {
		t.Fatalf("NAME column moved: long at %d, short at %d\n%s", a, b, viewHP(p))
	}
	if !strings.Contains(viewHP(p), "…") {
		t.Fatal("the long id should be truncated with an ellipsis")
	}
}

func TestHostedPickerNarrowWidths(t *testing.T) {
	fetch := func(context.Context) ([]llm.ModelInfo, error) { return hostedFixture(), nil }
	for _, w := range []int{8, 12, 20} {
		p := NewHostedPicker(context.Background(), "anthropic", fetch, "")
		p.setEmbedded(true)
		p.Update(tea.WindowSizeMsg{Width: w, Height: 10})
		cmd := p.Init() // loading state
		for i, l := range strings.Split(viewHP(p), "\n") {
			if got := lipgloss.Width(l); got > w {
				t.Errorf("width %d loading: line %d is %d cells: %q", w, i, got, l)
			}
		}
		pumpHP(p, cmd)
		pressHP(p, runeKey('t'))
		for i, l := range strings.Split(viewHP(p), "\n") {
			if got := lipgloss.Width(l); got > w {
				t.Errorf("width %d typing: line %d is %d cells: %q", w, i, got, l)
			}
		}
	}
}
