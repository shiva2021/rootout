package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/heur"
)

func TestConfident(t *testing.T) {
	tests := []struct {
		confidence float64
		want       bool
	}{
		{0.69, false},
		{0.699, false},
		{0.70, true},
		{0.71, true},
		{1, true},
		{0, false},
	}
	for _, tt := range tests {
		if got := (Assessment{Confidence: tt.confidence}).Confident(); got != tt.want {
			t.Errorf("Confident(%v) = %v, want %v", tt.confidence, got, tt.want)
		}
	}
}

// textProvider answers every Complete with a fixed text.
type textProvider struct{ text string }

func (textProvider) Name() string  { return "stub" }
func (textProvider) Model() string { return "m" }
func (p textProvider) Complete(context.Context, string, string, map[string]any) (string, error) {
	return p.text, nil
}

func TestAssessWrapsDecodeErrors(t *testing.T) {
	p := textProvider{text: "no json here"}
	if _, err := Assess(context.Background(), p, heur.Report{}); !errors.Is(err, ErrBadResponse) || !strings.HasPrefix(err.Error(), "stub: ") {
		t.Errorf("Assess err = %v", err)
	}
	if _, err := AssessApp(context.Background(), p, app.Facts{}); !errors.Is(err, ErrBadResponse) || !strings.HasPrefix(err.Error(), "stub: ") {
		t.Errorf("AssessApp err = %v", err)
	}
}
