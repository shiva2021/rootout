package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
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

func newTestScan(t *testing.T, assess AssessFunc) *ScanView {
	t.Helper()
	v := NewScanView(scanFixture(), assess)
	v.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	return v
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
	v := newTestScan(t, nil)
	out := viewS(v)
	for _, want := range []string{"Intent", "Capabilities", "[critical] credential_exfil", "out/b.js:10", "eval_dynamic", "Totally Fine", "Orphaned", "evil.stealer", "[cursor]"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q\n%s", want, out)
		}
	}
	if strings.Index(out, "Intent") > strings.Index(out, "Capabilities") {
		t.Error("Intent should come before Capabilities")
	}
	if strings.Contains(out, "ask the LLM") {
		t.Error("help shows the LLM key without an assess func")
	}
}

func TestScanViewToggleFilterConfirm(t *testing.T) {
	v := newTestScan(t, nil)

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
	if !strings.Contains(viewS(v), "Uninstall 2 extensions? y/n") {
		t.Fatalf("no inline confirm:\n%s", viewS(v))
	}
	pressS(v, runeKey('n'))
	if v.confirming {
		t.Fatal("n should dismiss the confirm")
	}
	pressS(v, keyEnter)
	if cmd := pressS(v, runeKey('y')); cmd == nil {
		t.Fatal("y should quit")
	}
	res, ok := v.Result()
	if !ok || len(res.Uninstall) != 2 || res.Uninstall[0].Ext.ID != "evil.stealer" || res.Uninstall[1].Ext.ID != "good.theme" {
		t.Fatalf("result = %+v ok=%v", res, ok)
	}
}

func TestScanViewEnterNothingTicked(t *testing.T) {
	v := newTestScan(t, nil)
	pressS(v, keyEnter)
	if v.confirming || !strings.Contains(viewS(v), nothingTicked) {
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
	v := newTestScan(t, assess)
	if !strings.Contains(viewS(v), "l ask the LLM") {
		t.Error("help should show the LLM key")
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
	v := newTestScan(t, assess)
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
	v := newTestScan(t, nil)
	if cmd := pressS(v, runeKey('l')); cmd != nil {
		t.Fatal("l without assess should do nothing")
	}
	if len(v.pending) != 0 {
		t.Fatal("nothing should be pending")
	}
}

func TestScanViewDetailsScroll(t *testing.T) {
	v := NewScanView(scanFixture(), nil)
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
