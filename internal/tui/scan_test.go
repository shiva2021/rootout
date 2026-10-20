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
	cmd := pressS(v, runeKey('l'))
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
	for _, msg := range runCmd(pressS(v, runeKey('l'))) {
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
	if cmd := pressS(v, runeKey('l')); cmd != nil {
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
	if v.picker == nil || v.picker.state != stateReady {
		t.Fatalf("picker did not reach the list")
	}
}

func TestScanViewPickModel(t *testing.T) {
	var picked string
	a := Assessor{
		Label:  "ollama · llama3.2:3b",
		Models: &ModelDeps{Client: ollamaServer(t), Hardware: testHW},
		OnModel: func(_ context.Context, model string) (Assessor, error) {
			picked = model
			return Assessor{Label: "ollama · x", Assess: func(context.Context, heur.Report) (llm.Assessment, error) { return llm.Assessment{}, nil }}, nil
		},
	}
	v := newTestScan(t, a)
	openEmbeddedPicker(t, v)
	out := viewS(v)
	if !strings.Contains(out, "llama3.2:3b (current)") || strings.Contains(out, "Scan results") {
		t.Fatalf("picker list not shown with the current model:\n%s", out)
	}
	if v.picker.width != 140 || v.picker.height != 40 {
		t.Fatalf("picker size %dx%d, want 140x40", v.picker.width, v.picker.height)
	}

	cmd := pressS(v, keyEnter)
	if v.picker != nil || cmd == nil {
		t.Fatal("enter on an installed model should close the picker and return the OnModel Cmd")
	}
	pump(v, cmd)
	if picked != "llama3.2:3b" {
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
		Models:  &ModelDeps{Client: ollamaServer(t), Hardware: testHW},
		OnModel: func(context.Context, string) (Assessor, error) { return Assessor{}, errors.New("no such provider") },
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
		OnModel: func(context.Context, string) (Assessor, error) { called = true; return Assessor{}, nil },
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

func TestScanViewHelpOverlay(t *testing.T) {
	v := newTestScan(t, Assessor{})
	if !strings.Contains(viewS(v), "? help") {
		t.Fatal("footer missing the help hint")
	}
	pressS(v, runeKey('?'))
	out := viewS(v)
	for _, want := range []string{helpClose, "tick / untick", "pick a local Ollama model", "type delete"} {
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
	if v.picker == nil || v.picker.state != stateNotInstalled {
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
