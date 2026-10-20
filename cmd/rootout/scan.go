package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/registry"
	"github.com/shiva2021/rootout/internal/tui"
)

type scanFlags struct {
	minScore  int
	online    bool
	editor    string
	listRules bool
	all       bool
	llm       bool
	provider  string
	model     string
	noTUI     bool
}

// scanItem is one JSON row: the heuristic report plus the model's opinion.
type scanItem struct {
	heur.Report
	Assessment *llm.Assessment `json:"assessment,omitempty"`
	AssessErr  string          `json:"assessment_error,omitempty"`
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
	fl.BoolVar(&f.llm, "llm", false, "ask the configured AI model about each shown extension")
	fl.StringVar(&f.provider, "provider", "", "AI provider for this run: anthropic, openai or ollama (implies --llm)")
	fl.StringVar(&f.model, "model", "", "model id for this run (implies --llm)")
	fl.BoolVar(&f.noTUI, "no-tui", false, "print results instead of opening the review screen")
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
		if g.json {
			return w.Emit([]scanItem{})
		}
		w.Printf("no extensions found\n")
		return nil
	}
	reports := heur.ScanAll(ctx, exts, heur.Limits{})
	if f.online {
		meta, errs := registry.New().LookupAll(ctx, exts)
		for i := range reports {
			if m := meta[reports[i].Ext.ID]; m != nil {
				reports[i] = heur.Rescore(ctx, reports[i], m)
			} else if e := errs[reports[i].Ext.ID]; e != nil {
				fmt.Fprintf(os.Stderr, "rootout: registry: %s: %v\n", reports[i].Ext.ID, e)
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

	interactive := !g.json && !f.noTUI && isTerminal(os.Stdin) && isTerminal(os.Stdout)
	var assess func(context.Context, heur.Report) (llm.Assessment, error)
	if f.llm || f.provider != "" || f.model != "" || interactive {
		c, _, err := loadConfig()
		if err != nil {
			return err
		}
		// In the review screen the model is asked on demand; only build it
		// eagerly when --llm was requested or a provider is configured.
		if f.llm || f.provider != "" || f.model != "" || c.LLM.Provider != "" {
			p, err := buildProvider(ctx, c, f.provider, f.model, interactive)
			if err != nil {
				return err
			}
			assess = assessFunc(p)
		}
	}

	if interactive {
		return reviewAndUninstall(ctx, env, shown, assess)
	}

	items := make([]scanItem, len(shown))
	for i, r := range shown {
		items[i].Report = r
		if assess != nil && (f.llm || f.provider != "" || f.model != "") {
			fmt.Fprintf(os.Stderr, "rootout: asking the model about %s...\n", r.Ext.ID)
			a, err := assess(ctx, r)
			if err != nil {
				items[i].AssessErr = err.Error()
			} else {
				items[i].Assessment = &a
			}
		}
	}
	return printReports(w, items, len(reports), min)
}

// reviewAndUninstall opens the results screen and uninstalls what the user ticked.
func reviewAndUninstall(ctx context.Context, env fsx.Env, shown []heur.Report, assess func(context.Context, heur.Report) (llm.Assessment, error)) error {
	res, ok, err := tui.ReviewScan(ctx, shown, assess)
	if err != nil || !ok || len(res.Uninstall) == 0 {
		return err
	}
	editors := ext.Editors(env, ext.Options{})
	failed := 0
	for _, r := range res.Uninstall {
		if err := ext.Uninstall(ctx, env, r.Ext, editors, ext.Options{}); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "rootout: %s: %v\n", r.Ext.ID, err)
			continue
		}
		fmt.Printf("uninstalled %s (%s)\n", r.Ext.ID, r.Ext.Editor)
	}
	if failed > 0 {
		return &exitErr{code: exitError, msg: fmt.Sprintf("%d extension(s) could not be uninstalled", failed)}
	}
	return nil
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

func printReports(w *output.Writer, items []scanItem, total, min int) error {
	t := output.Table{
		Columns: []string{"score", "extension", "editor", "version", "ai verdict", "top findings"},
		Rows:    items,
	}
	for _, it := range items {
		verdict := ""
		switch {
		case it.Assessment != nil:
			verdict = fmt.Sprintf("%s %d%%", it.Assessment.Verdict, int(it.Assessment.Confidence*100))
		case it.AssessErr != "":
			verdict = "error"
		}
		t.Cells = append(t.Cells, []string{
			fmt.Sprintf("%3d", it.Score), it.Ext.ID, it.Ext.Editor, it.Ext.Version, verdict, topFindings(it.Report, 3),
		})
	}
	if err := w.Print(t); err != nil {
		return err
	}
	w.Printf("\n%d of %d extensions at score >= %d\n", len(items), total, min)
	for _, it := range items {
		r := it.Report
		if it.Assessment != nil {
			w.Printf("\n%s: AI verdict %s (%d%%, %s)\n  %s\n", r.Ext.ID, it.Assessment.Verdict, int(it.Assessment.Confidence*100), it.Assessment.Model, it.Assessment.Reasoning)
		} else if it.AssessErr != "" {
			w.Printf("\n%s: AI assessment failed: %s\n", r.Ext.ID, it.AssessErr)
		}
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
