package tui

import (
	"context"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/llm"
)

// AppAssessFunc runs the configured model on one app's facts.
type AppAssessFunc func(ctx context.Context, f app.Facts) (llm.AppAssessment, error)

// AppAssessor is the AI the uninstall picker can ask about an app (ctrl+l),
// plus how to gather the facts it needs. It is the app twin of Assessor;
// see that type for the field semantics. The cmd package also uses it for
// `rootout assess`.
type AppAssessor struct {
	Provider string // "ollama" | "anthropic" | "openai"; "" = none configured
	Label    string // "anthropic · claude-sonnet-5-5"; "" = none configured
	// Inspect gathers the facts for an app (code signature, Gatekeeper,
	// origin, launch items); nil means Facts{App: a}, for tests.
	Inspect func(ctx context.Context, a app.App) app.Facts
	Assess  AppAssessFunc // nil = none
	// Cached answers from the saved assessments without a model call; nil
	// = nothing saved (local models, --no-cache).
	Cached func(f app.Facts) (llm.AppAssessment, bool)
}

// inspect runs Inspect or the test fallback.
func (a AppAssessor) inspect(ctx context.Context, x app.App) app.Facts {
	if a.Inspect == nil {
		return app.Facts{App: x}
	}
	return a.Inspect(ctx, x)
}
