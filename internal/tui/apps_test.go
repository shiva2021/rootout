package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/llm"
)

var keyCtrlA = tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}

func appsFixture() []app.App {
	return []app.App{
		{Name: "Alpha", BundleID: "com.example.alpha", Version: "1.0", Path: "/Applications/Alpha.app", SizeBytes: 12 << 20},
		{Name: "Ghostty", BundleID: "com.mitchellh.ghostty", Version: "1.2.3", Path: "/Applications/Ghostty.app", SizeBytes: 150 << 20, Cask: "ghostty"},
		{Name: "Mail", BundleID: "com.apple.mail", Version: "16.0", Path: "/System/Applications/Mail.app", SizeBytes: 30 << 20, Protected: true},
		{Name: "A very long application name that will not fit a narrow pane", BundleID: "com.example.long", Version: "0.1", Path: "/Applications/Long.app", SizeBytes: 1 << 20},
	}
}

func newTestApps(t *testing.T, a AppAssessor) *AppsView {
	t.Helper()
	v := NewAppsView(appsFixture(), a)
	v.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	return v
}

func pressA(v *AppsView, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = v.Update(k)
	}
	return cmd
}

func viewA(v *AppsView) string { return plain(v.View().Content) }

// quits reports whether cmd yields tea.QuitMsg.
func quits(cmd tea.Cmd) bool {
	for _, msg := range runCmd(cmd) {
		if _, ok := msg.(tea.QuitMsg); ok {
			return true
		}
	}
	return false
}

func TestAppsViewListsApps(t *testing.T) {
	v := newTestApps(t, AppAssessor{})
	out := viewA(v)
	for _, want := range []string{"Alpha", "Ghostty", "[brew]", "Mail", "[system]", "4 apps", "0 ticked", "1.2.3", "MB", "com.example.alpha", scanNoAI} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

func TestAppsViewTickAndEnter(t *testing.T) {
	v := newTestApps(t, AppAssessor{})
	pressA(v, keySpace, keyDown, keySpace)
	if !strings.Contains(viewA(v), "2 ticked") {
		t.Fatalf("ticks not counted:\n%s", viewA(v))
	}
	if !quits(pressA(v, keyEnter)) {
		t.Fatal("enter with ticks should quit")
	}
	res, ok := v.Result()
	if !ok || len(res.Uninstall) != 2 || res.Uninstall[0].Name != "Alpha" || res.Uninstall[1].Name != "Ghostty" {
		t.Fatalf("Result = %+v, %v", res, ok)
	}
}

func TestAppsViewEnterNothingTicked(t *testing.T) {
	v := newTestApps(t, AppAssessor{})
	if quits(pressA(v, keyEnter)) {
		t.Fatal("enter with nothing ticked must not quit")
	}
	if !strings.Contains(viewA(v), nothingTicked) {
		t.Fatalf("hint missing:\n%s", viewA(v))
	}
	if _, ok := v.Result(); ok {
		t.Fatal("nothing accepted")
	}
}

func TestAppsViewProtectedCannotTick(t *testing.T) {
	v := newTestApps(t, AppAssessor{})
	pressA(v, keyDown, keyDown, keySpace)
	out := viewA(v)
	if !strings.Contains(out, protectedFlash) || !strings.Contains(out, "0 ticked") {
		t.Fatalf("protected row ticked or no flash:\n%s", out)
	}
}

func TestAppsViewCtrlATicksAll(t *testing.T) {
	v := newTestApps(t, AppAssessor{})
	pressA(v, keyCtrlA)
	if v.tickedCount() != 3 || v.ticked[2] {
		t.Fatalf("ctrl+a ticked %d (Mail %v), want 3 and never the protected row", v.tickedCount(), v.ticked[2])
	}
	pressA(v, keyCtrlA)
	if v.tickedCount() != 0 {
		t.Fatalf("second ctrl+a should untick all, got %d", v.tickedCount())
	}
	pressA(v, keySpace, keyCtrlA)
	if v.tickedCount() != 3 {
		t.Fatalf("ctrl+a with some ticked should tick the rest, got %d", v.tickedCount())
	}
}

func TestAppsViewEscCancels(t *testing.T) {
	v := newTestApps(t, AppAssessor{})
	pressA(v, keySpace)
	if !quits(pressA(v, tea.KeyPressMsg{Code: tea.KeyEscape})) {
		t.Fatal("esc should quit")
	}
	if _, ok := v.Result(); ok || v.quitAll {
		t.Fatal("esc must cancel, not accept or quit all")
	}
}

func TestAppsViewCtrlXQuitsAll(t *testing.T) {
	v := newTestApps(t, AppAssessor{})
	if !quits(pressA(v, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})) || !v.quitAll {
		t.Fatal("ctrl+x should quit everything")
	}
}

