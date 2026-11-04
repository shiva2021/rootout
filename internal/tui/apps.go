package tui

import (
	"context"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/llm"
)

// AppAssessFunc runs the configured model on one app's facts.
type AppAssessFunc func(ctx context.Context, f app.Facts) (llm.AppAssessment, error)

// AppAssessor is the AI the apps screen and the picker can ask about an
// app, plus how to gather the facts it needs and how to swap its model. It
// mirrors Assessor for extensions; see that type for the field semantics.
type AppAssessor struct {
	Provider string // "ollama" | "anthropic" | "openai"; "" = none configured
	Label    string // "anthropic · claude-sonnet-5-5"; "" = none configured
	// Inspect gathers the facts for an app (code signature, Gatekeeper,
	// origin, launch items); nil means Facts{App: a}, for tests.
	Inspect  func(ctx context.Context, a app.App) app.Facts
	Assess   AppAssessFunc // nil = none
	Reassess AppAssessFunc // asks ignoring saved answers; nil falls back to Assess
	// Cached answers from the saved assessments without a model call; nil
	// = nothing saved (local models, --no-cache).
	Cached       func(f app.Facts) (llm.AppAssessment, bool)
	Models       *ModelDeps
	HostedModels func(ctx context.Context) ([]llm.ModelInfo, error)
	OnModel      func(ctx context.Context, provider, model string) (AppAssessor, error)
}
