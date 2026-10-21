package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/priv"
	"github.com/shiva2021/rootout/internal/progress"
	"github.com/shiva2021/rootout/internal/run"
)

type orphanFlags struct {
	dryRun, permanent, yes, includeRecent, sudo bool
	minAge                                      time.Duration
	remove                                      []string
}

func newOrphansCmd(g *globalFlags) *cobra.Command {
	var f orphanFlags
	cmd := &cobra.Command{
		Use:   "orphans",
		Short: "Find files left behind by apps that are no longer installed",
		Long: `Look through ~/Library for preferences, caches, containers and other files
whose names carry a bundle id that no installed app owns: the remains of apps
removed by dragging them to the Trash.

Detection is deliberately cautious. Only names that look like bundle ids
(com.vendor.app) are considered, never plain names. An id is kept if any
installed app owns it or a prefix of it, if Apple owns it, if any app bundle
anywhere on disk carries it, or if a loaded launchd job or running process
uses it. Groups changed in the last 30 days are marked "recently used".

Nothing is pre-selected. In the review screen you tick what to remove and
type delete. Removal goes to the Trash unless --permanent. Installer receipts
are reported but not removed in this version.`,
		Example: `  rootout orphans                    # review candidates, tick, type delete
  rootout orphans --dry-run          # list candidates and sizes, change nothing
  rootout orphans --json             # the same as JSON
  rootout orphans --min-age 90d      # mark anything used in the last 90 days
  rootout orphans --remove com.oldvendor.oldapp --yes   # scripted removal of one group`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOrphans(cmd.Context(), g, f)
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&f.dryRun, "dry-run", "n", false, "list candidates and exit")
	fl.BoolVar(&f.permanent, "permanent", false, "delete outright instead of moving to the Trash")
	fl.BoolVarP(&f.yes, "yes", "y", false, "do not ask for confirmation (requires --remove)")
	fl.BoolVar(&f.includeRecent, "include-recent", false, "allow ticking groups marked recently used")
	fl.BoolVar(&f.sudo, "sudo", false, "use sudo instead of the graphical admin prompt")
	fl.DurationVar(&f.minAge, "min-age", 30*24*time.Hour, "groups changed more recently than this are marked recently used")
	fl.StringArrayVar(&f.remove, "remove", nil, "owner id to remove without the review screen (repeatable; needs --yes or a typed confirmation)")
	return cmd
}

func runOrphans(ctx context.Context, g *globalFlags, f orphanFlags) error {
	env, err := fsx.Real()
	if err != nil {
		return err
	}
	if f.yes && len(f.remove) == 0 {
		return &exitErr{code: exitBadInput, msg: "--yes needs --remove <owner-id>; nothing is removed without an explicit choice"}
	}
	interactive := !g.json && isTerminal(os.Stdin) && isTerminal(os.Stdout)
	rep := stderrProgress(g)
	orphans, err := findOrphans(ctx, env, f, rep)
	if err != nil {
		return err
	}

	w := output.New(os.Stdout, g.json)
	if f.dryRun || (!interactive && len(f.remove) == 0) {
		return printOrphans(w, orphans)
	}

	p := orphanPlan(orphans, f)
	plan.MarkAccess(env, p)
	if len(p.Items) == 0 {
		w.Printf("no orphaned leftovers found\n")
		return nil
	}

	var ok bool
	switch {
	case len(f.remove) > 0:
		if err := selectOwners(p, orphans, f.remove); err != nil {
			return err
		}
		if f.yes {
			yesBanner(f.permanent)
			ok = true
		} else {
			ok, err = plan.Prompt{In: os.Stdin, Out: os.Stderr}.Confirm(ctx, p)
		}
	default:
		ok, err = tuiConfirmer().Confirm(ctx, p)
	}
	if err != nil {
		return declinedOnQuit(err, "nothing removed")
	}
	if !ok || len(p.Selected()) == 0 {
		return &exitErr{code: exitDeclined, msg: "nothing removed"}
	}

	res, err := plan.Execute(ctx, env, p, plan.Deps{
		Priv:     priv.Options{UseSudo: f.sudo},
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
			return &exitErr{code: exitDeclined, msg: "admin prompt cancelled"}
		}
		return err
	}
	if !res.OK() {
		return &exitErr{code: exitError, msg: "some paths could not be removed"}
	}
	return nil
}

