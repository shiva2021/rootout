package heur

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// FindAround must give exactly what FindAllSubmatchIndex gives whenever every
// match contains one of the literals, including at window edges, after a
// rune that bytes.ToLower would resize, and when the literal is so common
// that the whole-file fallback kicks in.
func TestFindAround(t *testing.T) {
	filler := strings.Repeat("x", 100)
	tests := []struct {
		name   string
		re     string
		data   string
		window int
		hits   int
		lits   []string
	}{
		{"lead-in group", `(^|[^\w$])eval\s*\(`, "eval(1); myeval(2); a=eval (3)" + filler + "eval(4)", 32, 100, []string{"eval"}},
		{"edge artefact: ^ and \\b at a window cut", `(^|[^\w$])eval\s*\(`, "xeval(" + strings.Repeat("y", 32) + "eval(", 32, 100, []string{"eval"}},
		{"case-sensitive literal", `\bnew\s+Function\s*\(`, "function f(){}; new Function('a'); new function(){}", 32, 100, []string{"Function"}},
		{"length-changing rune before the hit", `require\(["']child_process["']\)`, "İK" + filler + `require("child_process")`, 32, 100, []string{"child_process"}},
		{"fallback on a common literal", `\bexec\s*\(`, strings.Repeat("r.exec(s);", 40), 32, 3, []string{"exec"}},
		{"submatches", `(?i)\b(https?)://(\d+)\.(\d+)\.(\d+)\.(\d+)\b`, "a http://1.2.3.4 b HTTPS://10.0.0.1x", 64, 100, []string{"://"}},
		{"no hit", `\bchmod\b`, filler, 32, 100, []string{"chmod"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re := regexp.MustCompile(tt.re)
			c := &FileCtx{Rel: "a.js", Data: []byte(tt.data)}
			want := re.FindAllSubmatchIndex(c.Data, -1)
			got := c.FindAround(re, tt.window, tt.hits, tt.lits...)
			if len(want) == 0 && len(got) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("FindAround = %v, want %v", got, want)
			}
		})
	}
}

func TestLowerKeepsOffsets(t *testing.T) {
	c := &FileCtx{Data: []byte("AİbKC")}
	low := c.Lower()
	if len(low) != len(c.Data) {
		t.Fatalf("Lower() has %d bytes, Data %d", len(low), len(c.Data))
	}
	if string(low[:1]) != "a" || string(low[len(low)-1:]) != "c" {
		t.Errorf("Lower() = %q", low)
	}
}
