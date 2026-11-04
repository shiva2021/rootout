package llm

import (
	"context"
	"encoding/json"
	"sync"
)

// Fake is a Provider for tests: it records each user message and answers
// with Result or AppResult (whichever the schema asks for) as JSON, or Err.
type Fake struct {
	Result    Assessment
	AppResult AppAssessment
	Err       error
	Calls     []string // user messages, in order

	mu sync.Mutex
}

// Name implements Provider.
func (f *Fake) Name() string { return "fake" }

// Model implements Provider.
func (f *Fake) Model() string { return "fake" }

// Complete implements Provider. The extension schema is the one with a
// "verdict" property; anything else gets AppResult.
func (f *Fake) Complete(ctx context.Context, _, user string, schema map[string]any) (string, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, user)
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if f.Err != nil {
		return "", f.Err
	}
	var v any = f.AppResult
	if props, _ := schema["properties"].(map[string]any); props != nil {
		if _, ok := props["verdict"]; ok {
			v = f.Result
		}
	}
	out, err := json.Marshal(v)
	return string(out), err
}
