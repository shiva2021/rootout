package heur

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/shiva2021/purge/internal/ext"
)

// ErrNoDir is returned when the extension has no directory to scan.
var ErrNoDir = errors.New("heur: extension has no dir")

// Rule names the engine itself emits. They carry Severity Info and therefore
// never move the score; they only explain gaps in coverage.
const (
	ruleScanError     = "scan_error"
	ruleRulePanic     = "rule_panic"
	ruleSymlinkEscape = "symlink_escape"
	ruleReadError     = "read_error"
)

// discountPrefix marks rules whose findings weigh half for trusted
// publishers and vendored dependencies (legitimately minified bundles).
const discountPrefix = "obf_"

// Score multipliers, expressed in halves so the arithmetic stays integral.
const (
	fullWeight = 2
	halfWeight = 1
)

// readExts are read and handed to file rules. package.json is read too.
var readExts = map[string]bool{
	".js": true, ".cjs": true, ".mjs": true, ".jsx": true, ".ts": true, ".tsx": true,
	".sh": true, ".py": true, ".vsixmanifest": true,
}

// skipDirs are never descended into. skipNodeModulesDirs are skipped only
// when they sit below a node_modules directory.
var (
	skipDirs            = map[string]bool{"test": true, "tests": true, "__tests__": true, "fixtures": true, ".git": true}
	skipNodeModulesDirs = map[string]bool{"test": true, "tests": true, "docs": true, "example": true, "examples": true}
)

// Scan statically analyses one extension. It never executes anything.
func Scan(ctx context.Context, e ext.Extension, lim Limits) (Report, error) {
	s, err := scan(ctx, e, lim)
	if err != nil {
		return Report{}, err
	}
	return s.report(), nil
}

// ScanAll runs Scan concurrently (workers = min(4, len)) preserving input
// order; per-extension errors become a Finding{Rule:"scan_error",
// Severity: Info} rather than aborting.
func ScanAll(ctx context.Context, exts []ext.Extension, lim Limits) []Report {
	out := make([]Report, len(exts))
	if len(exts) == 0 {
		return out
	}
	idx := make(chan int)
	var wg sync.WaitGroup
	for range min(4, len(exts)) {
		wg.Go(func() {
			for i := range idx {
				out[i] = scanOrError(ctx, exts[i], lim)
			}
		})
	}
	for i := range exts {
		idx <- i
	}
	close(idx)
	wg.Wait()
	return out
}

func scanOrError(ctx context.Context, e ext.Extension, lim Limits) Report {
	r, err := Scan(ctx, e, lim)
	if err != nil {
		return Report{Ext: e, Findings: []Finding{{Rule: ruleScanError, Severity: Info, Detail: err.Error()}}}
	}
	return r
}

// withDefaults fills zero fields from DefaultLimits.
func (l Limits) withDefaults() Limits {
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultLimits.MaxFileBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = DefaultLimits.MaxTotalBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultLimits.MaxFiles
	}
	return l
}

// weighted is a finding plus its score multiplier in halves.
type weighted struct {
	Finding
	halves int
}

// scanner holds the state of one Scan.
type scanner struct {
	ext       *ext.Extension
	root      string // e.Dir, absolute with symlinks resolved
	lim       Limits
	trusted   bool
	fileRules []FileRule
	extRules  []ExtRule

	files     []string
	found     []weighted
	scanned   int
	total     int64
	truncated bool
}

func scan(ctx context.Context, e ext.Extension, lim Limits) (*scanner, error) {
	if e.Dir == "" {
		return nil, ErrNoDir
	}
	root, err := filepath.Abs(e.Dir)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return nil, fmt.Errorf("heur: resolve %s: %w", e.Dir, err)
	}
	s := &scanner{
		ext:     &e,
		root:    root,
		lim:     lim.withDefaults(),
		trusted: TrustedPublishers[strings.ToLower(e.Publisher)],
	}
	s.fileRules, s.extRules = snapshotRules()

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		return s.visit(ctx, p, d, err)
	})
	if cerr := ctx.Err(); cerr != nil {
		return nil, cerr
	}
	if err != nil {
		return nil, fmt.Errorf("heur: walk %s: %w", e.Dir, err)
	}
	ec := &ExtCtx{Ext: s.ext, Files: s.files}
	for _, r := range s.extRules {
		s.runExt(r, ec)
	}
	return s, nil
}

// snapshotRules copies the registries so a Scan is unaffected by (and never
// races with) registrations made while it runs.
func snapshotRules() ([]FileRule, []ExtRule) {
	regMu.Lock()
	defer regMu.Unlock()
	return slices.Clone(fileRules), slices.Clone(extRules)
}

