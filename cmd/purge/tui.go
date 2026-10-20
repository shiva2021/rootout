package main

import (
	"context"
	"fmt"
	"os"

	"github.com/shiva2021/purge/internal/app"
	"github.com/shiva2021/purge/internal/fsx"
	"github.com/shiva2021/purge/internal/tui"
)

// runTUI is the bare `purge` command: pick an app, then hand it to the removal
// flow (M2). Until then it reports the choice.
func runTUI(ctx context.Context, g *globalFlags) error {
	if g.json {
		return &exitErr{code: exitBadInput, msg: "--json needs a subcommand (list, remove, scan); the TUI is interactive"}
	}
	env, err := fsx.Real()
	if err != nil {
		return err
	}
	apps, err := app.Discover(env)
	if err != nil {
		return err
	}
	chosen, ok, err := tui.PickApp(ctx, apps)
	if err != nil {
		return err
	}
	if !ok {
		return &exitErr{code: exitDeclined, msg: "cancelled"}
	}
	_, err = fmt.Fprintf(os.Stdout, "selected %s (%s) — removal flow arrives in M2\n", chosen.Name, chosen.BundleID)
	return err
}
