package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/tui"
)

func fakeAppAssessor(answer llm.AppAssessment, err error) (tui.AppAssessor, *[]string) {
	var asked []string
	return tui.AppAssessor{
		Provider: "fake", Label: "fake · fake",
		Inspect: func(_ context.Context, a app.App) app.Facts {
			return app.Facts{App: a, Signature: app.Signature{Signed: true, TeamID: "TEAM"}}
		},
		Assess: func(_ context.Context, f app.Facts) (llm.AppAssessment, error) {
			asked = append(asked, f.App.Name)
			return answer, err
		},
	}, &asked
}

var appList = []app.App{
	{Name: "Alpha", BundleID: "com.example.alpha", Version: "1.0", Path: "/Applications/Alpha.app", SizeBytes: 12 << 20},
	{Name: "Mail", BundleID: "com.apple.mail", Version: "16.0", Path: "/System/Applications/Mail.app", Protected: true},
	{Name: "Ghostty", BundleID: "com.mitchellh.ghostty", Version: "1.2", Path: "/Applications/Ghostty.app", Cask: "ghostty"},
}

func TestAssessItemJSONShape(t *testing.T) {
	a := llm.AppAssessment{Purpose: "Edits.", Source: llm.SourceLegitimate, Confidence: 0.9, Recommendation: llm.Keep, Cached: true}
	raw, err := json.Marshal(assessItem{Facts: app.Facts{App: appList[0]}, Assessment: &a})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	for _, key := range []string{"facts", "assessment"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing %q in %s", key, raw)
		}
	}
	if _, ok := m["app"]; ok {
		t.Error("the app lives under facts only")
	}
	if facts, _ := m["facts"].(map[string]any); facts["app"] == nil {
		t.Errorf("facts.app missing in %s", raw)
	}
	if _, ok := m["assessment_error"]; ok {
		t.Error("assessment_error should be omitted when empty")
	}
	if !strings.Contains(string(raw), `"cached":true`) {
		t.Error("cached flag lost")
	}
	raw, _ = json.Marshal(assessItem{Facts: app.Facts{App: appList[0]}, AssessErr: "boom"})
	if strings.Contains(string(raw), `"assessment"`) || !strings.Contains(string(raw), `"assessment_error":"boom"`) {
		t.Errorf("error shape: %s", raw)
	}
}

func TestAssessAppsSkipsProtectedAndReportsErrors(t *testing.T) {
	answer := llm.AppAssessment{Purpose: "Does things.", Source: llm.SourceLegitimate, Confidence: 0.85, Recommendation: llm.Keep, Model: "fake"}
	a, asked := fakeAppAssessor(answer, nil)
	items := assessApps(context.Background(), a, appList)
	if len(items) != 2 || items[0].Facts.App.Name != "Alpha" || items[1].Facts.App.Name != "Ghostty" {
		t.Fatalf("items = %+v", items)
	}
	if strings.Join(*asked, ",") != "Alpha,Ghostty" {
		t.Fatalf("asked %v", *asked)
	}
	if items[0].Assessment == nil || items[0].Assessment.Recommendation != llm.Keep || items[0].Facts.Signature.TeamID != "TEAM" {
		t.Fatalf("item = %+v", items[0])
	}
	b, _ := fakeAppAssessor(llm.AppAssessment{}, errors.New("boom"))
	items = assessApps(context.Background(), b, appList[:1])
	if items[0].Assessment != nil || items[0].AssessErr != "boom" {
		t.Fatalf("error item = %+v", items[0])
	}
	// No model: facts only, no assessment and no error.
	c := tui.AppAssessor{Inspect: a.Inspect}
	items = assessApps(context.Background(), c, appList[:1])
	if items[0].Assessment != nil || items[0].AssessErr != "" || items[0].Facts.Signature.TeamID != "TEAM" {
		t.Fatalf("no-model item = %+v", items[0])
	}
}

func TestAssessOneUsesSavedAnswerFirst(t *testing.T) {
	a, asked := fakeAppAssessor(llm.AppAssessment{Recommendation: llm.Drop}, nil)
	a.Cached = func(f app.Facts) (llm.AppAssessment, bool) {
		return llm.AppAssessment{Recommendation: llm.Keep, Cached: true}, true
	}
	item := assessOne(context.Background(), a, appList[0])
	if len(*asked) != 0 || item.Assessment == nil || !item.Assessment.Cached {
		t.Fatalf("saved answer not used: asked=%v item=%+v", *asked, item)
	}
}

func TestPrintAssessItems(t *testing.T) {
	a := llm.AppAssessment{Purpose: "Edits text.", Source: llm.SourceLegitimate, Confidence: 0.85, Recommendation: llm.Keep, Model: "claude-x", Reasoning: "Signed by its developer.", Cached: true}
	items := []assessItem{
		{Facts: app.Facts{App: appList[0], Signature: app.Signature{Signed: true, TeamID: "TEAM", Authority: []string{"Developer ID Application: X (TEAM)"}}, Gatekeeper: app.Gatekeeper{Accepted: true, Source: "Notarized Developer ID"}, Origin: app.Origin{WhereFroms: []string{"https://x.example/a.dmg"}}}, Assessment: &a},
		{Facts: app.Facts{App: appList[2], Unavailable: []string{"spctl"}}, AssessErr: "boom"},
		{Facts: app.Facts{App: app.App{Name: "Store", Path: "/Applications/Store.app"}, AppStore: true, Plist: app.PlistFacts{UsageKeys: []string{"NSCameraUsageDescription"}}}, Assessment: &llm.AppAssessment{Recommendation: llm.Keep, Confidence: 0.57}},
	}
	var buf bytes.Buffer
	if err := printAssessItems(output.New(&buf, false), items); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Alpha", "Ghostty", "keep 85%", "Edits text.", "claude-x, saved", "Signed by its developer.", "error", "boom", "brew cask ghostty", "Developer ID Application: X (TEAM)", "Notarized Developer ID", "https://x.example/a.dmg", "could not run: spctl", "app store", "keep 57%", "permissions: Camera", "(no bundle id)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunAssessNeedsArgOrAll(t *testing.T) {
	t.Setenv("ROOTOUT_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	err := runAssess(context.Background(), &globalFlags{}, assessFlags{noTUI: true}, "")
	if !isExit(err, exitBadInput) || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("err = %v, want exit 2 naming --all", err)
	}
	err = runAssess(context.Background(), &globalFlags{json: true}, assessFlags{pickModel: true, all: true}, "")
	if !isExit(err, exitBadInput) || !strings.Contains(err.Error(), "--pick-model") {
		t.Fatalf("--pick-model without a terminal: err = %v, want exit 2 naming --pick-model", err)
	}
}

func TestAssessFlash(t *testing.T) {
	tests := []struct {
		done, skipped int
		failed        []string
		want          string
		wantErr       bool
	}{
		{1, 0, nil, "Removed 1 of 1 apps", false},
		{2, 1, nil, "Removed 2 of 3 apps · 1 skipped", false},
		{0, 2, nil, "Removed 0 of 2 apps · 2 skipped", false},
		{1, 0, []string{"Beta"}, "Removed 1 of 2 apps · 1 failed (Beta)", true},
	}
	for _, tt := range tests {
		f := assessFlash(tt.done, tt.skipped, tt.failed)
		if f.Text != tt.want || f.Err != tt.wantErr {
			t.Errorf("assessFlash(%d,%d,%v) = %+v, want %q err=%v", tt.done, tt.skipped, tt.failed, f, tt.want, tt.wantErr)
		}
	}
}
