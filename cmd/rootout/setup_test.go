package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/keychain"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/tui"
)

// mapStore is an in-memory keychain.Store.
type mapStore struct {
	keys map[string]string
	err  error // returned by every call when set
}

func (m *mapStore) Get(p string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	k, ok := m.keys[p]
	if !ok {
		return "", keychain.ErrNotFound
	}
	return k, nil
}

func (m *mapStore) Set(p, k string) error {
	if m.keys == nil {
		m.keys = map[string]string{}
	}
	m.keys[p] = k
	return nil
}

func (m *mapStore) Delete(p string) error { delete(m.keys, p); return nil }

func noEnv(string) string { return "" }

func tempConfig(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "rootout", "config.toml")
}

func TestNeedsSetup(t *testing.T) {
	path := tempConfig(t)
	if !needsSetup(path) {
		t.Fatal("missing file: want true")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if !needsSetup(path) {
		t.Fatal("dir only (firstRunHint creates it): want true")
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if needsSetup(path) {
		t.Fatal("empty file: want false")
	}
}

func TestApplySetupSkipFirstRunCreatesEmptyFile(t *testing.T) {
	path := tempConfig(t)
	store := &mapStore{}
	summary, err := applySetup(tui.SetupResult{Skipped: true}, store, path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "not set up") || !strings.Contains(summary, "rootout setup") {
		t.Errorf("summary = %q", summary)
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) != 0 {
		t.Fatalf("file: %v, %d bytes; want an empty file", err, len(b))
	}
	c, err := config.Load(path)
	if err != nil || !reflect.DeepEqual(c, config.Default()) {
		t.Errorf("Load = %+v, %v; want Default()", c, err)
	}
	if len(store.keys) != 0 {
		t.Errorf("store touched: %v", store.keys)
	}
}

func TestApplySetupSkipRerunLeavesFile(t *testing.T) {
	path := tempConfig(t)
	c := config.Default()
	c.LLM.Provider = "openai"
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, err := applySetup(tui.SetupResult{Skipped: true}, &mapStore{}, path); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("file changed on a skipped re-run:\n%s\n---\n%s", before, after)
	}
}

func TestApplySetupOllama(t *testing.T) {
	path := tempConfig(t)
	summary, err := applySetup(tui.SetupResult{Provider: "ollama", Model: "qwen3.5:4b"}, &mapStore{}, path)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "AI: Ollama · qwen3.5:4b" {
		t.Errorf("summary = %q", summary)
	}
	c, err := config.Load(path)
	if err != nil || c.LLM.Provider != "ollama" || c.LLM.Ollama.Model != "qwen3.5:4b" {
		t.Errorf("config = %+v, %v", c.LLM, err)
	}
}

func TestApplySetupHosted(t *testing.T) {
	tests := []struct {
		name     string
		res      tui.SetupResult
		stored   map[string]string
		wantKeys map[string]string
		wantSum  string
	}{
		{"claude verified", tui.SetupResult{Provider: "anthropic", Key: "sk-ant-new", KeyVerified: true}, nil,
			map[string]string{"anthropic": "sk-ant-new"}, "AI: Claude · claude-sonnet-5-5"},
		{"openai saved anyway", tui.SetupResult{Provider: "openai", Key: "sk-bad", Warning: "openai: the API key was rejected (401)"}, nil,
			map[string]string{"openai": "sk-bad"}, "AI: OpenAI · gpt-5.2 · key not verified: openai: the API key was rejected (401)"},
		{"keep stored key", tui.SetupResult{Provider: "anthropic", KeyVerified: true}, map[string]string{"anthropic": "sk-ant-old"},
			map[string]string{"anthropic": "sk-ant-old"}, "AI: Claude · claude-sonnet-5-5"},
		{"claude with model", tui.SetupResult{Provider: "anthropic", Key: "sk-ant-new", KeyVerified: true, Model: "claude-opus-5-5"}, nil,
			map[string]string{"anthropic": "sk-ant-new"}, "AI: Claude · claude-opus-5-5"},
		{"openai with model", tui.SetupResult{Provider: "openai", KeyVerified: true, Model: "o4-mini"}, map[string]string{"openai": "sk-old"},
			map[string]string{"openai": "sk-old"}, "AI: OpenAI · o4-mini"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tempConfig(t)
			store := &mapStore{keys: tt.stored}
			summary, err := applySetup(tt.res, store, path)
			if err != nil {
				t.Fatal(err)
			}
			if summary != tt.wantSum {
				t.Errorf("summary = %q, want %q", summary, tt.wantSum)
			}
			if !reflect.DeepEqual(store.keys, tt.wantKeys) {
				t.Errorf("store = %v, want %v", store.keys, tt.wantKeys)
			}
			c, err := config.Load(path)
			if err != nil || c.LLM.Provider != tt.res.Provider {
				t.Errorf("provider = %q, %v; want %q", c.LLM.Provider, err, tt.res.Provider)
			}
			if tt.res.Model != "" && configuredModel(c) != tt.res.Model {
				t.Errorf("configured model = %q, want %q", configuredModel(c), tt.res.Model)
			}
			def := config.Default()
			if tt.res.Model == "" && (c.LLM.Anthropic.Model != def.LLM.Anthropic.Model || c.LLM.OpenAI.Model != def.LLM.OpenAI.Model) {
				t.Errorf("no model chosen: defaults should be kept, got %+v", c.LLM)
			}
			if tt.res.Provider == "anthropic" && c.LLM.OpenAI.Model != def.LLM.OpenAI.Model {
				t.Errorf("openai model changed: %+v", c.LLM)
			}
			if tt.res.Provider == "openai" && c.LLM.Anthropic.Model != def.LLM.Anthropic.Model {
				t.Errorf("anthropic model changed: %+v", c.LLM)
			}
		})
	}
}

