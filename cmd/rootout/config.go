package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/keychain"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/llm/ollama"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/run"
	"github.com/shiva2021/rootout/internal/tui"
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
		Long: `Show and change rootout's settings: the AI provider used by "scan --llm",
its model, the Ollama address and the default scan threshold.

Settings live in ~/.config/rootout/config.toml (override the path with
ROOTOUT_CONFIG). API keys are stored in the macOS Keychain, never in the
file; the ANTHROPIC_API_KEY and OPENAI_API_KEY environment variables take
precedence over the Keychain. No setting is needed to remove apps or to scan
with heuristics only.

Valid keys: ` + strings.Join(config.Keys(), ", ") + `

--json applies to "config show" and "config models".`,
		Example: `  rootout config show                          # settings and which keys are set
  rootout config set llm.provider anthropic    # use Claude for scan --llm
  rootout config set-key anthropic             # paste the key into the Keychain
  rootout config models --pick                 # choose a local model that fits this Mac
  rootout config pull llama3.2                 # download a local model`,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "show", Short: "Show settings and which API keys are configured", Args: cobra.NoArgs,
			Long: `Print the config file path, every setting and, for each hosted provider,
whether an API key is set and where it comes from (Keychain or environment).
Keys are shown masked; the full key is never printed.

--json prints {path, config, keys}.`,
			Example: `  rootout config show
  rootout config show --json`,
			RunE: func(*cobra.Command, []string) error { return configShow(g) },
		},
		&cobra.Command{
			Use: "get <key>", Short: "Print one setting", Args: cobra.ExactArgs(1),
			Long: `Print the value of one setting. Keys: ` + strings.Join(config.Keys(), ", ") + `.`,
			Example: `  rootout config get llm.provider
  rootout config get llm.ollama.model`,
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
			Long: `Change one setting and save the config file. Unknown keys and invalid
values are rejected; nothing else in the file is touched.

Keys: ` + strings.Join(config.Keys(), ", ") + `.`,
			Example: `  rootout config set llm.provider ollama
  rootout config set llm.ollama.model llama3.2
  rootout config set scan.min_score 50`,
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
			Long: `Read an API key and store it in the macOS Keychain. On a terminal the key is
typed hidden; otherwise it is read from stdin. The key never goes into the
config file and is never printed in full. Ollama needs no key.`,
			Example: `  rootout config set-key anthropic              # prompts, input hidden
  rootout config set-key openai < key.txt       # from a file or pipe`,
			RunE: func(_ *cobra.Command, a []string) error { return configSetKey(a[0]) },
		},
		&cobra.Command{
			Use: "delete-key <anthropic|openai>", Short: "Remove a stored API key", Args: cobra.ExactArgs(1),
			Long: `Remove the provider's API key from the macOS Keychain. A key set through the
ANTHROPIC_API_KEY or OPENAI_API_KEY environment variable is not affected.`,
			Example: `  rootout config delete-key anthropic`,
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
			Long: `Download a model through the running Ollama server, with progress on stderr,
then set Ollama as the provider and this model as its default. Ollama must be
installed and running; "rootout config models --pick" can do both.`,
			Example: `  rootout config pull llama3.2
  rootout config pull qwen2.5-coder:7b`,
			RunE: func(cmd *cobra.Command, a []string) error { return configPull(cmd.Context(), a[0]) },
		},
	)
	return cmd
}

