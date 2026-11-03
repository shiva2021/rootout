package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/output"
)

func TestPrintReportsCached(t *testing.T) {
	items := []scanItem{{
		Report:     heur.Report{Ext: ext.Extension{Editor: "vscode", ID: "evil.stealer", Version: "1.0.0"}, Score: 85},
		Assessment: &llm.Assessment{Verdict: llm.Malicious, Confidence: 0.9, Reasoning: "Steals keys.", Model: "claude-x", Cached: true},
	}}
	var out bytes.Buffer
	if err := printReports(output.New(&out, true), items, 1, 30); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"cached": true`) || strings.Contains(out.String(), "saved assessment") {
		t.Fatalf("json should carry cached and nothing else:\n%s", out.String())
	}
	out.Reset()
	if err := printReports(output.New(&out, false), items, 1, 30); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "claude-x, saved") {
		t.Fatalf("text should mark the saved answer:\n%s", out.String())
	}
}
