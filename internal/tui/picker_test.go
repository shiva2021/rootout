package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/app"
)

func fixture() []app.App {
	return []app.App{
		{Name: "Safari", BundleID: "com.apple.Safari", Path: "/Applications/Safari.app", Version: "18.0", SizeBytes: 25_000_000, Protected: true},
		{Name: "Slack", BundleID: "com.tinyspeck.slackmacgap", Path: "/Applications/Slack.app", Version: "4.41.0", SizeBytes: 310_000_000, Cask: "slack"},
		{Name: "Spotify", BundleID: "com.spotify.client", Path: "/Applications/Spotify.app", Version: "1.2.50", SizeBytes: 420_000_000},
		{Name: "Visual Studio Code", BundleID: "com.microsoft.VSCode", Path: "/Applications/Visual Studio Code.app", Version: "1.95.0", SizeBytes: 600_000_000, Cask: "visual-studio-code"},
		{Name: "Pages", BundleID: "com.apple.iWork.Pages", Path: "/Applications/Pages.app", Version: "14.2", SizeBytes: 700_000_000},
		{Name: "Zoom", BundleID: "us.zoom.xos", Path: "/Applications/zoom.us.app", Version: "6.2.0", SizeBytes: 150_000_000},
	}
}

func newTestPicker(t *testing.T) *Picker {
	t.Helper()
	p := NewPicker(fixture())
	p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return p
}

func press(p *Picker, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = p.Update(k)
	}
	return cmd
}

