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
// fetches the API key. It returns (nil, "", nil) with a printed hint when
// the assessment cannot run, so the scan still completes with heuristics
// only. When provider is ollama with no model and the run is interactive,
// the picker opens (and saves its choice). The key is returned alongside
// so the scan screen can list the provider's models; it never reaches a
// struct the TUI keeps.
func buildProvider(ctx context.Context, c config.Config, provider, model string, interactive bool) (llm.Provider, string, error) {
	if provider == "" {
		provider = c.LLM.Provider
	}
	switch provider {
	case "":
		fmt.Fprintln(os.Stderr, "rootout: no AI provider configured; run `rootout setup` or press m in the scan screen (continuing with heuristics only)")
		return nil, "", nil
	case "ollama":
		if model == "" {
			model = c.LLM.Ollama.Model
		}
		if model == "" {
			if !interactive {
				fmt.Fprintln(os.Stderr, "rootout: no Ollama model selected; run `rootout config models --pick` (continuing with heuristics only)")
				return nil, "", nil
			}
			m, err := pickAndSaveModel(ctx)
			if err != nil {
				return nil, "", err
			}
			model = m
		}
		p, err := providers.New("ollama", model, "", c.LLM.Ollama.BaseURL)
		return p, "", err
	case "anthropic", "openai":
		if model == "" {
			model = hostedModel(c, provider)
		}
		key, _, err := keychain.Resolve(keychain.New(), provider, os.Getenv)
		if errors.Is(err, keychain.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "rootout: no %s API key; run `rootout setup` (continuing with heuristics only)\n", provider)
			return nil, "", nil
		}
		if err != nil {
			return nil, "", err
		}
		p, err := providers.New(provider, model, key, "")
		return p, key, err
	default:
		return nil, "", &exitErr{code: exitBadInput, msg: fmt.Sprintf("unknown provider %q (anthropic, openai, ollama)", provider)}
	}
}

// hostedLister lists a hosted provider's models with key; nil when there is
// no key. The closure holds the key, nothing else does.
func hostedLister(provider, key string) func(context.Context) ([]llm.ModelInfo, error) {
	if key == "" {
		return nil
	}
	return func(ctx context.Context) ([]llm.ModelInfo, error) {
		ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
		defer cancel()
		return providers.ListModels(ctx, provider, key)
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
// its label, and the hooks that let the user pick a model in place: the
// hosted provider's list when a key is at hand, the Ollama picker otherwise.
func buildAssessor(ctx context.Context, c config.Config, provider, model string, interactive bool) (tui.Assessor, error) {
	p, key, err := buildProvider(ctx, c, provider, model, interactive)
	if err != nil {
		return tui.Assessor{}, err
	}
	a := tui.Assessor{Assess: assessFunc(p)}
	if p != nil {
		a.Provider, a.Label = p.Name(), p.Name()+" · "+p.Model()
	}
	if !interactive {
		return a, nil
	}
	a.HostedModels = hostedLister(a.Provider, key)
	deps, err := modelDeps(ctx, c)
	if err == nil {
		a.Models = &deps
	} else {
		fmt.Fprintln(os.Stderr, "rootout: local model picking unavailable:", err)
	}
	a.OnModel = func(_ context.Context, provider, m string) (tui.Assessor, error) {
		if err := saveModel(provider, m); err != nil {
			return tui.Assessor{}, err
		}
		np, err := providers.New(provider, m, key, c.LLM.Ollama.BaseURL)
		if err != nil {
			return tui.Assessor{}, err
		}
		return tui.Assessor{
			Provider:     provider,
			Label:        provider + " · " + m,
			Assess:       assessFunc(np),
			Models:       a.Models,
			HostedModels: hostedLister(provider, key),
		}, nil
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
