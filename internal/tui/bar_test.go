package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestRenderBar(t *testing.T) {
	tests := []struct {
		name       string
		pct        float64
		full, pcts string
	}{
		{"zero", 0, "", "   0%"},
		{"half", 0.5, strings.Repeat(barFull, 5), "  50%"},
		{"full", 1, strings.Repeat(barFull, 10), " 100%"},
		{"clamp low", -3, "", "   0%"},
		{"clamp high", 7, strings.Repeat(barFull, 10), " 100%"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := renderBar(tc.pct, 10)
			if !strings.HasSuffix(out, tc.pcts) {
				t.Fatalf("renderBar(%v) = %q, want suffix %q", tc.pct, out, tc.pcts)
			}
			if n := strings.Count(out, barFull); n != strings.Count(tc.full, barFull) {
				t.Fatalf("renderBar(%v) has %d full cells, want %d", tc.pct, n, strings.Count(tc.full, barFull))
			}
			if n := strings.Count(out, barFull) + strings.Count(out, barEmpty); n != 10 {
				t.Fatalf("renderBar(%v) has %d cells, want 10", tc.pct, n)
			}
		})
	}
	if out := renderBar(0.5, 0); out != "  50%" {
		t.Fatalf("zero cells = %q", out)
	}
}

func TestRenderCount(t *testing.T) {
	detail := "/Users/me/Library/Application Support/com.example.very.long"
	out := renderCount(120, "scanning", 12, 47, detail)
	for _, want := range []string{"scanning ", " 12/47 ", detail, barFull, barEmpty} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderCount = %q, missing %q", out, want)
		}
	}
	if w := lipgloss.Width(out); w > 120 {
		t.Fatalf("width %d > 120", w)
	}

	narrow := renderCount(40, "scanning", 12, 47, detail)
	if w := lipgloss.Width(narrow); w > 40 {
		t.Fatalf("narrow width %d > 40: %q", w, narrow)
	}
	if !strings.Contains(narrow, "…") || strings.Contains(narrow, detail) {
		t.Fatalf("narrow detail not truncated: %q", narrow)
	}

	tiny := renderCount(10, "scanning", 1, 2, detail)
	if strings.Contains(tiny, "…") || !strings.Contains(tiny, "1/2") {
		t.Fatalf("tiny should drop detail entirely: %q", tiny)
	}

	if out := renderCount(40, "x", 0, 0, ""); strings.Count(out, barFull) != 0 || !strings.Contains(out, "0/0") {
		t.Fatalf("zero total = %q", out)
	}
}
