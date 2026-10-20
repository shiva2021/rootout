package heur

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/progress"
)

// Test rules are registered globally and stay registered for the whole test
// binary, so each one is keyed on Extension.Name and ignores every other
// extension: they cannot leak into other packages' tests, and other rules
// are filtered out here by name.

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// collector is a pair of test rules that record what the engine showed them.
type collector struct {
	mu      sync.Mutex
	read    map[string]bool // Rel -> InNodeModules, for every file handed to the file rule
	files   []string        // ExtCtx.Files as seen by the ext rule
	extRuns int
}

func newCollector(t *testing.T, tok string) *collector {
	t.Helper()
	c := &collector{read: map[string]bool{}}
	RegisterFile(FileRule{Name: "test_collect_file", Check: func(fc *FileCtx) []Finding {
		if fc.Ext.Name != tok {
			return nil
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.read[fc.Rel] = fc.InNodeModules
		return nil
	}})
	RegisterExt(ExtRule{Name: "test_collect_ext", Check: func(ec *ExtCtx) []Finding {
		if ec.Ext.Name != tok {
			return nil
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.files = slices.Clone(ec.Files)
		c.extRuns++
		return nil
	}})
	return c
}

func findings(r Report, rule string) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func TestScanWalk(t *testing.T) {
	tok := t.Name()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.js": "1", "b.ts": "1", "c.py": "1", "d.sh": "1", "e.cjs": "1", "f.mjs": "1",
		"g.jsx": "1", "h.tsx": "1", "extension.vsixmanifest": "1", "package.json": "{}",
		"src/docs/ok.js":               "1",
		"README.md":                    "ignored",
		"image.png":                    "ignored",
		"bin/addon.node":               "binary, recorded but never read",
		"node_modules/p/index.js":      "1",
		"node_modules/p/docs/d.js":     "skipped",
		"node_modules/p/example/e.js":  "skipped",
		"node_modules/p/examples/e.js": "skipped",
		"test/x.js":                    "skipped",
		"tests/y.js":                   "skipped",
		"__tests__/z.js":               "skipped",
		"fixtures/w.js":                "skipped",
		".git/v.js":                    "skipped",
		"sub/test/q.js":                "skipped",
	})
	c := newCollector(t, tok)

	r, err := Scan(context.Background(), ext.Extension{Dir: dir, Name: tok}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	wantRead := []string{
		"a.js", "b.ts", "c.py", "d.sh", "e.cjs", "extension.vsixmanifest", "f.mjs", "g.jsx", "h.tsx",
		"node_modules/p/index.js", "package.json", "src/docs/ok.js",
	}
	if got := sortedKeys(c.read); !slices.Equal(got, wantRead) {
		t.Errorf("files read:\n got %v\nwant %v", got, wantRead)
	}
	if r.Scanned != len(wantRead) {
		t.Errorf("Scanned = %d, want %d", r.Scanned, len(wantRead))
	}
	if r.Truncated {
		t.Error("Truncated = true, want false")
	}
	if !c.read["node_modules/p/index.js"] || c.read["a.js"] {
		t.Errorf("InNodeModules flags wrong: %v", c.read)
	}
	wantFiles := append(slices.Clone(wantRead), "bin/addon.node")
	slices.Sort(wantFiles)
	gotFiles := slices.Clone(c.files)
	slices.Sort(gotFiles)
	if !slices.Equal(gotFiles, wantFiles) {
		t.Errorf("ExtCtx.Files:\n got %v\nwant %v", gotFiles, wantFiles)
	}
	if c.extRuns != 1 {
		t.Errorf("ext rule ran %d times, want 1", c.extRuns)
	}
	if r.Findings == nil {
		t.Error("Findings is nil, want an empty slice")
	}
}

func TestScanSymlink(t *testing.T) {
	tok := t.Name()
	dir := t.TempDir()
	outside := t.TempDir()
	writeTree(t, dir, map[string]string{"a.js": "1"})
	writeTree(t, outside, map[string]string{"secret.js": "1"})
	links := map[string]string{
		"escape.js":   filepath.Join(outside, "secret.js"),
		"escapedir":   outside,
		"inner.js":    filepath.Join(dir, "a.js"),
		"dangling.js": filepath.Join(dir, "missing.js"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	c := newCollector(t, tok)

	r, err := Scan(context.Background(), ext.Extension{Dir: dir, Name: tok}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(c.read); !slices.Equal(got, []string{"a.js"}) {
		t.Errorf("files read = %v, want [a.js]", got)
	}
	if !slices.Equal(c.files, []string{"a.js"}) {
		t.Errorf("ExtCtx.Files = %v, want [a.js]", c.files)
	}
	var escaped []string
	for _, f := range findings(r, ruleSymlinkEscape) {
		escaped = append(escaped, f.File)
		if f.Severity != Info {
			t.Errorf("%s severity = %s, want info", f.File, f.Severity)
		}
	}
	slices.Sort(escaped)
	if want := []string{"escape.js", "escapedir"}; !slices.Equal(escaped, want) {
		t.Errorf("symlink_escape files = %v, want %v", escaped, want)
	}
}

func TestScanLimits(t *testing.T) {
	tok := t.Name()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.js": "0123456789", "b.js": "0123456789", "c.js": "0123456789"})
	c := newCollector(t, tok)

	tests := []struct {
		name      string
		lim       Limits
		scanned   int
		truncated bool
	}{
		{"defaults", Limits{}, 3, false},
		{"file bytes", Limits{MaxFileBytes: 5}, 0, true},
		{"max files", Limits{MaxFiles: 2}, 2, true},
		{"total bytes", Limits{MaxTotalBytes: 25}, 2, true},
		{"roomy", Limits{MaxFileBytes: 10, MaxTotalBytes: 30, MaxFiles: 3}, 3, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Scan(context.Background(), ext.Extension{Dir: dir, Name: tok}, tc.lim)
			if err != nil {
				t.Fatal(err)
			}
			if r.Scanned != tc.scanned || r.Truncated != tc.truncated {
				t.Errorf("Scanned=%d Truncated=%v, want %d/%v", r.Scanned, r.Truncated, tc.scanned, tc.truncated)
			}
			if want := []string{"a.js", "b.js", "c.js"}; !slices.Equal(c.files, want) {
				t.Errorf("ExtCtx.Files = %v, want %v (unread files stay listed)", c.files, want)
			}
		})
	}
}

func TestScore(t *testing.T) {
	w := func(rule string, sev Severity, halves int) weighted {
		return weighted{Finding: Finding{Rule: rule, Severity: sev}, halves: halves}
	}
	full, half := fullWeight, halfWeight
	tests := []struct {
		name    string
		in      []weighted
		trusted bool
		want    int
	}{
		{"empty", nil, false, 0},
		{"info only", []weighted{w("x", Info, full)}, false, 0},
		{"one low indicator", []weighted{w("x", Low, full)}, false, 5},
		{"indicator repeats add 25% each", []weighted{w("x", High, full), w("x", High, full)}, false, 38},
		{"indicator repeats cap at +50%", []weighted{w("x", High, full), w("x", High, full), w("x", High, full), w("x", High, full)}, false, 45},
		{"indicators combine with diminishing returns", []weighted{w("x", High, full), w("y", Medium, full)}, false, 41},
		{"criticals approach 100", []weighted{w("x", Critical, full), w("x", Critical, full), w("y", Critical, full)}, false, 81},
		{"capabilities capped", []weighted{w("eval_dynamic", High, full), w("child_process_shell", Medium, full), w("obf_a", High, full)}, false, CapabilityCap},
		{"capabilities capped lower when trusted", []weighted{w("eval_dynamic", High, full), w("obf_a", High, full)}, true, TrustedCapabilityCap},
		{"capability below cap counts fully", []weighted{w("child_process_shell", Medium, full)}, false, 15},
		{"half-weight obf", []weighted{w("obf_a", High, half)}, false, 15},
		{"indicators not discounted for trusted", []weighted{w("x", Critical, full)}, true, 50},
		{"capability plus indicator", []weighted{w("eval_dynamic", High, full), w("obf_a", High, full), w("x", Critical, full)}, false, 85},
		{"clamp at 100", []weighted{w("eval_dynamic", High, full), w("obf_a", High, full), w("x", Critical, full), w("y", Critical, full), w("z", Critical, full)}, false, 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := score(tc.in, tc.trusted); got != tc.want {
				t.Errorf("score = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMultiplier(t *testing.T) {
	tests := []struct {
		rule          string
		trusted, inNM bool
		want          int
	}{
		{"obf_x", false, false, fullWeight},
		{"obf_x", true, false, halfWeight},
		{"obf_x", false, true, halfWeight},
		{"obf_x", true, true, halfWeight}, // discounts do not stack
		{"eval_x", true, true, fullWeight},
		{"xobf_x", true, true, fullWeight},
	}
	for _, tc := range tests {
		if got := multiplier(tc.rule, tc.trusted, tc.inNM); got != tc.want {
			t.Errorf("multiplier(%q, trusted=%v, nm=%v) = %d, want %d", tc.rule, tc.trusted, tc.inNM, got, tc.want)
		}
	}
}

// TestScanDiscounts checks the engine assigns the multipliers through the
// real walk: publisher trust and node_modules placement both halve an
// obf_ rule, non-obf rules never are, and the two never stack.
func TestScanDiscounts(t *testing.T) {
	tok := t.Name()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.js": "1", "node_modules/p/b.js": "1"})
	RegisterFile(FileRule{Name: "obf_test_engine", Check: func(fc *FileCtx) []Finding {
		if fc.Ext.Name != tok {
			return nil
		}
		return []Finding{{Rule: "obf_test_engine", Severity: High}, {Rule: "test_plain", Severity: High}}
	}})
	RegisterExt(ExtRule{Name: "obf_test_engine_ext", Check: func(ec *ExtCtx) []Finding {
		if ec.Ext.Name != tok {
			return nil
		}
		return []Finding{{Rule: "obf_test_engine_ext", Severity: Medium}}
	}})

	tests := []struct {
		publisher string
		want      map[string]int // "rule file" -> halves
	}{
		{"nobody", map[string]int{
			"obf_test_engine a.js": fullWeight, "obf_test_engine node_modules/p/b.js": halfWeight,
			"test_plain a.js": fullWeight, "test_plain node_modules/p/b.js": fullWeight,
			"obf_test_engine_ext ": fullWeight,
		}},
		{"MS-Python", map[string]int{
			"obf_test_engine a.js": halfWeight, "obf_test_engine node_modules/p/b.js": halfWeight,
			"test_plain a.js": fullWeight, "test_plain node_modules/p/b.js": fullWeight,
			"obf_test_engine_ext ": halfWeight,
		}},
	}
	for _, tc := range tests {
		t.Run(tc.publisher, func(t *testing.T) {
			s, err := scan(context.Background(), ext.Extension{Dir: dir, Name: tok, Publisher: tc.publisher}, Limits{})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]int{}
			for _, w := range s.found {
				if strings.HasPrefix(w.Rule, "obf_test_") || strings.HasPrefix(w.Rule, "test_") {
					got[w.Rule+" "+w.File] = w.halves
				}
			}
			for k, want := range tc.want {
				if got[k] != want {
					t.Errorf("%s: halves = %d, want %d", k, got[k], want)
				}
			}
			if len(got) != len(tc.want) {
				t.Errorf("got %d test findings %v, want %d", len(got), got, len(tc.want))
			}
		})
	}
}

func TestScanSortsFindings(t *testing.T) {
	tok := t.Name()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.js": "1", "b.js": "1"})
	RegisterFile(FileRule{Name: "test_sort", Check: func(fc *FileCtx) []Finding {
		if fc.Ext.Name != tok {
			return nil
		}
		return []Finding{
			{Rule: "test_sort_b", Severity: Low, Line: 2},
			{Rule: "test_sort_a", Severity: Low, Line: 9},
			{Rule: "test_sort_a", Severity: Low, Line: 1},
			{Rule: "test_sort_z", Severity: High},
		}
	}})
	r, err := Scan(context.Background(), ext.Extension{Dir: dir, Name: tok}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range r.Findings {
		if strings.HasPrefix(f.Rule, "test_sort") {
			got = append(got, f.Rule+" "+f.File+" "+strconv.Itoa(f.Line))
		}
	}
	want := []string{
		"test_sort_z a.js 0", "test_sort_z b.js 0",
		"test_sort_a a.js 1", "test_sort_a a.js 9", "test_sort_a b.js 1", "test_sort_a b.js 9",
		"test_sort_b a.js 2", "test_sort_b b.js 2",
	}
	if !slices.Equal(got, want) {
		t.Errorf("order:\n got %v\nwant %v", got, want)
	}
}

func TestScanRulePanic(t *testing.T) {
	tok := t.Name()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.js": "1"})
	RegisterFile(FileRule{Name: "test_panic_file", Check: func(fc *FileCtx) []Finding {
		if fc.Ext.Name == tok {
			panic("boom")
		}
		return nil
	}})
	RegisterFile(FileRule{Name: "test_survivor", Check: func(fc *FileCtx) []Finding {
		if fc.Ext.Name != tok {
			return nil
		}
		return []Finding{{Rule: "test_survivor", Severity: Low}}
	}})
	RegisterExt(ExtRule{Name: "test_panic_ext", Check: func(ec *ExtCtx) []Finding {
		if ec.Ext.Name == tok {
			_ = ec.Files[len(ec.Files)+1] // runtime panic
		}
		return nil
	}})

	r, err := Scan(context.Background(), ext.Extension{Dir: dir, Name: tok}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range findings(r, ruleRulePanic) {
		if strings.Contains(f.Detail, "test_panic_") {
			got = append(got, f.File+": "+f.Detail)
		}
		if f.Severity != Info {
			t.Errorf("rule_panic severity = %s, want info", f.Severity)
		}
	}
	slices.Sort(got)
	if len(got) != 2 || !strings.HasPrefix(got[0], ": rule test_panic_ext panicked:") || !strings.HasPrefix(got[1], "a.js: rule test_panic_file panicked: boom") {
		t.Errorf("rule_panic findings = %v", got)
	}
	if len(findings(r, "test_survivor")) != 1 {
		t.Errorf("rules after the panicking one did not run: %v", r.Findings)
	}
	if r.Scanned != 1 {
		t.Errorf("Scanned = %d, want 1", r.Scanned)
	}
}

func TestScanAll(t *testing.T) {
	tok := t.Name()
	var exts []ext.Extension
	for i := range 6 {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"a.js": "1"})
		exts = append(exts, ext.Extension{Dir: dir, Name: tok, ID: string(rune('a' + i))})
	}
	exts[2].Dir = filepath.Join(t.TempDir(), "missing")
	exts[4].Dir = ""

	reports := ScanAll(context.Background(), exts, Limits{})
	if len(reports) != len(exts) {
		t.Fatalf("got %d reports, want %d", len(reports), len(exts))
	}
	for i, r := range reports {
		if r.Ext.ID != exts[i].ID {
			t.Errorf("report %d is for %q, want %q", i, r.Ext.ID, exts[i].ID)
		}
		errs := findings(r, ruleScanError)
		switch i {
		case 2, 4:
			if len(errs) != 1 || errs[0].Severity != Info || errs[0].Detail == "" {
				t.Errorf("report %d: scan_error = %v, want one info finding", i, errs)
			}
		default:
			if len(errs) != 0 || r.Scanned != 1 {
				t.Errorf("report %d: scan_error=%v Scanned=%d", i, errs, r.Scanned)
			}
		}
	}
	if got := ScanAll(context.Background(), nil, Limits{}); len(got) != 0 {
		t.Errorf("ScanAll(nil) = %v, want empty", got)
	}
}

func TestScanAllWithProgress(t *testing.T) {
	tok := t.Name()
	var exts []ext.Extension
	for i := range 6 {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{"a.js": "1"})
		exts = append(exts, ext.Extension{Dir: dir, Name: tok, ID: "ext-" + strconv.Itoa(i)})
	}
	exts[4].Dir = "" // errors still count as a step

	rec := &progress.Recorder{}
	reports := ScanAllWith(context.Background(), exts, Limits{}, rec)
	if len(reports) != len(exts) {
		t.Fatalf("got %d reports, want %d", len(reports), len(exts))
	}
	if rec.Label != "Scanning extensions" || rec.Total != 6 || !rec.Finished {
		t.Errorf("recorder = label %q total %d finished %v", rec.Label, rec.Total, rec.Finished)
	}
	got := slices.Clone(rec.Steps)
	slices.Sort(got)
	var want []string
	for _, e := range exts {
		want = append(want, e.ID)
	}
	if !slices.Equal(got, want) {
		t.Errorf("Steps = %v, want one per extension %v", rec.Steps, want)
	}

	empty := &progress.Recorder{}
	if out := ScanAllWith(context.Background(), nil, Limits{}, empty); len(out) != 0 || empty.Total != 0 || !empty.Finished {
		t.Errorf("empty input: out=%v total=%d finished=%v", out, empty.Total, empty.Finished)
	}
}

func TestScanCancel(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.js": "1"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Scan(ctx, ext.Extension{Dir: dir, Name: t.Name()}, Limits{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if r := ScanAll(ctx, []ext.Extension{{Dir: dir, Name: t.Name()}}, Limits{}); len(findings(r[0], ruleScanError)) != 1 {
		t.Errorf("ScanAll under cancel: findings = %v, want one scan_error", r[0].Findings)
	}
}
