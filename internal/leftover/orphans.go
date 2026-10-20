package leftover

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"howett.net/plist"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/progress"
	"github.com/shiva2021/rootout/internal/safety"
)

// ByOrphan is the MatchedBy value of every item FindOrphans returns.
const ByOrphan = "orphan"

// Orphan is a group of leftovers whose owner (a reverse-DNS bundle id parsed
// from the entry names) no installed app claims. See D-020.
type Orphan struct {
	Owner     string     `json:"owner"`      // inferred bundle id, first spelling seen
	Items     []Leftover `json:"items"`      // every filesystem path passed safety.Validate; MatchedBy "orphan"
	SizeBytes int64      `json:"size_bytes"` // sum of Items
	LastUsed  time.Time  `json:"last_used"`  // newest mtime across Items (bounded walk)
	Recent    bool       `json:"recent"`     // LastUsed within RecentWindow
	Reasons   []string   `json:"reasons"`    // why the owner is considered gone
}

// OrphanOptions tunes FindOrphans.
type OrphanOptions struct {
	Installed    []app.App         // required; their ids, prefixes and extensions are owned
	RecentWindow time.Duration     // 0 means 30 days
	Now          func() time.Time  // nil means time.Now
	Run          Runner            // nil skips the launchctl/mdfind/ps guards and receipts
	Progress     progress.Reporter // nil means no reporting
}

// Reasons FindOrphans attaches to every Orphan; the guard reasons appear only
// when the guard actually ran.
const (
	reasonNotInstalled = "no installed app with this id"
	reasonNoBundle     = "no app bundle with this id on disk"
	reasonNotLoaded    = "not loaded in launchd"
	reasonNoProcess    = "no matching process"
	receiptNote        = "receipt only; not removed in this version"
)

const (
	defaultRecentWindow = 30 * 24 * time.Hour
	guardTimeout        = 5 * time.Second // launchctl, ps, pkgutil
	mdfindTimeout       = 3 * time.Second // Spotlight can be slow on a cold index
	maxBundles          = 2000            // mdfind results read
	maxMtimeEntries     = 5000            // per item, when walking a directory for LastUsed
	orphanGuards        = 4               // mdfind, launchctl, ps, pkgutil
)

