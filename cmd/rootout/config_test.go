package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/assesscache"
	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/keychain"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/llm/ollama"
)

// fakeOllama serves version and delete; it records deleted model bodies.
func fakeOllama(t *testing.T) (*ollama.Client, *[]string) {
	t.Helper()
	var deleted []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"0.12.6"}`)
	})
	mux.HandleFunc("DELETE /api/delete", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "ghost") {
			http.Error(w, `{"error":"model 'ghost:1b' not found"}`, http.StatusNotFound)
			return
		}
		deleted = append(deleted, string(b))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return ollama.New(srv.URL), &deleted
}

func savedConfig(t *testing.T, model string) (config.Config, string) {
	t.Helper()
	c := config.Default()
	c.LLM.Provider = "ollama"
	c.LLM.Ollama.Model = model
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	return c, path
}

func TestRemoveModel(t *testing.T) {
	ctx := context.Background()

	t.Run("clears the default when it matches", func(t *testing.T) {
		cl, deleted := fakeOllama(t)
		c, path := savedConfig(t, "llama3.2:latest")
		var out bytes.Buffer
		if err := removeModel(ctx, cl, c, path, "llama3.2", true, strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}
		if len(*deleted) != 1 || !strings.Contains((*deleted)[0], `"model":"llama3.2"`) {
			t.Fatalf("server saw %v", *deleted)
		}
		got, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.LLM.Ollama.Model != "" || got.LLM.Provider != "ollama" {
			t.Fatalf("config after rm: model %q provider %q", got.LLM.Ollama.Model, got.LLM.Provider)
		}
		if !strings.Contains(out.String(), "uninstalled llama3.2") || !strings.Contains(out.String(), "default") {
			t.Fatalf("output:\n%s", out.String())
		}
	})

	t.Run("keeps a different default", func(t *testing.T) {
		cl, _ := fakeOllama(t)
		c, path := savedConfig(t, "qwen3.5:9b")
		if err := removeModel(ctx, cl, c, path, "llama3.2", true, strings.NewReader(""), io.Discard); err != nil {
			t.Fatal(err)
		}
		got, _ := config.Load(path)
		if got.LLM.Ollama.Model != "qwen3.5:9b" {
			t.Fatalf("default changed to %q", got.LLM.Ollama.Model)
		}
	})

	t.Run("prompts and honours no", func(t *testing.T) {
		cl, deleted := fakeOllama(t)
		c, path := savedConfig(t, "")
		var out bytes.Buffer
		err := removeModel(ctx, cl, c, path, "llama3.2", false, strings.NewReader("n\n"), &out)
		var ee *exitErr
		if !errors.As(err, &ee) || ee.code != exitDeclined {
			t.Fatalf("err = %v, want declined exit", err)
		}
		if len(*deleted) != 0 || !strings.Contains(out.String(), "Uninstall llama3.2") {
			t.Fatalf("deleted %v, output %q", *deleted, out.String())
		}
	})

	t.Run("prompts and honours yes", func(t *testing.T) {
		cl, deleted := fakeOllama(t)
		c, path := savedConfig(t, "")
		if err := removeModel(ctx, cl, c, path, "llama3.2", false, strings.NewReader("y\n"), io.Discard); err != nil {
			t.Fatal(err)
		}
		if len(*deleted) != 1 {
			t.Fatalf("deleted %v", *deleted)
		}
	})

	t.Run("missing model", func(t *testing.T) {
		cl, _ := fakeOllama(t)
		c, path := savedConfig(t, "")
		err := removeModel(ctx, cl, c, path, "ghost:1b", true, strings.NewReader(""), io.Discard)
		if !errors.Is(err, ollama.ErrModelNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPrintHostedModels(t *testing.T) {
	models := []llm.ModelInfo{
		{ID: "claude-new", DisplayName: "Claude New", Created: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "claude-sonnet-5-5", DisplayName: "Claude Sonnet 5.5", Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "claude-old", DisplayName: "Claude Old", Created: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Deprecated: true},
	}
	var out bytes.Buffer
	if err := printHostedModels(&out, false, "anthropic", "claude-sonnet-5-5", models); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"Claude models", "MODEL", "NAME", "RELEASED", "claude-new", "2026-06-01", "claude-sonnet-5-5 (current)", "claude-old", "deprecated", "config set llm.anthropic.model"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}

	out.Reset()
	if err := printHostedModels(&out, true, "openai", "gpt-5.2", models[:1]); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Provider string          `json:"provider"`
		Current  string          `json:"current"`
		Models   []llm.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if got.Provider != "openai" || got.Current != "gpt-5.2" || len(got.Models) != 1 || got.Models[0].ID != "claude-new" {
		t.Errorf("json = %+v", got)
	}
}

func TestResolveHostedKeyEmptyIsNotFound(t *testing.T) {
	store := &mapStore{keys: map[string]string{"anthropic": "  "}}
	if _, err := resolveHostedKey(store, "anthropic", noEnv); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("blank Keychain item: err = %v, want ErrNotFound", err)
	}
	store.keys["anthropic"] = "sk-ant-x"
	if k, err := resolveHostedKey(store, "anthropic", noEnv); err != nil || k != "sk-ant-x" {
		t.Fatalf("key = %q, %v", k, err)
	}
}

func TestCheckModelsProvider(t *testing.T) {
	for _, ok := range []string{"anthropic", "openai"} {
		if err := checkModelsProvider(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"ollama", "gemini", "Anthropic"} {
		err := checkModelsProvider(bad)
		var ee *exitErr
		if !errors.As(err, &ee) || ee.code != exitBadInput || !strings.Contains(ee.msg, "--provider") {
			t.Errorf("%q: err = %v, want exit %d naming --provider", bad, err, exitBadInput)
		}
		if bad == "ollama" && !strings.Contains(ee.msg, "without --provider") {
			t.Errorf("ollama should be pointed at the plain listing: %v", ee.msg)
		}
	}
}

func TestCacheClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assessments.json")
	var out bytes.Buffer
	if err := cacheClear(&out, path); err != nil || !strings.Contains(out.String(), "no saved assessments") {
		t.Fatalf("absent: %v\n%s", err, out.String())
	}
	c, _ := assesscache.Open(path)
	_ = c.Store(assesscache.EntryID{ExtID: "a"}, llm.Assessment{})
	_ = c.Store(assesscache.EntryID{ExtID: "b"}, llm.Assessment{})
	out.Reset()
	if err := cacheClear(&out, path); err != nil || !strings.Contains(out.String(), "removed 2 saved assessments") || !strings.Contains(out.String(), path) {
		t.Fatalf("present: %v\n%s", err, out.String())
	}
}

func TestCacheClearRefusesForeignFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.json")
	if err := os.WriteFile(path, []byte(`{"todo":"keep"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := cacheClear(&out, path)
	var ee *exitErr
	if !errors.As(err, &ee) || ee.code != exitBadInput || !strings.Contains(ee.msg, "not a rootout assessments file") {
		t.Fatalf("err = %v, want exit %d with an explanation", err, exitBadInput)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("foreign file must be left alone")
	}
}
