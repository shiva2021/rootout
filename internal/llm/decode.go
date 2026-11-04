package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Decode parses a model's JSON reply into an Assessment. Any failure wraps
// ErrBadResponse. Callers still run Normalize on the result.
func Decode(text string) (Assessment, error) {
	return decode(text, func(a Assessment) string {
		if a.Verdict == "" {
			return "verdict"
		}
		return ""
	})
}

// decode is the shared parser: the reply as a whole, else the outermost
// {...} (models without a server-side schema wrap the object in a code
// fence or a sentence). missing names a required field the reply lacks.
func decode[T any](text string, missing func(T) string) (T, error) {
	var zero, a T
	text = strings.TrimSpace(text)
	if text == "" {
		return zero, fmt.Errorf("%w: empty reply", ErrBadResponse)
	}
	if err := json.Unmarshal([]byte(text), &a); err != nil {
		obj := extractObject(text)
		if obj == "" {
			return zero, fmt.Errorf("%w: %w", ErrBadResponse, err)
		}
		if err2 := json.Unmarshal([]byte(obj), &a); err2 != nil {
			return zero, fmt.Errorf("%w: %w", ErrBadResponse, err2)
		}
	}
	if field := missing(a); field != "" {
		return zero, fmt.Errorf("%w: missing %s", ErrBadResponse, field)
	}
	return a, nil
}

// clampConfidence maps a percent answer (10..100) to a fraction and clamps
// to 0..1; models without a schema often answer in percent.
func clampConfidence(c float64) float64 {
	if c >= 10 && c <= 100 {
		c /= 100
	}
	return max(0, min(1, c))
}

// extractObject returns the text between the first '{' and the last '}', or "".
func extractObject(text string) string {
	i := strings.IndexByte(text, '{')
	j := strings.LastIndexByte(text, '}')
	if i < 0 || j <= i {
		return ""
	}
	return text[i : j+1]
}