func TestKeyVerifierResolvesStoredKey(t *testing.T) {
	var got [][2]string
	models := []llm.ModelInfo{{ID: "claude-x"}}
	list := func(_ context.Context, provider, key string) ([]llm.ModelInfo, error) {
		got = append(got, [2]string{provider, key})
		return models, nil
	}
	store := &mapStore{keys: map[string]string{"anthropic": "sk-ant-stored"}}
	env := func(name string) string {
		if name == "OPENAI_API_KEY" {
			return "sk-env"
		}
		return ""
	}
	v := keyVerifier(store, env, list)
	ctx := context.Background()
	if ms, err := v(ctx, "anthropic", "sk-typed"); err != nil || !reflect.DeepEqual(ms, models) {
		t.Fatalf("models = %v, %v", ms, err)
	}
	if _, err := v(ctx, "anthropic", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := v(ctx, "openai", ""); err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"anthropic", "sk-typed"}, {"anthropic", "sk-ant-stored"}, {"openai", "sk-env"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("verify calls = %v, want %v", got, want)
	}
	if _, err := v(ctx, "openai", ""); err != nil {
		t.Fatal(err)
	}
	store.keys = nil
	if _, err := v(ctx, "anthropic", ""); err == nil || !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("no stored key: err = %v, want ErrNotFound", err)
	}
}

func TestExistingKey(t *testing.T) {
	store := &mapStore{keys: map[string]string{"anthropic": "sk-ant-api03-abcdefghijWXYZ"}}
	ex := existingKey(store, noEnv)
	masked, source, err := ex("anthropic")
	if err != nil || source != "keychain" || masked != "sk-ant-…WXYZ" {
		t.Errorf("anthropic: %q %q %v", masked, source, err)
	}
	if masked, _, err := ex("openai"); err != nil || masked != "" {
		t.Errorf("openai: %q %v; want none", masked, err)
	}
	broken := &mapStore{err: errors.New("keychain: locked")}
	if _, _, err := existingKey(broken, noEnv)("anthropic"); err == nil {
		t.Error("store error should surface")
	}
}

func TestAIStatus(t *testing.T) {
	withProvider := func(p string) config.Config {
		c := config.Default()
		c.LLM.Provider = p
		return c
	}
	ollamaModel := withProvider("ollama")
	ollamaModel.LLM.Ollama.Model = "qwen3.5:4b"
	tests := []struct {
		name  string
		c     config.Config
		store keychain.Store
		env   func(string) string
		want  string
	}{
		{"none", config.Default(), &mapStore{}, noEnv, `AI: not set up · heuristics only · pick "Set up AI"`},
		{"ollama no model", withProvider("ollama"), &mapStore{}, noEnv, "AI: Ollama · no model chosen"},
		{"ollama", ollamaModel, &mapStore{}, noEnv, "AI: Ollama · qwen3.5:4b"},
		{"claude keychain", withProvider("anthropic"), &mapStore{keys: map[string]string{"anthropic": "k"}}, noEnv, "AI: Claude · claude-sonnet-5-5"},
		{"openai env", withProvider("openai"), &mapStore{}, func(n string) string { return map[string]string{"OPENAI_API_KEY": "k"}[n] }, "AI: OpenAI · gpt-5.2 · key from env"},
		{"claude no key", withProvider("anthropic"), &mapStore{}, noEnv, "AI: Claude · no key (Set up AI)"},
		{"claude keychain error", withProvider("anthropic"), &mapStore{err: errors.New("locked")}, noEnv, "AI: Claude · key unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := aiStatus(tt.c, tt.store, tt.env); got != tt.want {
				t.Errorf("aiStatus = %q, want %q", got, tt.want)
			}
		})
	}
}
