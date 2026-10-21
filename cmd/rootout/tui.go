package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/priv"
	"github.com/shiva2021/rootout/internal/progress"
	"github.com/shiva2021/rootout/internal/run"
	"github.com/shiva2021/rootout/internal/tui"
)

// runTUI is the bare `rootout` command: the home screen, one action, back to
// the home screen with the outcome on its flash line, until the user quits.
func runTUI(ctx context.Context, g *globalFlags) error {
	if g.json {
		return &exitErr{code: exitBadInput, msg: "--json needs a subcommand (list, remove, scan, orphans); the TUI is interactive"}
	}
	env, err := fsx.Real()
	if err != nil {
		return err
	}
	var flash tui.Flash
	for {
		a, err := tui.PickAction(ctx, version, flash)
		if errors.Is(err, tui.ErrQuit) {
			return nil
		}
		if err != nil {
			return err
		}
		// Bubbletea turns SIGTERM into a plain quit, which leaves nothing chosen.
		if a == tui.ActionNone {
			return nil
		}
		flash, err = runAction(ctx, env, a)
		if errors.Is(err, tui.ErrQuit) {
			return nil
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "rootout:", err)
			flash = errorFlash(err)
		}
	}
}

// runAction runs one home-screen action and returns what to show on the flash.
func runAction(ctx context.Context, env fsx.Env, a tui.Action) (tui.Flash, error) {
	switch a {
	case tui.ActionUninstall:
		return homeUninstall(ctx, env)
	case tui.ActionOrphans:
		return homeOrphans(ctx, env)
	case tui.ActionScan:
		return homeScan(ctx, env)
	case tui.ActionModel:
		return homeModel(ctx)
	}
	return tui.Flash{}, nil
}

func homeUninstall(ctx context.Context, env fsx.Env) (tui.Flash, error) {
	// Discovery and removal run between screens, so the terminal is in
	// normal mode and the stderr progress line is visible.
	rep := progress.Term(os.Stderr)
	load := func(r progress.Reporter) ([]app.App, error) {
		return app.DiscoverWith(env, app.Options{Progress: r})
	}
	for {
		a, ok, err := tui.PickAppLoad(ctx, load)
		if err != nil {
			return tui.Flash{}, err
		}
		if !ok {
			return tui.Flash{}, nil
		}
		p, err := plan.Build(ctx, env, a, plan.Options{Mode: plan.Trash, CrashReports: true, Run: run.Default, Progress: rep})
		if err != nil {
			f := errorFlash(err)
			f.Text = "could not plan " + a.Name + ": " + f.Text
			return f, nil
		}
		ok, err = tui.Confirmer{}.Confirm(ctx, p)
		if err != nil {
			return tui.Flash{}, err
		}
		if !ok {
			continue
		}
		res, err := plan.Execute(ctx, env, p, plan.Deps{Priv: priv.Options{}, Progress: rep})
		if res != nil {
			report(res)
		}
		if err != nil && !errors.Is(err, priv.ErrCancelled) {
			return tui.Flash{}, err
		}
		return removalFlash(a.Name, p, res, err), nil
	}
}

func homeOrphans(ctx context.Context, env fsx.Env) (tui.Flash, error) {
	rep := progress.Term(os.Stderr)
	orphans, err := findOrphans(ctx, env, orphanFlags{}, rep)
	if err != nil {
		return tui.Flash{}, err
	}
	p := orphanPlan(orphans, orphanFlags{})
	plan.MarkAccess(env, p)
	if len(p.Items) == 0 {
		return tui.Flash{Text: "No orphaned leftovers found"}, nil
	}
	ok, err := tui.Confirmer{}.Confirm(ctx, p)
	if err != nil {
		return tui.Flash{}, err
	}
	if !ok || len(p.Selected()) == 0 {
		return tui.Flash{Text: "Nothing removed"}, nil
	}
	res, err := plan.Execute(ctx, env, p, plan.Deps{Priv: priv.Options{}, Progress: rep})
	if res != nil {
		report(res)
	}
	if err != nil && !errors.Is(err, priv.ErrCancelled) {
		return tui.Flash{}, err
	}
	return removalFlash("orphaned leftovers", p, res, err), nil
}