func TestAppsViewRowsExactWidth(t *testing.T) {
	for _, w := range []int{40, 80, 140} {
		for _, withIcons := range []bool{false, true} {
			v := NewAppsView(appsFixture(), AppAssessor{})
			if withIcons {
				v.results[0] = llm.AppAssessment{Recommendation: llm.Keep, Confidence: 0.9}
				v.results[1] = llm.AppAssessment{Recommendation: llm.Review, Confidence: 0.5}
				v.errs[3] = errors.New("boom")
				v.pending[2] = true
			}
			v.Update(tea.WindowSizeMsg{Width: w, Height: 12})
			leftW, _ := v.columns()
			lines := strings.Split(plain(v.renderList(leftW, v.paneRows())), "\n")
			for i, l := range lines {
				if got := lipgloss.Width(l); got != leftW {
					t.Errorf("width %d icons %v: list line %d is %d cells, want %d: %q", w, withIcons, i, got, leftW, l)
				}
			}
		}
	}
}

func TestAppsViewNarrowDropsTagBeforeName(t *testing.T) {
	v := NewAppsView(appsFixture(), AppAssessor{})
	v.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	leftW, _ := v.columns()
	out := plain(v.renderList(leftW, v.paneRows()))
	if strings.Contains(out, "[brew]") || !strings.Contains(out, "Ghostty") {
		t.Fatalf("narrow pane should drop the tag before the name:\n%s", out)
	}
	v.Update(tea.WindowSizeMsg{Width: 140, Height: 12})
	leftW, _ = v.columns()
	if out := plain(v.renderList(leftW, v.paneRows())); !strings.Contains(out, "[brew]") || !strings.Contains(out, "1.2.3") {
		t.Fatalf("wide pane should show tag and version:\n%s", out)
	}
}

func TestAppsViewRecommendationIcon(t *testing.T) {
	v := newTestApps(t, AppAssessor{})
	v.results[0] = llm.AppAssessment{Recommendation: llm.Keep, Confidence: 0.9}
	v.results[1] = llm.AppAssessment{Recommendation: llm.Drop, Confidence: 0.95}
	v.results[2] = llm.AppAssessment{Recommendation: llm.Drop, Confidence: 0.4}
	v.errs[3] = errors.New("boom")
	for i, want := range []string{iconBenign, iconMalicious, iconUnsure, iconError} {
		if got, _ := v.recIcon(i); got != want {
			t.Errorf("row %d icon = %q, want %q", i, got, want)
		}
	}
	v.pending[0] = true
	if got, _ := v.recIcon(0); got == iconBenign {
		t.Error("pending row should show the spinner, not the verdict")
	}
	if !strings.Contains(viewA(v), "✓ keep ✗ drop ⚠") {
		t.Fatalf("legend missing once something is assessed:\n%s", viewA(v))
	}
}

// recordingInspect returns facts carrying a signature and records the app
// names in call order; block, when set, holds every call until closed.
func recordingInspect(block chan struct{}) (func(context.Context, app.App) app.Facts, *[]string) {
	var names []string
	var mu sync.Mutex
	return func(_ context.Context, a app.App) app.Facts {
		mu.Lock()
		names = append(names, a.Name)
		mu.Unlock()
		if block != nil {
			<-block
		}
		return app.Facts{App: a, Signature: app.Signature{Signed: true, TeamID: "TEAM" + a.Name}, Gatekeeper: app.Gatekeeper{Accepted: true, Source: "Notarized Developer ID"}}
	}, &names
}

// recordingAppAssess answers every app with keep and records the names.
func recordingAppAssess() (AppAssessFunc, *[]string) {
	var names []string
	var mu sync.Mutex
	return func(_ context.Context, f app.Facts) (llm.AppAssessment, error) {
		mu.Lock()
		names = append(names, f.App.Name)
		mu.Unlock()
		return llm.AppAssessment{Purpose: "Does " + f.App.Name + " things.", Source: llm.SourceLegitimate, Confidence: 0.8, Recommendation: llm.Keep, Reasoning: "Signed by " + f.Signature.TeamID + ". Fine."}, nil
	}, &names
}

