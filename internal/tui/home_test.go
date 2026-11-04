package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const testAI = "AI: not set up · heuristics only"

func newTestHome(t *testing.T, flash Flash) *Home {
	t.Helper()
	h := NewHome("", flash, testAI, nil)
	h.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return h
}

func TestHomeNoticeNilIsIdle(t *testing.T) {
	if cmd := newTestHome(t, Flash{}).Init(); cmd != nil {
		t.Error("Init with no notice func should return nil")
	}
}

func TestHomeNotice(t *testing.T) {
	const notice = "v0.8.0 available · brew update && brew upgrade --cask rootout"
	h := NewHome("v0.7.0", Flash{}, testAI, func() string { return notice })
	h.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	cmd := h.Init()
	if cmd == nil {
		t.Fatal("Init should return a command that runs the notice func")
	}
	if first := strings.SplitN(viewH(h), "\n", 2)[0]; strings.Contains(first, "available") {
		t.Errorf("notice shown before the command ran: %q", first)
	}
	h.Update(cmd())
	lines := strings.Split(viewH(h), "\n")
	if want := "rootout v0.7.0  " + notice; strings.TrimSpace(lines[0]) != want {
		t.Errorf("title = %q, want %q", lines[0], want)
	}
	if len(lines) != 30 {
		t.Errorf("view has %d lines, want 30", len(lines))
	}
}

func TestHomeNoticeEmptyOrNarrow(t *testing.T) {
	tests := []struct {
		name   string
		notice string
		width  int
	}{
		{"empty notice", "", 120},
		{"too narrow for the notice", "v0.8.0 available · brew update && brew upgrade --cask rootout", 18},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHome("v0.7.0", Flash{}, testAI, func() string { return tt.notice })
			h.Update(tea.WindowSizeMsg{Width: tt.width, Height: 30})
			h.Update(h.Init()())
			lines := strings.Split(viewH(h), "\n")
			if strings.TrimSpace(lines[0]) != "rootout v0.7.0" {
				t.Errorf("title = %q, want plain", lines[0])
			}
			if len(lines) != 30 {
				t.Errorf("view has %d lines, want 30", len(lines))
			}
		})
	}
}

func TestHomeNoticeTruncates(t *testing.T) {
	h := NewHome("v0.7.0", Flash{}, testAI, func() string { return strings.Repeat("x", 200) })
	h.Update(tea.WindowSizeMsg{Width: 40, Height: 30})
	h.Update(h.Init()())
	lines := strings.Split(viewH(h), "\n")
	if lipgloss.Width(lines[0]) > 40 {
		t.Errorf("title line is %d cells wide, want at most 40: %q", lipgloss.Width(lines[0]), lines[0])
	}
	if !strings.Contains(lines[0], "x") {
		t.Errorf("notice dropped entirely at width 40: %q", lines[0])
	}
}

func TestHomeTitleVersion(t *testing.T) {
	h := NewHome("v0.3.0", Flash{}, testAI, nil)
	h.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if first := strings.SplitN(viewH(h), "\n", 2)[0]; strings.TrimSpace(first) != "rootout v0.3.0" {
		t.Errorf("title = %q, want %q", first, "rootout v0.3.0")
	}
	if first := strings.SplitN(viewH(newTestHome(t, Flash{})), "\n", 2)[0]; strings.TrimSpace(first) != "rootout" {
		t.Errorf("title without version = %q, want %q", first, "rootout")
	}
}

func pressH(h *Home, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = h.Update(k)
	}
	return cmd
}

func viewH(h *Home) string { return plain(h.View().Content) }

var (
	keyCtrlX    = tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}
	keyCtrlC    = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	keyQuestion = tea.KeyPressMsg{Code: '?', Text: "?"}
)

func TestHomeView(t *testing.T) {
	h := newTestHome(t, Flash{})
	v := viewH(h)
	for _, r := range homeRows {
		if !strings.Contains(v, r.title) || !strings.Contains(v, r.blurb) {
			t.Errorf("view missing row %q:\n%s", r.title, v)
		}
	}
	if !strings.Contains(v, "› "+homeRows[0].title) {
		t.Errorf("first row should carry the marker:\n%s", v)
	}
	lines := strings.Split(v, "\n")
	footer := strings.TrimRight(lines[len(lines)-1], " ")
	if !strings.Contains(footer, homeHelp) || !strings.HasSuffix(footer, "? help") {
		t.Errorf("footer = %q, want %q", footer, homeHelp)
	}
	if len(lines) != 30 {
		t.Errorf("view has %d lines, want 30", len(lines))
	}
}

