package main

import (
	"path/filepath"
	"testing"

	"github.com/shiva2021/rootout/internal/config"
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
