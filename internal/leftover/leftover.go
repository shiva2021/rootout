// Package leftover finds the files an application leaves behind: preferences,
// caches, containers, launchd jobs, receipts and so on. Every filesystem path
// it returns has passed safety.Validate; the matching rules live in rules.go
// and are documented in docs/kb/safety.md.
package leftover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/shiva2021/rootout/internal/run"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/safety"
)

// Kind classifies a leftover. The zero value is the application bundle itself.
type Kind int

// Kinds, in the order Find sorts them.
const (
	Bundle Kind = iota // the .app itself
	Preferences
	Cache
	AppSupport
	Container
	GroupContainer
	SavedState
	Log
	LaunchAgent
	LaunchDaemon
	PrivilegedHelper
	Receipt
	CrashReport
	Cookies
	HTTPStorage
	WebKit
	AppScripts
	LoginItem
	Dotfile
)

var kindNames = [...]string{
	Bundle:           "Bundle",
	Preferences:      "Preferences",
	Cache:            "Cache",
	AppSupport:       "Application Support",
	Container:        "Container",
	GroupContainer:   "Group Container",
	SavedState:       "Saved State",
	Log:              "Log",
	LaunchAgent:      "Launch agent",
	LaunchDaemon:     "Launch daemon",
	PrivilegedHelper: "Privileged helper",
	Receipt:          "Receipt",
	CrashReport:      "Crash report",
	Cookies:          "Cookies",
	HTTPStorage:      "HTTP storage",
	WebKit:           "WebKit",
	AppScripts:       "Application Scripts",
	LoginItem:        "Login item",
	Dotfile:          "Dotfile",
}

// String returns the human-readable name, e.g. "Preferences" or "Launch agent".
func (k Kind) String() string {
	if k < 0 || int(k) >= len(kindNames) {
		return fmt.Sprintf("Kind(%d)", int(k))
	}
	return kindNames[k]
}

// MarshalJSON encodes the Kind as its String().
func (k Kind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

// MatchedBy values.
const (
	ByBundleID     = "bundle_id"
	ByBundlePrefix = "bundle_prefix"
	ByTeamBundle   = "team_bundle"
	ByName         = "name"
	ByProgramPath  = "program_path"
	ByReceipt      = "receipt"
	ByLoginItem    = "login_item"
)

// Leftover is one item that belongs to an application.
type Leftover struct {
	Path      string `json:"path"`
	Kind      Kind   `json:"kind"`
	MatchedBy string `json:"matched_by"` // "bundle_id" | "bundle_prefix" | "team_bundle" | "name" | "program_path" | "receipt" | "login_item"
	SizeBytes int64  `json:"size_bytes"`
	NeedsRoot bool   `json:"needs_root"`     // the current uid cannot delete it
	Note      string `json:"note,omitempty"` // e.g. "report only; remove in System Settings > Login Items"
}

// Runner executes an external command; nil disables the steps that need one.
type Runner = run.Runner

// Options tunes Find.
type Options struct {
	Dotfiles     bool   // include ~/.config/<name> and ~/.<name> (name-guarded); default false
	CrashReports bool   // include DiagnosticReports; callers default this to true
	Run          Runner // for `pkgutil` (receipts) and `sfltool dumpbtm` (login items); nil skips both
}

// Find returns every leftover for a, including the bundle itself as the first
// item (Kind Bundle, MatchedBy "bundle_id"), sorted by Kind then Path.
//
// Every returned filesystem Path has passed
// safety.Validate(env, path, safety.Options{AllowDotfiles: opts.Dotfiles});
// a path that fails validation is dropped and logged at slog.Debug, never
// returned. Two kinds are exempt because they are not filesystem paths and
// are never deleted by path: Receipt items carry the pseudo-path "pkg:<id>"
// (forgotten with `pkgutil --forget`), and the LoginItem item is report-only.
//
// If the bundle path itself fails validation Find returns that error, since it
// cannot produce the promised shape. An app with an empty BundleID yields only
// the Bundle item: id-based rules need an id and name-based rules need
// safety.NameOK, which also requires one.
func Find(ctx context.Context, env fsx.Env, a app.App, opts Options) ([]Leftover, error) {
	s := &scan{
		env:    env,
		app:    a,
		opts:   opts,
		vopts:  safety.Options{AllowDotfiles: opts.Dotfiles},
		nameOK: safety.NameOK(a.Name, a.BundleID),
		seen:   map[string]bool{},
		labels: map[string]string{},
	}
	if err := safety.Validate(env, a.Path, s.vopts); err != nil {
		return nil, fmt.Errorf("leftover: bundle %s: %w", a.Path, err)
	}
	bundle := s.describe(Leftover{Path: filepath.Clean(a.Path), Kind: Bundle, MatchedBy: ByBundleID})
	if a.SizeBytes > 0 {
		bundle.SizeBytes = a.SizeBytes
	}
	s.seen[bundle.Path] = true
	s.found = append(s.found, bundle)

	for _, r := range rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r.when != nil && !r.when(opts) {
			continue
		}
		s.apply(r)
	}
	if opts.Run != nil {
		if err := s.receipts(ctx); err != nil {
			return nil, err
		}
		if err := s.loginItem(ctx); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(s.found, func(i, j int) bool {
		if s.found[i].Kind != s.found[j].Kind {
			return s.found[i].Kind < s.found[j].Kind
		}
		return s.found[i].Path < s.found[j].Path
	})
	return s.found, nil
}

// scan carries the state of one Find call through the rule table.
type scan struct {
	env    fsx.Env
	app    app.App
	opts   Options
	vopts  safety.Options
	nameOK bool              // safety.NameOK(app.Name, app.BundleID), computed once
	seen   map[string]bool   // paths already added
	labels map[string]string // launchd Label -> MatchedBy, from matched LaunchDaemons
	found  []Leftover
}

// apply runs one rule: list its directory and try each matcher, in order, on
// every entry's basename. The first matcher that fires decides MatchedBy.
func (s *scan) apply(r rule) {
	dir := r.dir(s.env)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("leftover: read dir", "dir", dir, "err", err)
		}
		return
	}
	for _, e := range entries {
		base := e.Name()
		path := filepath.Join(dir, base)
		for _, m := range r.matchers {
			by, ok := s.match(m, r.kind, path, base)
			if !ok {
				continue
			}
			s.add(Leftover{Path: path, Kind: r.kind, MatchedBy: by})
			break
		}
	}
}