// feedApps runs cmd and feeds its facts and assessment messages back into
// v, returning what the view wants to run next.
func feedApps(v *AppsView, cmd tea.Cmd) tea.Cmd {
	var next []tea.Cmd
	for _, msg := range runCmd(cmd) {
		switch msg.(type) {
		case factsMsg, appAssessMsg:
			if _, c := v.Update(msg); c != nil {
				next = append(next, c)
			}
		}
	}
	if len(next) == 0 {
		return nil
	}
	return tea.Batch(next...)
}

// pressDrain presses each key and runs what it starts (cursor probes), as
// the program would.
func pressDrain(v *AppsView, keys ...tea.KeyPressMsg) {
	for _, k := range keys {
		drain(v, pressA(v, k))
	}
}

// drain feeds messages until nothing is left to run.
func drain(v *AppsView, cmd tea.Cmd) {
	for cmd != nil {
		cmd = feedApps(v, cmd)
	}
}

func TestAppsViewInspectsOnCursor(t *testing.T) {
	inspect, names := recordingInspect(nil)
	v := newTestApps(t, AppAssessor{Inspect: inspect})
	drain(v, v.Init())
	if _, ok := v.facts[0]; !ok || !slices.Equal(*names, []string{"Alpha"}) {
		t.Fatalf("Init should inspect the cursor row: facts=%v names=%v", v.facts, *names)
	}
	out := viewA(v)
	if !strings.Contains(out, "TEAMAlpha") || !strings.Contains(out, "Notarized Developer ID") {
		t.Fatalf("facts not shown:\n%s", out)
	}
	drain(v, pressA(v, keyDown))
	if !slices.Equal(*names, []string{"Alpha", "Ghostty"}) {
		t.Fatalf("moving the cursor should inspect the new row: %v", *names)
	}
	drain(v, pressA(v, keyUp))
	if len(*names) != 2 {
		t.Fatalf("a row with facts must not be inspected again: %v", *names)
	}
}

func TestAppsViewInspectBounded(t *testing.T) {
	block := make(chan struct{})
	inspect, names := recordingInspect(block)
	v := newTestApps(t, AppAssessor{Inspect: inspect})
	first := v.Init()
	second := pressA(v, keyDown)
	third := pressA(v, keyDown)
	fourth := pressA(v, keyDown)
	if len(v.inspecting) != 2 || third != nil || fourth != nil {
		t.Fatalf("inspecting = %v (third %v, fourth %v); want two in flight and no more started", v.inspecting, third != nil, fourth != nil)
	}
	if !strings.Contains(viewA(v), inspectingLabel) == v.inspecting[3] {
		t.Fatalf("details should say inspecting only for a row in flight:\n%s", viewA(v))
	}
	close(block)
	// The first probe finishing frees a slot: the cursor row (3) is next.
	next := feedApps(v, first)
	if next == nil || !v.inspecting[3] {
		t.Fatalf("finished probe should start one for the cursor row: inspecting=%v", v.inspecting)
	}
	drain(v, next)
	drain(v, second)
	// Dispatch order is Alpha, Ghostty, then the cursor row; the recorder
	// sees execution order, so check the set and that Mail (never rested
	// on) was skipped.
	if len(*names) != 3 || slices.Contains(*names, "Mail") || !slices.Contains(*names, "A very long application name that will not fit a narrow pane") {
		t.Fatalf("inspected = %v", *names)
	}
}

func TestAppsViewAskTickedInOrder(t *testing.T) {
	assess, names := recordingAppAssess()
	v := newTestApps(t, AppAssessor{Assess: assess})
	pressDrain(v, keyDown, keySpace, keyUp, keySpace) // tick 1 then 0
	cmd := pressA(v, runeKey('l'))
	if cmd == nil || !strings.Contains(viewA(v), "asking 2") {
		t.Fatalf("l should ask the two ticked rows:\n%s", viewA(v))
	}
	drain(v, cmd)
	if !slices.Equal(*names, []string{"Alpha", "Ghostty"}) {
		t.Fatalf("ask order = %v, want display order", *names)
	}
	if _, ok := v.facts[0]; !ok {
		t.Fatal("the ask path should keep the facts it gathered")
	}
	if a := v.results[0]; a.Recommendation != llm.Keep || len(v.pending) != 0 {
		t.Fatalf("results = %+v pending = %v", v.results, v.pending)
	}
	if !strings.Contains(viewA(v), iconBenign) {
		t.Fatalf("icon missing:\n%s", viewA(v))
	}
}

