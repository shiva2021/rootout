package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/llm"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/tui"
)

type assessFlags struct {
	all       bool
	pickModel bool
	noCache   bool
	provider  string
	model     string
}

// assessItem is one JSON row: the app, what rootout found out about it and
// the model's answer.
type assessItem struct {
	Facts      app.Facts          `json:"facts"` // carries the app itself
	Assessment *llm.AppAssessment `json:"assessment,omitempty"`
	AssessErr  string             `json:"assessment_error,omitempty"`
}

func newAssessCmd(g *globalFlags) *cobra.Command {
	var f assessFlags
	cmd := &cobra.Command{
		Use:   "assess [app]",
		Short: "Ask the AI what an installed app is and whether to keep it",
		Long: `Gather the facts about an installed application (code signature and Team ID,
Gatekeeper's verdict, where it was downloaded from, App Store receipt, launch
agents and daemons, the permissions it declares) and ask the configured AI
model what the app is for, whether its source is legitimate, how confident it
is and whether to keep or drop it. The recommendation is advice: nothing is
removed unless you choose to, and every removal still shows its files first.

Name an app to assess just that one; --all assesses every app (every app's
facts go to the model, so it is an explicit choice). On a terminal the same
assessment is a keypress away: in "rootout", choose Uninstall an app and press
ctrl+l on the highlighted app to see its facts and the answer before deciding.

The facts come from local commands; spctl may contact Apple's notary service.
App metadata, signature facts and download URLs reach a hosted provider only
when you ask. Answers from Claude or OpenAI are saved under ~/.cache/rootout
and reused for the same app version, provider, model and facts; --no-cache
neither reads nor writes them (and so asks again) and "rootout config cache
clear" forgets them. Local Ollama answers are never saved. Without a
configured model the facts are still shown.

--json prints one object per app: the app, the facts and, when a model is
configured, the answer ("cached": true when it was a saved answer).`,
		Example: `  rootout assess Spotify                # one app: facts and the AI's answer
  rootout assess Spotify --json         # the same for scripts
  rootout assess --all                  # every app, as a table
  rootout assess --provider ollama --model llama3.2 Spotify   # one-off model`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			return runAssess(cmd.Context(), g, f, arg)
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&f.all, "all", "a", false, "assess every installed app")
	fl.BoolVar(&f.pickModel, "pick-model", false, "choose a local Ollama model first (terminal only)")
	fl.BoolVar(&f.noCache, "no-cache", false, "neither read nor save answers from Claude/OpenAI for this run")
	fl.StringVar(&f.provider, "provider", "", "AI provider for this run: anthropic, openai or ollama")
	fl.StringVar(&f.model, "model", "", "model id for this run")
	return cmd
}

func runAssess(ctx context.Context, g *globalFlags, f assessFlags, arg string) error {
	if f.pickModel && (g.json || !isTerminal(os.Stdin) || !isTerminal(os.Stdout)) {
		return &exitErr{code: exitBadInput, msg: "--pick-model needs a terminal; use `rootout config models` and `--model <name>` in scripts"}
	}
	if arg == "" && !f.all {
		return &exitErr{code: exitBadInput, msg: "name an app or pass --all (the uninstall picker's ctrl+l asks about one app on screen)"}
	}
	c, _, err := loadConfig()
	if err != nil {
		return err
	}
	if f.pickModel {
		m, err := pickAndSaveModel(ctx)
		if err != nil {
			return declinedOnQuit(err, "no model chosen")
		}
		f.provider, f.model = "ollama", m
		c.LLM.Provider, c.LLM.Ollama.Model = "ollama", m
	}
	env, err := fsx.Real()
	if err != nil {
		return err
	}
	rep := stderrProgress(g)
	apps, err := app.DiscoverWith(env, app.Options{Progress: rep})
	if err != nil {
		return err
	}
	w := output.New(os.Stdout, g.json)
	a, err := buildAppAssessor(ctx, env, c, f.provider, f.model, f.noCache)
	if err != nil {
		return err
	}
	if arg != "" {
		target, err := resolveApp(apps, arg)
		if err != nil {
			return err
		}
		return printAssessItems(w, []assessItem{assessOne(ctx, a, target)})
	}
	return printAssessItems(w, assessApps(ctx, a, apps))
}

