// Command purge uninstalls macOS apps completely and scans VS Code-family
// extensions for malicious behaviour. Bare `purge` opens the TUI; subcommands
// do the same work non-interactively.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/shiva2021/purge/internal/license"
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

var errNotYet = errors.New("not implemented yet")

type globalFlags struct {
	json    bool
	verbose bool
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if os.Geteuid() == 0 {
		fmt.Fprintln(os.Stderr, "purge: refusing to run as root; run it as your normal user and it will ask for admin rights only when needed")
		return exitError
	}

	var g globalFlags
	root := &cobra.Command{
		Use:           "purge",
		Short:         "Uninstall macOS apps completely and scan editor extensions for threats",
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
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return errNotYet // TUI lands in M1
		},
	}
	root.PersistentFlags().BoolVar(&g.json, "json", false, "machine-readable output on stdout")
	root.PersistentFlags().BoolVarP(&g.verbose, "verbose", "v", false, "debug logging on stderr")

	root.AddCommand(
		stub("list", "List installed applications"),
		stub("remove <app>", "Remove an application and its leftover files"),
		stub("scan", "Scan editor extensions for malicious behaviour"),
		stub("config", "Manage providers, models and API keys"),
		stub("doctor", "Check the environment purge depends on"),
	)

	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		return exitOK
	}
	var ee *exitErr
	if errors.As(err, &ee) {
		fmt.Fprintln(os.Stderr, "purge:", ee.msg)
		return ee.code
	}
	fmt.Fprintln(os.Stderr, "purge:", err)
	return exitError
}

// exitErr carries a specific exit code out of a command.
type exitErr struct {
	code int
	msg  string
}

func (e *exitErr) Error() string { return e.msg }

func stub(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fmt.Errorf("%s: %w", cmd.Name(), errNotYet)
		},
	}
}