// findOrphans discovers the installed apps and the leftovers none of them own.
func findOrphans(ctx context.Context, env fsx.Env, f orphanFlags, rep progress.Reporter) ([]leftover.Orphan, error) {
	if f.minAge == 0 {
		f.minAge = 30 * 24 * time.Hour
	}
	installed, err := app.DiscoverWith(env, app.Options{Progress: rep, SkipSizes: true})
	if err != nil {
		return nil, err
	}
	return leftover.FindOrphans(ctx, env, leftover.OrphanOptions{
		Installed:    installed,
		RecentWindow: f.minAge,
		Run:          run.Default,
		Progress:     rep,
	})
}

// orphanPlan turns the candidates into an unselected plan grouped by owner.
// Receipts are dropped (report-only in this version); recent groups are kept
// but their items carry a note so the screen can tag them.
func orphanPlan(orphans []leftover.Orphan, f orphanFlags) *plan.Plan {
	mode := plan.Trash
	if f.permanent {
		mode = plan.Permanent
	}
	var items []plan.Item
	for _, o := range orphans {
		for _, l := range o.Items {
			if l.Kind == leftover.Receipt {
				continue
			}
			if o.Recent {
				note := "recently used (" + humanAge(o.LastUsed) + ")"
				if !f.includeRecent {
					note += "; pass --include-recent to tick"
				}
				l.Note = note
			}
			items = append(items, plan.Item{Leftover: l, Group: o.Owner})
		}
	}
	return plan.New("Orphaned leftovers", items, mode, true)
}

// selectOwners ticks every item of the given owner ids for --remove.
func selectOwners(p *plan.Plan, orphans []leftover.Orphan, owners []string) error {
	known := map[string]bool{}
	for _, o := range orphans {
		known[strings.ToLower(o.Owner)] = true
	}
	for _, want := range owners {
		if !known[strings.ToLower(want)] {
			return &exitErr{code: exitBadInput, msg: fmt.Sprintf("%q is not an orphan candidate (see rootout orphans --dry-run)", want)}
		}
	}
	for i := range p.Items {
		for _, want := range owners {
			if strings.EqualFold(p.Items[i].Group, want) {
				p.Items[i].Selected = true
			}
		}
	}
	return nil
}

func printOrphans(w *output.Writer, orphans []leftover.Orphan) error {
	t := output.Table{
		Columns: []string{"owner", "paths", "size", "last used", "note"},
		Rows:    orphans,
	}
	var total int64
	for _, o := range orphans {
		note := strings.Join(o.Reasons, "; ")
		if o.Recent {
			note = "recently used (" + humanAge(o.LastUsed) + ")"
		}
		t.Cells = append(t.Cells, []string{o.Owner, fmt.Sprint(len(o.Items)), output.Bytes(o.SizeBytes), o.LastUsed.Format("2006-01-02"), note})
		total += o.SizeBytes
	}
	if err := w.Print(t); err != nil {
		return err
	}
	w.Printf("\n%d orphaned owners, %s\n", len(orphans), output.Bytes(total))
	for _, o := range orphans {
		w.Printf("\n%s\n", o.Owner)
		for _, l := range o.Items {
			w.Printf("  %-9s %s  [%s]\n", output.Bytes(l.SizeBytes), l.Path, l.Kind)
		}
	}
	return nil
}

func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 48*time.Hour:
		return "today or yesterday"
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%d months ago", int(d.Hours()/24/30))
	}
}
