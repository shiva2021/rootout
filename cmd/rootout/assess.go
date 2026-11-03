package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/shiva2021/rootout/internal/assesscache"
	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/fsx"
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

// openCache returns the saved-assessments cache for a hosted provider, or
// nil when there is nothing to save (Ollama, --no-cache, no provider) or
// the file cannot be read (a warning; the scan goes on without it).
func openCache(provider string, noCache bool) *assesscache.Cache {
	if noCache || (provider != "anthropic" && provider != "openai") {
		return nil
	}
	env, err := fsx.Real()
	if err != nil {
		return nil
	}
	c, err := assesscache.Open(assesscache.Path(env, os.Getenv))
	if err != nil {
		fmt.Fprintln(os.Stderr, "rootout: saved assessments unavailable:", err)
		return nil
	}
	return c
}

// assessFunc wraps a provider with a per-call timeout and, when cache is
// set, answers from it first (unless force) and stores every answer. A
// failed store is a warning; the answer is still returned.
func assessFunc(p llm.Provider, cache *assesscache.Cache, force bool) tui.AssessFunc {
	if p == nil {
		return nil
	}
	return func(ctx context.Context, r heur.Report) (llm.Assessment, error) {
		id := assesscache.IDFor(r, p.Name(), p.Model())
		if cache != nil && !force {
			if a, ok := cache.Lookup(id); ok {
				return a, nil
			}
		}
		ctx, cancel := context.WithTimeout(ctx, assessTimeout)
		defer cancel()
		a, err := p.Assess(ctx, r)
		if err != nil {
			return a, err
		}
		a = llm.Normalize(a, p.Name(), p.Model())
		if cache != nil {
			if err := cache.Store(id, a); err != nil {
				fmt.Fprintln(os.Stderr, "rootout: could not save the assessment:", err)
			}
		}
		return a, nil
	}
}

// cachedFunc is the scan screen's Cached: a saved answer for the report
// under p's name and model; nil when there is no cache.
func cachedFunc(p llm.Provider, cache *assesscache.Cache) func(heur.Report) (llm.Assessment, bool) {
	if p == nil || cache == nil {
		return nil
	}
	return func(r heur.Report) (llm.Assessment, bool) {
		return cache.Lookup(assesscache.IDFor(r, p.Name(), p.Model()))
	}
}

// withCache fills an Assessor's model-calling fields for p.
func withCache(a tui.Assessor, p llm.Provider, cache *assesscache.Cache) tui.Assessor {
	a.Assess = assessFunc(p, cache, false)
	a.Reassess = assessFunc(p, cache, true)
	a.Cached = cachedFunc(p, cache)
	return a
}

// buildAssessor is what the scan screen gets: the current provider (if any),
// its label, and the hooks that let the user pick a model in place: the
// hosted provider's list when a key is at hand, the Ollama picker otherwise.
func buildAssessor(ctx context.Context, c config.Config, provider, model string, interactive, noCache bool) (tui.Assessor, error) {
	p, key, err := buildProvider(ctx, c, provider, model, interactive)
	if err != nil {
		return tui.Assessor{}, err
	}
	// One cache for the run: the saved answers are one file whatever the
	// hosted provider, and two instances would overwrite each other.
	var cache *assesscache.Cache
	cacheFor := func(provider string) *assesscache.Cache {
		if cache == nil {
			cache = openCache(provider, noCache)
		}
		if provider != "anthropic" && provider != "openai" {
			return nil
		}
		return cache
	}
	var a tui.Assessor
	if p != nil {
		a.Provider, a.Label = p.Name(), p.Name()+" · "+p.Model()
		a = withCache(a, p, cacheFor(p.Name()))
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
		return withCache(tui.Assessor{
			Provider:     provider,
			Label:        provider + " · " + m,
			Models:       a.Models,
			HostedModels: hostedLister(provider, key),
		}, np, cacheFor(provider)), nil
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
