package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/assesscache"
	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/testutil"
	"github.com/shiva2021/rootout/internal/tui"
)

func TestSaveModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("ROOTOUT_CONFIG", path)
	def := config.Default()

	if err := saveModel("anthropic", "claude-opus-5-5"); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil || c.LLM.Provider != "anthropic" || c.LLM.Anthropic.Model != "claude-opus-5-5" || c.LLM.OpenAI.Model != def.LLM.OpenAI.Model {
		t.Fatalf("after anthropic: %+v, %v", c.LLM, err)
	}

	if err := saveModel("openai", "o4-mini"); err != nil {
		t.Fatal(err)
	}
	c, _ = config.Load(path)
	if c.LLM.Provider != "openai" || c.LLM.OpenAI.Model != "o4-mini" || c.LLM.Anthropic.Model != "claude-opus-5-5" {
		t.Fatalf("after openai: %+v", c.LLM)
	}

	if err := saveModel("ollama", "qwen3.5:4b"); err != nil {
		t.Fatal(err)
	}
	c, _ = config.Load(path)
	if c.LLM.Provider != "ollama" || c.LLM.Ollama.Model != "qwen3.5:4b" {
		t.Fatalf("after ollama: %+v", c.LLM)
	}

	if err := saveModel("gemini", "x"); err == nil {
		t.Fatal("unknown provider: want error")
	}
}

func TestSaveModelIsSilent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("ROOTOUT_CONFIG", path)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	saveErr := saveModel("anthropic", "claude-opus-5-5")
	os.Stderr = old
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	if len(out) != 0 {
		t.Fatalf("saveModel wrote to stderr (it runs under the scan screen): %q", out)
	}
}

