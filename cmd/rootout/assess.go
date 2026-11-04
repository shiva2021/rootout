package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/assesscache"
	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/keychain"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/llm/providers"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/run"
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
		fmt.Fprintln(os.Stderr, "rootout: no AI provider configured; run `rootout setup` or press m in the scan screen (continuing without AI)")
		return nil, "", nil
	case "ollama":
		if model == "" {
			model = c.LLM.Ollama.Model
		}
		if model == "" {
			if !interactive {
				fmt.Fprintln(os.Stderr, "rootout: no Ollama model selected; run `rootout config models --pick` (continuing without AI)")
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
			fmt.Fprintf(os.Stderr, "rootout: no %s API key; run `rootout setup` (continuing without AI)\n", provider)
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
	if c.Unreadable() {
		fmt.Fprintf(os.Stderr, "rootout: %s is not a rootout assessments file; it will be replaced by the next answer\n", c.Path())
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
		a, err := llm.Assess(ctx, p, r)
		if err != nil {
			return a, err
		}
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

// assessAppFunc is assessFunc for apps: the saved answer first (unless
// force), else the model under the per-call timeout, every answer stored.
func assessAppFunc(p llm.Provider, cache *assesscache.Cache, force bool) tui.AppAssessFunc {
	if p == nil {
		return nil
	}
	return func(ctx context.Context, f app.Facts) (llm.AppAssessment, error) {
		id := assesscache.IDForApp(f, p.Name(), p.Model())
		if cache != nil && !force {
			if a, ok := cache.LookupApp(id); ok {
				return a, nil
			}
		}
		ctx, cancel := context.WithTimeout(ctx, assessTimeout)
		defer cancel()
		a, err := llm.AssessApp(ctx, p, f)
		if err != nil {
			return a, err
		}
		if cache != nil {
			if err := cache.StoreApp(id, a); err != nil {
				fmt.Fprintln(os.Stderr, "rootout: could not save the assessment:", err)
			}
		}
		return a, nil
	}
}

// cachedAppFunc is the apps screen's Cached: a saved answer for the facts
// under p's name and model; nil when there is no cache.
func cachedAppFunc(p llm.Provider, cache *assesscache.Cache) func(app.Facts) (llm.AppAssessment, bool) {
	if p == nil || cache == nil {
		return nil
	}
	return func(f app.Facts) (llm.AppAssessment, bool) {
		return cache.LookupApp(assesscache.IDForApp(f, p.Name(), p.Model()))
	}
}

// withAppCache fills an AppAssessor's model-calling fields for p.
func withAppCache(a tui.AppAssessor, p llm.Provider, cache *assesscache.Cache) tui.AppAssessor {
	a.Assess = assessAppFunc(p, cache, false)
	a.Reassess = assessAppFunc(p, cache, true)
	a.Cached = cachedAppFunc(p, cache)
	return a
}

// inspectFunc gathers an app's facts with the real commands. The TUI never
// imports run; it gets this closure.
func inspectFunc(env fsx.Env) func(context.Context, app.App) app.Facts {
	return func(ctx context.Context, a app.App) app.Facts {
		return app.Inspect(ctx, env, a, run.Default)
	}
}

// assessorCore is what both assessor builders share: the resolved provider
// and key, one cache for the run (the saved answers are one file whatever
// the hosted provider, and two instances would overwrite each other), the
// local model deps when interactive, and the model swap.
type assessorCore struct {
	p         llm.Provider
	key       string
	ollamaURL string
	cacheFor  func(provider string) *assesscache.Cache
	models    *tui.ModelDeps
}

func newAssessorCore(ctx context.Context, c config.Config, provider, model string, interactive, noCache bool) (assessorCore, error) {
	p, key, err := buildProvider(ctx, c, provider, model, interactive)
	if err != nil {
		return assessorCore{}, err
	}
	core := assessorCore{p: p, key: key, ollamaURL: c.LLM.Ollama.BaseURL}
	var cache *assesscache.Cache
	core.cacheFor = func(provider string) *assesscache.Cache {
		if cache == nil {
			cache = openCache(provider, noCache)
		}
		if provider != "anthropic" && provider != "openai" {
			return nil
		}
		return cache
	}
	if interactive {
		deps, err := modelDeps(ctx, c)
		if err == nil {
			core.models = &deps
		} else {
			fmt.Fprintln(os.Stderr, "rootout: local model picking unavailable:", err)
		}
	}
	return core, nil
}

// swap saves the user's model choice and returns a provider for it.
func (core assessorCore) swap(provider, model string) (llm.Provider, error) {
	if err := saveModel(provider, model); err != nil {
		return nil, err
	}
	return providers.New(provider, model, core.key, core.ollamaURL)
}

// buildAssessor is what the scan screen gets: the current provider (if any),
// its label, and the hooks that let the user pick a model in place: the
// hosted provider's list when a key is at hand, the Ollama picker otherwise.
func buildAssessor(ctx context.Context, c config.Config, provider, model string, interactive, noCache bool) (tui.Assessor, error) {
	core, err := newAssessorCore(ctx, c, provider, model, interactive, noCache)
	if err != nil {
		return tui.Assessor{}, err
	}
	var a tui.Assessor
	if core.p != nil {
		a.Provider, a.Label = core.p.Name(), core.p.Name()+" · "+core.p.Model()
		a = withCache(a, core.p, core.cacheFor(core.p.Name()))
	}
	if !interactive {
		return a, nil
	}
	a.HostedModels = hostedLister(a.Provider, core.key)
	a.Models = core.models
	a.OnModel = func(_ context.Context, provider, m string) (tui.Assessor, error) {
		np, err := core.swap(provider, m)
		if err != nil {
			return tui.Assessor{}, err
		}
		return withCache(tui.Assessor{
			Provider:     provider,
			Label:        provider + " · " + m,
			Models:       core.models,
			HostedModels: hostedLister(provider, core.key),
		}, np, core.cacheFor(provider)), nil
	}
	return a, nil
}

// buildAppAssessor is buildAssessor for the apps screen and the picker.
// Inspect is always set, so facts show even without a model.
func buildAppAssessor(ctx context.Context, env fsx.Env, c config.Config, provider, model string, interactive, noCache bool) (tui.AppAssessor, error) {
	core, err := newAssessorCore(ctx, c, provider, model, interactive, noCache)
	if err != nil {
		return tui.AppAssessor{}, err
	}
	a := tui.AppAssessor{Inspect: inspectFunc(env)}
	if core.p != nil {
		a.Provider, a.Label = core.p.Name(), core.p.Name()+" · "+core.p.Model()
		a = withAppCache(a, core.p, core.cacheFor(core.p.Name()))
	}
	if !interactive {
		return a, nil
	}
	a.HostedModels = hostedLister(a.Provider, core.key)
	a.Models = core.models
	a.OnModel = func(_ context.Context, provider, m string) (tui.AppAssessor, error) {
		np, err := core.swap(provider, m)
		if err != nil {
			return tui.AppAssessor{}, err
		}
		return withAppCache(tui.AppAssessor{
			Provider:     provider,
			Label:        provider + " · " + m,
			Inspect:      a.Inspect,
			Models:       core.models,
			HostedModels: hostedLister(provider, core.key),
		}, np, core.cacheFor(provider)), nil
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
