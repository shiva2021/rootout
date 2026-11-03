package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/llm/ollama"
)

func runeKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func scanFixture() []heur.Report {
	return []heur.Report{
		{
			Ext:   ext.Extension{Editor: "vscode", ID: "evil.stealer", Publisher: "evil", DisplayName: "Totally Fine", Version: "1.0.0", Dir: "/x/evil.stealer-1.0.0", Orphaned: true},
			Score: 85,
			Findings: []heur.Finding{
				{Rule: "eval_dynamic", Severity: heur.Medium, File: "out/a.js", Line: 3, Detail: "eval of a computed string", Snippet: "eval(atob(x))"},
				{Rule: "credential_exfil", Severity: heur.Critical, File: "out/b.js", Line: 10, Detail: "reads ~/.ssh and posts it", Snippet: "fetch(u,{body:key})"},
			},
		},
		{
			Ext:      ext.Extension{Editor: "cursor", ID: "mid.tool", Publisher: "mid", Version: "2.0.0"},
			Score:    40,
			Findings: []heur.Finding{{Rule: "child_process_shell", Severity: heur.Medium, File: "main.js", Line: 7, Detail: "spawns a shell"}},
		},
		{
			Ext:   ext.Extension{Editor: "vscode", ID: "good.theme", Publisher: "good", Version: "0.1.0"},
			Score: 5,
		},
	}
}

func newTestScan(t *testing.T, a Assessor) *ScanView {
	t.Helper()
	v := NewScanView(scanFixture(), a)
	v.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	return v
}

// typeS feeds s to the view one key press at a time.
func typeS(v *ScanView, s string) {
	for _, r := range s {
		pressS(v, runeKey(r))
	}
}

// pump runs cmd, feeds every resulting message except spinner ticks back
// into v and repeats until nothing is left to run.
func pump(v *ScanView, cmd tea.Cmd) {
	for _, msg := range runCmd(cmd) {
		if _, tick := msg.(spinner.TickMsg); tick || msg == nil {
			continue
		}
		_, next := v.Update(msg)
		pump(v, next)
	}
}

// ollamaServer serves a running Ollama with one installed model.
func ollamaServer(t *testing.T) *ollama.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.12.0"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:3b","size":2000000000,"details":{"parameter_size":"3.2B","quantization_level":"Q4_K_M"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return ollama.New(srv.URL)
}

func pressS(v *ScanView, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = v.Update(k)
	}
	return cmd
}

func viewS(v *ScanView) string { return plain(v.View().Content) }

func TestScanViewGroupsFindings(t *testing.T) {
	v := newTestScan(t, Assessor{})
	out := viewS(v)
	for _, want := range []string{"Intent", "Capabilities", "[critical] credential_exfil", "out/b.js:10", "eval_dynamic", "Totally Fine", "Orphaned", "evil.stealer", "[cursor]"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q\n%s", want, out)
		}
	}
	if strings.Index(out, "Intent") > strings.Index(out, "Capabilities") {
		t.Error("Intent should come before Capabilities")
	}
	if strings.Contains(out, "ask AI") || strings.Contains(out, "m model") {
		t.Error("help shows AI keys without an Assessor")
	}
	if !strings.Contains(out, scanNoAI+"\n") {
		t.Errorf("header should say %q with no Models:\n%s", scanNoAI, out)
	}
}

func TestScanViewHeaderAI(t *testing.T) {
	v := newTestScan(t, Assessor{Label: "anthropic · claude-sonnet-5-5"})
	if out := viewS(v); !strings.Contains(out, "AI: anthropic · claude-sonnet-5-5") {
		t.Fatalf("label missing:\n%s", out)
	}
	v = newTestScan(t, Assessor{Models: &ModelDeps{}})
	if out := viewS(v); !strings.Contains(out, scanNoAIPick) || !strings.Contains(out, "m model") {
		t.Fatalf("pick hint missing:\n%s", out)
	}
}

