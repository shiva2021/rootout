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
	pickModel bool
	uninstall []string
	yes       bool
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
VSCodium and Windsurf. Extension code is never executed.

Each extension gets a 0-100 risk score with the reasons. The score separates
what an extension can do (spawn processes, eval) from evidence of intent
(hidden payloads, credential reads, exfiltration hosts). Only extensions at
or above --min-score (default 30) are shown; --all shows every one.

On a terminal the results open in a review screen where you can tick
extensions to uninstall; nothing is uninstalled until you confirm. --no-tui
prints the table instead. --online adds publisher verification and install
counts from the registry. --llm asks the configured AI model (Claude, OpenAI
or a local Ollama model) for a second opinion; it sees only the findings and
short snippets, never the whole extension.

--json prints one object per extension: the findings, the score and, with
--llm, the model's verdict. It never opens the review screen.`,
		Example: `  rootout scan                          # score extensions, review on screen
  rootout scan --all --no-tui           # print every extension and its score
  rootout scan --editor cursor          # only Cursor's extensions
  rootout scan --online                 # add publisher trust data
  rootout scan --llm                    # also ask the configured AI model
  rootout scan --provider ollama --model llama3.2 --json   # one-off model, JSON out`,
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
	fl.BoolVar(&f.pickModel, "pick-model", false, "choose a local Ollama model before scanning (terminal only)")
	fl.StringArrayVar(&f.uninstall, "uninstall", nil, "extension id to uninstall without the review screen (repeatable; asks for the typed word unless --yes)")
	fl.BoolVarP(&f.yes, "yes", "y", false, "do not ask for confirmation (only with --uninstall)")
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
	rep := stderrProgress(g)
	reports := heur.ScanAllWith(ctx, exts, heur.Limits{}, rep)
	if f.online {
		rep.Start("Checking registries", 0)
		meta, errs := registry.New().LookupAll(ctx, exts)
		rep.Done()
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
	if len(f.uninstall) > 0 {
		return uninstallByID(ctx, env, exts, f)
	}
	if f.yes {
		return &exitErr{code: exitBadInput, msg: "--yes only applies with --uninstall <id>"}
	}
	wantsAI := f.llm || f.provider != "" || f.model != "" || f.pickModel
	var assessor tui.Assessor
	if wantsAI || interactive {
		c, _, err := loadConfig()
		if err != nil {
			return err
		}
		if f.pickModel {
			if !interactive {
				return &exitErr{code: exitBadInput, msg: "--pick-model needs a terminal; use `rootout config models` and `--model <name>` in scripts"}
			}
			m, err := pickAndSaveModel(ctx)
			if err != nil {
				return err
			}
			f.provider, f.model = "ollama", m
			c.LLM.Provider, c.LLM.Ollama.Model = "ollama", m
		}
		// In the review screen the model is asked on demand; only build it
		// eagerly when --llm was requested or a provider is configured.
		if wantsAI || c.LLM.Provider != "" || interactive {
			assessor, err = buildAssessor(ctx, c, f.provider, f.model, interactive)
			if err != nil {
				return err
			}
		}
	}

	if interactive {
		return reviewAndUninstall(ctx, env, shown, assessor)
	}

	items := make([]scanItem, len(shown))
	for i, r := range shown {
		items[i].Report = r
		if assessor.Assess != nil && wantsAI {
			fmt.Fprintf(os.Stderr, "rootout: asking the model about %s...\n", r.Ext.ID)
			a, err := assessor.Assess(ctx, r)
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
func reviewAndUninstall(ctx context.Context, env fsx.Env, shown []heur.Report, a tui.Assessor) error {
	res, ok, err := tui.ReviewScan(ctx, shown, a)
	if err != nil || !ok || len(res.Uninstall) == 0 {
		return err
	}
	var targets []ext.Extension
	for _, r := range res.Uninstall {
		targets = append(targets, r.Ext)
	}
	return uninstallExtensions(ctx, env, targets)
}

// uninstallByID is the scripted path: --uninstall <id>... with the typed
// word on the terminal, or --yes.
func uninstallByID(ctx context.Context, env fsx.Env, exts []ext.Extension, f scanFlags) error {
	var targets []ext.Extension
	for _, id := range f.uninstall {
		found := false
		for _, e := range exts {
			if strings.EqualFold(e.ID, id) {
				targets = append(targets, e)
				found = true
			}
		}
		if !found {
			return &exitErr{code: exitBadInput, msg: fmt.Sprintf("no installed extension %q", id)}
		}
	}
	if f.yes {
		yesBanner(false)
	} else {
		var names []string
		for _, e := range targets {
			names = append(names, e.ID+" ("+e.Editor+")")
		}
		if !confirmTyped(os.Stdin, os.Stderr, "About to uninstall: "+strings.Join(names, ", ")) {
			return &exitErr{code: exitDeclined, msg: "nothing uninstalled"}
		}
	}
	return uninstallExtensions(ctx, env, targets)
}

func uninstallExtensions(ctx context.Context, env fsx.Env, targets []ext.Extension) error {
	editors := ext.Editors(env, ext.Options{})
	failed := 0
	for _, r := range targets {
		if err := ext.Uninstall(ctx, env, r, editors, ext.Options{}); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "rootout: %s: %v\n", r.ID, err)
			continue
		}
		fmt.Printf("uninstalled %s (%s)\n", r.ID, r.Editor)
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