// checkModelsProvider validates "config models --provider".
func checkModelsProvider(p string) error {
	for _, h := range hostedProviders {
		if p == h {
			return nil
		}
	}
	msg := fmt.Sprintf("--provider %q: want %s", p, strings.Join(hostedProviders, " or "))
	if p == "ollama" {
		msg += " (Ollama models are listed without --provider)"
	}
	return &exitErr{code: exitBadInput, msg: msg}
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
			fmt.Printf("  %-10s not set (rootout config set-key %s)\n", k.Provider, k.Provider)
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
	var provider string
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List the models you can use: Ollama on this Mac, or a hosted provider's",
		Long: `Without flags, print this Mac's chip and RAM, the Ollama models already
installed, and the models recommended for this hardware with their download
size. With --pick, open the interactive picker, which can install Ollama,
start it, download a model and make it the default. Nothing is installed or
downloaded without your confirmation.

With --provider anthropic|openai, list the models that provider's saved API
key can use (one free request, newest first); --pick then opens a picker
that saves the choice to llm.<provider>.model and makes it the provider.

--json prints {ollama, version, hardware, current, installed, recommended},
or {provider, current, models} with --provider.
"config models rm <model>" uninstalls an Ollama model.`,
		Example: `  rootout config models                        # what is installed and what would fit
  rootout config models --pick                 # choose, download and set the default
  rootout config models --provider anthropic   # models your Claude key can use
  rootout config models --provider openai --pick
  rootout config models rm llama3.2            # uninstall a model
  rootout config models --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if provider != "" {
				if err := checkModelsProvider(provider); err != nil {
					return err
				}
				return declinedOnQuit(hostedModels(cmd.Context(), g, provider, pick), "no model chosen")
			}
			if pick {
				_, err := pickAndSaveModel(cmd.Context())
				return declinedOnQuit(err, "no model chosen")
			}
			return listModels(cmd.Context(), g)
		},
	}
	cmd.Flags().BoolVar(&pick, "pick", false, "open the interactive picker")
	cmd.Flags().StringVar(&provider, "provider", "", "list a hosted provider's models instead: anthropic or openai")
	cmd.AddCommand(newModelsRmCmd())
	return cmd
}

func newModelsRmCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use: "rm <model>", Short: "Uninstall a local Ollama model", Args: cobra.ExactArgs(1),
		Long: `Delete a model from the running Ollama server after a y/n confirmation. If it
was the default model for assessments, the default is cleared; choose another
with "rootout config models --pick". The picker's d key does the same.`,
		Example: `  rootout config models rm llama3.2
  rootout config models rm qwen3.5:9b --yes`,
		RunE: func(cmd *cobra.Command, a []string) error {
			c, path, err := loadConfig()
			if err != nil {
				return err
			}
			return removeModel(cmd.Context(), ollamaClient(c), c, path, a[0], yes, os.Stdin, os.Stderr)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation")
	return cmd
}

// removeModel deletes model from Ollama and, when it was the configured
// default, forgets it so scans do not ask for a model that is gone.
func removeModel(ctx context.Context, cl *ollama.Client, c config.Config, path, model string, yes bool, in io.Reader, out io.Writer) error {
	if _, err := cl.Detect(ctx); err != nil {
		return fmt.Errorf("%w (run `rootout config models --pick` to install or start it)", err)
	}
	if !yes {
		_, _ = fmt.Fprintf(out, "Uninstall %s from Ollama? [y/N] ", model)
		line, _ := bufio.NewReader(in).ReadString('\n')
		if l := strings.ToLower(strings.TrimSpace(line)); l != "y" && l != "yes" {
			return &exitErr{code: exitDeclined, msg: "cancelled"}
		}
	}
	if err := cl.Delete(ctx, model); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "uninstalled %s\n", model)
	if sameModel(c.LLM.Ollama.Model, model) {
		c.LLM.Ollama.Model = ""
		if err := config.Save(path, c); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "it was the default model; choose another with `rootout config models --pick`")
	}
	return nil
}

// sameModel compares model names ignoring an explicit ":latest" tag.
func sameModel(a, b string) bool {
	return a != "" && strings.TrimSuffix(a, ":latest") == strings.TrimSuffix(b, ":latest")
}

