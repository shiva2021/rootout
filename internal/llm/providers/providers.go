// Package providers picks an llm.Provider by name. It lives outside package
// llm because the adapters import llm, so llm cannot import them.
package providers

import (
	"context"
	"fmt"

	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/llm/anthropic"
	"github.com/shiva2021/rootout/internal/llm/ollamachat"
	"github.com/shiva2021/rootout/internal/llm/openai"
)

// New returns the adapter for provider ("anthropic" | "openai" | "ollama").
// key is ignored for Ollama; ollamaURL is ignored for the hosted providers.
// A missing key or model is reported by Assess, so callers can still show
// which provider is configured.
func New(provider, model, key, ollamaURL string) (llm.Provider, error) {
	switch provider {
	case "anthropic":
		return anthropic.New(key, model), nil
	case "openai":
		return openai.New(key, model), nil
	case "ollama":
		return ollamachat.New(ollamaURL, model, nil), nil
	}
	return nil, fmt.Errorf("llm: unknown provider %q (want anthropic, openai or ollama)", provider)
}

// Verify checks an API key for a hosted provider ("anthropic" | "openai")
// with one free request. Ollama needs no key and is an error here.
func Verify(ctx context.Context, provider, key string) error {
	switch provider {
	case "anthropic":
		return anthropic.Verify(ctx, key)
	case "openai":
		return openai.Verify(ctx, key)
	}
	return fmt.Errorf("llm: %q takes no API key (want anthropic or openai)", provider)
}
