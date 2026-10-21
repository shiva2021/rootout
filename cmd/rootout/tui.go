package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/priv"
	"github.com/shiva2021/rootout/internal/progress"
	"github.com/shiva2021/rootout/internal/run"
	"github.com/shiva2021/rootout/internal/tui"
)

// runTUI is the bare `rootout` command: pick an app (or the orphans row),
// review what would be removed, type delete, remove. Loops back to the
// picker after each round until the user quits.
func runTUI(ctx context.Context, g *globalFlags) error {
	if g.json {
		return &exitErr{code: exitBadInput, msg: "--json needs a subcommand (list, remove, scan, orphans); the TUI is interactive"}
	}
	env, err := fsx.Real()
	if err != nil {
		return err
	}
	for {
		load := func(rep progress.Reporter) ([]app.App, error) {
			return app.DiscoverWith(env, app.Options{Progress: rep})
		}
		pick, ok, err := tui.PickAppLoad(ctx, load, tui.PickOptions{OrphansRow: true})
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if pick.Orphans {
			if err := runOrphans(ctx, g, orphanFlags{}); err != nil {
				if isExit(err, exitDeclined) {
					continue
				}
				return err
			}
			continue
		}
		// Discovery and removal run between screens, so the terminal is in
		// normal mode and the stderr progress line is visible.
		rep := progress.Term(os.Stderr)
		p, err := plan.Build(ctx, env, pick.App, plan.Options{Mode: plan.Trash, CrashReports: true, Run: run.Default, Progress: rep})
		if err != nil {
			fmt.Fprintln(os.Stderr, "rootout:", err)
			continue
		}
		ok, err = tui.Confirmer{}.Confirm(ctx, p)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		res, err := plan.Execute(ctx, env, p, plan.Deps{Priv: priv.Options{}, Progress: rep})
		if res != nil {
			report(res)
		}
		if err != nil && !errors.Is(err, priv.ErrCancelled) {
			return err
		}
	}
}