func typeText(p *Picker, s string) {
	for _, r := range s {
		press(p, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// sgr matches the SGR escape sequences lipgloss emits so tests can assert on
// plain text.
var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return sgr.ReplaceAllString(s, "") }

func view(p *Picker) string { return plain(p.View().Content) }

func names(ms []app.Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.App.Name
	}
	return out
}

func TestTypingFilters(t *testing.T) {
	p := newTestPicker(t)
	if got := len(p.Matches()); got != 6 {
		t.Fatalf("initial matches = %d, want 6", got)
	}

	typeText(p, "spo")
	ms := p.Matches()
	if len(ms) == 0 || ms[0].App.Name != "Spotify" {
		t.Fatalf("after typing spo, matches = %v, want Spotify first", names(ms))
	}
	if p.Cursor() != 0 {
		t.Errorf("cursor = %d after query change, want 0", p.Cursor())
	}

	press(p, tea.KeyPressMsg{Code: tea.KeyDown})
	press(p, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if p.Cursor() != 0 {
		t.Errorf("cursor = %d after second query change, want 0", p.Cursor())
	}
}

func TestCursorMovement(t *testing.T) {
	p := newTestPicker(t)
	n := len(p.Matches())

	press(p, tea.KeyPressMsg{Code: tea.KeyDown})
	if p.Cursor() != 1 {
		t.Fatalf("after down cursor = %d, want 1", p.Cursor())
	}
	press(p, tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if p.Cursor() != 2 {
		t.Fatalf("after ctrl+n cursor = %d, want 2", p.Cursor())
	}
	press(p, tea.KeyPressMsg{Code: tea.KeyUp})
	if p.Cursor() != 1 {
		t.Fatalf("after up cursor = %d, want 1", p.Cursor())
	}
	press(p, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	press(p, tea.KeyPressMsg{Code: tea.KeyUp})
	if p.Cursor() != 0 {
		t.Fatalf("cursor should clamp at 0, got %d", p.Cursor())
	}
	press(p, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if p.Cursor() != n-1 {
		t.Fatalf("after pgdown cursor = %d, want %d", p.Cursor(), n-1)
	}
	press(p, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if p.Cursor() != 0 {
		t.Fatalf("after pgup cursor = %d, want 0", p.Cursor())
	}
}

func TestScrollKeepsCursorVisible(t *testing.T) {
	p := NewPicker(fixture())
	p.Update(tea.WindowSizeMsg{Width: 100, Height: chromeRows + 3}) // three list rows

	for i := 0; i < 5; i++ {
		press(p, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if p.Cursor() != 5 {
		t.Fatalf("cursor = %d, want 5", p.Cursor())
	}
	v := view(p)
	if !strings.Contains(v, "› Zoom") {
		t.Errorf("cursor row Zoom not visible:\n%s", v)
	}
	if strings.Contains(v, "Safari") {
		t.Errorf("Safari should have scrolled off:\n%s", v)
	}
	if !strings.Contains(v, "6 of 6") {
		t.Errorf("footer should read 6 of 6:\n%s", v)
	}
}

func TestEnterSelects(t *testing.T) {
	p := newTestPicker(t)
	typeText(p, "spo")
	cmd := press(p, tea.KeyPressMsg{Code: tea.KeyEnter})

	got, ok := p.Selected()
	if !ok || got.Name != "Spotify" {
		t.Fatalf("Selected() = %q, %v; want Spotify, true", got.Name, ok)
	}
	assertQuit(t, cmd)
}

func TestEnterOnProtectedFlashes(t *testing.T) {
	p := newTestPicker(t)
	typeText(p, "safari")
	if ms := p.Matches(); len(ms) == 0 || !ms[0].App.Protected {
		t.Fatalf("expected protected Safari first, got %v", names(ms))
	}
	cmd := press(p, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("enter on protected app should not quit")
	}
	if _, ok := p.Selected(); ok {
		t.Errorf("protected app must not be selected")
	}
	if v := view(p); !strings.Contains(v, protectedFlash) {
		t.Errorf("flash missing from view:\n%s", v)
	}

	press(p, tea.KeyPressMsg{Code: tea.KeyDown})
	if v := view(p); strings.Contains(v, protectedFlash) {
		t.Errorf("flash should clear on next key:\n%s", v)
	}
}

func TestQuitKeys(t *testing.T) {
	t.Run("esc", func(t *testing.T) {
		p := newTestPicker(t)
		assertQuit(t, press(p, tea.KeyPressMsg{Code: tea.KeyEscape}))
		if _, ok := p.Selected(); ok {
			t.Error("esc must not select")
		}
	})
	t.Run("ctrl+c", func(t *testing.T) {
		p := newTestPicker(t)
		assertQuit(t, press(p, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}))
	})
	t.Run("q with empty search quits", func(t *testing.T) {
		p := newTestPicker(t)
		assertQuit(t, press(p, tea.KeyPressMsg{Code: 'q', Text: "q"}))
	})
	t.Run("q with text is a character", func(t *testing.T) {
		p := newTestPicker(t)
		typeText(p, "s")
		cmd := press(p, tea.KeyPressMsg{Code: 'q', Text: "q"})
		if cmd != nil && isQuit(cmd) {
			t.Error("q should be typed, not quit, when the search box has text")
		}
		if p.input.Value() != "sq" {
			t.Errorf("search value = %q, want sq", p.input.Value())
		}
	})
}

func TestViewContents(t *testing.T) {
	p := newTestPicker(t)
	v := view(p)

	for _, want := range []string{
		searchPrompt, helpText, "1 of 6",
		"Bundle ID", "Version", "Path", "Size",
		"Safari", "Safari (system)", "com.apple.Safari",
		"Protected: system application", // may wrap inside the pane
		"25 MB",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}

	typeText(p, "slack")
	v = view(p)
	if !strings.Contains(v, "Installed via Homebrew cask slack") {
		t.Errorf("cask line missing:\n%s", v)
	}
	if strings.Contains(v, "Protected:") {
		t.Errorf("non-protected app must not show the protected warning:\n%s", v)
	}
}

func TestViewFitsWindow(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}, {50, 12}, {120, 40}} {
		p := NewPicker(fixture())
		p.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(p.View().Content, "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d: view has %d lines, exceeds height", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w > size[0] {
				t.Errorf("%dx%d: line %d is %d cells wide: %q", size[0], size[1], i, w, plain(l))
			}
		}
	}
}

func TestEmptyResult(t *testing.T) {
	p := newTestPicker(t)
	typeText(p, "zzzzqqq")
	if len(p.Matches()) != 0 {
		t.Fatalf("expected no matches, got %v", names(p.Matches()))
	}
	if cmd := press(p, tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Error("enter with no matches should do nothing")
	}
	v := view(p)
	if !strings.Contains(v, "no matching apps") || !strings.Contains(v, "0 of 0") {
		t.Errorf("empty state missing:\n%s", v)
	}
}

func assertQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a quit command, got nil")
	}
	if !isQuit(cmd) {
		t.Fatalf("command produced %T, want tea.QuitMsg", cmd())
	}
}

func isQuit(cmd tea.Cmd) bool {
	_, ok := cmd().(tea.QuitMsg)
	return ok
}