func cacheFixture(t *testing.T) (*assesscache.Cache, heur.Report) {
	t.Helper()
	c, err := assesscache.Open(filepath.Join(t.TempDir(), "assessments.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := heur.Report{Ext: ext.Extension{Editor: "vscode", ID: "evil.stealer", Version: "1.0.0"}, Score: 85}
	return c, r
}

func TestAssessFuncCachesAnswers(t *testing.T) {
	cache, r := cacheFixture(t)
	p := &llm.Fake{Result: llm.Assessment{Verdict: llm.Benign, Confidence: 0.8, Reasoning: "fine"}}
	f := assessFunc(p, cache, false)
	first, err := f(context.Background(), r)
	if err != nil || first.Cached || first.Verdict != llm.Benign {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := f(context.Background(), r)
	if err != nil || !second.Cached || second.Reasoning != "fine" || second.Provider != "fake" {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if len(p.Calls) != 1 {
		t.Fatalf("model called %d times, want 1", len(p.Calls))
	}
	if _, ok := cache.Lookup(assesscache.IDFor(r, "fake", "fake")); !ok {
		t.Fatal("answer not stored under the provider's name and model")
	}
	// A failed call stores nothing.
	p2 := &llm.Fake{Err: errors.New("boom")}
	cache2, _ := cacheFixture(t)
	if _, err := assessFunc(p2, cache2, false)(context.Background(), r); err == nil || cache2.Len() != 0 {
		t.Fatalf("error: %v, stored %d", err, cache2.Len())
	}
}

func TestAssessFuncForceBypassesLookupButStores(t *testing.T) {
	cache, r := cacheFixture(t)
	p := &llm.Fake{Result: llm.Assessment{Verdict: llm.Malicious, Confidence: 0.9}}
	f := assessFunc(p, cache, true)
	for range 2 {
		if a, err := f(context.Background(), r); err != nil || a.Cached {
			t.Fatalf("forced call = %+v, %v", a, err)
		}
	}
	if len(p.Calls) != 2 || cache.Len() != 1 {
		t.Fatalf("calls %d, stored %d; want 2 and 1", len(p.Calls), cache.Len())
	}
}

func TestAssessFuncWithoutCache(t *testing.T) {
	_, r := cacheFixture(t)
	p := &llm.Fake{Result: llm.Assessment{Verdict: llm.Benign, Confidence: 0.8}}
	f := assessFunc(p, nil, false)
	_, _ = f(context.Background(), r)
	_, _ = f(context.Background(), r)
	if len(p.Calls) != 2 {
		t.Fatalf("calls %d, want 2 (no cache)", len(p.Calls))
	}
}

func TestCachedFunc(t *testing.T) {
	cache, r := cacheFixture(t)
	p := &llm.Fake{}
	if _, ok := cachedFunc(p, cache)(r); ok {
		t.Fatal("empty cache should miss")
	}
	_ = cache.Store(assesscache.IDFor(r, "fake", "fake"), llm.Assessment{Verdict: llm.Benign})
	if a, ok := cachedFunc(p, cache)(r); !ok || !a.Cached {
		t.Fatalf("hit = %+v, %v", a, ok)
	}
	if cachedFunc(p, nil) != nil {
		t.Fatal("no cache: want nil func")
	}
}

func TestOpenCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "assessments.json")
	t.Setenv(assesscache.EnvVar, path)
	if c := openCache("ollama", false); c != nil {
		t.Error("ollama must never use the cache")
	}
	if c := openCache("anthropic", true); c != nil {
		t.Error("--no-cache must disable the cache")
	}
	if c := openCache("", false); c != nil {
		t.Error("no provider: no cache")
	}
	c := openCache("anthropic", false)
	if c == nil || c.Path() != path {
		t.Fatalf("openCache anthropic = %v", c)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("opening must not create the file")
	}
}

func appFixture() app.Facts {
	return app.Facts{App: app.App{Name: "Foo", BundleID: "com.example.foo", Version: "1.2", Path: "/Applications/Foo.app"},
		Signature: app.Signature{Signed: true, TeamID: "ABCDE12345"}}
}

func TestAssessAppFuncCachesAnswers(t *testing.T) {
	cache, _ := cacheFixture(t)
	p := &llm.Fake{AppResult: llm.AppAssessment{Purpose: "Edits text.", Source: llm.SourceLegitimate, Confidence: 0.8, Recommendation: llm.Keep}}
	f := assessAppFunc(p, cache, false)
	first, err := f(context.Background(), appFixture())
	if err != nil || first.Cached || first.Recommendation != llm.Keep || first.Provider != "fake" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := f(context.Background(), appFixture())
	if err != nil || !second.Cached || second.Purpose != "Edits text." {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if len(p.Calls) != 1 {
		t.Fatalf("model called %d times, want 1", len(p.Calls))
	}
	if _, ok := cache.LookupApp(assesscache.IDForApp(appFixture(), "fake", "fake")); !ok {
		t.Fatal("answer not stored under the provider's name and model")
	}
	p2 := &llm.Fake{Err: errors.New("boom")}
	cache2, _ := cacheFixture(t)
	if _, err := assessAppFunc(p2, cache2, false)(context.Background(), appFixture()); err == nil || cache2.Len() != 0 {
		t.Fatalf("error: %v, stored %d", err, cache2.Len())
	}
}

func TestAssessAppFuncForceBypassesLookupButStores(t *testing.T) {
	cache, _ := cacheFixture(t)
	p := &llm.Fake{AppResult: llm.AppAssessment{Recommendation: llm.Drop, Confidence: 0.9}}
	f := assessAppFunc(p, cache, true)
	for range 2 {
		if a, err := f(context.Background(), appFixture()); err != nil || a.Cached {
			t.Fatalf("forced call = %+v, %v", a, err)
		}
	}
	if len(p.Calls) != 2 || cache.Len() != 1 {
		t.Fatalf("calls %d, stored %d; want 2 and 1", len(p.Calls), cache.Len())
	}
}

func TestAssessAppFuncWithoutCache(t *testing.T) {
	p := &llm.Fake{AppResult: llm.AppAssessment{Recommendation: llm.Keep}}
	f := assessAppFunc(p, nil, false)
	_, _ = f(context.Background(), appFixture())
	_, _ = f(context.Background(), appFixture())
	if len(p.Calls) != 2 {
		t.Fatalf("calls %d, want 2 (no cache)", len(p.Calls))
	}
	if assessAppFunc(nil, nil, false) != nil {
		t.Fatal("no provider: want nil func")
	}
}

func TestCachedAppFunc(t *testing.T) {
	cache, _ := cacheFixture(t)
	p := &llm.Fake{}
	if _, ok := cachedAppFunc(p, cache)(appFixture()); ok {
		t.Fatal("empty cache should miss")
	}
	_ = cache.StoreApp(assesscache.IDForApp(appFixture(), "fake", "fake"), llm.AppAssessment{Recommendation: llm.Keep})
	if a, ok := cachedAppFunc(p, cache)(appFixture()); !ok || !a.Cached {
		t.Fatalf("hit = %+v, %v", a, ok)
	}
	if cachedAppFunc(p, nil) != nil || cachedAppFunc(nil, cache) != nil {
		t.Fatal("no cache or no provider: want nil func")
	}
}

func TestWithAppCacheFillsEveryHook(t *testing.T) {
	cache, _ := cacheFixture(t)
	p := &llm.Fake{AppResult: llm.AppAssessment{Recommendation: llm.Keep}}
	a := withAppCache(tui.AppAssessor{Provider: "fake", Label: "fake · fake"}, p, cache)
	if a.Assess == nil || a.Reassess == nil || a.Cached == nil || a.Provider != "fake" {
		t.Fatalf("assessor = %+v", a)
	}
	b := withAppCache(tui.AppAssessor{}, p, nil)
	if b.Assess == nil || b.Reassess == nil || b.Cached != nil {
		t.Fatalf("no cache: %+v", b)
	}
}

// TestBuildAppAssessorWithoutProvider: no provider configured means no
// model hooks, but the inspector is still set so facts can be shown.
func TestBuildAppAssessorWithoutProvider(t *testing.T) {
	t.Setenv("ROOTOUT_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	env := testutil.FakeHome(t, testutil.Spec{})
	a, err := buildAppAssessor(context.Background(), env, config.Default(), "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if a.Provider != "" || a.Assess != nil || a.Cached != nil {
		t.Fatalf("assessor = %+v, want no model", a)
	}
	if a.Inspect == nil {
		t.Fatal("Inspect must be set even without a model")
	}
}
