package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/keychain"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/llm/providers"
)

// assessTimeout bounds one model call; local models on small Macs are slow.
const assessTimeout = 3 * time.Minute

// buildProvider resolves provider and model from flags, then config, and
// fetches the API key. It returns (nil, nil) with a printed hint when the
// assessment cannot run, so the scan still completes with heuristics only.
func buildProvider(ctx context.Context, c config.Config, provider, model string, interactive bool) (llm.Provider, error) {
	if provider == "" {
		provider = c.LLM.Provider
	}
	switch provider {
	case "":
		fmt.Fprintln(os.Stderr, "rootout: no AI provider configured; run `rootout config set llm.provider <anthropic|openai|ollama>` (continuing with heuristics only)")
		return nil, nil
	case "ollama":
		if model == "" {
			model = c.LLM.Ollama.Model
		}
		if model == "" {
			if !interactive {
				fmt.Fprintln(os.Stderr, "rootout: no Ollama model selected; run `rootout config models --pick` (continuing with heuristics only)")
				return nil, nil
			}
			m, err := pickAndSaveModel(ctx)
			if err != nil {
				return nil, err
			}
			model = m
		}
		return providers.New("ollama", model, "", c.LLM.Ollama.BaseURL)
	case "anthropic", "openai":
		if model == "" {
			model = c.LLM.Anthropic.Model
			if provider == "openai" {
				model = c.LLM.OpenAI.Model
			}
		}
		key, _, err := keychain.Resolve(keychain.New(), provider, os.Getenv)
		if errors.Is(err, keychain.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "rootout: no %s API key; run `rootout config set-key %s` (continuing with heuristics only)\n", provider, provider)
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return providers.New(provider, model, key, "")
	default:
		return nil, &exitErr{code: exitBadInput, msg: fmt.Sprintf("unknown provider %q (anthropic, openai, ollama)", provider)}
	}
}

// assessFunc wraps a provider with a per-call timeout.
func assessFunc(p llm.Provider) func(ctx context.Context, r heur.Report) (llm.Assessment, error) {
	if p == nil {
		return nil
	}
	return func(ctx context.Context, r heur.Report) (llm.Assessment, error) {
		ctx, cancel := context.WithTimeout(ctx, assessTimeout)
		defer cancel()
		a, err := p.Assess(ctx, r)
		if err != nil {
			return a, err
		}
		return llm.Normalize(a, p.Name(), p.Model()), nil
	}
}
