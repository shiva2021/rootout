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

// ListModels returns the models a hosted provider's key can use ("anthropic"
// | "openai"), newest first. It is the same single request Verify makes.
// Ollama lists its models through its own client and is an error here.
func ListModels(ctx context.Context, provider, key string) ([]llm.ModelInfo, error) {
	switch provider {
	case "anthropic":
		return anthropic.ListModels(ctx, key)
	case "openai":
		return openai.ListModels(ctx, key)
	}
	return nil, fmt.Errorf("llm: %q has no hosted model list (want anthropic or openai)", provider)
}
