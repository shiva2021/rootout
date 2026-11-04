package tui

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/llm"
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

// --- loading and help ---

func newLoadingPicker(t *testing.T) *Picker {
	t.Helper()
	p := NewPickerLoading(func(progress.Reporter) ([]app.App, error) { return fixture(), nil })
	p.loadID = 1 // what startLoad would assign; tests inject messages directly
	p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return p
}

func TestLoadingStateRendersProgress(t *testing.T) {
	p := newLoadingPicker(t)
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
	p := newLoadingPicker(t)
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
	p := newLoadingPicker(t)
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
	p := newLoadingPicker(t)
	p.Update(loadedMsg{id: 1, err: errors.New("mdfind exploded")})
	v := view(p)
	if !strings.Contains(v, "could not load apps: mdfind exploded") {
		t.Fatalf("error missing:\n%s", v)
	}
	if _, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Error("enter after a load error must do nothing")
	}
	assertQuit(t, press(p, tea.KeyPressMsg{Code: tea.KeyEscape}))
	if p.quitAll {
		t.Error("esc on the load-error screen must not set quitAll")
	}

	q := newLoadingPicker(t)
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
	p := NewPickerLoading(load)
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

var keyCtrlL = tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}

// pickerAssessor records the apps inspected and asked and answers keep.
func pickerAssessor(answer llm.AppAssessment, err error) (*AppAssessor, *[]string) {
	var asked []string
	return &AppAssessor{
		Provider: "fake", Label: "fake · m",
		Inspect: func(_ context.Context, a app.App) app.Facts {
			return app.Facts{App: a, Signature: app.Signature{Signed: true, TeamID: "TEAM"}}
		},
		Assess: func(_ context.Context, f app.Facts) (llm.AppAssessment, error) {
			asked = append(asked, f.App.Name)
			return answer, err
		},
	}, &asked
}

var keepAnswer = llm.AppAssessment{Purpose: "Plays music.", Source: llm.SourceLegitimate, Confidence: 0.85, Recommendation: llm.Keep, Reasoning: "Signed by Spotify AB.", Model: "m"}

// feedPicker runs cmd and feeds its pickerAssessMsgs back into p.
func feedPicker(p *Picker, cmd tea.Cmd) {
	for _, msg := range runCmd(cmd) {
		if _, ok := msg.(pickerAssessMsg); ok {
			p.Update(msg)
		}
	}
}

func TestPickerAskIgnoredWithoutAssessor(t *testing.T) {
	p := newTestPicker(t)
	typeText(p, "spot")
	before := view(p)
	if cmd := press(p, keyCtrlL); cmd != nil || view(p) != before {
		t.Fatal("ctrl+l without an assessor must do nothing")
	}
	if strings.Contains(before, "ctrl+l") {
		t.Fatal("footer must not offer ctrl+l without an assessor")
	}
	press(p, keyEsc)
}

func TestPickerAskHighlighted(t *testing.T) {
	a, asked := pickerAssessor(keepAnswer, nil)
	p := newTestPicker(t)
	p.SetAssessor(a)
	if !strings.Contains(view(p), "ctrl+l ask AI") {
		t.Fatalf("footer should offer ctrl+l:\n%s", view(p))
	}
	typeText(p, "spot") // the filter keeps its text and ctrl+l still asks
	cmd := press(p, keyCtrlL)
	if cmd == nil || p.input.Value() != "spot" {
		t.Fatalf("ctrl+l should ask (cmd %v) and leave the filter (%q)", cmd != nil, p.input.Value())
	}
	if !strings.Contains(view(p), "asking the AI") {
		t.Fatalf("pending indicator missing:\n%s", view(p))
	}
	feedPicker(p, cmd)
	if !slices.Equal(*asked, []string{"Spotify"}) {
		t.Fatalf("asked %v", *asked)
	}
	out := view(p)
	for _, want := range []string{"KEEP", "85%", "Plays music.", "Signed by Spotify AB.", "legitimate"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "asking the AI") {
		t.Error("pending indicator left on")
	}
	// Asking again for an answered app costs nothing.
	if cmd := press(p, keyCtrlL); cmd != nil || len(*asked) != 1 {
		t.Fatal("second ctrl+l must not ask again")
	}
}