func TestScanViewToggleFilterConfirm(t *testing.T) {
	v := newTestScan(t, Assessor{})

	pressS(v, keySpace, keyDown, keySpace, keySpace) // tick 0, tick+untick 1
	pressS(v, keyDown, keySpace)                     // tick 2
	if n := v.tickedCount(); n != 2 {
		t.Fatalf("ticked = %d, want 2", n)
	}

	pressS(v, runeKey('f'))
	if len(v.visible) != 2 || strings.Contains(viewS(v), "good.theme") {
		t.Fatalf("filter should hide good.theme; visible=%v", v.visible)
	}
	pressS(v, runeKey('f'))
	if len(v.visible) != 3 {
		t.Fatalf("unfilter: visible=%v", v.visible)
	}

	pressS(v, keyEnter)
	if !strings.Contains(viewS(v), "Type delete to uninstall 2 extensions") {
		t.Fatalf("no typed box:\n%s", viewS(v))
	}
	typeS(v, "y")
	if cmd := pressS(v, keyEnter); cmd != nil || v.confirm == nil || !strings.Contains(viewS(v), typedWrongErr) {
		t.Fatalf("y+enter should be rejected:\n%s", viewS(v))
	}
	if _, ok := v.Result(); ok {
		t.Fatal("rejected word accepted the result")
	}
	pressS(v, keyEsc)
	if v.confirm != nil || strings.Contains(viewS(v), "Type delete") {
		t.Fatal("esc should close the box")
	}
	if _, ok := v.Result(); ok {
		t.Fatal("esc from the box should not accept")
	}

	pressS(v, keyEnter)
	typeS(v, "delete")
	if cmd := pressS(v, keyEnter); cmd == nil {
		t.Fatal("delete+enter should quit")
	}
	res, ok := v.Result()
	if !ok || len(res.Uninstall) != 2 || res.Uninstall[0].Ext.ID != "evil.stealer" || res.Uninstall[1].Ext.ID != "good.theme" {
		t.Fatalf("result = %+v ok=%v", res, ok)
	}
}

func TestScanViewEnterNothingTicked(t *testing.T) {
	v := newTestScan(t, Assessor{})
	pressS(v, keyEnter)
	if v.confirm != nil || !strings.Contains(viewS(v), nothingTicked) {
		t.Fatal("enter with nothing ticked should flash")
	}
	pressS(v, keyEsc)
	if _, ok := v.Result(); ok {
		t.Fatal("esc should cancel")
	}
}

// runCmd executes cmd, expanding batches, and returns every message.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestScanViewAssess(t *testing.T) {
	var got string
	assess := func(_ context.Context, r heur.Report) (llm.Assessment, error) {
		got = r.Ext.ID
		return llm.Assessment{Verdict: llm.Malicious, Confidence: 0.92, Reasoning: "Sends SSH keys to a remote host.", IOCs: []string{"evil.example.com"}}, nil
	}
	v := newTestScan(t, Assessor{Assess: assess})
	if !strings.Contains(viewS(v), "l ask AI") {
		t.Error("help should show the AI key")
	}
	cmd := pressS(v, keySpace, runeKey('l'))
	if cmd == nil {
		t.Fatal("l should return a Cmd")
	}
	if !strings.Contains(viewS(v), "asking the LLM") {
		t.Error("no pending indicator")
	}
	var found bool
	for _, msg := range runCmd(cmd) {
		if _, ok := msg.(assessMsg); ok {
			found = true
			v.Update(msg)
		}
	}
	if !found || got != "evil.stealer" {
		t.Fatalf("assess not run on the cursor report (got %q)", got)
	}
	out := viewS(v)
	for _, want := range []string{"MALICIOUS", "92% confidence", "Sends SSH keys", "evil.example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "asking the LLM") {
		t.Error("pending indicator left on")
	}
}

func TestScanViewAssessError(t *testing.T) {
	assess := func(context.Context, heur.Report) (llm.Assessment, error) {
		return llm.Assessment{}, llm.ErrNoKey
	}
	v := newTestScan(t, Assessor{Assess: assess})
	for _, msg := range runCmd(pressS(v, keySpace, runeKey('l'))) {
		v.Update(msg)
	}
	if !strings.Contains(viewS(v), "LLM unavailable: no API key configured") {
		t.Fatalf("error not shown:\n%s", viewS(v))
	}
	if !errors.Is(v.errs[0], llm.ErrNoKey) {
		t.Fatal("error not stored")
	}
}

func TestScanViewAssessNil(t *testing.T) {
	v := newTestScan(t, Assessor{})
	if cmd := pressS(v, keySpace, runeKey('l')); cmd != nil {
		t.Fatal("l without assess should do nothing")
	}
	if len(v.pending) != 0 {
		t.Fatal("nothing should be pending")
	}
}

