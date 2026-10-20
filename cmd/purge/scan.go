package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shiva2021/purge/internal/ext"
	"github.com/shiva2021/purge/internal/fsx"
	"github.com/shiva2021/purge/internal/heur"
	"github.com/shiva2021/purge/internal/output"
	"github.com/shiva2021/purge/internal/registry"
)

type scanFlags struct {
	minScore  int
	online    bool
	editor    string
	listRules bool
	all       bool
}

func newScanCmd(g *globalFlags) *cobra.Command {
	var f scanFlags
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan editor extensions for malicious behaviour",
		Long: `Statically scan the extensions installed in VS Code, VS Code Insiders, Cursor,
VSCodium and Windsurf. Nothing is executed. Each extension gets a 0-100 risk
score with the reasons; --online adds publisher trust data from the registry.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runScan(cmd.Context(), g, f)
		},
	}
	fl := cmd.Flags()
	fl.IntVar(&f.minScore, "min-score", 30, "only show extensions at or above this score (0 shows all)")
	fl.BoolVar(&f.online, "online", false, "look up publisher verification and install counts")
	fl.StringVar(&f.editor, "editor", "", "limit to one editor id: vscode, vscode-insiders, cursor, vscodium, windsurf")
	fl.BoolVar(&f.listRules, "list-rules", false, "print the rule names and exit")
	fl.BoolVarP(&f.all, "all", "a", false, "shorthand for --min-score 0")
	return cmd
}

func runScan(ctx context.Context, g *globalFlags, f scanFlags) error {
	w := output.New(os.Stdout, g.json)
	if f.listRules {
		files, exts := heur.Rules()
		return w.Emit(map[string][]string{"file_rules": files, "extension_rules": exts})
	}
	env, err := fsx.Real()
	if err != nil {
		return err
	}
	exts, err := ext.Enumerate(env, ext.Options{})
	if err != nil {
		return err
	}
	if f.editor != "" {
		exts = filterEditor(exts, f.editor)
	}
	if len(exts) == 0 {
		w.Printf("no extensions found\n")
		return w.Emit([]heur.Report{})
	}
	reports := heur.ScanAll(ctx, exts, heur.Limits{})
	if f.online {
		meta, errs := registry.New().LookupAll(ctx, exts)
		for i := range reports {
			if m := meta[reports[i].Ext.ID]; m != nil {
				reports[i] = heur.Rescore(ctx, reports[i], m)
			} else if e := errs[reports[i].Ext.ID]; e != nil {
				fmt.Fprintf(os.Stderr, "purge: registry: %s: %v\n", reports[i].Ext.ID, e)
			}
		}
	}
	sort.SliceStable(reports, func(i, j int) bool { return reports[i].Score > reports[j].Score })
	min := f.minScore
	if f.all {
		min = 0
	}
	shown := reports[:0:0]
	for _, r := range reports {
		if r.Score >= min {
			shown = append(shown, r)
		}
	}
	return printReports(w, shown, len(reports), min)
}

func filterEditor(exts []ext.Extension, id string) []ext.Extension {
	out := exts[:0:0]
	for _, e := range exts {
		if e.Editor == id {
			out = append(out, e)
		}
	}
	return out
}

func printReports(w *output.Writer, reports []heur.Report, total, min int) error {
	t := output.Table{
		Columns: []string{"score", "extension", "editor", "version", "top findings"},
		Rows:    reports,
	}
	for _, r := range reports {
		t.Cells = append(t.Cells, []string{
			fmt.Sprintf("%3d", r.Score), r.Ext.ID, r.Ext.Editor, r.Ext.Version, topFindings(r, 3),
		})
	}
	if err := w.Print(t); err != nil {
		return err
	}
	w.Printf("\n%d of %d extensions at score >= %d\n", len(reports), total, min)
	for _, r := range reports {
		if r.Score < 50 {
			continue
		}
		w.Printf("\n%s (%d)\n", r.Ext.ID, r.Score)
		for _, fd := range r.Findings {
			if fd.Severity < heur.Medium {
				continue
			}
			loc := ""
			if fd.File != "" {
				loc = fmt.Sprintf(" %s:%d", fd.File, fd.Line)
			}
			w.Printf("  [%s] %s%s\n      %s\n", fd.Severity, fd.Rule, loc, fd.Detail)
		}
	}
	return nil
}

// topFindings lists up to n distinct rule names, intent indicators before
// capabilities, each group in severity order.
func topFindings(r heur.Report, n int) string {
	seen := map[string]bool{}
	var out []string
	for _, wantCap := range []bool{false, true} {
		for _, fd := range r.Findings {
			if len(out) == n {
				return strings.Join(out, ", ")
			}
			if fd.Severity == heur.Info || seen[fd.Rule] || heur.IsCapability(fd.Rule) != wantCap {
				continue
			}
			seen[fd.Rule] = true
			out = append(out, fd.Rule)
		}
	}
	return strings.Join(out, ", ")
}
