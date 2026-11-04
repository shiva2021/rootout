package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/priv"
	"github.com/shiva2021/rootout/internal/run"
)

type removeFlags struct {
	dryRun, permanent, yes, dotfiles, noCrash, noBrew, sudo bool
	also                                                    []string
}

func newRemoveCmd(g *globalFlags) *cobra.Command {
	var f removeFlags
	cmd := &cobra.Command{
		Use:   "remove <app>",
		Short: "Remove an application and its leftover files",
		Long: `Remove an application bundle and every file it left behind: preferences,
caches, containers, launch agents, login items, receipts and more.

The full list is shown for confirmation before anything happens; --dry-run
stops there. Files go to the Trash unless --permanent is given, so a mistake
can be undone from the Finder. Nothing runs as root: files that need admin
rights are removed in one step after a single macOS password prompt.
System applications are never removed. Homebrew casks are also uninstalled
with "brew uninstall --zap" unless --no-brew is given.

<app> is a name, bundle id or path; fuzzy matches are accepted when they
are unambiguous. "rootout list --search" helps find the right one.

--json prints the plan (with --dry-run) or the result as JSON and uses a
plain confirmation instead of the review screen.`,
		Example: `  rootout remove Spotify --dry-run         # show what would be removed
  rootout remove Spotify                   # review the list, then move it to the Trash
  rootout remove com.spotify.client        # exact bundle id
  rootout remove "Google Chrome" --dotfiles  # also look in ~/.config and ~/.<name>
  rootout remove Spotify --permanent       # delete outright; no Trash
  rootout remove Spotify --yes --json      # no questions; result as JSON`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(cmd.Context(), g, f, args[0])
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&f.dryRun, "dry-run", "n", false, "list what would be removed and exit")
	fl.BoolVar(&f.permanent, "permanent", false, "delete outright instead of moving to the Trash")
	fl.BoolVarP(&f.yes, "yes", "y", false, "do not ask for confirmation")
	fl.BoolVar(&f.dotfiles, "dotfiles", false, "also look in ~/.config/<name> and ~/.<name>")
	fl.BoolVar(&f.noCrash, "no-crash-reports", false, "leave crash reports in place")
	fl.BoolVar(&f.noBrew, "no-brew", false, "do not run brew uninstall --zap for cask apps")
	fl.BoolVar(&f.sudo, "sudo", false, "use sudo instead of the graphical admin prompt")
	fl.StringArrayVar(&f.also, "also", nil, "extra path to remove (repeatable; must be under ~/Library, /Library or the Applications folders)")
	return cmd
}

func runRemove(ctx context.Context, g *globalFlags, f removeFlags, arg string) error {
	env, err := fsx.Real()
	if err != nil {
		return err
	}
	rep := stderrProgress(g)
	apps, err := app.DiscoverWith(env, app.Options{Progress: rep})
	if err != nil {
		return err
	}
	target, err := resolveApp(apps, arg)
	if err != nil {
		return err
	}

	mode := plan.Trash
	switch {
	case f.dryRun:
		mode = plan.DryRun
	case f.permanent:
		mode = plan.Permanent
	}
	p, err := plan.Build(ctx, env, target, plan.Options{
		Mode:         mode,
		Dotfiles:     f.dotfiles,
		CrashReports: !f.noCrash,
		Also:         f.also,
		Run:          run.Default,
		Progress:     rep,
	})
	if err != nil {
		return err
	}
	if f.noBrew {
		p.BrewZap = false
	}

	w := output.New(os.Stdout, g.json)
	if mode == plan.DryRun {
		if g.json {
			return w.Emit(p)
		}
		plan.Describe(os.Stdout, p)
		return nil
	}

	if f.yes {
		yesBanner(f.permanent)
	}
	ok, err := chooseConfirmer(g, f).Confirm(ctx, p)
	if err != nil {
		return declinedOnQuit(err, "nothing removed")
	}
	if !ok {
		return &exitErr{code: exitDeclined, msg: "nothing removed"}
	}

	res, err := plan.Execute(ctx, env, p, plan.Deps{
		Priv:     priv.Options{UseSudo: f.sudo, Dotfiles: f.dotfiles},
		Dotfiles: f.dotfiles,
		Progress: rep,
	})
	if res != nil {
		if g.json {
			_ = w.Emit(res)
		} else {
			report(res)
		}
	}
	if err != nil {
		if errors.Is(err, priv.ErrCancelled) {
			return &exitErr{code: exitDeclined, msg: "admin prompt cancelled; user-level files were already handled"}
		}
		return err
	}
	if !res.OK() {
		return &exitErr{code: exitError, msg: "some paths could not be removed"}
	}
	return nil
}

// chooseConfirmer picks --yes, the TUI on a terminal, or a plain prompt.
func chooseConfirmer(g *globalFlags, f removeFlags) plan.Confirmer {
	if f.yes {
		return plan.Auto{}
	}
	if !g.json && isTerminal(os.Stdin) && isTerminal(os.Stdout) {
		return tuiConfirmer()
	}
	return plan.Prompt{In: os.Stdin, Out: os.Stderr}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func report(r *plan.Result) {
	if r.BrewZapped {
		fmt.Println("brew: cask zapped")
	}
	if r.BrewError != "" {
		fmt.Println("brew:", r.BrewError)
	}
	for _, p := range r.Removed {
		fmt.Println("removed", p)
	}
	for _, p := range r.Skipped {
		fmt.Println("already gone", p)
	}
	for p, e := range r.Failed {
		fmt.Println("FAILED", p, "-", e)
	}
	for _, l := range r.Reported {
		fmt.Println("login item left in place:", l.Path, "-", l.Note)
	}
	fmt.Printf("%d removed, %d failed\n", len(r.Removed), len(r.Failed))
}
