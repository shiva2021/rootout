package providers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
)

func TestNew(t *testing.T) {
	for _, name := range []string{"anthropic", "openai", "ollama"} {
		t.Run(name, func(t *testing.T) {
			p, err := New(name, "m", "k", "http://127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			if p.Name() != name || p.Model() != "m" {
				t.Errorf("got %s/%s", p.Name(), p.Model())
			}
		})
	}
	if _, err := New("gemini", "m", "k", ""); err == nil {
		t.Error("unknown provider: want error")
	}
}

func TestNewNoKey(t *testing.T) {
	for _, name := range []string{"anthropic", "openai"} {
		p, err := New(name, "m", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := llm.Assess(context.Background(), p, heur.Report{}); !errors.Is(err, llm.ErrNoKey) {
			t.Errorf("%s: err = %v, want ErrNoKey", name, err)
		}
	}
}

func TestVerifyUnknownProvider(t *testing.T) {
	for _, name := range []string{"ollama", "gemini", ""} {
		if err := Verify(context.Background(), name, "k"); err == nil {
			t.Errorf("%q: want error", name)
		}
	}
}

func TestVerifyDispatches(t *testing.T) {
	// A hosted provider with an empty key fails before any request is made.
	for _, name := range []string{"anthropic", "openai"} {
		if err := Verify(context.Background(), name, ""); err == nil || !strings.Contains(err.Error(), "API key") {
			t.Errorf("%s: err = %v, want API key error", name, err)
		}
	}
}

func TestListModelsDispatches(t *testing.T) {
	// A hosted provider with an empty key fails before any request is made.
	for _, name := range []string{"anthropic", "openai"} {
		if _, err := ListModels(context.Background(), name, ""); err == nil || !strings.Contains(err.Error(), "API key") {
			t.Errorf("%s: err = %v, want API key error", name, err)
		}
	}
	for _, name := range []string{"ollama", "gemini", ""} {
		if _, err := ListModels(context.Background(), name, "k"); err == nil {
			t.Errorf("%q: want error", name)
		}
	}
}
