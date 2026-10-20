package llm

import (
	"context"
	"sync"

	"github.com/shiva2021/rootout/internal/heur"
)

// Fake is a Provider for tests: it records each report and returns Result
// (normalised) or Err.
type Fake struct {
	Result Assessment
	Err    error
	Calls  []heur.Report

	mu sync.Mutex
}

// Name implements Provider.
func (f *Fake) Name() string { return "fake" }

// Model implements Provider.
func (f *Fake) Model() string { return "fake" }

// Assess implements Provider.
func (f *Fake) Assess(ctx context.Context, r heur.Report) (Assessment, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, r)
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Assessment{}, err
	}
	if f.Err != nil {
		return Assessment{}, f.Err
	}
	return Normalize(f.Result, f.Name(), f.Model()), nil
}
