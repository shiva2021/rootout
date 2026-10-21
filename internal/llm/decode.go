package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Decode parses a model's JSON reply into an Assessment. Any failure wraps
// ErrBadResponse. Callers still run Normalize on the result.
func Decode(text string) (Assessment, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Assessment{}, fmt.Errorf("%w: empty reply", ErrBadResponse)
	}
	var a Assessment
	if err := json.Unmarshal([]byte(text), &a); err != nil {
		// Without a server-side schema the model may wrap the object in a
		// code fence or a sentence; take the outermost {...}.
		obj := extractObject(text)
		if obj == "" {
			return Assessment{}, fmt.Errorf("%w: %w", ErrBadResponse, err)
		}
		if err2 := json.Unmarshal([]byte(obj), &a); err2 != nil {
			return Assessment{}, fmt.Errorf("%w: %w", ErrBadResponse, err2)
		}
	}
	if a.Verdict == "" {
		return Assessment{}, fmt.Errorf("%w: missing verdict", ErrBadResponse)
	}
	return a, nil
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
