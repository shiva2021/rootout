package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

var helpFixture = []keyHelp{
	{"↑/↓", "move"},
	{"space", "toggle"},
	{"a", "select all"},
	{"n", "select none"},
	{"enter", "continue"},
	{"esc", "quit"},
}

func TestRenderHelp(t *testing.T) {
	out := renderHelp(80, 24, "Keys", helpFixture)
	for _, k := range helpFixture {
		if !strings.Contains(out, k.key) || !strings.Contains(out, k.what) {
			t.Fatalf("help missing %q / %q:\n%s", k.key, k.what, out)
		}
	}
	for _, want := range []string{"Keys", helpClose, "╭", "╰"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q:\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 24 {
		t.Fatalf("got %d lines, want 24", len(lines))
	}
	// Box is centred: blank rows above and below.
	if strings.TrimSpace(lines[0]) != "" || strings.TrimSpace(lines[23]) != "" {
		t.Fatalf("box not centred vertically:\n%s", out)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > 80 {
			t.Fatalf("line %d is %d wide", i, w)
		}
	}
}

func TestRenderHelpSmall(t *testing.T) {
	out := renderHelp(40, 10, "Keys", helpFixture)
	lines := strings.Split(out, "\n")
	if len(lines) != 10 {
		t.Fatalf("got %d lines, want 10", len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > 40 {
			t.Fatalf("line %d is %d wide: %q", i, w, l)
		}
	}
	if !strings.Contains(out, "Keys") || !strings.Contains(out, helpClose) {
		t.Fatalf("small help lost title or close hint:\n%s", out)
	}
	// Does not panic at absurd sizes.
	_ = renderHelp(1, 1, "Keys", helpFixture)
	_ = renderHelp(0, 0, "Keys", nil)
}

func TestOverlay(t *testing.T) {
	base := make([]string, 10)
	for i := range base {
		base[i] = strings.Repeat("b", 40)
	}
	box := "┌──┐\n│hi│\n└──┘"
	out := overlay(strings.Join(base, "\n"), box, 40, 10)
	lines := strings.Split(out, "\n")
	if len(lines) != 10 {
		t.Fatalf("got %d lines, want 10", len(lines))
	}
	// Box is 3 tall → rows 3,4,5 replaced; all others untouched.
	for i, l := range lines {
		switch i {
		case 3, 4, 5:
			if !strings.Contains(l, "┌──┐") && !strings.Contains(l, "│hi│") && !strings.Contains(l, "└──┘") {
				t.Fatalf("line %d should hold the box: %q", i, l)
			}
			if strings.Contains(l, "b") {
				t.Fatalf("line %d still shows base behind box: %q", i, l)
			}
		default:
			if l != base[i] {
				t.Fatalf("line %d changed: %q", i, l)
			}
		}
		if w := lipgloss.Width(l); w > 40 {
			t.Fatalf("line %d is %d wide", i, w)
		}
	}
	if lines[4] != strings.Repeat(" ", 18)+"│hi│" {
		t.Fatalf("box not centred horizontally: %q", lines[4])
	}

	// Oversized box and short base degrade without panic or overflow.
	wide := strings.Repeat("x", 60) + "\n" + strings.Repeat("y", 60)
	out = overlay("one\ntwo", wide, 40, 10)
	lines = strings.Split(out, "\n")
	if len(lines) != 10 {
		t.Fatalf("short base: got %d lines", len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > 40 {
			t.Fatalf("wide box: line %d is %d wide", i, w)
		}
	}
}