// match applies one matcher to a basename. It returns the MatchedBy value and
// whether the entry matched. All comparisons are case-insensitive and exact
// (never substring or glob) apart from the prefix/suffix forms stated in
// docs/kb/safety.md.
func (s *scan) match(m matchKind, kind Kind, path, base string) (string, bool) {
	id := s.app.BundleID
	switch m {
	case matchID:
		return ByBundleID, id != "" && eq(base, id)
	case matchIDPrefix:
		return ByBundlePrefix, id != "" && hasPrefix(base, id+".")
	case matchPlist:
		if id == "" {
			return "", false
		}
		if eq(base, id+".plist") {
			return ByBundleID, true
		}
		return ByBundlePrefix, hasPrefix(base, id+".") && hasSuffix(base, ".plist")
	case matchTeam:
		team := s.app.TeamID
		if id == "" || team == "" {
			return "", false
		}
		grp := team + ".group." + id
		return ByTeamBundle, eq(base, team+"."+id) || eq(base, grp) || hasPrefix(base, grp+".")
	case matchGroup:
		return ByTeamBundle, id != "" && eq(base, "group."+id)
	case matchName:
		return ByName, s.nameOK && eq(base, s.app.Name)
	case matchDotName:
		return ByName, s.nameOK && eq(base, "."+s.app.Name)
	case matchSavedState:
		return ByBundleID, id != "" && eq(base, id+".savedState")
	case matchBinaryCookies:
		return ByBundleID, id != "" && eq(base, id+".binarycookies")
	case matchLaunchd:
		return s.matchLaunchd(kind, path, base)
	case matchHelperLabel:
		for label, by := range s.labels {
			if eq(base, label) {
				return by, true
			}
		}
		return "", false
	case matchCrashReport:
		return ByName, s.isCrashReport(base)
	}
	return "", false
}

// isCrashReport matches "<Name>-*.ips", "<Name>_*.crash" and the like. It uses
// the raw app name, not NameOK: the prefix separator plus the report extension
// is specific enough on its own.
func (s *scan) isCrashReport(base string) bool {
	name := s.app.Name
	if name == "" {
		return false
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".ips", ".crash", ".diag":
	default:
		return false
	}
	return hasPrefix(base, name+"-") || hasPrefix(base, name+"_")
}