func homeScan(ctx context.Context, env fsx.Env) (tui.Flash, error) {
	rep := progress.Term(os.Stderr)
	const minScore = 30
	shown, total, err := scanReports(ctx, env, scanFlags{minScore: minScore}, rep)
	if err != nil {
		return tui.Flash{}, err
	}
	if total == 0 {
		return tui.Flash{Text: "No extensions found"}, nil
	}
	if len(shown) == 0 {
		return tui.Flash{Text: fmt.Sprintf("No extensions at score %d or above (%d scanned)", minScore, total)}, nil
	}
	c, _, err := loadConfig()
	if err != nil {
		return tui.Flash{}, err
	}
	assessor, err := buildAssessor(ctx, c, "", "", true)
	if errors.Is(err, tui.ErrQuit) {
		return tui.Flash{}, err
	}
	if isExit(err, exitDeclined) {
		return tui.Flash{Text: "Scan cancelled: no model chosen"}, nil
	}
	if err != nil {
		return tui.Flash{}, err
	}
	done, failed, ok, err := reviewAndUninstall(ctx, env, shown, assessor)
	if err != nil {
		return tui.Flash{}, err
	}
	if !ok || done+failed == 0 {
		return tui.Flash{Text: "No changes"}, nil
	}
	return scanFlash(done, failed), nil
}

func homeModel(ctx context.Context) (tui.Flash, error) {
	m, err := pickAndSaveModel(ctx)
	if errors.Is(err, tui.ErrQuit) {
		return tui.Flash{}, err
	}
	if isExit(err, exitDeclined) {
		return tui.Flash{}, nil
	}
	if err != nil {
		return tui.Flash{}, err
	}
	return tui.Flash{Text: "Default local model: " + m}, nil
}

// removalFlash summarises a removal for the home screen's flash line. It
// reports what happened on disk: plan.Execute trashes user-owned paths before
// the admin step, so a cancelled prompt can still follow real removals.
func removalFlash(subject string, p *plan.Plan, res *plan.Result, err error) tui.Flash {
	cancelled := errors.Is(err, priv.ErrCancelled)
	if res == nil {
		res = &plan.Result{}
	}
	var f tui.Flash
	switch {
	case len(res.Removed) > 0:
		removed := make(map[string]bool, len(res.Removed))
		for _, path := range res.Removed {
			removed[path] = true
		}
		var bytes int64
		for _, it := range p.Selected() {
			if removed[it.Path] {
				bytes += it.SizeBytes
			}
		}
		noun := "items"
		if len(res.Removed) == 1 {
			noun = "item"
		}
		dest := "→ Trash"
		if p.Mode == plan.Permanent {
			dest = "deleted"
		}
		f.Text = fmt.Sprintf("Removed %s · %d %s · %s %s", subject, len(res.Removed), noun, output.Bytes(bytes), dest)
		if len(res.Failed) > 0 {
			f.Text += fmt.Sprintf(" · %d failed", len(res.Failed))
			f.Err = true
		}
	case len(res.Failed) > 0:
		f.Text = fmt.Sprintf("Nothing removed from %s · %d failed", subject, len(res.Failed))
		f.Err = true
	case len(res.Skipped) > 0:
		f.Text = subject + " was already gone"
	default:
		f.Text = "Nothing removed from " + subject
	}
	if res.BrewError != "" {
		f.Text += " · brew failed"
		f.Err = true
	}
	if cancelled {
		f.Text += " · admin prompt cancelled"
		f.Err = true
	}
	return f
}

// errorFlash turns an error into a one-line error flash: the first line of
// its text without the "tui: " prefix.
func errorFlash(err error) tui.Flash {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return tui.Flash{Text: strings.TrimPrefix(line, "tui: "), Err: true}
}

// scanFlash summarises a scan-screen uninstall for the home screen.
func scanFlash(done, failed int) tui.Flash {
	noun := "extensions"
	if done == 1 {
		noun = "extension"
	}
	f := tui.Flash{Text: fmt.Sprintf("Uninstalled %d %s", done, noun)}
	if failed > 0 {
		f.Text += fmt.Sprintf(" · %d failed", failed)
		f.Err = true
	}
	return f
}
