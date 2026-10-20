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
		return Assessment{}, fmt.Errorf("%w: %w", ErrBadResponse, err)
	}
	if a.Verdict == "" {
		return Assessment{}, fmt.Errorf("%w: missing verdict", ErrBadResponse)
	}
	return a, nil
}
