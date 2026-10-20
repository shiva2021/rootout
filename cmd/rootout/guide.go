package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/llm/ollama"
	"github.com/shiva2021/rootout/internal/output"
)

// guideStep is one numbered step of the tour. Text is prose for the terminal
// and stays out of the JSON, which carries only the state and the commands.
type guideStep struct {
	Step     int      `json:"step"`
	Title    string   `json:"title"`
	State    string   `json:"state"`
	Commands []string `json:"commands"`
	text     string
}

func newGuideCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "guide",
		Short: "A step-by-step tour of rootout, adapted to this Mac",
		Long: `Print a numbered tour: check the setup, remove an app, scan editor
extensions, add an AI second opinion, find leftovers of apps you already
deleted, and script rootout. The steps reflect this Mac: the tour runs the
same checks as "rootout doctor" and names an app that is really installed.

The guide only reads. It never removes anything and never changes settings;
every command it shows is for you to run yourself.

--json prints the steps as [{step, title, state, commands}].`,
		Example: `  rootout guide            # the tour, adapted to this Mac
  rootout guide --json     # the same steps as JSON`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			steps := buildGuide(cmd.Context())
			w := output.New(os.Stdout, g.json)
			if g.json {
				return w.Emit(steps)
			}
			printGuide(w, steps)
			return nil
		},
	}
}

func buildGuide(ctx context.Context) []guideStep {
	checks := runChecks(ctx)
	env, envErr := fsx.Real()

	var steps []guideStep
	add := func(title, state, text string, commands ...string) {
		steps = append(steps, guideStep{Step: len(steps) + 1, Title: title, State: state, Commands: commands, text: text})
	}

	// 1. doctor summary
	okN, fixN := 0, 0
	for _, c := range checks {
		if c.OK {
			okN++
		} else {
			fixN++
		}
	}
	text := "rootout doctor lists what it found on this Mac: the Trash backend, Homebrew, your editors, the config file and any AI providers."
	if fixN > 0 {
		text += fmt.Sprintf(" Right now %d item(s) need attention; each line shows the fix.", fixN)
	}
	add("Check your setup", fmt.Sprintf("%d ok, %d to fix", okN, fixN), text, "rootout doctor")

	// 2. remove
	appName := "<App>"
	if envErr == nil {
		appName = sampleApp(env)
	}
	state := "example: " + appName
	if appName == "<App>" {
		state = "no app found; replace <App> with a name"
	}
	add("Remove an app completely", state,
		"rootout removes the app bundle and every file it left behind: preferences, caches, containers, launch agents, receipts. --dry-run only lists them. Without it the list is shown for confirmation and the files go to the Trash.",
		"rootout remove "+shellArg(appName)+" --dry-run",
		"rootout remove "+shellArg(appName))

	// 3. scan
	var editors []ext.Editor
	if envErr == nil {
		editors = ext.Editors(env, ext.Options{})
	}
	if len(editors) == 0 {
		add("Scan your editor extensions", "no editor found",
			"No VS Code-family editor (VS Code, Cursor, VSCodium, Windsurf, VS Code Insiders) was found on this Mac, so there is nothing to scan yet. The command is here for when you install one.",
			"rootout scan")
	} else {
		names := make([]string, 0, len(editors))
		for _, e := range editors {
			names = append(names, e.Name)
		}
		add("Scan your editor extensions", "found: "+strings.Join(names, ", "),
			fmt.Sprintf("rootout scores the extensions in %s from 0 to 100 without running any of them. On a terminal the review screen lets you tick extensions to uninstall; --no-tui just prints the table.", strings.Join(names, " and ")),
			"rootout scan",
			"rootout scan --all --no-tui")
	}

	// 4. AI
	var cmds []string
	switch st := ollamaState(checks); st {
	case "running":
		text = "Ollama is running. Pick a local model that fits this Mac; the scan then asks it about each flagged extension. The model sees only the findings and short snippets, never the whole extension."
		cmds = []string{"rootout config models --pick", "rootout scan --llm"}
	case "not running":
		text = "Ollama is installed but not running. The picker can start it and choose a model that fits this Mac."
		cmds = []string{"rootout config models --pick"}
	default:
		text = "Ollama is not installed. It is optional: install it to run a local model, then pick one. rootout installs nothing without asking you first."
		for _, r := range ollama.InstallGuide(hasBrew()) {
			if r.Runnable {
				cmds = append(cmds, r.Command)
			}
		}
		if len(cmds) == 0 {
			cmds = append(cmds, ollama.InstallGuide(false)[1].Command)
		}
		cmds = append(cmds, "rootout config models --pick")
	}
	text += " Or use a hosted model: store an API key in the macOS Keychain and set the provider."
	cmds = append(cmds, "rootout config set-key anthropic", "rootout config set llm.provider anthropic")
	add("Add an AI second opinion", "ollama "+ollamaState(checks), text, cmds...)

	// 5. orphans
	add("Find leftovers of apps you already deleted", "ready",
		"Apps dragged to the Trash usually leave files behind. rootout orphans lists the candidates grouped by the old app id. Nothing is removed unless you type delete.",
		"rootout orphans --dry-run")

	// 6. scripting
	add("Scripting", "optional",
		"Every command takes --json and then prints only JSON on stdout. --yes skips the confirmation; combined with --permanent it deletes without the Trash and without asking, so use it only in scripts you trust.",
		"rootout list --json",
		"rootout remove "+shellArg(appName)+" --dry-run --json",
		"rootout remove "+shellArg(appName)+" --yes")

	return steps
}

// ollamaState reads the doctor's Ollama line: "running", "not running" or
// "not installed".
func ollamaState(checks []check) string {
	for _, c := range checks {
		if c.Name != "Ollama" {
			continue
		}
		switch {
		case strings.HasPrefix(c.Detail, "installed, not running"):
			return "not running"
		case strings.HasPrefix(c.Detail, "not installed"):
			return "not installed"
		default:
			return "running"
		}
	}
	return "not installed"
}

// sampleApp names one installed, non-Apple app for the examples. It reads
// only directory listings and one Info.plist per candidate, which is far
// cheaper than app.Discover; "<App>" when nothing fits.
func sampleApp(env fsx.Env) string {
	for _, dir := range []string{env.Sys("Applications"), env.User("Applications")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || !strings.HasSuffix(name, ".app") {
				continue
			}
			plist, err := os.ReadFile(filepath.Join(dir, name, "Contents", "Info.plist"))
			if err != nil || bytes.Contains(plist, []byte("com.apple.")) {
				continue
			}
			return strings.TrimSuffix(name, ".app")
		}
	}
	return "<App>"
}

// shellArg quotes an app name that contains spaces.
func shellArg(s string) string {
	if strings.ContainsAny(s, " \t'") {
		return `"` + s + `"`
	}
	return s
}

func printGuide(w *output.Writer, steps []guideStep) {
	for i, s := range steps {
		if i > 0 {
			w.Printf("\n")
		}
		w.Printf("%d. %s (%s)\n", s.Step, s.Title, s.State)
		for _, line := range wrap(s.text, 72) {
			w.Printf("   %s\n", line)
		}
		for _, c := range s.Commands {
			w.Printf("     %s\n", c)
		}
	}
}

// wrap breaks text into lines of at most width characters at spaces.
func wrap(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) > width:
			lines = append(lines, line)
			line = word
		default:
			line += " " + word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