// resolveApp finds the app arg names (exact bundle id, exact name, then
// fuzzy), mapping the ambiguous and not-found cases to exit 2.
func resolveApp(apps []app.App, arg string) (app.App, error) {
	target, err := app.Resolve(apps, arg)
	if err == nil {
		return target, nil
	}
	var amb *app.AmbiguousError
	if errors.As(err, &amb) {
		fmt.Fprintf(os.Stderr, "rootout: %q matches several apps:\n", arg)
		for _, m := range amb.Candidates {
			fmt.Fprintf(os.Stderr, "  %s (%s)\n", m.App.Name, m.App.BundleID)
		}
		return app.App{}, &exitErr{code: exitBadInput, msg: "be more specific, or use the bundle id"}
	}
	return app.App{}, &exitErr{code: exitBadInput, msg: fmt.Sprintf("no application matches %q (try: rootout list --search %q)", arg, arg)}
}

// assessOne gathers the facts and, when a model is configured, its answer:
// the saved one first, else a call.
func assessOne(ctx context.Context, a tui.AppAssessor, target app.App) assessItem {
	facts := app.Facts{App: target}
	if a.Inspect != nil {
		facts = a.Inspect(ctx, target)
	}
	item := assessItem{Facts: facts}
	if a.Assess == nil {
		return item
	}
	if a.Cached != nil {
		if r, ok := a.Cached(facts); ok {
			fmt.Fprintf(os.Stderr, "rootout: %s: using the saved assessment\n", target.Name)
			item.Assessment = &r
			return item
		}
	}
	fmt.Fprintf(os.Stderr, "rootout: asking the model about %s...\n", target.Name)
	r, err := a.Assess(ctx, facts)
	if err != nil {
		item.AssessErr = err.Error()
	} else {
		item.Assessment = &r
	}
	return item
}

// assessApps is assessOne for every removable app, in list order.
func assessApps(ctx context.Context, a tui.AppAssessor, apps []app.App) []assessItem {
	var items []assessItem
	for _, x := range apps {
		if x.Protected {
			continue
		}
		items = append(items, assessOne(ctx, a, x))
	}
	return items
}

// printAssessItems renders the table (or the JSON array) and, after it,
// the facts and the model's reasoning for every app.
func printAssessItems(w *output.Writer, items []assessItem) error {
	t := output.Table{
		Columns: []string{"app", "version", "source", "ai", "purpose"},
		Rows:    items,
	}
	for _, it := range items {
		ai, purpose := "", ""
		switch {
		case it.Assessment != nil:
			ai = fmt.Sprintf("%s %.0f%%", it.Assessment.Recommendation, it.Assessment.Confidence*100)
			purpose = it.Assessment.Purpose
		case it.AssessErr != "":
			ai = "error"
		}
		t.Cells = append(t.Cells, []string{it.Facts.App.Name, it.Facts.App.Version, it.Facts.Source(), ai, purpose})
	}
	if err := w.Print(t); err != nil {
		return err
	}
	for _, it := range items {
		a := it.Facts.App
		id := a.BundleID
		if id == "" {
			id = "no bundle id"
		}
		w.Printf("\n%s (%s)\n", a.Name, id)
		for _, line := range it.Facts.Summary() {
			w.Printf("  %s: %s\n", strings.ToLower(line.Label), line.Value)
		}
		switch {
		case it.Assessment != nil:
			r := it.Assessment
			model := r.Model
			if r.Cached {
				model += ", saved"
			}
			w.Printf("  AI: %s (%.0f%%, %s) · source %s\n", strings.ToUpper(r.Recommendation), r.Confidence*100, model, r.Source)
			if r.Purpose != "" {
				w.Printf("    %s\n", r.Purpose)
			}
			if r.Reasoning != "" {
				w.Printf("    %s\n", r.Reasoning)
			}
		case it.AssessErr != "":
			w.Printf("  AI assessment failed: %s\n", it.AssessErr)
		}
	}
	return nil
}