func TestScanViewDetailsScroll(t *testing.T) {
	v := NewScanView(scanFixture(), Assessor{})
	v.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	pressS(v, tea.KeyPressMsg{Code: tea.KeyTab})
	pressS(v, keyDown, keyDown, keyDown)
	if v.detailTop == 0 || v.cursor != 0 {
		t.Fatalf("details focus: detailTop=%d cursor=%d", v.detailTop, v.cursor)
	}
	pressS(v, tea.KeyPressMsg{Code: tea.KeyTab}, keyDown)
	if v.cursor != 1 || v.detailTop != 0 {
		t.Fatalf("list focus: detailTop=%d cursor=%d", v.detailTop, v.cursor)
	}
}

func TestScanViewModelKeyWithoutDeps(t *testing.T) {
	v := newTestScan(t, Assessor{})
	if cmd := pressS(v, runeKey('m')); cmd != nil || v.picker != nil {
		t.Fatal("m without Models should do nothing")
	}
	if !strings.Contains(viewS(v), "Scan results") {
		t.Fatal("scan view should still show")
	}
}

// openEmbeddedPicker presses m and runs the picker's detect/list Cmds
// against the fake Ollama until the list is shown.
func openEmbeddedPicker(t *testing.T, v *ScanView) {
	t.Helper()
	cmd := pressS(v, runeKey('m'))
	if v.picker == nil || cmd == nil {
		t.Fatal("m should open the picker and start detection")
	}
	if !strings.Contains(viewS(v), "looking for Ollama") {
		t.Fatalf("picker not rendered:\n%s", viewS(v))
	}
	pump(v, cmd)
	if mp, ok := v.picker.(*ModelPicker); !ok || mp.state != stateReady {
		t.Fatalf("picker did not reach the list")
	}
}

func TestScanViewPickModel(t *testing.T) {
	var picked string
	a := Assessor{
		Provider: "ollama",
		Label:    "ollama · llama3.2:3b",
		Models:   &ModelDeps{Client: ollamaServer(t), Hardware: testHW},
		OnModel: func(_ context.Context, provider, model string) (Assessor, error) {
			picked = provider + "/" + model
			return Assessor{Label: "ollama · x", Assess: func(context.Context, heur.Report) (llm.Assessment, error) { return llm.Assessment{}, nil }}, nil
		},
	}
	v := newTestScan(t, a)
	openEmbeddedPicker(t, v)
	out := viewS(v)
	if !strings.Contains(out, "llama3.2:3b (current)") || strings.Contains(out, "Scan results") {
		t.Fatalf("picker list not shown with the current model:\n%s", out)
	}
	if mp := v.picker.(*ModelPicker); mp.width != 140 || mp.height != 40 {
		t.Fatalf("picker size %dx%d, want 140x40", mp.width, mp.height)
	}

	cmd := pressS(v, keyEnter)
	if v.picker != nil || cmd == nil {
		t.Fatal("enter on an installed model should close the picker and return the OnModel Cmd")
	}
	pump(v, cmd)
	if picked != "ollama/llama3.2:3b" {
		t.Fatalf("OnModel got %q", picked)
	}
	out = viewS(v)
	if !strings.Contains(out, "AI: ollama · x") || !strings.Contains(out, "l ask AI") || v.assessor.Models != nil {
		t.Fatalf("assessor not replaced:\n%s", out)
	}
	if v.assessor.OnModel == nil {
		t.Fatal("OnModel should be kept when the replacement has none")
	}
}

func TestScanViewPickModelError(t *testing.T) {
	a := Assessor{
		Models: &ModelDeps{Client: ollamaServer(t), Hardware: testHW},
		OnModel: func(context.Context, string, string) (Assessor, error) {
			return Assessor{}, errors.New("no such provider")
		},
	}
	v := newTestScan(t, a)
	openEmbeddedPicker(t, v)
	pump(v, pressS(v, keyEnter))
	out := viewS(v)
	if !strings.Contains(out, "model: no such provider") || !strings.Contains(out, scanNoAIPick) {
		t.Fatalf("error line missing or assessor changed:\n%s", out)
	}
	pressS(v, keyDown)
	if strings.Contains(viewS(v), "no such provider") {
		t.Fatal("error line should clear on the next key")
	}
}

