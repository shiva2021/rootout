package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/shiva2021/purge/internal/config"
	"github.com/shiva2021/purge/internal/fsx"
	"github.com/shiva2021/purge/internal/keychain"
	"github.com/shiva2021/purge/internal/llm/ollama"
	"github.com/shiva2021/purge/internal/output"
	"github.com/shiva2021/purge/internal/run"
	"github.com/shiva2021/purge/internal/tui"
)

// hostedProviders need an API key; ollama does not.
var hostedProviders = []string{"anthropic", "openai"}

func loadConfig() (config.Config, string, error) {
	env, err := fsx.Real()
	if err != nil {
		return config.Config{}, "", err
	}
	path := config.Path(env, os.Getenv)
	c, err := config.Load(path)
	return c, path, err
}

func newConfigCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage AI providers, models, API keys and defaults",
		Long: `Settings live in ~/.config/purge/config.toml (override with PURGE_CONFIG).
API keys are stored in the macOS Keychain, never in the file; the
ANTHROPIC_API_KEY and OPENAI_API_KEY environment variables override them.`,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "show", Short: "Show settings and which API keys are configured", Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error { return configShow(g) },
		},
		&cobra.Command{
			Use: "get <key>", Short: "Print one setting", Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, a []string) error {
				c, _, err := loadConfig()
				if err != nil {
					return err
				}
				v, err := c.Get(a[0])
				if err != nil {
					return err
				}
				fmt.Println(v)
				return nil
			},
		},
		&cobra.Command{
			Use: "set <key> <value>", Short: "Change one setting (keys: " + strings.Join(config.Keys(), ", ") + ")", Args: cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, a []string) error {
				c, path, err := loadConfig()
				if err != nil {
					return err
				}
				if err := c.Set(a[0], a[1]); err != nil {
					return err
				}
				return config.Save(path, c)
			},
		},
		&cobra.Command{
			Use: "set-key <anthropic|openai>", Short: "Store an API key in the macOS Keychain", Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, a []string) error { return configSetKey(a[0]) },
		},
		&cobra.Command{
			Use: "delete-key <anthropic|openai>", Short: "Remove a stored API key", Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, a []string) error {
				if err := checkHosted(a[0]); err != nil {
					return err
				}
				return keychain.New().Delete(a[0])
			},
		},
		newModelsCmd(g),
		&cobra.Command{
			Use: "pull <model>", Short: "Download a local Ollama model and make it the default", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, a []string) error { return configPull(cmd.Context(), a[0]) },
		},
	)
	return cmd
}

func checkHosted(p string) error {
	for _, h := range hostedProviders {
		if p == h {
			return nil
		}
	}
	return &exitErr{code: exitBadInput, msg: fmt.Sprintf("%q takes no API key; providers with keys: %s", p, strings.Join(hostedProviders, ", "))}
}

func configShow(g *globalFlags) error {
	c, path, err := loadConfig()
	if err != nil {
		return err
	}
	type keyStatus struct {
		Provider string `json:"provider"`
		Source   string `json:"source"` // env | keychain | none
		Masked   string `json:"masked,omitempty"`
	}
	var keys []keyStatus
	store := keychain.New()
	for _, p := range hostedProviders {
		k, src, err := keychain.Resolve(store, p, os.Getenv)
		switch {
		case err == nil:
			keys = append(keys, keyStatus{p, src, keychain.Mask(k)})
		case errors.Is(err, keychain.ErrNotFound):
			keys = append(keys, keyStatus{Provider: p, Source: "none"})
		default:
			return err
		}
	}
	w := output.New(os.Stdout, g.json)
	if g.json {
		return w.Emit(map[string]any{"path": path, "config": c, "keys": keys})
	}
	fmt.Printf("config file: %s\n\n", path)
	for _, k := range config.Keys() {
		v, _ := c.Get(k)
		fmt.Printf("  %-22s %s\n", k, v)
	}
	fmt.Println("\nAPI keys:")
	for _, k := range keys {
		if k.Source == "none" {
			fmt.Printf("  %-10s not set (purge config set-key %s)\n", k.Provider, k.Provider)
		} else {
			fmt.Printf("  %-10s %s (from %s)\n", k.Provider, k.Masked, k.Source)
		}
	}
	return nil
}

func configSetKey(provider string) error {
	if err := checkHosted(provider); err != nil {
		return err
	}
	var key string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "Paste your %s API key (input hidden): ", provider)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		key = string(b)
	} else {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			return err
		}
		key = string(b)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return &exitErr{code: exitBadInput, msg: "no key entered"}
	}
	if err := keychain.New().Set(provider, key); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "stored %s key %s in the Keychain\n", provider, keychain.Mask(key))
	return nil
}

func newModelsCmd(g *globalFlags) *cobra.Command {
	var pick bool
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List installed Ollama models and the ones recommended for this Mac",
		Long:  "Without --pick, prints installed and recommended models. With --pick (or on a terminal with no model configured), opens the picker, which can install Ollama and download a model.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if pick {
				_, err := pickAndSaveModel(cmd.Context())
				return err
			}
			return listModels(cmd.Context(), g)
		},
	}
	cmd.Flags().BoolVar(&pick, "pick", false, "open the interactive picker")
	return cmd
}

