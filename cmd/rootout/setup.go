package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/keychain"
	"github.com/shiva2021/rootout/internal/llm/providers"
	"github.com/shiva2021/rootout/internal/tui"
)

// verifyTimeout bounds one API key check.
const verifyTimeout = 20 * time.Second

// providerNames are the display names used on the home screen and in summaries.
var providerNames = map[string]string{"anthropic": "Claude", "openai": "OpenAI", "ollama": "Ollama"}

func newSetupCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Choose how rootout gets AI recommendations (optional)",
		Long: `Open the setup screen: pick a free local model through Ollama, paste a
Claude or OpenAI API key, or choose "Not now". Nothing is required: without
a model, scans still score extensions with the built-in rules. With one,
pressing l on an extension in the scan screen asks the model whether to
keep or uninstall it.

A pasted key is checked with one free request (the provider's model list)
before it is stored in the macOS Keychain. If the check fails you can retry,
go back, or save the key anyway.

The same screen opens on the first launch of rootout. Run it again any time
to change the provider. Scripts use "rootout config" instead.`,
		Example: `  rootout setup                      # the setup screen
  rootout config set-key anthropic   # the scripted equivalent
  rootout config models --pick       # just the local model picker`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if g.json {
				return &exitErr{code: exitBadInput, msg: "setup is interactive; use rootout config set-key / config set / config models --pick in scripts"}
			}
			if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
				return &exitErr{code: exitBadInput, msg: "setup needs a terminal; use rootout config set-key <anthropic|openai>, config set llm.provider <name> or config models --pick"}
			}
			f, err := runSetup(cmd.Context())
			if errors.Is(err, tui.ErrQuit) {
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, f.Text)
			return nil
		},
	}
}

// needsSetup reports whether the config file is absent, which is how a Mac
// that has never finished (or skipped) the setup screen is recognised. The
// first-run hint only creates the directory, so that does not count.
func needsSetup(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// runSetup shows the setup screen and persists the choice. It returns the
// summary as a flash for the home screen; ErrQuit passes through.
func runSetup(ctx context.Context) (tui.Flash, error) {
	c, path, err := loadConfig()
	if err != nil {
		return tui.Flash{}, err
	}
	store := keychain.New()
	res, err := tui.RunSetup(ctx, setupDeps(ctx, c, store))
	if err != nil {
		return tui.Flash{}, err
	}
	summary, err := applySetup(res, store, path)
	if err != nil {
		return tui.Flash{}, err
	}
	return tui.Flash{Text: summary, Err: res.Warning != ""}, nil
}

// setupDeps wires the setup screen to Ollama, the key check and the Keychain.
func setupDeps(ctx context.Context, c config.Config, store keychain.Store) tui.SetupDeps {
	d := tui.SetupDeps{
		Verify:   keyVerifier(store, os.Getenv, providers.Verify),
		Existing: existingKey(store, os.Getenv),
		Current:  c.LLM.Ollama.Model,
	}
	if deps, err := modelDeps(ctx, c); err == nil {
		d.Models = &deps
	} else {
		d.ModelsErr = err.Error()
	}
	return d
}

// keyVerifier returns the screen's Verify: an empty key means "keep the
// stored one", resolved from the environment or the Keychain. Each check
// has its own timeout.
func keyVerifier(store keychain.Store, getenv func(string) string, verify func(ctx context.Context, provider, key string) error) func(ctx context.Context, provider, key string) error {
	return func(ctx context.Context, provider, key string) error {
		if key == "" {
			stored, _, err := keychain.Resolve(store, provider, getenv)
			if err != nil {
				return err
			}
			key = stored
		}
		ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
		defer cancel()
		return verify(ctx, provider, key)
	}
}

// existingKey returns the screen's Existing: the stored key masked, with its
// source; "" when none. Keychain failures are returned, not hidden.
func existingKey(store keychain.Store, getenv func(string) string) func(provider string) (masked, source string, err error) {
	return func(provider string) (string, string, error) {
		key, source, err := keychain.Resolve(store, provider, getenv)
		if errors.Is(err, keychain.ErrNotFound) {
			return "", "", nil
		}
		if err != nil {
			return "", "", err
		}
		return keychain.Mask(key), source, nil
	}
}

// applySetup persists res: the key into store, the provider and model into
// the config file at path. A skipped first run creates an empty config file
// so the screen does not open again; a skipped re-run changes nothing.
func applySetup(res tui.SetupResult, store keychain.Store, path string) (string, error) {
	if res.Skipped {
		if needsSetup(path) {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return "", fmt.Errorf("config: mkdir: %w", err)
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				return "", fmt.Errorf("config: create %s: %w", path, err)
			}
		}
		return "AI: not set up · heuristics only (rootout setup any time)", nil
	}
	c, err := config.Load(path)
	if err != nil {
		return "", err
	}
	if res.Key != "" {
		if err := store.Set(res.Provider, res.Key); err != nil {
			return "", err
		}
	}
	c.LLM.Provider = res.Provider
	if res.Provider == "ollama" {
		c.LLM.Ollama.Model = res.Model
	}
	if err := config.Save(path, c); err != nil {
		return "", err
	}
	summary := "AI: " + providerNames[res.Provider] + " · " + configuredModel(c)
	if res.Warning != "" {
		summary += " · key not verified: " + res.Warning
	}
	return summary, nil
}

// configuredModel is the model the configured provider would use.
func configuredModel(c config.Config) string {
	switch c.LLM.Provider {
	case "anthropic":
		return c.LLM.Anthropic.Model
	case "openai":
		return c.LLM.OpenAI.Model
	case "ollama":
		return c.LLM.Ollama.Model
	}
	return ""
}

// aiStatus is the home screen's one-line AI status. It reads the Keychain
// for hosted providers, so callers compute it once per launch and after
// the setup action, not on every loop iteration.
func aiStatus(c config.Config, store keychain.Store, getenv func(string) string) string {
	switch p := c.LLM.Provider; p {
	case "":
		return `AI: not set up · heuristics only · pick "Set up AI"`
	case "ollama":
		if c.LLM.Ollama.Model == "" {
			return "AI: Ollama · no model chosen"
		}
		return "AI: Ollama · " + c.LLM.Ollama.Model
	default:
		s := "AI: " + providerNames[p] + " · " + configuredModel(c)
		_, source, err := keychain.Resolve(store, p, getenv)
		switch {
		case errors.Is(err, keychain.ErrNotFound):
			return "AI: " + providerNames[p] + " · no key (Set up AI)"
		case err != nil:
			return "AI: " + providerNames[p] + " · key unavailable"
		case source == "env":
			return s + " · key from env"
		}
		return s
	}
}

// currentAIStatus loads the config and renders aiStatus; errors become the status text.
func currentAIStatus() string {
	c, _, err := loadConfig()
	if err != nil {
		return "AI: config error: " + err.Error()
	}
	return aiStatus(c, keychain.New(), os.Getenv)
}

// configPath is the config file for this user.
func configPath() (string, error) {
	env, err := fsx.Real()
	if err != nil {
		return "", err
	}
	return config.Path(env, os.Getenv), nil
}