func TestAppsViewAskWaitsForInspect(t *testing.T) {
	block := make(chan struct{})
	inspect, _ := recordingInspect(block)
	assess, names := recordingAppAssess()
	v := newTestApps(t, AppAssessor{Inspect: inspect, Assess: assess})
	probe := v.Init() // row 0 is being inspected
	pressA(v, keySpace)
	if cmd := pressA(v, runeKey('l')); cmd != nil || len(v.queue) != 1 || len(v.pending) != 0 {
		t.Fatalf("l while the row is being inspected should queue it: cmd=%v queue=%v pending=%v", cmd != nil, v.queue, v.pending)
	}
	close(block)
	drain(v, probe)
	if !slices.Equal(*names, []string{"Alpha"}) || len(v.queue) != 0 {
		t.Fatalf("the finished probe should start the queued ask: names=%v queue=%v", *names, v.queue)
	}
}

func TestAppsViewAskBounded(t *testing.T) {
	assess, _ := recordingAppAssess()
	v := newTestApps(t, AppAssessor{Assess: assess})
	pressA(v, keyCtrlA)
	cmd := pressA(v, runeKey('l'))
	if len(v.pending) != assessWorkers || len(v.queue) != 1 {
		t.Fatalf("pending %d queue %d; want %d and 1", len(v.pending), len(v.queue), assessWorkers)
	}
	drain(v, cmd)
	if len(v.results) != 3 || len(v.queue) != 0 {
		t.Fatalf("results %d queue %d", len(v.results), len(v.queue))
	}
}

func TestAppsViewAskSkipsAnsweredRetriesErrored(t *testing.T) {
	assess, names := recordingAppAssess()
	v := newTestApps(t, AppAssessor{Assess: assess})
	v.results[0] = llm.AppAssessment{Recommendation: llm.Keep}
	v.errs[1] = errors.New("boom")
	pressA(v, keyCtrlA)
	drain(v, pressA(v, runeKey('l')))
	if !slices.Equal(*names, []string{"Ghostty", "A very long application name that will not fit a narrow pane"}) {
		t.Fatalf("asked %v: answered rows are skipped, errored rows retried", *names)
	}
	if v.errs[1] != nil {
		t.Fatal("retry should clear the error")
	}
}

func TestAppsViewLReasksCached(t *testing.T) {
	assess, asked := recordingAppAssess()
	reassess, reasked := recordingAppAssess()
	v := newTestApps(t, AppAssessor{Assess: assess, Reassess: reassess})
	v.results[0] = llm.AppAssessment{Recommendation: llm.Keep, Cached: true}
	pressA(v, keySpace)
	if cmd := pressA(v, runeKey('l')); cmd != nil || !strings.Contains(viewA(v), "1 already answered") {
		t.Fatalf("l should skip the saved answer:\n%s", viewA(v))
	}
	drain(v, pressA(v, runeKey('L')))
	if len(*asked) != 0 || !slices.Equal(*reasked, []string{"Alpha"}) {
		t.Fatalf("L should use Reassess: asked=%v reasked=%v", *asked, *reasked)
	}
	if v.results[0].Cached {
		t.Fatal("the fresh answer replaces the saved one")
	}
}

func TestAppsViewCachedFillOnFacts(t *testing.T) {
	inspect, _ := recordingInspect(nil)
	assess, names := recordingAppAssess()
	cached := func(f app.Facts) (llm.AppAssessment, bool) {
		if f.App.Name == "Alpha" {
			return llm.AppAssessment{Purpose: "Saved purpose.", Recommendation: llm.Keep, Confidence: 0.9, Model: "claude-x", Cached: true}, true
		}
		return llm.AppAssessment{}, false
	}
	v := newTestApps(t, AppAssessor{Inspect: inspect, Assess: assess, Cached: cached})
	drain(v, v.Init())
	if a, ok := v.results[0]; !ok || !a.Cached {
		t.Fatalf("facts arriving should fill the saved answer: %+v", v.results)
	}
	out := viewA(v)
	if !strings.Contains(out, "claude-x · saved") || !strings.Contains(out, "Saved purpose.") || !strings.Contains(out, iconBenign) {
		t.Fatalf("saved answer not shown:\n%s", out)
	}
	if len(*names) != 0 {
		t.Fatal("a saved answer must not call the model")
	}
	drain(v, pressA(v, keyDown))
	if _, ok := v.results[1]; ok {
		t.Fatal("a row without a saved answer stays unanswered")
	}
}