func ollamaClient(c config.Config) *ollama.Client {
	url := c.LLM.Ollama.BaseURL
	if url == "" {
		url = ollama.DefaultURL
	}
	return ollama.New(url)
}

func listModels(ctx context.Context, g *globalFlags) error {
	c, _, err := loadConfig()
	if err != nil {
		return err
	}
	hw, err := ollama.Probe(ctx, run.Default)
	if err != nil {
		return err
	}
	cl := ollamaClient(c)
	version, derr := cl.Detect(ctx)
	var installed []ollama.Installed
	if derr == nil {
		if installed, err = cl.ListInstalled(ctx, hw); err != nil {
			return err
		}
	}
	recs := ollama.Recommend(hw, installed)
	w := output.New(os.Stdout, g.json)
	if g.json {
		status := "running"
		switch {
		case errors.Is(derr, ollama.ErrNotInstalled):
			status = "not installed"
		case errors.Is(derr, ollama.ErrNotRunning):
			status = "not running"
		case derr != nil:
			status = derr.Error()
		}
		return w.Emit(map[string]any{"ollama": status, "version": version, "hardware": hw, "current": c.LLM.Ollama.Model, "installed": installed, "recommended": recs})
	}
	fmt.Printf("This Mac: %s, %d GB RAM\n", hw.Chip, hw.RAMBytes>>30)
	switch {
	case errors.Is(derr, ollama.ErrNotInstalled):
		fmt.Println("\nOllama is not installed. Install it with one of:")
		for _, r := range ollama.InstallGuide(hasBrew()) {
			fmt.Printf("  %s: %s\n", r.Label, r.Command)
		}
		fmt.Println("or run `purge config models --pick` to be guided through it.")
	case errors.Is(derr, ollama.ErrNotRunning):
		fmt.Println("\nOllama is installed but not running. `purge config models --pick` can start it.")
	case derr != nil:
		return derr
	default:
		fmt.Printf("\nOllama %s — installed models:\n", version)
		if len(installed) == 0 {
			fmt.Println("  (none)")
		}
		for _, m := range installed {
			cur := ""
			if m.Name == c.LLM.Ollama.Model {
				cur = "  (current)"
			}
			fit := "fits"
			if !m.FitsRAM {
				fit = "too large for this Mac"
			}
			fmt.Printf("  %-26s %-6s %-8s %8s  %s%s\n", m.Name, m.ParameterSize, m.Quantization, output.Bytes(m.SizeBytes), fit, cur)
		}
	}
	fmt.Println("\nRecommended for this Mac:")
	for _, r := range recs {
		state := r.Fit
		if r.Installed {
			state += ", installed"
		}
		fmt.Printf("  %-26s %8s  %-11s %s\n", r.Name, output.Bytes(r.DownloadBytes), state, r.Why)
	}
	fmt.Println("\nDownload one with: purge config pull <model>")
	return nil
}

func hasBrew() bool {
	_, err := run.Default(context.Background(), "/usr/bin/which", "brew")
	return err == nil
}

// pickAndSaveModel runs the picker, then makes the chosen model the default
// and Ollama the provider.
func pickAndSaveModel(ctx context.Context) (string, error) {
	c, path, err := loadConfig()
	if err != nil {
		return "", err
	}
	hw, err := ollama.Probe(ctx, run.Default)
	if err != nil {
		return "", err
	}
	brew := hasBrew()
	deps := tui.ModelDeps{Client: ollamaClient(c), Hardware: hw, HasBrew: brew}
	if brew {
		deps.RunInstall = func(ctx context.Context) error {
			if out, err := run.Default(ctx, "brew", "install", "ollama"); err != nil {
				return fmt.Errorf("brew install ollama: %w: %s", err, strings.TrimSpace(string(out)))
			}
			if out, err := run.Default(ctx, "brew", "services", "start", "ollama"); err != nil {
				return fmt.Errorf("brew services start ollama: %w: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	model, ok, err := tui.PickModel(ctx, deps, c.LLM.Ollama.Model)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", &exitErr{code: exitDeclined, msg: "no model chosen"}
	}
	c.LLM.Provider = "ollama"
	c.LLM.Ollama.Model = model
	if err := config.Save(path, c); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "using Ollama model %s for assessments\n", model)
	return model, nil
}

func configPull(ctx context.Context, model string) error {
	c, path, err := loadConfig()
	if err != nil {
		return err
	}
	cl := ollamaClient(c)
	if _, err := cl.Detect(ctx); err != nil {
		return fmt.Errorf("%w (run `purge config models --pick` to install or start it)", err)
	}
	last := ""
	err = cl.Pull(ctx, model, func(status string, done, total int64) {
		line := status
		if total > 0 {
			line = fmt.Sprintf("%s %3d%% of %s", status, done*100/total, output.Bytes(total))
		}
		if line != last {
			fmt.Fprintf(os.Stderr, "\r\033[K%s", line)
			last = line
		}
	})
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return err
	}
	c.LLM.Provider = "ollama"
	c.LLM.Ollama.Model = model
	if err := config.Save(path, c); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "pulled %s; it is now the default model\n", model)
	return nil
}
