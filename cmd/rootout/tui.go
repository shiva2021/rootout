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
	"github.com/shiva2021/rootout/internal/run"
	"github.com/shiva2021/rootout/internal/tui"
)

// runTUI is the bare `rootout` command: pick an app, review its leftovers in
// the confirm screen, then remove. Loops back to the picker after each app
// until the user quits.
func runTUI(ctx context.Context, g *globalFlags) error {
	if g.json {
		return &exitErr{code: exitBadInput, msg: "--json needs a subcommand (list, remove, scan); the TUI is interactive"}
	}
	env, err := fsx.Real()
	if err != nil {
		return err
	}
	for {
		apps, err := app.Discover(env)
		if err != nil {
			return err
		}
		chosen, ok, err := tui.PickApp(ctx, apps)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		p, err := plan.Build(ctx, env, chosen, plan.Options{Mode: plan.Trash, CrashReports: true, Run: run.Default})
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
		res, err := plan.Execute(ctx, env, p, plan.Deps{Priv: priv.Options{}})
		if res != nil {
			report(res)
		}
		if err != nil && !errors.Is(err, priv.ErrCancelled) {
			return err
		}
	}
}
