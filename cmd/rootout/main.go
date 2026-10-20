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
	"github.com/shiva2021/rootout/internal/license"
)

// version is set by the linker (see Makefile).
var version = "dev"

// Exit codes shared by every subcommand (see docs/kb/conventions.md).
const (
	exitOK        = 0
	exitError     = 1
	exitBadInput  = 2
	exitDeclined  = 3
	exitLicensing = 4
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

Bare "rootout" opens the app picker: choose an app, review every file it
left behind, and remove it. The subcommands do the same work from scripts.

Nothing runs as root; files that need admin rights are removed in one step
after a single macOS password prompt. Removal moves files to the Trash
unless --permanent is given, and the full list is shown before anything
happens. System apps are listed but never removable. Extension scanning is
static: extension code is never executed.

Every subcommand takes --json and then prints only JSON on stdout.

Getting started
  rootout doctor                 check what rootout can use on this Mac
  rootout                        pick an app, review its leftovers, remove it
  rootout scan                   score your editor extensions
  rootout config models --pick   choose a local AI model that fits this Mac
  rootout guide                  a step-by-step tour`,
		Example: `  rootout                               # open the app picker
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
			if st := license.Check(cmd.Context(), cmd.Name()); !st.Allowed {
				return &exitErr{code: exitLicensing, msg: "license: " + st.Reason}
			}
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
		newConfigCmd(&g),
		newDoctorCmd(&g),
		newGuideCmd(&g),
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

// firstRunHint points a new user at the guide, once. It runs only on a
// terminal without --json, skips the commands that are themselves help, and
// creates the config directory so the hint does not repeat.
func firstRunHint(cmd *cobra.Command, g *globalFlags) {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "guide", "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
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
	fmt.Fprintln(os.Stderr, "New here? rootout guide walks you through it.")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Debug("first-run hint: create config dir", "dir", dir, "err", err)
	}
}

// exitErr carries a specific exit code out of a command.
type exitErr struct {
	code int
	msg  string
}

func (e *exitErr) Error() string { return e.msg }