func TestScanViewPickModelCancel(t *testing.T) {
	called := false
	a := Assessor{
		Models:  &ModelDeps{Client: ollamaServer(t), Hardware: testHW},
		OnModel: func(context.Context, string, string) (Assessor, error) { called = true; return Assessor{}, nil },
	}
	v := newTestScan(t, a)
	openEmbeddedPicker(t, v)
	if cmd := pressS(v, keyEsc); cmd != nil || v.picker != nil || called {
		t.Fatal("esc should close the picker without OnModel or quitting")
	}
	if !strings.Contains(viewS(v), "Scan results") {
		t.Fatal("scan view not restored")
	}
	if _, ok := v.Result(); ok {
		t.Fatal("cancelling the picker accepted the scan")
	}
}

func TestScanViewCtrlXQuitsAll(t *testing.T) {
	ctrlX := tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}
	for name, setup := range map[string]func(t *testing.T, v *ScanView){
		"list": func(t *testing.T, v *ScanView) {},
		"typed box": func(t *testing.T, v *ScanView) {
			pressS(v, keyEnter)
			if v.confirm == nil {
				t.Fatal("typed box not open")
			}
		},
		"embedded picker": func(t *testing.T, v *ScanView) { openEmbeddedPicker(t, v) },
		"help overlay": func(t *testing.T, v *ScanView) {
			pressS(v, runeKey('?'))
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := Assessor{Models: &ModelDeps{Client: ollamaServer(t), Hardware: testHW}}
			v := newTestScan(t, a)
			pressS(v, keySpace) // tick one so enter opens the typed box
			setup(t, v)
			assertQuit(t, pressS(v, ctrlX))
			if v.picker != nil || !v.quitAll {
				t.Errorf("picker=%v quitAll=%v, want nil/true", v.picker, v.quitAll)
			}
			if _, ok := v.Result(); ok {
				t.Error("ctrl+x must not accept the scan")
			}
		})
	}
}

func TestScanViewHelpOverlay(t *testing.T) {
	v := newTestScan(t, Assessor{})
	if !strings.Contains(viewS(v), "? help") {
		t.Fatal("footer missing the help hint")
	}
	pressS(v, runeKey('?'))
	out := viewS(v)
	for _, want := range []string{helpClose, "tick / untick", "pick a model", "type delete", "quit rootout"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Scan results") {
		t.Fatal("overlay should replace the screen")
	}
	pressS(v, keySpace) // closes, and must not tick anything
	if v.help || !strings.Contains(viewS(v), "Scan results") || v.tickedCount() != 0 {
		t.Fatalf("any key should only close the help; ticked=%d", v.tickedCount())
	}
}

// Regression: with Ollama absent the embedded picker shows the install
// guide; esc must hand control back to the scan, and a second esc quits.
func TestScanViewPickModelCancelWhenNotInstalled(t *testing.T) {
	cl := ollama.New("http://127.0.0.1:1")
	cl.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	cl.Stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	v := newTestScan(t, Assessor{Models: &ModelDeps{Client: cl, Hardware: testHW, HasBrew: true}})
	cmd := pressS(v, runeKey('m'))
	if v.picker == nil || cmd == nil {
		t.Fatal("m should open the picker")
	}
	pump(v, cmd)
	if mp, ok := v.picker.(*ModelPicker); !ok || mp.state != stateNotInstalled {
		t.Fatalf("picker state = %v, want not installed", v.picker)
	}
	if !strings.Contains(viewS(v), "not installed") {
		t.Fatalf("install guide not shown:\n%s", viewS(v))
	}
	if cmd := pressS(v, keyEsc); v.picker != nil || cmd != nil {
		t.Fatalf("first esc should close the picker (picker=%v cmd=%v)", v.picker != nil, cmd != nil)
	}
	if !strings.Contains(viewS(v), "Scan results") {
		t.Fatal("scan view not restored")
	}
	if cmd := pressS(v, keyEsc); cmd == nil {
		t.Fatal("second esc should quit")
	}
}