var (
	teamPrefix = regexp.MustCompile(`^[A-Z0-9]{10}\.`)
	hostSuffix = regexp.MustCompile(`\.[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	idLike     = regexp.MustCompile(`^[A-Za-z]{2,6}(\.[A-Za-z0-9_-]+){2,}$`)
)

// ParseOwner returns the reverse-DNS id an entry name belongs to, or "" when
// the name is not id-like. It strips one of the suffixes .plist, .savedState
// and .binarycookies, a leading team id ("ABCDE12345."), a leading "group."
// and a trailing ByHost UUID, and requires at least three segments. The id is
// returned as written; callers compare case-insensitively. Exported for the
// CLI's --remove validation.
func ParseOwner(basename string) string {
	s := basename
	for _, suf := range []string{".plist", ".savedState", ".binarycookies"} {
		if hasSuffix(s, suf) {
			s = s[:len(s)-len(suf)]
			break
		}
	}
	s = teamPrefix.ReplaceAllString(s, "")
	if hasPrefix(s, "group.") {
		s = s[len("group."):]
	}
	s = hostSuffix.ReplaceAllString(s, "")
	if !idLike.MatchString(s) {
		return ""
	}
	return s
}

// FindOrphans lists the entries under ~/Library whose name parses as a bundle
// id that nothing owns: no installed app, no app bundle on disk, no loaded
// launchd job and no running process (the last three only when opts.Run is
// set). Results are grouped by owner and sorted by size, largest first, then
// owner. Every filesystem path has passed safety.Validate; receipts carry the
// pseudo-path "pkg:<id>" and are report-only.
func FindOrphans(ctx context.Context, env fsx.Env, opts OrphanOptions) ([]Orphan, error) {
	if len(opts.Installed) == 0 {
		return nil, errors.New("leftover: orphans: Installed is required")
	}
	if opts.RecentWindow == 0 {
		opts.RecentWindow = defaultRecentWindow
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Progress == nil {
		opts.Progress = progress.Nop()
	}
	rows := orphanRows(env)
	guards := 0
	if opts.Run != nil {
		guards = orphanGuards
	}
	opts.Progress.Start("Looking for orphaned leftovers", len(rows)+guards)
	defer opts.Progress.Done()

	s := &orphanScan{
		env:    env,
		opts:   opts,
		fs:     &scan{env: env},
		ids:    map[string]bool{"com.apple": true},
		procs:  map[string]bool{},
		seen:   map[string]bool{},
		groups: map[string]*Orphan{},
	}
	for _, a := range opts.Installed {
		s.own(a.BundleID)
	}
	if opts.Run != nil {
		if err := s.guards(ctx); err != nil {
			return nil, err
		}
	}
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dir := r.dir(env)
		s.apply(dir, r.kind)
		rel, err := filepath.Rel(env.Library(), dir)
		if err != nil {
			rel = dir
		}
		opts.Progress.Step(rel)
	}
	if opts.Run != nil {
		if err := s.receipts(ctx); err != nil {
			return nil, err
		}
		opts.Progress.Step("pkgutil")
	}
	return s.result(), nil
}

// orphanRows returns the rule rows FindOrphans scans: user-Library
// directories that are not behind an opt-in flag.
func orphanRows(env fsx.Env) []rule {
	var rows []rule
	for _, r := range rules {
		if r.when == nil && under(env.Library(), r.dir(env)) {
			rows = append(rows, r)
		}
	}
	return rows
}

// orphanScan carries the state of one FindOrphans call.
type orphanScan struct {
	env    fsx.Env
	opts   OrphanOptions
	fs     *scan           // for describe (size, NeedsRoot)
	ids    map[string]bool // owned ids, lower-cased
	procs  map[string]bool // running command basenames, lower-cased
	ran    struct{ bundles, launchd, procs bool }
	seen   map[string]bool    // paths already added
	groups map[string]*Orphan // lower-cased owner -> group
	order  []string           // group keys in first-seen order
}

func (s *orphanScan) own(id string) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id != "" {
		s.ids[id] = true
	}
}

// owned reports whether id (any case) belongs to something present: it equals
// an owned id, extends one ("<owned>.helper"), is a prefix of one (vendor
// folders), or its last segment names a running process.
func (s *orphanScan) owned(id string) bool {
	c := strings.ToLower(id)
	for o := range s.ids {
		if c == o || strings.HasPrefix(c, o+".") || strings.HasPrefix(o, c+".") {
			return true
		}
	}
	if i := strings.LastIndex(c, "."); i >= 0 && s.procs[c[i+1:]] {
		return true
	}
	return false
}

// tool runs one external command under its own timeout. A failure is logged
// and returned; callers treat it as "no information" unless the parent
// context is done.
func (s *orphanScan) tool(parent context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	out, err := s.opts.Run(ctx, name, args...)
	if err != nil {
		slog.Debug("leftover: orphans: "+name, "err", err)
		if parent.Err() != nil {
			return nil, parent.Err()
		}
		return nil, err
	}
	return out, nil
}

// guards fills the owned set from Spotlight, launchd and the process table.
func (s *orphanScan) guards(ctx context.Context) error {
	steps := []struct {
		name    string
		args    []string
		timeout time.Duration
		parse   func([]byte)
	}{
		{"mdfind", []string{"kMDItemContentType == 'com.apple.application-bundle'"}, mdfindTimeout, s.ownBundles},
		{"launchctl", []string{"list"}, guardTimeout, s.ownLaunchd},
		{"ps", []string{"-axo", "comm="}, guardTimeout, s.ownProcs},
	}
	for _, st := range steps {
		out, err := s.tool(ctx, st.timeout, st.name, st.args...)
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			st.parse(out)
		}
		s.opts.Progress.Step(st.name)
	}
	return nil
}

// ownBundles reads CFBundleIdentifier from every bundle mdfind listed.
func (s *orphanScan) ownBundles(out []byte) {
	s.ran.bundles = true
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		p := strings.TrimSpace(line)
		if p == "" {
			continue
		}
		if n >= maxBundles {
			slog.Debug("leftover: orphans: mdfind results capped", "max", maxBundles)
			break
		}
		n++
		id, err := bundleID(p)
		if err != nil {
			slog.Debug("leftover: orphans: bundle id", "err", err)
			continue
		}
		s.own(id)
	}
}

// bundleID parses CFBundleIdentifier from <bundle>/Contents/Info.plist.
func bundleID(bundle string) (string, error) {
	p := filepath.Join(bundle, "Contents", "Info.plist")
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("leftover: read %s: %w", p, err)
	}
	var in struct {
		ID string `plist:"CFBundleIdentifier"`
	}
	if _, err := plist.Unmarshal(data, &in); err != nil {
		return "", fmt.Errorf("leftover: parse %s: %w", p, err)
	}
	return in.ID, nil
}

// ownLaunchd takes the Label column of `launchctl list`.
func (s *orphanScan) ownLaunchd(out []byte) {
	s.ran.launchd = true
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[2] == "Label" {
			continue
		}
		s.own(f[2])
	}
}

// ownProcs records the basename of every running command.
func (s *orphanScan) ownProcs(out []byte) {
	s.ran.procs = true
	for _, line := range strings.Split(string(out), "\n") {
		p := strings.TrimSpace(line)
		if p != "" {
			s.procs[strings.ToLower(filepath.Base(p))] = true
		}
	}
}

// apply lists one directory and records every id-like entry nothing owns.
func (s *orphanScan) apply(dir string, kind Kind) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("leftover: orphans: read dir", "dir", dir, "err", err)
		}
		return
	}
	for _, e := range entries {
		owner := ParseOwner(e.Name())
		if owner == "" || s.owned(owner) {
			continue
		}
		s.add(owner, Leftover{Path: filepath.Join(dir, e.Name()), Kind: kind, MatchedBy: ByOrphan})
	}
}

// add validates, deduplicates, measures and files one filesystem leftover
// under its owner's group.
func (s *orphanScan) add(owner string, l Leftover) {
	l.Path = filepath.Clean(l.Path)
	if s.seen[l.Path] {
		return
	}
	if err := safety.Validate(s.env, l.Path, safety.Options{}); err != nil {
		slog.Debug("leftover: orphans: dropped", "path", l.Path, "err", err)
		return
	}
	s.seen[l.Path] = true
	l = s.fs.describe(l)
	g := s.group(owner)
	g.Items = append(g.Items, l)
	g.SizeBytes += l.SizeBytes
	if t := newestMtime(l.Path); t.After(g.LastUsed) {
		g.LastUsed = t
	}
}

// group returns the Orphan for owner, creating it on first sight.
func (s *orphanScan) group(owner string) *Orphan {
	key := strings.ToLower(owner)
	g := s.groups[key]
	if g == nil {
		g = &Orphan{Owner: owner}
		s.groups[key] = g
		s.order = append(s.order, key)
	}
	return g
}

// receipts runs `pkgutil --pkgs` and files every unowned package id as a
// report-only Receipt item, joining the group whose owner it equals or
// extends, else a group of its own.
func (s *orphanScan) receipts(ctx context.Context) error {
	out, err := s.tool(ctx, guardTimeout, "pkgutil", "--pkgs")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		pkg := strings.TrimSpace(line)
		path := "pkg:" + pkg
		if pkg == "" || s.seen[path] || s.owned(pkg) {
			continue
		}
		s.seen[path] = true
		g := s.groupForPkg(pkg)
		// Pseudo-path, exempt from safety.Validate and size: it is never
		// deleted by path.
		g.Items = append(g.Items, Leftover{Path: path, Kind: Receipt, MatchedBy: ByOrphan, Note: receiptNote})
	}
	return nil
}

// groupForPkg picks the existing group with the longest owner that pkg equals
// or extends ("com.foo.app.pkg" joins "com.foo.app"), else a new one.
func (s *orphanScan) groupForPkg(pkg string) *Orphan {
	var best *Orphan
	for _, key := range s.order {
		g := s.groups[key]
		if (eq(pkg, g.Owner) || hasPrefix(pkg, g.Owner+".")) && (best == nil || len(g.Owner) > len(best.Owner)) {
			best = g
		}
	}
	if best != nil {
		return best
	}
	owner := ParseOwner(pkg)
	if owner == "" {
		owner = pkg
	}
	return s.group(owner)
}

// result finalises Recent, Reasons and ordering.
func (s *orphanScan) result() []Orphan {
	reasons := []string{reasonNotInstalled}
	if s.ran.bundles {
		reasons = append(reasons, reasonNoBundle)
	}
	if s.ran.launchd {
		reasons = append(reasons, reasonNotLoaded)
	}
	if s.ran.procs {
		reasons = append(reasons, reasonNoProcess)
	}
	now := s.opts.Now()
	out := make([]Orphan, 0, len(s.order))
	for _, key := range s.order {
		g := *s.groups[key]
		g.Recent = !g.LastUsed.IsZero() && now.Sub(g.LastUsed) < s.opts.RecentWindow
		g.Reasons = append([]string(nil), reasons...)
		sort.SliceStable(g.Items, func(i, j int) bool {
			if g.Items[i].Kind != g.Items[j].Kind {
				return g.Items[i].Kind < g.Items[j].Kind
			}
			return g.Items[i].Path < g.Items[j].Path
		})
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SizeBytes != out[j].SizeBytes {
			return out[i].SizeBytes > out[j].SizeBytes
		}
		return strings.ToLower(out[i].Owner) < strings.ToLower(out[j].Owner)
	})
	return out
}

// newestMtime returns the newest ModTime of path and, for a directory, of
// the first maxMtimeEntries entries under it. A missing path yields the zero
// time.
func newestMtime(path string) time.Time {
	fi, err := os.Lstat(path)
	if err != nil {
		return time.Time{}
	}
	newest := fi.ModTime()
	if !fi.IsDir() {
		return newest
	}
	n := 0
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		n++
		if n > maxMtimeEntries {
			return fs.SkipAll
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}