// visit is the WalkDir callback.
func (s *scanner) visit(ctx context.Context, p string, d fs.DirEntry, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	rel := s.rel(p)
	if err != nil {
		if p == s.root {
			return err
		}
		s.add(Finding{Rule: ruleReadError, Severity: Info, File: rel, Detail: err.Error()}, fullWeight)
		return nil
	}
	if d.IsDir() {
		if rel != "." && skipDir(rel) {
			return fs.SkipDir
		}
		return nil
	}
	if d.Type()&fs.ModeSymlink != 0 {
		s.symlink(p, rel)
		return nil
	}
	if !d.Type().IsRegular() {
		return nil
	}
	read, record := classify(rel)
	if !record {
		return nil
	}
	s.files = append(s.files, rel)
	if !read {
		return nil
	}
	info, err := d.Info()
	if err != nil {
		s.add(Finding{Rule: ruleReadError, Severity: Info, File: rel, Detail: err.Error()}, fullWeight)
		return nil
	}
	size := info.Size()
	if size > s.lim.MaxFileBytes || s.scanned >= s.lim.MaxFiles || s.total+size > s.lim.MaxTotalBytes {
		s.truncated = true
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		s.add(Finding{Rule: ruleReadError, Severity: Info, File: rel, Detail: err.Error()}, fullWeight)
		return nil
	}
	s.total += int64(len(data))
	s.scanned++
	fc := &FileCtx{Ext: s.ext, Rel: rel, Data: data, InNodeModules: inNodeModules(rel)}
	for _, r := range s.fileRules {
		s.runFile(r, fc)
	}
	return nil
}

func (s *scanner) rel(p string) string {
	r, err := filepath.Rel(s.root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// symlink records a symlink whose target lies outside the extension. Links
// are never followed, so one inside the tree needs no note.
func (s *scanner) symlink(p, rel string) {
	target, err := filepath.EvalSymlinks(p)
	if err != nil {
		return // dangling: nothing could be read through it
	}
	if target == s.root || strings.HasPrefix(target, s.root+string(filepath.Separator)) {
		return
	}
	s.add(Finding{
		Rule: ruleSymlinkEscape, Severity: Info, File: rel,
		Detail: "symlink resolves outside the extension: " + target,
	}, fullWeight)
}

func (s *scanner) runFile(r FileRule, fc *FileCtx) {
	defer s.recoverRule(r.Name, fc.Rel)
	for _, f := range r.Check(fc) {
		if f.Rule == "" {
			f.Rule = r.Name
		}
		if f.File == "" {
			f.File = fc.Rel
		}
		s.add(f, s.multiplier(f.Rule, fc.InNodeModules))
	}
}

func (s *scanner) runExt(r ExtRule, ec *ExtCtx) {
	defer s.recoverRule(r.Name, "")
	for _, f := range r.Check(ec) {
		if f.Rule == "" {
			f.Rule = r.Name
		}
		s.add(f, s.multiplier(f.Rule, false))
	}
}

// recoverRule turns a panicking rule into an Info finding so a rule bug
// never takes the scanner down.
func (s *scanner) recoverRule(name, file string) {
	if v := recover(); v != nil {
		s.add(Finding{
			Rule: ruleRulePanic, Severity: Info, File: file,
			Detail: fmt.Sprintf("rule %s panicked: %v", name, v),
		}, fullWeight)
	}
}

func (s *scanner) add(f Finding, halves int) {
	s.found = append(s.found, weighted{Finding: f, halves: halves})
}

// multiplier returns the weight of a finding in halves: obfuscation-style
// findings count half for trusted publishers or vendored code, once, never
// a quarter.
func (s *scanner) multiplier(rule string, inNodeModules bool) int {
	return multiplier(rule, s.trusted, inNodeModules)
}

func multiplier(rule string, trusted, inNodeModules bool) int {
	if strings.HasPrefix(rule, discountPrefix) && (trusted || inNodeModules) {
		return halfWeight
	}
	return fullWeight
}

func (s *scanner) report() Report {
	findings := make([]Finding, 0, len(s.found))
	for _, w := range s.found {
		findings = append(findings, w.Finding)
	}
	slices.SortStableFunc(findings, func(a, b Finding) int {
		return cmp.Or(
			cmp.Compare(b.Severity, a.Severity),
			cmp.Compare(a.Rule, b.Rule),
			cmp.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line),
		)
	})
	return Report{
		Ext:       *s.ext,
		Score:     score(s.found),
		Findings:  findings,
		Truncated: s.truncated,
		Scanned:   s.scanned,
		files:     s.files,
		weighted:  s.found,
	}
}

// score folds weighted findings into 0..100. Per rule, the effective
// weights (Weight × multiplier) are summed and capped at PerRuleCap × the
// rule's highest effective weight; the capped sums are added, rounded up
// from halves to points and clamped.
func score(ws []weighted) int {
	type acc struct{ sum, top int }
	per := map[string]*acc{}
	for _, w := range ws {
		h := w.Severity.Weight() * w.halves
		a := per[w.Rule]
		if a == nil {
			a = &acc{}
			per[w.Rule] = a
		}
		a.sum += h
		a.top = max(a.top, h)
	}
	total := 0
	for _, a := range per {
		total += min(a.sum, PerRuleCap*a.top)
	}
	return max(0, min(100, (total+1)/2))
}

// classify says whether a file is read (and handed to file rules) and
// whether it is recorded in ExtCtx.Files at all. Native .node addons are
// recorded but never read.
func classify(rel string) (read, record bool) {
	base := path.Base(rel)
	if base == "package.json" {
		return true, true
	}
	e := strings.ToLower(path.Ext(base))
	if readExts[e] {
		return true, true
	}
	return false, e == ".node"
}

func skipDir(rel string) bool {
	base := path.Base(rel)
	if skipDirs[base] {
		return true
	}
	return inNodeModules(rel) && skipNodeModulesDirs[base]
}

// inNodeModules reports whether rel lies below a node_modules directory.
func inNodeModules(rel string) bool {
	return strings.HasPrefix(rel, "node_modules/") || strings.Contains(rel, "/node_modules/")
}
