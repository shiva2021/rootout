// Package llm asks a language model for a second opinion on an extension the
// heuristics flagged, or on an installed app. The model sees only the
// extension's metadata, the findings and short code snippets (BuildPrompt),
// never the whole extension; for an app it sees the locally gathered facts
// (BuildAppPrompt). Providers: Anthropic, OpenAI, Ollama (D-004, D-013).
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/heur"
)

// Verdict values the model may return.
const (
	Benign     = "benign"
	Suspicious = "suspicious"
	Malicious  = "malicious"
	Unknown    = "unknown"
)

// Assessment is the model's structured answer.
type Assessment struct {
	Verdict    string   `json:"verdict"`          // benign | suspicious | malicious | unknown
	Confidence float64  `json:"confidence"`       // 0..1
	Reasoning  string   `json:"reasoning"`        // two to five sentences
	IOCs       []string `json:"iocs"`             // hosts, paths, hashes the model considers indicators
	Cached     bool     `json:"cached,omitempty"` // answered from the saved assessments, not the model
	Provider   string   `json:"provider"`         // filled by the caller, not the model
	Model      string   `json:"model"`
}

// ConfidenceThreshold is the confidence from which a verdict is shown as
// settled (a tick or a cross in the scan list); below it the row is marked
// unsure whatever the verdict.
const ConfidenceThreshold = 0.70

// Confident reports whether the assessment's confidence reaches
// ConfidenceThreshold.
func (a Assessment) Confident() bool { return a.Confidence >= ConfidenceThreshold }

// ModelInfo is one entry of a hosted provider's model list, as shown by the
// model picker. Deprecated models are still callable but headed for
// retirement; retired ones are never listed.
type ModelInfo struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name,omitempty"`
	Created     time.Time `json:"created,omitempty"`
	Deprecated  bool      `json:"deprecated,omitempty"`
}

// Provider is one model backend: pure transport. Complete sends the system
// and user messages with a JSON schema the reply must follow and returns the
// reply text; it maps its own authentication, refusal and transport errors.
// Assess and AssessApp build the prompt, call Complete, decode and normalise.
type Provider interface {
	Name() string  // "anthropic" | "openai" | "ollama" | "fake"
	Model() string // model id in use
	Complete(ctx context.Context, system, user string, schema map[string]any) (string, error)
}

// Assess asks p about an extension report.
func Assess(ctx context.Context, p Provider, r heur.Report) (Assessment, error) {
	system, user := BuildPrompt(r, 0)
	text, err := p.Complete(ctx, system, user, Schema())
	if err != nil {
		return Assessment{}, err
	}
	a, err := Decode(text)
	if err != nil {
		return Assessment{}, fmt.Errorf("%s: %w", p.Name(), err)
	}
	return Normalize(a, p.Name(), p.Model()), nil
}

// AssessApp asks p about an installed app.
func AssessApp(ctx context.Context, p Provider, f app.Facts) (AppAssessment, error) {
	system, user := BuildAppPrompt(f)
	text, err := p.Complete(ctx, system, user, AppSchema())
	if err != nil {
		return AppAssessment{}, err
	}
	a, err := DecodeApp(text)
	if err != nil {
		return AppAssessment{}, fmt.Errorf("%s: %w", p.Name(), err)
	}
	return NormalizeApp(a, p.Name(), p.Model()), nil
}

var (
	// ErrNoKey means the provider needs an API key and none was found in the
	// Keychain or the environment.
	ErrNoKey = errors.New("llm: no API key configured")
	// ErrBadResponse means the model's reply did not parse as an Assessment.
	ErrBadResponse = errors.New("llm: response is not a valid assessment")
)

// Schema is the JSON schema every provider asks the model to follow. Kept as
// a map so each SDK can take it directly.
func Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"verdict", "confidence", "reasoning", "iocs"},
		"properties": map[string]any{
			"verdict":    map[string]any{"type": "string", "enum": []string{Benign, Suspicious, Malicious, Unknown}},
			"confidence": map[string]any{"type": "number"},
			"reasoning":  map[string]any{"type": "string"},
			"iocs":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}

// Normalize clamps confidence, lower-cases and validates the verdict, and
// fills Provider/Model. Unknown verdict strings become Unknown.
func Normalize(a Assessment, provider, model string) Assessment {
	a.Verdict = strings.ToLower(strings.TrimSpace(a.Verdict))
	switch a.Verdict {
	case Benign, Suspicious, Malicious, Unknown:
	default:
		a.Verdict = Unknown
	}
	if a.Confidence >= 10 && a.Confidence <= 100 {
		a.Confidence /= 100 // models without a schema often answer in percent
	}
	a.Confidence = max(0, min(1, a.Confidence))
	if a.IOCs == nil {
		a.IOCs = []string{}
	}
	a.Provider, a.Model = provider, model
	return a
}