func TestScanViewModelKeyHosted(t *testing.T) {
	var picked string
	fetched := 0
	a := Assessor{
		Provider: "anthropic",
		Label:    "anthropic · claude-sonnet-5-5",
		HostedModels: func(context.Context) ([]llm.ModelInfo, error) {
			fetched++
			return hostedFixture(), nil
		},
		OnModel: func(_ context.Context, provider, model string) (Assessor, error) {
			picked = provider + "/" + model
			return Assessor{Provider: provider, Label: hostedLabel(provider, model), Assess: func(context.Context, heur.Report) (llm.Assessment, error) { return llm.Assessment{}, nil }}, nil
		},
	}
	v := newTestScan(t, a)
	if !strings.Contains(viewS(v), "m model") {
		t.Fatalf("footer should offer m for a hosted provider:\n%s", viewS(v))
	}
	cmd := pressS(v, runeKey('m'))
	if v.picker == nil || cmd == nil || !strings.Contains(viewS(v), "listing models") {
		t.Fatalf("m should open the hosted picker and start the fetch:\n%s", viewS(v))
	}
	pump(v, cmd)
	out := viewS(v)
	if fetched != 1 || !strings.Contains(out, "› claude-sonnet-5-5 (current)") || strings.Contains(out, "Scan results") {
		t.Fatalf("hosted list not shown with the current model (fetched %d):\n%s", fetched, out)
	}
	cmd = pressS(v, keyUp, keyEnter)
	if v.picker != nil || cmd == nil {
		t.Fatal("enter should close the picker and return the OnModel Cmd")
	}
	pump(v, cmd)
	if picked != "anthropic/claude-new" {
		t.Fatalf("OnModel got %q", picked)
	}
	if out := viewS(v); !strings.Contains(out, "AI: anthropic · claude-new") || !strings.Contains(out, "l ask AI") {
		t.Fatalf("assessor not replaced:\n%s", out)
	}
}

func TestScanViewModelKeyHostedFetchError(t *testing.T) {
	a := Assessor{
		Provider: "openai",
		Label:    "openai · gpt-5.2",
		HostedModels: func(context.Context) ([]llm.ModelInfo, error) {
			return nil, errors.New("openai: could not list models: offline")
		},
		OnModel: func(_ context.Context, p, m string) (Assessor, error) {
			return Assessor{Provider: p, Label: hostedLabel(p, m)}, nil
		},
	}
	v := newTestScan(t, a)
	pump(v, pressS(v, runeKey('m')))
	out := viewS(v)
	if !strings.Contains(out, "could not list models") || !strings.Contains(out, "gpt-5.2 (current)") || !strings.Contains(out, "› Type a model id") {
		t.Fatalf("error with the typing fallback expected:\n%s", out)
	}
	pressS(v, keyEnter)
	typeS(v, "-pro")
	pump(v, pressS(v, keyEnter))
	if out := viewS(v); !strings.Contains(out, "AI: openai · gpt-5.2-pro") {
		t.Fatalf("typed model not applied:\n%s", out)
	}
}

func TestScanViewModelKeyHostedEsc(t *testing.T) {
	called := false
	a := Assessor{
		Provider:     "anthropic",
		Label:        "anthropic · claude-sonnet-5-5",
		HostedModels: func(context.Context) ([]llm.ModelInfo, error) { return hostedFixture(), nil },
		OnModel:      func(context.Context, string, string) (Assessor, error) { called = true; return Assessor{}, nil },
	}
	v := newTestScan(t, a)
	pump(v, pressS(v, runeKey('m')))
	if cmd := pressS(v, keyEsc); cmd != nil || v.picker != nil || called {
		t.Fatal("esc should close the picker without OnModel or quitting")
	}
	if out := viewS(v); !strings.Contains(out, "Scan results") || !strings.Contains(out, "AI: anthropic · claude-sonnet-5-5") {
		t.Fatalf("scan view not restored:\n%s", out)
	}
}

func TestScanViewModelKeyHostedWithoutFetchFallsBackToOllama(t *testing.T) {
	// A hosted provider without a model list still offers the Ollama picker.
	a := Assessor{Provider: "anthropic", Label: "anthropic · claude-sonnet-5-5", Models: &ModelDeps{Client: ollamaServer(t), Hardware: testHW}}
	v := newTestScan(t, a)
	openEmbeddedPicker(t, v)
	if _, ok := v.picker.(*ModelPicker); !ok {
		t.Fatal("expected the Ollama picker")
	}
}

// recordingAssess answers every report with a benign verdict and records the
// ids in call order.
func recordingAssess() (AssessFunc, *[]string) {
	var ids []string
	return func(_ context.Context, r heur.Report) (llm.Assessment, error) {
		ids = append(ids, r.Ext.ID)
		return llm.Assessment{Verdict: llm.Benign, Confidence: 0.8, Reasoning: "Nothing unusual. Looks fine."}, nil
	}, &ids
}