func TestAppsViewSummaryPane(t *testing.T) {
	assess, _ := recordingAppAssess()
	v := newTestApps(t, AppAssessor{Assess: assess})
	pressDrain(v, keySpace, keyDown, keySpace)
	drain(v, pressA(v, runeKey('l')))
	out := viewA(v)
	for _, want := range []string{"AI summary", "2 ticked", "2 answered", "Alpha", "KEEP 80%", "Does Alpha things.", "Ghostty"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Bundle ID") {
		t.Errorf("summary should replace the single detail:\n%s", out)
	}
	pressA(v, keySpace) // untick the cursor row: back to the single detail
	if !strings.Contains(viewA(v), "Bundle ID") {
		t.Fatalf("one ticked row should show the detail again:\n%s", viewA(v))
	}
}

func TestAppsViewModelSwapKeepsFacts(t *testing.T) {
	inspect, _ := recordingInspect(nil)
	cachedOld := func(f app.Facts) (llm.AppAssessment, bool) {
		return llm.AppAssessment{Recommendation: llm.Keep, Model: "old", Cached: true}, true
	}
	cachedNew := func(f app.Facts) (llm.AppAssessment, bool) {
		if f.App.Name == "Alpha" {
			return llm.AppAssessment{Recommendation: llm.Drop, Confidence: 0.9, Model: "new", Cached: true}, true
		}
		return llm.AppAssessment{}, false
	}
	v := newTestApps(t, AppAssessor{Inspect: inspect, Cached: cachedOld, Label: "p · old"})
	drain(v, v.Init())
	drain(v, pressA(v, keyDown))
	v.results[1] = llm.AppAssessment{Recommendation: llm.Review, Model: "old"} // answered on this screen, not saved
	v.Update(appAssessorMsg{a: AppAssessor{Label: "p · new", Cached: cachedNew}})
	if v.gen != 1 || v.assessor.Label != "p · new" {
		t.Fatalf("gen %d label %q", v.gen, v.assessor.Label)
	}
	if len(v.facts) != 2 {
		t.Fatal("facts must survive a model swap")
	}
	if a := v.results[0]; a.Model != "new" || a.Recommendation != llm.Drop {
		t.Fatalf("row 0 should take the new model's saved answer: %+v", a)
	}
	if a, ok := v.results[1]; !ok || a.Model != "old" {
		t.Fatalf("row 1's own answer should stay: %+v %v", a, ok)
	}
	v.Update(appAssessorMsg{err: errors.New("no key")})
	if !strings.Contains(viewA(v), "model: no key") {
		t.Fatalf("swap error should show on the error line:\n%s", viewA(v))
	}
}

func TestAppsViewStaleAnswerKeptOnlyWhenNothingNewer(t *testing.T) {
	v := newTestApps(t, AppAssessor{Assess: func(context.Context, app.Facts) (llm.AppAssessment, error) { return llm.AppAssessment{}, nil }})
	v.pending[0], v.pending[1] = true, true
	v.Update(appAssessorMsg{a: AppAssessor{Label: "p · new"}}) // gen 1
	v.results[1] = llm.AppAssessment{Recommendation: llm.Keep, Model: "new"}
	v.Update(appAssessMsg{app: 0, gen: 0, a: llm.AppAssessment{Recommendation: llm.Drop, Model: "old"}})
	v.Update(appAssessMsg{app: 1, gen: 0, a: llm.AppAssessment{Recommendation: llm.Drop, Model: "old"}})
	if v.results[0].Model != "old" || v.results[1].Model != "new" || len(v.pending) != 0 {
		t.Fatalf("results = %+v pending = %v", v.results, v.pending)
	}
}

func TestAppsViewUntickedQueuedRowIsSkipped(t *testing.T) {
	assess, names := recordingAppAssess()
	v := newTestApps(t, AppAssessor{Assess: assess})
	pressA(v, keyCtrlA)
	cmd := pressA(v, runeKey('l'))                 // two in flight, the long one queued
	pressA(v, keyDown, keyDown, keyDown, keySpace) // untick row 3 while queued
	drain(v, cmd)
	if slices.Contains(*names, "A very long application name that will not fit a narrow pane") {
		t.Fatalf("an unticked queued row must not be asked: %v", *names)
	}
}

