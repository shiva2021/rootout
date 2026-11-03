package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shiva2021/rootout/internal/assesscache"
	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
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
