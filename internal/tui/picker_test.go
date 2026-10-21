package tui

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/progress"
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
		if p.quitAll {
			t.Error("esc must only back out, not quit rootout")
		}
	})
	t.Run("ctrl+c", func(t *testing.T) {
		p := newTestPicker(t)
		assertQuit(t, press(p, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}))
		if !p.quitAll {
			t.Error("ctrl+c should set quitAll")
		}
	})
	t.Run("ctrl+x", func(t *testing.T) {
		p := newTestPicker(t)
		assertQuit(t, press(p, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}))
		if !p.quitAll {
			t.Error("ctrl+x should set quitAll")
		}
	})
	t.Run("q with empty search quits", func(t *testing.T) {
		p := newTestPicker(t)
		assertQuit(t, press(p, tea.KeyPressMsg{Code: 'q', Text: "q"}))
		if p.quitAll {
			t.Error("q must only back out, not quit rootout")
		}
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

// --- loading, orphans row and help ---

func newLoadingPicker(t *testing.T, opts PickOptions) *Picker {
	t.Helper()
	p := NewPickerLoading(func(progress.Reporter) ([]app.App, error) { return fixture(), nil }, opts)
	p.loadID = 1 // what startLoad would assign; tests inject messages directly
	p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return p
}

func TestLoadingStateRendersProgress(t *testing.T) {
	p := newLoadingPicker(t, PickOptions{})
	v := view(p)
	if !strings.Contains(v, loadLabel) || !strings.Contains(v, "0/0") {
		t.Fatalf("loading view missing label or count:\n%s", v)
	}
	p.Update(loadProgressMsg{id: 1, done: 3, total: 10, detail: "Slack.app"})
	v = view(p)
	if !strings.Contains(v, "3/10") || !strings.Contains(v, "Slack.app") {
		t.Fatalf("progress not rendered:\n%s", v)
	}
	if _, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Error("enter while loading must do nothing")
	}
	// Stale or mismatched ids are ignored.
	p.Update(loadProgressMsg{id: 7, done: 9, total: 10})
	if p.loadDone != 3 {
		t.Errorf("stale progress applied: done = %d", p.loadDone)
	}
}

func TestLoadedMsgPopulatesList(t *testing.T) {
	p := newLoadingPicker(t, PickOptions{})
	p.Update(loadedMsg{id: 1, apps: fixture()})
	if p.loading || len(p.Matches()) != 6 {
		t.Fatalf("loading=%v matches=%d after loadedMsg", p.loading, len(p.Matches()))
	}
	v := view(p)
	if strings.Contains(v, loadLabel) || !strings.Contains(v, "Safari") {
		t.Fatalf("list not shown after load:\n%s", v)
	}
}

func TestTypingDuringLoadIsApplied(t *testing.T) {
	p := newLoadingPicker(t, PickOptions{})
	typeText(p, "spo")
	if len(p.Matches()) != 0 {
		t.Fatalf("matches before load = %d, want 0", len(p.Matches()))
	}
	p.Update(loadedMsg{id: 1, apps: fixture()})
	ms := p.Matches()
	if len(ms) == 0 || ms[0].App.Name != "Spotify" {
		t.Fatalf("query typed during load not applied: %v", names(ms))
	}
}

func TestLoadErrorShowsAndEscQuits(t *testing.T) {
	p := newLoadingPicker(t, PickOptions{OrphansRow: true})
	p.Update(loadedMsg{id: 1, err: errors.New("mdfind exploded")})
	v := view(p)
	if !strings.Contains(v, "could not load apps: mdfind exploded") {
		t.Fatalf("error missing:\n%s", v)
	}
	if strings.Contains(v, orphansTitle) {
		t.Errorf("orphans row must stay hidden on error:\n%s", v)
	}
	if _, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Error("enter after a load error must do nothing")
	}
	assertQuit(t, press(p, tea.KeyPressMsg{Code: tea.KeyEscape}))
	if p.quitAll {
		t.Error("esc on the load-error screen must not set quitAll")
	}

	q := newLoadingPicker(t, PickOptions{OrphansRow: true})
	q.Update(loadedMsg{id: 1, err: errors.New("mdfind exploded")})
	assertQuit(t, press(q, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}))
	if !q.quitAll {
		t.Error("ctrl+x on the load-error screen should set quitAll")
	}
}