func TestHomeCursor(t *testing.T) {
	last := len(homeRows) - 1
	tests := []struct {
		name string
		key  tea.KeyPressMsg
		n    int
		want int
	}{
		{"down clamps", keyDown, 6, last},
		{"ctrl+n clamps", tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}, 6, last},
		{"down once", keyDown, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHome(t, Flash{})
			for range tt.n {
				pressH(h, tt.key)
			}
			if h.cursor != tt.want {
				t.Errorf("cursor = %d, want %d", h.cursor, tt.want)
			}
		})
	}
	for _, key := range []tea.KeyPressMsg{keyUp, {Code: 'p', Mod: tea.ModCtrl}} {
		h := newTestHome(t, Flash{})
		h.cursor = last
		for range 6 {
			pressH(h, key)
		}
		if h.cursor != 0 {
			t.Errorf("%v: cursor = %d, want 0", key, h.cursor)
		}
	}
}

func TestHomeEnterSelects(t *testing.T) {
	tests := []struct {
		row  int
		want Action
	}{
		{0, ActionUninstall},
		{1, ActionOrphans},
		{2, ActionScan},
		{3, ActionAssess},
		{4, ActionSetup},
	}
	for _, tt := range tests {
		h := newTestHome(t, Flash{})
		for range tt.row {
			pressH(h, keyDown)
		}
		assertQuit(t, pressH(h, keyEnter))
		got, ok := h.Chosen()
		if !ok || got != tt.want || h.quitAll {
			t.Errorf("row %d: Chosen() = %v, %v, quitAll %v; want %v", tt.row, got, ok, h.quitAll, tt.want)
		}
	}

	h := newTestHome(t, Flash{})
	for range len(homeRows) - 1 {
		pressH(h, keyDown)
	}
	assertQuit(t, pressH(h, keyEnter))
	if _, ok := h.Chosen(); ok || !h.quitAll {
		t.Errorf("Quit row: ok = %v, quitAll = %v; want false, true", ok, h.quitAll)
	}
}

func TestHomeQuitKeys(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{keyCtrlX, keyCtrlC} {
		h := newTestHome(t, Flash{})
		assertQuit(t, pressH(h, key))
		if _, ok := h.Chosen(); ok || !h.quitAll {
			t.Errorf("%v: ok = %v, quitAll = %v; want false, true", key, ok, h.quitAll)
		}
	}
}

func TestHomeEscIgnored(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'q', Text: "q"}} {
		h := newTestHome(t, Flash{})
		pressH(h, keyDown)
		if cmd := pressH(h, key); cmd != nil {
			t.Errorf("%v: got a command, want nil", key)
		}
		if h.cursor != 1 || h.quitAll || h.ok {
			t.Errorf("%v changed state: cursor %d quitAll %v ok %v", key, h.cursor, h.quitAll, h.ok)
		}
	}
}

func TestHomeFlash(t *testing.T) {
	const text = "removed Foo, 12 MB to Trash"
	h := newTestHome(t, Flash{Text: text})
	if raw := h.View().Content; !strings.Contains(raw, h.styles.flash.Render(text)) {
		t.Errorf("normal flash should use the flash style:\n%q", raw)
	}
	pressH(h, keyDown)
	if v := viewH(h); !strings.Contains(v, text) {
		t.Errorf("flash should survive cursor keys:\n%s", v)
	}

	e := newTestHome(t, Flash{Text: text, Err: true})
	raw := e.View().Content
	if !strings.Contains(raw, e.styles.danger.Render(text)) {
		t.Errorf("error flash should use the danger style:\n%q", raw)
	}
	if strings.Contains(raw, e.styles.flash.Render(text)) {
		t.Error("error flash must not use the flash style")
	}
}

func TestHomeHelpOverlay(t *testing.T) {
	h := newTestHome(t, Flash{})
	pressH(h, keyQuestion)
	v := viewH(h)
	for _, want := range []string{"Keys", "quit rootout", helpClose} {
		if !strings.Contains(v, want) {
			t.Fatalf("help overlay missing %q:\n%s", want, v)
		}
	}
	pressH(h, keyDown)
	if v := viewH(h); strings.Contains(v, helpClose) {
		t.Errorf("any key should close the overlay:\n%s", v)
	}
	if h.cursor != 0 {
		t.Errorf("closing key should not move the cursor, got %d", h.cursor)
	}

	pressH(h, keyQuestion)
	assertQuit(t, pressH(h, keyCtrlX))
	if !h.quitAll {
		t.Error("ctrl+x while help is open should quit rootout")
	}
}

func TestHomeAILine(t *testing.T) {
	h := newTestHome(t, Flash{})
	lines := strings.Split(viewH(h), "\n")
	if got := strings.TrimSpace(lines[1]); got != testAI {
		t.Errorf("line under the title = %q, want %q", got, testAI)
	}
	if !strings.Contains(viewH(h), "Set up AI") {
		t.Errorf("home rows should offer Set up AI:\n%s", viewH(h))
	}
	if len(lines) != 30 {
		t.Errorf("view has %d lines, want 30", len(lines))
	}
}