// feedAssess runs cmd and feeds its assessMsgs back into v, returning what
// the view wants to run next.
func feedAssess(v *ScanView, cmd tea.Cmd) tea.Cmd {
	var next []tea.Cmd
	for _, msg := range runCmd(cmd) {
		if _, ok := msg.(assessMsg); ok {
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

func TestScanViewAssessNothingTicked(t *testing.T) {
	assess, ids := recordingAssess()
	v := newTestScan(t, Assessor{Assess: assess})
	if cmd := pressS(v, runeKey('l')); cmd != nil {
		t.Fatal("l with nothing ticked should send nothing")
	}
	if !strings.Contains(viewS(v), nothingTickedAssess) || len(v.pending) != 0 || len(*ids) != 0 {
		t.Fatalf("hint missing or something started:\n%s", viewS(v))
	}
}

func TestScanViewAssessTickedInVisibleOrder(t *testing.T) {
	assess, ids := recordingAssess()
	v := newTestScan(t, Assessor{Assess: assess})
	pressS(v, keySpace, keyDown, keyDown, keySpace, keyUp) // tick 0 and 2, cursor on 1
	cmd := pressS(v, runeKey('l'))
	if cmd == nil {
		t.Fatal("l should return a Cmd")
	}
	if !strings.Contains(viewS(v), "asking 2") {
		t.Fatalf("flash should count the questions:\n%s", viewS(v))
	}
	feedAssess(v, cmd)
	if want := "evil.stealer good.theme"; strings.Join(*ids, " ") != want {
		t.Fatalf("assessed %v, want %q (ticked rows, never the cursor row)", *ids, want)
	}
	if len(v.results) != 2 || len(v.pending) != 0 {
		t.Fatalf("results %d pending %d", len(v.results), len(v.pending))
	}
}

func TestScanViewAssessBounded(t *testing.T) {
	assess, ids := recordingAssess()
	v := newTestScan(t, Assessor{Assess: assess})
	pressS(v, keySpace, keyDown, keySpace, keyDown, keySpace) // tick all three
	cmd := pressS(v, runeKey('l'))
	if len(v.pending) != assessWorkers || len(v.queue) != 1 {
		t.Fatalf("pending %d queue %d, want %d and 1", len(v.pending), len(v.queue), assessWorkers)
	}
	// The first batch carries exactly the two workers (plus the spinner).
	next := feedAssess(v, cmd)
	if len(*ids) != 2 || next == nil {
		t.Fatalf("after the first batch: asked %v, next cmd %v", *ids, next != nil)
	}
	if len(v.queue) != 0 || len(v.pending) != 1 {
		t.Fatalf("third should have started: pending %d queue %d", len(v.pending), len(v.queue))
	}
	feedAssess(v, next)
	if len(*ids) != 3 || len(v.pending) != 0 || len(v.results) != 3 {
		t.Fatalf("all three should be answered: asked %v, pending %d, results %d", *ids, len(v.pending), len(v.results))
	}
}

func TestScanViewAssessSkipsAnswered(t *testing.T) {
	assess, ids := recordingAssess()
	v := newTestScan(t, Assessor{Assess: assess})
	v.results[0] = llm.Assessment{Verdict: llm.Malicious, Confidence: 0.9}
	pressS(v, keySpace, keyDown, keySpace) // tick 0 and 1
	cmd := pressS(v, runeKey('l'))
	if !strings.Contains(viewS(v), "1 already answered") {
		t.Fatalf("flash should say what was skipped:\n%s", viewS(v))
	}
	feedAssess(v, cmd)
	if want := "mid.tool"; strings.Join(*ids, " ") != want {
		t.Fatalf("assessed %v, want %q", *ids, want)
	}
	// Everything answered: l says so and sends nothing.
	if cmd := pressS(v, runeKey('l')); cmd != nil || !strings.Contains(viewS(v), "2 already answered") {
		t.Fatalf("second l should send nothing:\n%s", viewS(v))
	}
}

func TestScanViewAssessRetriesErrored(t *testing.T) {
	assess, ids := recordingAssess()
	v := newTestScan(t, Assessor{Assess: assess})
	v.errs[0] = errors.New("boom")
	pressS(v, keySpace)
	feedAssess(v, pressS(v, runeKey('l')))
	if len(*ids) != 1 || v.errs[0] != nil {
		t.Fatalf("errored row should be asked again: %v, err %v", *ids, v.errs[0])
	}
}

func TestScanViewAssessHiddenTicked(t *testing.T) {
	assess, ids := recordingAssess()
	v := newTestScan(t, Assessor{Assess: assess})
	pressS(v, keyDown, keyDown, keySpace, runeKey('f')) // tick good.theme, then hide it
	pressS(v, keyUp, keyUp, keySpace)                   // tick evil.stealer
	cmd := pressS(v, runeKey('l'))
	if len(v.pending) != 2 {
		t.Fatalf("hidden ticked rows are asked too: pending %d", len(v.pending))
	}
	if out := viewS(v); !strings.Contains(out, "good.theme") || !strings.Contains(out, "(hidden)") {
		t.Fatalf("summary should list the hidden row and say so:\n%s", out)
	}
	feedAssess(v, cmd)
	if want := "evil.stealer good.theme"; strings.Join(*ids, " ") != want {
		t.Fatalf("assessed %v, want %q (visible first, then hidden)", *ids, want)
	}
}

func TestScanViewSummaryPane(t *testing.T) {
	var release = make(chan struct{})
	assess := func(_ context.Context, r heur.Report) (llm.Assessment, error) {
		if r.Ext.ID == "mid.tool" {
			<-release
			return llm.Assessment{Verdict: llm.Suspicious, Confidence: 0.6, Reasoning: "Spawns a shell. Could be fine."}, nil
		}
		return llm.Assessment{Verdict: llm.Malicious, Confidence: 0.92, Reasoning: "Sends SSH keys to a remote host. More text here.", Model: "claude-x"}, nil
	}
	v := newTestScan(t, Assessor{Assess: assess})
	pressS(v, keySpace, keyDown, keySpace) // tick 0 and 1
	cmd := pressS(v, runeKey('l'))
	// Run only the first worker's Cmd so the second stays pending.
	batch, _ := cmd().(tea.BatchMsg)
	for _, c := range batch {
		if msg := c(); msg != nil {
			if am, ok := msg.(assessMsg); ok && am.report == 0 {
				v.Update(am)
			}
		}
		if len(v.results) == 1 {
			break
		}
	}
	out := viewS(v)
	for _, want := range []string{"AI summary", "2 ticked", "evil.stealer", "MALICIOUS 92%", "Sends SSH keys to a remote host.", "mid.tool", "asking…"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Intent") || strings.Contains(out, "More text here") {
		t.Errorf("summary should replace the single detail and keep the first sentence only:\n%s", out)
	}
	close(release)
	// Finish the second worker.
	for _, c := range batch {
		if msg := c(); msg != nil {
			if am, ok := msg.(assessMsg); ok && am.report == 1 {
				v.Update(am)
			}
		}
	}
	out = viewS(v)
	if strings.Contains(out, "asking…") || !strings.Contains(out, "SUSPICIOUS 60%") {
		t.Fatalf("second result not shown:\n%s", out)
	}
	// The pane scrolls with tab focus at a small height.
	v.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	pressS(v, tea.KeyPressMsg{Code: tea.KeyTab}, keyDown, keyDown)
	if v.detailTop == 0 {
		t.Fatal("summary pane should scroll when focused")
	}
}

func TestScanViewSummaryFallsBackToSingle(t *testing.T) {
	assess, _ := recordingAssess()
	v := newTestScan(t, Assessor{Assess: assess})
	// Two ticked but nothing asked: the cursor row's detail stays.
	pressS(v, keySpace, keyDown, keySpace)
	if out := viewS(v); !strings.Contains(out, "Capabilities") || strings.Contains(out, "AI summary") {
		t.Fatalf("ticking for uninstall must not hide the findings:\n%s", out)
	}
	// One ticked and asked: still the single detail, with the verdict box.
	pressS(v, keySpace) // untick 1
	feedAssess(v, pressS(v, runeKey('l')))
	pressS(v, keyUp)
	if out := viewS(v); !strings.Contains(out, "Intent") || !strings.Contains(out, "BENIGN") || strings.Contains(out, "AI summary") {
		t.Fatalf("single ticked row keeps the full detail:\n%s", out)
	}
}

func TestScanViewModelSwapMidQueue(t *testing.T) {
	first, firstIDs := recordingAssess()
	second, secondIDs := recordingAssess()
	v := newTestScan(t, Assessor{Assess: first})
	pressS(v, keySpace, keyDown, keySpace, keyDown, keySpace)
	cmd := pressS(v, runeKey('l'))
	v.Update(assessorMsg{a: Assessor{Label: "ollama · other", Assess: second}})
	feedAssess(v, feedAssess(v, cmd))
	if len(*firstIDs) != 2 || len(*secondIDs) != 1 || (*secondIDs)[0] != "good.theme" {
		t.Fatalf("queued item should use the new model: first %v second %v", *firstIDs, *secondIDs)
	}
}

func TestVerdictIcon(t *testing.T) {
	tests := []struct {
		name   string
		result *llm.Assessment
		err    error
		want   string
	}{
		{"benign confident", &llm.Assessment{Verdict: llm.Benign, Confidence: 0.9}, nil, iconBenign},
		{"benign at threshold", &llm.Assessment{Verdict: llm.Benign, Confidence: 0.70}, nil, iconBenign},
		{"benign unsure", &llm.Assessment{Verdict: llm.Benign, Confidence: 0.5}, nil, iconUnsure},
		{"malicious confident", &llm.Assessment{Verdict: llm.Malicious, Confidence: 0.70}, nil, iconMalicious},
		{"malicious unsure", &llm.Assessment{Verdict: llm.Malicious, Confidence: 0.69}, nil, iconUnsure},
		{"suspicious", &llm.Assessment{Verdict: llm.Suspicious, Confidence: 0.99}, nil, iconUnsure},
		{"unknown", &llm.Assessment{Verdict: llm.Unknown, Confidence: 0.99}, nil, iconUnsure},
		{"error", nil, errors.New("boom"), iconError},
		{"none", nil, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newTestScan(t, Assessor{})
			if tt.result != nil {
				v.results[0] = *tt.result
			}
			if tt.err != nil {
				v.errs[0] = tt.err
			}
			if got, _ := v.verdictIcon(0); got != tt.want {
				t.Errorf("icon = %q, want %q", got, tt.want)
			}
		})
	}
	v := newTestScan(t, Assessor{})
	v.pending[0] = true
	if got, _ := v.verdictIcon(0); got == "" || got == iconUnsure {
		t.Errorf("pending should show the spinner, got %q", got)
	}
}

func TestScanViewRowIconRendered(t *testing.T) {
	v := newTestScan(t, Assessor{})
	v.results[0] = llm.Assessment{Verdict: llm.Malicious, Confidence: 0.92}
	v.results[2] = llm.Assessment{Verdict: llm.Benign, Confidence: 0.95}
	v.errs[1] = errors.New("boom")
	leftW, _ := v.columns()
	row := func(i int) string { return strings.TrimRight(plain(v.renderRow(i, false, leftW)), " ") }
	if got := row(0); !strings.HasSuffix(got, "[vscode] "+iconMalicious) {
		t.Errorf("evil row = %q, want it to end with the editor tag and %s", got, iconMalicious)
	}
	if got := row(1); !strings.HasSuffix(got, "[cursor] "+iconError) {
		t.Errorf("mid row = %q, want %s", got, iconError)
	}
	if got := row(2); !strings.HasSuffix(got, "[vscode] "+iconBenign) {
		t.Errorf("good row = %q, want %s", got, iconBenign)
	}
	// The legend appears once something has been assessed.
	out := viewS(v)
	if !strings.Contains(out, iconBenign+" benign") || !strings.Contains(out, iconMalicious+" malicious") || !strings.Contains(out, iconUnsure+" unsure") {
		t.Errorf("legend missing:\n%s", out)
	}
	if strings.Contains(viewS(newTestScan(t, Assessor{})), iconBenign+" benign") {
		t.Error("legend should be hidden before any assessment")
	}
}

func TestScanViewRowsExactWidth(t *testing.T) {
	for _, w := range []int{40, 80, 140} {
		for _, withIcons := range []bool{false, true} {
			v := NewScanView(scanFixture(), Assessor{})
			if withIcons {
				v.results[0] = llm.Assessment{Verdict: llm.Malicious, Confidence: 0.92}
				v.results[1] = llm.Assessment{Verdict: llm.Suspicious, Confidence: 0.5}
				v.errs[2] = errors.New("boom")
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

func TestScanViewIconLegendInHelp(t *testing.T) {
	v := newTestScan(t, Assessor{})
	pressS(v, runeKey('?'))
	if out := viewS(v); !strings.Contains(out, iconBenign+" "+iconMalicious+" "+iconUnsure) || !strings.Contains(out, "70") {
		t.Fatalf("help should explain the icons and the threshold:\n%s", out)
	}
}
