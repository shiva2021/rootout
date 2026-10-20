// Package llm asks a language model for a second opinion on an extension the
// heuristics flagged. The model sees only the extension's metadata, the
// findings and short code snippets (BuildPrompt), never the whole extension.
// Providers: Anthropic, OpenAI, Ollama (decisions D-004, D-013).
package llm

import (
	"context"
	"errors"
	"strings"

	"github.com/shiva2021/purge/internal/heur"
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
	Verdict    string   `json:"verdict"`    // benign | suspicious | malicious | unknown
	Confidence float64  `json:"confidence"` // 0..1
	Reasoning  string   `json:"reasoning"`  // two to five sentences
	IOCs       []string `json:"iocs"`       // hosts, paths, hashes the model considers indicators
	Provider   string   `json:"provider"`   // filled by the caller, not the model
	Model      string   `json:"model"`
}

// Provider is one model backend.
type Provider interface {
	Name() string  // "anthropic" | "openai" | "ollama" | "fake"
	Model() string // model id in use
	Assess(ctx context.Context, r heur.Report) (Assessment, error)
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
	a.Confidence = max(0, min(1, a.Confidence))
	if a.IOCs == nil {
		a.IOCs = []string{}
	}
	a.Provider, a.Model = provider, model
	return a
}