// TestLoadBridge drives the real goroutine + channel path from startLoad.
func TestLoadBridge(t *testing.T) {
	load := func(r progress.Reporter) ([]app.App, error) {
		apps := fixture()
		r.Start("Scanning applications", len(apps))
		for _, a := range apps {
			r.Step(a.Path)
		}
		r.Done()
		return apps, nil
	}
	p := NewPickerLoading(load, PickOptions{})
	cmd := p.startLoad()
	var progressSeen int
	for i := 0; cmd != nil && i < 100; i++ {
		msg := cmd()
		if _, ok := msg.(loadProgressMsg); ok {
			progressSeen++
		}
		_, cmd = p.Update(msg)
	}
	if p.loading {
		t.Fatal("picker still loading after the bridge drained")
	}
	if progressSeen != 7 { // Start + 6 Steps
		t.Errorf("progress messages = %d, want 7", progressSeen)
	}
	if len(p.Matches()) != 6 {
		t.Errorf("matches = %d, want 6", len(p.Matches()))
	}
}

func TestOrphansRow(t *testing.T) {
	p := NewPicker(nil)
	p.opts.OrphansRow = true
	p.setApps(fixture())
	p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	v := view(p)
	if !strings.Contains(v, "› "+orphansTitle) || !strings.Contains(v, "find files left by apps") {
		t.Fatalf("orphans row missing or not first:\n%s", v)
	}
	if !strings.Contains(v, "1 of 7") {
		t.Errorf("footer should count the orphans row:\n%s", v)
	}
	if p.Cursor() != -1 {
		t.Errorf("Cursor() on the orphans row = %d, want -1", p.Cursor())
	}
	press(p, tea.KeyPressMsg{Code: tea.KeyDown})
	if p.Cursor() != 0 {
		t.Errorf("Cursor() after down = %d, want 0 (Safari)", p.Cursor())
	}

	typeText(p, "sl")
	if v := view(p); strings.Contains(v, orphansTitle) {
		t.Fatalf("orphans row should hide while a query is typed:\n%s", v)
	}
	if _, ok := p.Picked(); ok {
		t.Fatal("nothing picked yet")
	}
	press(p, tea.KeyPressMsg{Code: tea.KeyBackspace}, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if v := view(p); !strings.Contains(v, orphansTitle) {
		t.Fatalf("orphans row should return when the query is cleared:\n%s", v)
	}

	cmd := press(p, tea.KeyPressMsg{Code: tea.KeyEnter})
	assertQuit(t, cmd)
	pick, ok := p.Picked()
	if !ok || !pick.Orphans || pick.App.Name != "" {
		t.Fatalf("Picked() = %+v, %v; want Orphans", pick, ok)
	}
	if _, ok := p.Selected(); ok {
		t.Error("Selected() must report ok=false for the orphans row")
	}
}

func TestOrphansRowAbsentByDefault(t *testing.T) {
	p := newTestPicker(t)
	if v := view(p); strings.Contains(v, orphansTitle) {
		t.Fatalf("orphans row shown without the option:\n%s", v)
	}
	if !strings.Contains(view(p), "6 of 6") && !strings.Contains(view(p), "1 of 6") {
		t.Errorf("footer count wrong:\n%s", view(p))
	}
}

func TestPickerHelpOverlay(t *testing.T) {
	p := newTestPicker(t)
	if v := view(p); !strings.Contains(v, helpHint) {
		t.Fatalf("footer should offer ? help when the query is empty:\n%s", v)
	}
	press(p, tea.KeyPressMsg{Code: '?', Text: "?"})
	v := view(p)
	for _, want := range []string{"Keys", "pgup/pgdn", "filter the list", "ctrl+x", helpClose} {
		if !strings.Contains(v, want) {
			t.Fatalf("help overlay missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, searchPrompt) {
		t.Errorf("overlay should cover the screen:\n%s", v)
	}
	// ctrl+x quits even with the overlay open.
	q := newTestPicker(t)
	press(q, tea.KeyPressMsg{Code: '?', Text: "?"})
	assertQuit(t, press(q, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}))
	if !q.quitAll {
		t.Error("ctrl+x with the overlay open should set quitAll")
	}
	// Any key closes the overlay and is consumed.
	cmd := press(p, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if cmd != nil && isQuit(cmd) {
		t.Fatal("closing key must not act")
	}
	if p.input.Value() != "" {
		t.Errorf("closing key leaked into the search: %q", p.input.Value())
	}
	if v := view(p); strings.Contains(v, helpClose) {
		t.Fatalf("overlay still open:\n%s", v)
	}

	// With a query, ? is a character and the footer drops the hint.
	typeText(p, "s?")
	if p.input.Value() != "s?" {
		t.Errorf("search value = %q, want s?", p.input.Value())
	}
	if v := view(p); strings.Contains(v, helpClose) || strings.Contains(v, helpHint) {
		t.Fatalf("? with a query must not open help or show the hint:\n%s", v)
	}
}