// add validates, deduplicates, measures and records one filesystem leftover.
func (s *scan) add(l Leftover) {
	l.Path = filepath.Clean(l.Path)
	if s.seen[l.Path] {
		return
	}
	if err := safety.Validate(s.env, l.Path, s.vopts); err != nil {
		slog.Debug("leftover: dropped", "path", l.Path, "err", err)
		return
	}
	s.seen[l.Path] = true
	s.found = append(s.found, s.describe(l))
}

// describe fills SizeBytes and NeedsRoot for a filesystem leftover.
func (s *scan) describe(l Leftover) Leftover {
	l.SizeBytes = sizeOf(l.Path)
	l.NeedsRoot = s.needsRoot(l.Path)
	return l
}

// receipts runs `pkgutil --pkgs` and records every package id equal to the
// bundle id or prefixed by it. The receipt's files are deliberately not
// listed; the plan forgets the receipt instead.
func (s *scan) receipts(ctx context.Context) error {
	id := s.app.BundleID
	if id == "" {
		return nil
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	out, err := s.opts.Run(ctx, "pkgutil", "--pkgs")
	if err != nil {
		if parent.Err() != nil {
			return parent.Err()
		}
		slog.Debug("leftover: pkgutil --pkgs", "err", err)
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		pkg := strings.TrimSpace(line)
		if pkg == "" || (!eq(pkg, id) && !hasPrefix(pkg, id+".")) {
			continue
		}
		// Pseudo-path, exempt from safety.Validate: it is not a filesystem
		// path and is only ever passed to `pkgutil --forget`.
		s.found = append(s.found, Leftover{
			Path:      "pkg:" + pkg,
			Kind:      Receipt,
			MatchedBy: ByReceipt,
			Note:      "forgotten with pkgutil --forget",
		})
	}
	return nil
}

// loginItem runs `sfltool dumpbtm` and reports (never deletes) a login item
// that mentions the bundle id or the bundle path.
func (s *scan) loginItem(ctx context.Context) error {
	// sfltool can block indefinitely without Full Disk Access; never let a
	// report-only step stall discovery.
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, loginItemTimeout)
	defer cancel()
	out, err := s.opts.Run(ctx, "sfltool", "dumpbtm")
	if err != nil {
		if parent.Err() != nil {
			return parent.Err()
		}
		slog.Debug("leftover: sfltool dumpbtm", "err", err)
		return nil
	}
	text := string(out)
	hit := strings.Contains(text, s.app.Path)
	if !hit && s.app.BundleID != "" {
		hit = strings.Contains(text, s.app.BundleID)
	}
	if !hit {
		return nil
	}
	// Report only: exempt from safety.Validate because it is never deleted.
	s.found = append(s.found, Leftover{
		Path:      s.app.Path,
		Kind:      LoginItem,
		MatchedBy: ByLoginItem,
		Note:      "report only; remove in System Settings > General > Login Items",
	})
	return nil
}

// needsRoot reports whether the current user cannot delete path: it is owned
// by another uid, or it sits under /Library or /Applications in a directory
// the user cannot write to.
func (s *scan) needsRoot(path string) bool {
	if uid, ok := uidOf(path); ok && uid != os.Getuid() {
		return true
	}
	for _, pre := range []string{s.env.Sys("Library"), s.env.Sys("Applications")} {
		if under(pre, path) {
			return !dirWritable(filepath.Dir(path))
		}
	}
	return false
}

// Ownership probes, split out as variables so tests can override them. The
// defaults are in owner_darwin.go and owner_other.go.
// toolTimeout bounds every external tool run during discovery; the login
// item probe gets less because sfltool blocks without Full Disk Access and
// the result is report-only.
const (
	toolTimeout      = 10 * time.Second
	loginItemTimeout = 3 * time.Second
)

var (
	uidOf       = lstatUID
	dirWritable = accessWrite
)

// sizeOf returns the Lstat size of a file, or the sum of Lstat sizes under a
// directory. Per-entry errors are ignored; a missing path has size 0.
func sizeOf(path string) int64 {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	if !fi.IsDir() {
		return fi.Size()
	}
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if info, err := d.Info(); err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// under reports whether p is strictly inside parent (both cleaned, absolute).
func under(parent, p string) bool {
	rel, err := filepath.Rel(parent, p)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func eq(a, b string) bool { return strings.EqualFold(a, b) }

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}