func TestAppsViewAskNothingTickedOrNoModel(t *testing.T) {
	assess, names := recordingAppAssess()
	v := newTestApps(t, AppAssessor{Assess: assess})
	if cmd := pressA(v, runeKey('l')); cmd != nil || !strings.Contains(viewA(v), nothingTickedApps) || len(*names) != 0 {
		t.Fatalf("l with nothing ticked should hint:\n%s", viewA(v))
	}
	w := newTestApps(t, AppAssessor{})
	pressA(w, keySpace)
	if cmd := pressA(w, runeKey('l')); cmd != nil || strings.Contains(viewA(w), "l ask AI") {
		t.Fatal("no model: l does nothing and the footer hides it")
	}
}

func TestAppsViewLateFactsAfterQuit(t *testing.T) {
	block := make(chan struct{})
	inspect, _ := recordingInspect(block)
	v := newTestApps(t, AppAssessor{Inspect: inspect})
	probe := v.Init()
	pressA(v, keyEsc)
	close(block)
	if _, cmd := v.Update(runCmd(probe)[0]); cmd != nil && quits(cmd) {
		t.Fatal("a late probe result must not change the outcome")
	}
	if _, ok := v.Result(); ok {
		t.Fatal("esc result must stay cancelled")
	}
}

// pumpA runs cmd, feeds every resulting message except spinner ticks back
// into v and repeats until nothing is left to run.
func pumpA(v *AppsView, cmd tea.Cmd) {
	for _, msg := range runCmd(cmd) {
		if _, tick := msg.(spinner.TickMsg); tick || msg == nil {
			continue
		}
		_, next := v.Update(msg)
		pumpA(v, next)
	}
}

func TestAppsViewModelKeyWithoutDeps(t *testing.T) {
	v := newTestApps(t, AppAssessor{})
	if cmd := pressA(v, runeKey('m')); cmd != nil || v.picker != nil {
		t.Fatal("m without Models should do nothing")
	}
	if out := viewA(v); !strings.Contains(out, "Installed apps") || strings.Contains(out, "m model") {
		t.Fatalf("apps view should still show without the m hint:\n%s", out)
	}
}

func TestAppsViewPickModelOllama(t *testing.T) {
	var picked string
	a := AppAssessor{
		Provider: "ollama",
		Label:    "ollama · llama3.2:3b",
		Models:   &ModelDeps{Client: ollamaServer(t), Hardware: testHW},
		OnModel: func(_ context.Context, provider, model string) (AppAssessor, error) {
			picked = provider + "/" + model
			return AppAssessor{Provider: provider, Label: "ollama · x", Assess: func(context.Context, app.Facts) (llm.AppAssessment, error) { return llm.AppAssessment{}, nil }}, nil
		},
	}
	v := newTestApps(t, a)
	cmd := pressA(v, runeKey('m'))
	if v.picker == nil || cmd == nil || !strings.Contains(viewA(v), "looking for Ollama") {
		t.Fatalf("m should open the Ollama picker:\n%s", viewA(v))
	}
	pumpA(v, cmd)
	if mp, ok := v.picker.(*ModelPicker); !ok || mp.state != stateReady || mp.width != 140 {
		t.Fatalf("picker did not reach the list at the screen size")
	}
	if out := viewA(v); !strings.Contains(out, "llama3.2:3b (current)") || strings.Contains(out, "Installed apps") {
		t.Fatalf("picker list not shown with the current model:\n%s", out)
	}
	cmd = pressA(v, keyEnter)
	if v.picker != nil || cmd == nil {
		t.Fatal("enter should close the picker and return the OnModel Cmd")
	}
	pumpA(v, cmd)
	if picked != "ollama/llama3.2:3b" {
		t.Fatalf("OnModel got %q", picked)
	}
	if out := viewA(v); !strings.Contains(out, "AI: ollama · x") || !strings.Contains(out, "l ask AI") || v.assessor.OnModel == nil {
		t.Fatalf("assessor not replaced (OnModel kept):\n%s", out)
	}
}

