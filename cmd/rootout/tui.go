package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/priv"
	"github.com/shiva2021/rootout/internal/progress"
	"github.com/shiva2021/rootout/internal/run"
	"github.com/shiva2021/rootout/internal/tui"
	"github.com/shiva2021/rootout/internal/update"
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
	// A Mac without a config file has never seen the setup screen: show it
	// first. Skipping writes an empty file, so it does not come back.
	if path, err := configPath(); err == nil && needsSetup(path) {
		flash, err = runSetup(ctx)
		if errors.Is(err, tui.ErrQuit) {
			return nil
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "rootout:", err)
			flash = errorFlash(err)
		}
	}
	ai := currentAIStatus()
	// After setup, so the first screen never waits and a fresh config is
	// what the check reads.
	notice := currentUpdateNotice(ctx, env)
	for {
		a, err := tui.PickAction(ctx, version, flash, ai, notice)
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
		// Setup and the scan screen's m key can change the saved model.
		if a == tui.ActionSetup || a == tui.ActionScan || a == tui.ActionAssess {
			ai = currentAIStatus()
		}
	}
}

// currentUpdateNotice wires updateNotice to the real config, cache and binary.
func currentUpdateNotice(ctx context.Context, env fsx.Env) func() string {
	enabled := config.Default().Updates.Check
	if c, _, err := loadConfig(); err == nil {
		enabled = c.Updates.Check
	}
	return updateNotice(ctx, update.New(version), enabled, version, update.Path(env, os.Getenv), executable(), time.Now)
}

// updateNotice returns a func that yields the home screen's update notice:
// "v0.8.0 available · <how to upgrade>" when a newer release exists, else
// "". The answer is computed once (the home screen is rebuilt after every
// action) from the daily record when it is fresh, otherwise from one GitHub
// request with a 5 s timeout; the answer, or the failure, is recorded so
// GitHub is asked at most once a day. A fetch cut short because ctx itself
// was cancelled is not a check and is not recorded. When the record cannot
// be written (read-only cache directory) every launch fetches; that is the
// one case the once-a-day rule does not cover. Errors never reach the screen.
func updateNotice(ctx context.Context, cl *update.Client, enabled bool, installed, recordPath, exe string, now func() time.Time) func() string {
	var once sync.Once
	var text string
	return func() string {
		once.Do(func() {
			if !enabled {
				return
			}
			if _, ok := update.Installed(installed); !ok {
				return
			}
			rel, ok := update.ReadRecord(recordPath, now())
			if !ok {
				fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				var err error
				if rel, err = cl.Latest(fctx); err != nil {
					slog.Debug("update check", "err", err)
					if ctx.Err() != nil {
						return // rootout is shutting down, not GitHub failing
					}
					rel = update.Release{}
				}
				if err := update.WriteRecord(recordPath, rel, now()); err != nil {
					slog.Debug("update check: record", "path", recordPath, "err", err)
				}
			}
			if rel.Tag != "" && update.Newer(rel.Tag, installed) {
				text = update.Canonical(rel.Tag) + " available · " + noticeHint(exe)
			}
		})
		return text
	}
}

// noticeHint is upgradeHint's short form for the title line.
func noticeHint(exe string) string {
	if upgradeHint(exe, "") == brewUpgrade {
		return brewUpgrade
	}
	return strings.TrimPrefix(update.ReleasesPage, "https://")
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
	case tui.ActionAssess:
		return homeAssess(ctx, env)
	case tui.ActionSetup:
		return runSetup(ctx)
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
	// The picker's ctrl+l asks the configured model about the highlighted
	// app. Built once per visit (it reads the Keychain), and only when a
	// provider is configured so the picker stays quiet without AI.
	var assessor *tui.AppAssessor
	if c, _, err := loadConfig(); err == nil && c.LLM.Provider != "" {
		if a, err := buildAppAssessor(ctx, env, c, "", "", false, false); err == nil {
			assessor = &a
		} else {
			fmt.Fprintln(os.Stderr, "rootout: AI unavailable in the picker:", err)
		}
	}
	for {
		a, ok, err := tui.PickAppLoad(ctx, load, assessor)
		if err != nil {
			return tui.Flash{}, err
		}
		if !ok {
			return tui.Flash{}, nil
		}
		f, confirmed, err := uninstallOne(ctx, env, a, rep)
		if err != nil {
			return tui.Flash{}, err
		}
		if !confirmed {
			continue
		}
		return f, nil
	}
}

// uninstallOne plans one app's removal, shows the plan and removes what
// the user confirmed; confirmed is false when they backed out (a plan
// that cannot be built is reported on the flash and counts as confirmed,
// so the caller does not loop on it).
func uninstallOne(ctx context.Context, env fsx.Env, a app.App, rep progress.Reporter) (f tui.Flash, confirmed bool, err error) {
	p, err := plan.Build(ctx, env, a, plan.Options{Mode: plan.Trash, CrashReports: true, Run: run.Default, Progress: rep})
	if err != nil {
		f := errorFlash(err)
		f.Text = "could not plan " + a.Name + ": " + f.Text
		return f, true, nil
	}
	ok, err := tui.Confirmer{}.Confirm(ctx, p)
	if err != nil {
		return tui.Flash{}, false, err
	}
	if !ok {
		return tui.Flash{}, false, nil
	}
	res, err := plan.Execute(ctx, env, p, plan.Deps{Priv: priv.Options{}, Progress: rep})
	if res != nil {
		report(res)
	}
	if err != nil && !errors.Is(err, priv.ErrCancelled) {
		return tui.Flash{}, true, err
	}
	return removalFlash(a.Name, p, res, err), true, nil
}

// homeAssess opens the apps review screen and removes the ticked apps one
// by one through the usual plan and confirm.
func homeAssess(ctx context.Context, env fsx.Env) (tui.Flash, error) {
	rep := progress.Term(os.Stderr)
	apps, err := app.DiscoverWith(env, app.Options{Progress: rep})
	if err != nil {
		return tui.Flash{}, err
	}
	if len(apps) == 0 {
		return tui.Flash{Text: "No applications found"}, nil
	}
	c, _, err := loadConfig()
	if err != nil {
		return tui.Flash{}, err
	}
	assessor, err := buildAppAssessor(ctx, env, c, "", "", true, false)
	if errors.Is(err, tui.ErrQuit) {
		return tui.Flash{}, err
	}
	if isExit(err, exitDeclined) {
		return tui.Flash{Text: "Assess cancelled: no model chosen"}, nil
	}
	if err != nil {
		return tui.Flash{}, err
	}
	return reviewAndUninstallApps(ctx, env, apps, assessor, rep)
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
	assessor, err := buildAssessor(ctx, c, "", "", true, false)
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
