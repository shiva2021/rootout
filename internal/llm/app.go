package llm

import "strings"

// Source values the model may return for where an app comes from.
const (
	SourceLegitimate = "legitimate"
	SourceSuspicious = "suspicious"
	SourceUnknown    = "unknown"
)

// Recommendation values: keep, drop, or review (the model cannot settle it).
const (
	Keep   = "keep"
	Drop   = "drop"
	Review = "review"
)

// AppAssessment is the model's structured answer about an installed app.
type AppAssessment struct {
	Purpose        string  `json:"purpose"`          // one plain sentence
	Source         string  `json:"source"`           // legitimate | suspicious | unknown
	Confidence     float64 `json:"confidence"`       // 0..1
	Recommendation string  `json:"recommendation"`   // keep | drop | review
	Reasoning      string  `json:"reasoning"`        // two to five sentences
	Cached         bool    `json:"cached,omitempty"` // answered from the saved assessments, not the model
	Provider       string  `json:"provider"`         // filled by the caller, not the model
	Model          string  `json:"model"`
}

// Confident reports whether the assessment's confidence reaches
// ConfidenceThreshold.
func (a AppAssessment) Confident() bool { return a.Confidence >= ConfidenceThreshold }

// AppSchema is the JSON schema every provider asks the model to follow for
// an app. It has no "verdict" property, which is how Fake tells it from
// Schema.
func AppSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"purpose", "source", "confidence", "recommendation", "reasoning"},
		"properties": map[string]any{
			"purpose":        map[string]any{"type": "string"},
			"source":         map[string]any{"type": "string", "enum": []string{SourceLegitimate, SourceSuspicious, SourceUnknown}},
			"confidence":     map[string]any{"type": "number"},
			"recommendation": map[string]any{"type": "string", "enum": []string{Keep, Drop, Review}},
			"reasoning":      map[string]any{"type": "string"},
		},
	}
}

// NormalizeApp clamps confidence, lower-cases and validates the enums (an
// unknown source becomes unknown, an unknown recommendation becomes
// review), keeps the purpose on one line and fills Provider/Model.
func NormalizeApp(a AppAssessment, provider, model string) AppAssessment {
	a.Source = strings.ToLower(strings.TrimSpace(a.Source))
	switch a.Source {
	case SourceLegitimate, SourceSuspicious, SourceUnknown:
	default:
		a.Source = SourceUnknown
	}
	a.Recommendation = strings.ToLower(strings.TrimSpace(a.Recommendation))
	switch a.Recommendation {
	case Keep, Drop, Review:
	default:
		a.Recommendation = Review
	}
	a.Confidence = clampConfidence(a.Confidence)
	a.Purpose = strings.Join(strings.Fields(a.Purpose), " ")
	a.Provider, a.Model = provider, model
	return a
}

// DecodeApp parses a model's JSON reply into an AppAssessment. Any failure
// wraps ErrBadResponse. Callers still run NormalizeApp on the result.
func DecodeApp(text string) (AppAssessment, error) {
	return decode(text, func(a AppAssessment) string {
		if a.Recommendation == "" {
			return "recommendation"
		}
		return ""
	})
}