func TestAppsViewPickModelHosted(t *testing.T) {
	var picked string
	a := AppAssessor{
		Provider:     "anthropic",
		Label:        "anthropic · claude-sonnet-5-5",
		HostedModels: func(context.Context) ([]llm.ModelInfo, error) { return hostedFixture(), nil },
	}
	a.OnModel = func(_ context.Context, provider, model string) (AppAssessor, error) {
		picked = provider + "/" + model
		return AppAssessor{Provider: provider, Label: hostedLabel(provider, model), HostedModels: a.HostedModels}, nil
	}
	v := newTestApps(t, a)
	if !strings.Contains(viewA(v), "m model") {
		t.Fatalf("footer should offer m for a hosted provider:\n%s", viewA(v))
	}
	pumpA(v, pressA(v, runeKey('m')))
	if out := viewA(v); !strings.Contains(out, "› claude-sonnet-5-5 (current)") {
		t.Fatalf("hosted list not shown:\n%s", out)
	}
	pumpA(v, pressA(v, keyUp, keyEnter))
	if picked != "anthropic/claude-new" || !strings.Contains(viewA(v), "AI: anthropic · claude-new") {
		t.Fatalf("OnModel got %q:\n%s", picked, viewA(v))
	}
	// esc closes the picker without a pick.
	called := false
	v.assessor.OnModel = func(context.Context, string, string) (AppAssessor, error) { called = true; return AppAssessor{}, nil }
	pumpA(v, pressA(v, runeKey('m')))
	if cmd := pressA(v, keyEsc); cmd != nil || v.picker != nil || called || !strings.Contains(viewA(v), "Installed apps") {
		t.Fatal("esc should close the picker without OnModel or quitting")
	}
}

func TestAppsViewCtrlXClosesPickerAndQuits(t *testing.T) {
	a := AppAssessor{Provider: "anthropic", Label: "anthropic · m",
		HostedModels: func(context.Context) ([]llm.ModelInfo, error) { return hostedFixture(), nil }}
	v := newTestApps(t, a)
	pumpA(v, pressA(v, runeKey('m')))
	if !quits(pressA(v, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})) || !v.quitAll || v.picker != nil {
		t.Fatal("ctrl+x inside the picker should quit everything")
	}
}

func TestAppsViewHelpOverlay(t *testing.T) {
	v := newTestApps(t, AppAssessor{})
	pressA(v, runeKey('?'))
	out := viewA(v)
	for _, want := range []string{"ctrl+a", "tick every app", "ask the configured AI", "ignoring saved answers", "pick a model", "keep · drop", "70", "uninstall the ticked apps", "ctrl+x"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Installed apps") {
		t.Error("help should replace the screen")
	}
	pressA(v, runeKey('x'))
	if !strings.Contains(viewA(v), "Installed apps") {
		t.Fatal("any key should close the help")
	}
}

func TestAppsViewFooterFitsWidth(t *testing.T) {
	for _, w := range []int{40, 80, 140} {
		v := NewAppsView(appsFixture(), AppAssessor{Assess: func(context.Context, app.Facts) (llm.AppAssessment, error) { return llm.AppAssessment{}, nil }, Models: &ModelDeps{}})
		v.Update(tea.WindowSizeMsg{Width: w, Height: 20})
		lines := strings.Split(viewA(v), "\n")
		footer := strings.TrimRight(lines[len(lines)-1], " ")
		if got := lipgloss.Width(footer); got > w {
			t.Errorf("width %d: footer is %d cells: %q", w, got, footer)
		}
		if !strings.HasSuffix(footer, "? help") {
			t.Errorf("width %d: footer must end with ? help: %q", w, footer)
		}
		if len(lines) != 20 {
			t.Errorf("width %d: view has %d lines, want 20", w, len(lines))
		}
	}
}

func TestAppsViewDetailsScroll(t *testing.T) {
	inspect, _ := recordingInspect(nil)
	v := newTestApps(t, AppAssessor{Inspect: inspect})
	drain(v, v.Init())
	v.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
	pressA(v, tea.KeyPressMsg{Code: tea.KeyTab}, keyDown, keyDown)
	if v.detailTop == 0 {
		t.Fatal("details should scroll when focused at a small height")
	}
	pressA(v, tea.KeyPressMsg{Code: tea.KeyTab}, keyDown)
	if v.cursor != 1 || v.detailTop != 0 {
		t.Fatalf("tab back: cursor %d detailTop %d", v.cursor, v.detailTop)
	}
}