// clearDefaultModel forgets the configured Ollama model.
func clearDefaultModel() error {
	c, path, err := loadConfig()
	if err != nil {
		return err
	}
	c.LLM.Ollama.Model = ""
	return config.Save(path, c)
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
		fmt.Println("or run `rootout config models --pick` to be guided through it.")
	case errors.Is(derr, ollama.ErrNotRunning):
		fmt.Println("\nOllama is installed but not running. `rootout config models --pick` can start it.")
	case derr != nil:
		return derr
	default:
		fmt.Printf("\nOllama %s — installed models\n", version)
		if len(installed) == 0 {
			fmt.Println("  (none)")
			break
		}
		fmt.Println("  " + modelGridRow("MODEL", "SIZE", "FIT") + "NOTES")
		for _, m := range installed {
			name := m.Name
			if m.Name == c.LLM.Ollama.Model {
				name += " (current)"
			}
			fit := "fits"
			if !m.FitsRAM {
				fit = ollama.FitTooLarge
			}
			notes := strings.Trim(m.ParameterSize+" · "+m.Quantization, " ·")
			fmt.Println("  " + modelGridRow(name, output.Bytes(m.SizeBytes), fit) + notes)
		}
	}
	fmt.Println("\nRecommended for this Mac")
	fmt.Println("  " + modelGridRow("MODEL", "SIZE", "FIT") + fmt.Sprintf("%-13s  %s", "STATUS", "WHY"))
	printRec := func(r ollama.Recommendation) {
		status := "not installed"
		if r.Installed {
			status = "installed"
		}
		fmt.Println("  " + modelGridRow(r.Name, output.Bytes(r.DownloadBytes), r.Fit) + fmt.Sprintf("%-13s  %s", status, r.Why))
	}
	var large []ollama.Recommendation
	for _, r := range recs {
		if r.Fit == ollama.FitTooLarge {
			large = append(large, r)
			continue
		}
		printRec(r)
	}
	if len(large) > 0 {
		fmt.Println("  Need more memory than this Mac has")
		for _, r := range large {
			printRec(r)
		}
	}
	fmt.Println("\nDownload one with: rootout config pull <model>")
	return nil
}

// modelGridRow lays out name, size and fit like the model picker's grid
// (tui modelNameW, sizeWidth, modelFitW), with the trailing gap.
func modelGridRow(name, size, fit string) string {
	return fmt.Sprintf("%-26s  %8s  %-11s  ", output.Truncate(name, 26), size, fit)
}

func hasBrew() bool {
	_, err := run.Default(context.Background(), "/usr/bin/which", "brew")
	return err == nil
}

// modelDeps builds what the model picker needs on this Mac.
func modelDeps(ctx context.Context, c config.Config) (tui.ModelDeps, error) {
	hw, err := ollama.Probe(ctx, run.Default)
	if err != nil {
		return tui.ModelDeps{}, err
	}
	brew := hasBrew()
	deps := tui.ModelDeps{Client: ollamaClient(c), Hardware: hw, HasBrew: brew, ClearDefault: clearDefaultModel}
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
	return deps, nil
}

// saveOllamaModel makes model the default and Ollama the provider.
func saveOllamaModel(model string) error { return saveModel("ollama", model) }

// saveModel makes model the default for provider and provider the one used
// for assessments: llm.provider and llm.<provider>.model in the config file.
func saveModel(provider, model string) error {
	c, path, err := loadConfig()
	if err != nil {
		return err
	}
	switch provider {
	case "ollama":
		c.LLM.Ollama.Model = model
	case "anthropic", "openai":
		setHostedModel(&c, provider, model)
	default:
		return fmt.Errorf("config: unknown provider %q (anthropic, openai, ollama)", provider)
	}
	c.LLM.Provider = provider
	return config.Save(path, c)
}

// announceModel is the one-line confirmation for the CLI paths; the scan
// screen's m key saves silently (a stderr line would land on the screen).
func announceModel(provider, model string) {
	fmt.Fprintf(os.Stderr, "using %s model %s for assessments\n", providerNames[provider], model)
}

