package llm

import "testing"

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