func TestPickerAskProtected(t *testing.T) {
	a, asked := pickerAssessor(keepAnswer, nil)
	p := newTestPicker(t)
	p.SetAssessor(a)
	typeText(p, "safari")
	if cmd := press(p, keyCtrlL); cmd != nil || len(*asked) != 0 || !strings.Contains(view(p), protectedAsk) {
		t.Fatalf("protected app: want a flash and no call:\n%s", view(p))
	}
}

func TestPickerAskError(t *testing.T) {
	a, _ := pickerAssessor(llm.AppAssessment{}, errors.New("llm: no API key configured"))
	p := newTestPicker(t)
	p.SetAssessor(a)
	typeText(p, "spot")
	feedPicker(p, press(p, keyCtrlL))
	if out := view(p); !strings.Contains(out, "AI error: no API key configured") {
		t.Fatalf("error not shown:\n%s", out)
	}
	// An errored app can be asked again.
	if cmd := press(p, keyCtrlL); cmd == nil {
		t.Fatal("ctrl+l after an error should retry")
	}
}

func TestPickerAskSaved(t *testing.T) {
	a, asked := pickerAssessor(keepAnswer, nil)
	saved := keepAnswer
	saved.Cached = true
	a.Cached = func(f app.Facts) (llm.AppAssessment, bool) { return saved, true }
	p := newTestPicker(t)
	p.SetAssessor(a)
	typeText(p, "spot")
	feedPicker(p, press(p, keyCtrlL))
	if len(*asked) != 0 || !strings.Contains(view(p), savedTag) {
		t.Fatalf("saved answer should be used without a call (asked %v):\n%s", *asked, view(p))
	}
}

func TestPickerAskNarrowUsesFlash(t *testing.T) {
	a, _ := pickerAssessor(keepAnswer, nil)
	p := NewPicker(fixture())
	p.SetAssessor(a)
	p.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	typeText(p, "spot")
	cmd := press(p, keyCtrlL)
	if !strings.Contains(view(p), "Spotify: "+askingLabel) {
		t.Fatalf("narrow terminal should say who is being asked on the flash line:\n%s", view(p))
	}
	feedPicker(p, cmd)
	out := view(p)
	if !strings.Contains(out, "Spotify: KEEP 85%") || !strings.Contains(out, "Plays music.") {
		t.Fatalf("narrow terminal should put the named answer on the flash line:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if lipgloss.Width(l) > 50 {
			t.Errorf("line %d wider than the terminal: %q", i, l)
		}
	}
	// An error is named too.
	b, _ := pickerAssessor(llm.AppAssessment{}, errors.New("llm: boom"))
	q := NewPicker(fixture())
	q.SetAssessor(b)
	q.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	typeText(q, "zoom")
	feedPicker(q, press(q, keyCtrlL))
	if !strings.Contains(view(q), "Zoom: AI error: boom") {
		t.Fatalf("narrow terminal should put the error on the flash line:\n%s", view(q))
	}
}

func TestPickerAskNoModel(t *testing.T) {
	// An assessor without a model (facts only) hides the key.
	p := newTestPicker(t)
	p.SetAssessor(&AppAssessor{Inspect: func(_ context.Context, a app.App) app.Facts { return app.Facts{App: a} }})
	if cmd := press(p, keyCtrlL); cmd != nil || strings.Contains(view(p), "ctrl+l") {
		t.Fatal("no model: ctrl+l does nothing and is not offered")
	}
}

func TestPickerHelpListsAsk(t *testing.T) {
	a, _ := pickerAssessor(keepAnswer, nil)
	p := newTestPicker(t)
	p.SetAssessor(a)
	press(p, runeKey('?'))
	if !strings.Contains(view(p), "ctrl+l") {
		t.Fatalf("help should list ctrl+l:\n%s", view(p))
	}
}
