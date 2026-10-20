package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/shiva2021/purge/internal/heur"
	"github.com/shiva2021/purge/internal/llm"
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
		if _, err := p.Assess(context.Background(), heur.Report{}); !errors.Is(err, llm.ErrNoKey) {
			t.Errorf("%s: err = %v, want ErrNoKey", name, err)
		}
	}
}