// hostedModels lists (or, with pick, picks and saves) a hosted provider's
// models using the saved key.
func hostedModels(ctx context.Context, g *globalFlags, provider string, pick bool) error {
	c, _, err := loadConfig()
	if err != nil {
		return err
	}
	key, err := resolveHostedKey(keychain.New(), provider, os.Getenv)
	if errors.Is(err, keychain.ErrNotFound) {
		return &exitErr{code: exitBadInput, msg: fmt.Sprintf("no %s API key; run `rootout setup`", provider)}
	}
	if err != nil {
		return err
	}
	models, err := hostedLister(provider, key)(ctx)
	current := hostedModel(c, provider)
	fetchErr := ""
	if err != nil {
		if !pick {
			return err
		}
		fetchErr = err.Error() // the picker shows it and still takes a typed id
	}
	if !pick {
		return printHostedModels(os.Stdout, g.json, provider, current, models)
	}
	model, ok, err := tui.PickHostedModel(ctx, provider, models, current, fetchErr)
	if err != nil {
		return err
	}
	if !ok {
		return &exitErr{code: exitDeclined, msg: "no model chosen"}
	}
	if err := saveModel(provider, model); err != nil {
		return err
	}
	announceModel(provider, model)
	return nil
}

// resolveHostedKey is keychain.Resolve with a blank item (edited outside
// rootout) reported as ErrNotFound rather than returned as an empty key.
func resolveHostedKey(store keychain.Store, provider string, getenv func(string) string) (string, error) {
	key, _, err := keychain.Resolve(store, provider, getenv)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(key) == "" {
		return "", keychain.ErrNotFound
	}
	return key, nil
}

// printHostedModels prints the hosted model grid, or the JSON form.
func printHostedModels(out io.Writer, asJSON bool, provider, current string, models []llm.ModelInfo) error {
	if asJSON {
		if models == nil {
			models = []llm.ModelInfo{}
		}
		return output.New(out, true).Emit(map[string]any{"provider": provider, "current": current, "models": models})
	}
	_, _ = fmt.Fprintf(out, "%s models your key can use (newest first)\n", providerNames[provider])
	_, _ = fmt.Fprintf(out, "  %-34s %-28s %s\n", "MODEL", "NAME", "RELEASED")
	listed := false
	for _, m := range models {
		name := m.ID
		if m.ID == current {
			name += " (current)"
			listed = true
		}
		released := ""
		if !m.Created.IsZero() {
			released = m.Created.Format("2006-01-02")
		}
		note := ""
		if m.Deprecated {
			note = "  deprecated"
		}
		_, _ = fmt.Fprintf(out, "  %-34s %-28s %s%s\n", output.Truncate(name, 34), output.Truncate(m.DisplayName, 28), released, note)
	}
	if !listed && current != "" {
		_, _ = fmt.Fprintf(out, "  %-34s %-28s\n", output.Truncate(current+" (current)", 34), "not in the list")
	}
	_, _ = fmt.Fprintf(out, "\nChange it with: rootout config models --provider %s --pick, or rootout config set llm.%s.model <id>\n", provider, provider)
	return nil
}

// pickAndSaveModel runs the picker, then makes the chosen model the default
// and Ollama the provider.
func pickAndSaveModel(ctx context.Context) (string, error) {
	c, _, err := loadConfig()
	if err != nil {
		return "", err
	}
	deps, err := modelDeps(ctx, c)
	if err != nil {
		return "", err
	}
	model, ok, err := tui.PickModel(ctx, deps, c.LLM.Ollama.Model)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", &exitErr{code: exitDeclined, msg: "no model chosen"}
	}
	if err := saveOllamaModel(model); err != nil {
		return "", err
	}
	announceModel("ollama", model)
	return model, nil
}

func configPull(ctx context.Context, model string) error {
	c, path, err := loadConfig()
	if err != nil {
		return err
	}
	cl := ollamaClient(c)
	if _, err := cl.Detect(ctx); err != nil {
		return fmt.Errorf("%w (run `rootout config models --pick` to install or start it)", err)
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
