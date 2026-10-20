package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/keychain"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/llm/providers"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/tui"
)

// assessTimeout bounds one model call; local models on small Macs are slow.
const assessTimeout = 3 * time.Minute

// buildProvider resolves provider and model from flags, then config, and
// fetches the API key. It returns (nil, nil) with a printed hint when the
// assessment cannot run, so the scan still completes with heuristics only.
// When provider is ollama with no model and the run is interactive, the
// picker opens (and saves its choice).
func buildProvider(ctx context.Context, c config.Config, provider, model string, interactive bool) (llm.Provider, error) {
	if provider == "" {
		provider = c.LLM.Provider
	}
	switch provider {
	case "":
		fmt.Fprintln(os.Stderr, "rootout: no AI provider configured; run `rootout config set llm.provider <anthropic|openai|ollama>` or press m in the scan screen (continuing with heuristics only)")
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
func assessFunc(p llm.Provider) tui.AssessFunc {
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

// buildAssessor is what the scan screen gets: the current provider (if any),
// its label, and the hooks that let the user pick a local model in place.
func buildAssessor(ctx context.Context, c config.Config, provider, model string, interactive bool) (tui.Assessor, error) {
	p, err := buildProvider(ctx, c, provider, model, interactive)
	if err != nil {
		return tui.Assessor{}, err
	}
	a := tui.Assessor{Assess: assessFunc(p)}
	if p != nil {
		a.Label = p.Name() + " · " + p.Model()
	}
	if interactive {
		deps, err := modelDeps(ctx, c)
		if err == nil {
			a.Models = &deps
		} else {
			fmt.Fprintln(os.Stderr, "rootout: local model picking unavailable:", err)
		}
		a.OnModel = func(ctx context.Context, m string) (tui.Assessor, error) {
			if err := saveOllamaModel(m); err != nil {
				return tui.Assessor{}, err
			}
			np, err := providers.New("ollama", m, "", c.LLM.Ollama.BaseURL)
			if err != nil {
				return tui.Assessor{}, err
			}
			return tui.Assessor{Label: "ollama · " + m, Assess: assessFunc(np), Models: a.Models}, nil
		}
	}
	return a, nil
}

// confirmTyped is the plain-terminal form of the typed confirmation used for
// scripted uninstalls without --yes.
func confirmTyped(in io.Reader, out io.Writer, what string) bool {
	_, _ = fmt.Fprintf(out, "%s\nType %s to continue (anything else cancels): ", what, plan.ConfirmWord)
	line, _ := bufio.NewReader(in).ReadString('\n')
	return plan.IsConfirmWord(line)
}

// yesBanner warns when a destructive command runs without confirmation.
func yesBanner(permanent bool) {
	msg := "WARNING: --yes skips the confirmation."
	if permanent {
		msg = "WARNING: --yes --permanent deletes without confirmation and without the Trash."
	}
	fmt.Fprintf(os.Stderr, "\033[31m%s\033[0m\n", msg)
}
