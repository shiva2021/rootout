package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/config"
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
