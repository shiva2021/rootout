// Command rootout uninstalls macOS apps completely and scans VS Code-family
// extensions for malicious behaviour. Bare `rootout` opens the TUI; subcommands
// do the same work non-interactively.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/progress"
)

// version is set by the linker (see Makefile).
var version = "dev"

// Exit codes shared by every subcommand (see docs/kb/conventions.md).
const (
	exitOK       = 0
	exitError    = 1
	exitBadInput = 2
	exitDeclined = 3
)

type globalFlags struct {
	json    bool
	verbose bool
}

func main() {
	os.Exit(runMain(os.Args[1:]))
}

func runMain(args []string) int {
	if os.Geteuid() == 0 {
		fmt.Fprintln(os.Stderr, "rootout: refusing to run as root; run it as your normal user and it will ask for admin rights only when needed")
		return exitError
	}

	var g globalFlags
	root := &cobra.Command{
		Use:   "rootout",
		Short: "Uninstall macOS apps completely and scan editor extensions for threats",
		Long: `rootout uninstalls macOS apps completely and scans editor extensions for
malicious behaviour.

Bare "rootout" opens the home screen: uninstall an app, remove orphaned
leftovers, scan editor extensions or choose a local AI model. Every action
returns to the home screen; esc goes back one screen, ctrl+x quits. The
subcommands do the same work from scripts.

Nothing runs as root; files that need admin rights are removed in one step
after a single macOS password prompt. Removal moves files to the Trash
unless --permanent is given, and the full list is shown before anything
happens. System apps are listed but never removable. Extension scanning is
static: extension code is never executed.

Every subcommand takes --json and then prints only JSON on stdout.

Getting started
  rootout doctor                 check what rootout can use on this Mac
  rootout                        open the home screen
  rootout scan                   score your editor extensions
  rootout setup                  choose an AI model for recommendations (optional)
  rootout guide                  a step-by-step tour`,
		Example: `  rootout                               # open the home screen
  rootout remove Spotify --dry-run      # list what removing Spotify would touch
  rootout remove Spotify                # review the list, then move it to the Trash
  rootout scan --no-tui                 # print extension scores
  rootout list --json                   # installed apps as JSON`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			lvl := slog.LevelWarn
			if g.verbose {
				lvl = slog.LevelDebug
			}
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
			firstRunHint(cmd, &g)
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd.Context(), &g)
		},
	}
	root.PersistentFlags().BoolVar(&g.json, "json", false, "machine-readable output on stdout")
	root.PersistentFlags().BoolVarP(&g.verbose, "verbose", "v", false, "debug logging on stderr")

	root.AddCommand(
		newListCmd(&g),
		newRemoveCmd(&g),
		newScanCmd(&g),
		newAssessCmd(&g),
		newConfigCmd(&g),
		newDoctorCmd(&g),
		newOrphansCmd(&g),
		newGuideCmd(&g),
		newSetupCmd(&g),
	)

	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		return exitOK
	}
	var ee *exitErr
	if errors.As(err, &ee) {
		fmt.Fprintln(os.Stderr, "rootout:", ee.msg)
		return ee.code
	}
	fmt.Fprintln(os.Stderr, "rootout:", err)
	return exitError
}

// firstRunHint points a new user at setup and the guide, once. It runs only
// on a terminal without --json, skips the bare command (the setup screen
// covers it) and the commands that are themselves help, and creates the
// config directory so the hint does not repeat.
func firstRunHint(cmd *cobra.Command, g *globalFlags) {
	if cmd.Parent() == nil {
		return // bare rootout: the setup screen covers the first run
	}
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "guide", "help", "setup", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return
		}
	}
	if g.json || !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		return
	}
	env, err := fsx.Real()
	if err != nil {
		return
	}
	dir := filepath.Dir(config.Path(env, os.Getenv))
	if _, err := os.Stat(dir); err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "New here? rootout setup adds an optional AI second opinion; rootout guide walks you through everything.")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Debug("first-run hint: create config dir", "dir", dir, "err", err)
	}
}

// stderrProgress reports progress on stderr for human runs and stays silent
// under --json (progress.Term is also a no-op when stderr is not a terminal).
func stderrProgress(g *globalFlags) progress.Reporter {
	if g.json {
		return progress.Nop()
	}
	return progress.Term(os.Stderr)
}

// exitErr carries a specific exit code out of a command.
type exitErr struct {
	code int
	msg  string
}

func (e *exitErr) Error() string { return e.msg }

// isExit reports whether err is an exitErr with the given code.
func isExit(err error, code int) bool {
	var ee *exitErr
	return errors.As(err, &ee) && ee.code == code
}
