// Package plan turns an App into a reviewed list of paths and carries out
// the removal. It is the only package that deletes anything, and it never
// does so without a Confirmer saying yes.
package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shiva2021/purge/internal/app"
	"github.com/shiva2021/purge/internal/fsx"
	"github.com/shiva2021/purge/internal/leftover"
	"github.com/shiva2021/purge/internal/run"
	"github.com/shiva2021/purge/internal/safety"
)

// Mode is what Execute does with the selected paths.
type Mode int

const (
	Trash     Mode = iota // move to the macOS Trash (default)
	Permanent             // delete outright
	DryRun                // list only; Execute refuses to run
)

func (m Mode) String() string {
	switch m {
	case Trash:
		return "trash"
	case Permanent:
		return "permanent"
	case DryRun:
		return "dry-run"
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}

// ByManual marks a path the user added with --also or the TUI.
const ByManual = "manual"

// Item is one leftover plus the user's selection.
type Item struct {
	leftover.Leftover
	Selected bool `json:"selected"`
}

// Plan is the reviewed removal.
type Plan struct {
	App     app.App `json:"app"`
	Items   []Item  `json:"items"`
	Mode    Mode    `json:"-"`
	BrewZap bool    `json:"brew_zap"` // run `brew uninstall --zap` first; only meaningful when App.Cask != ""
}

// Options for Build.
type Options struct {
	Mode         Mode
	Dotfiles     bool
	CrashReports bool
	Also         []string   // extra paths; each must pass safety.Validate
	Run          run.Runner // for pkgutil / sfltool during discovery; nil skips them
}

var (
	// ErrDryRun is returned by Execute when the plan is a dry run.
	ErrDryRun = errors.New("plan: dry run; nothing executed")
	// ErrDeclined is returned when the Confirmer says no.
	ErrDeclined = errors.New("plan: declined")
)

// Build discovers everything that belongs to a and returns it selected. It
// refuses protected apps and rejects any --also path that fails validation.
func Build(ctx context.Context, env fsx.Env, a app.App, opts Options) (*Plan, error) {
	if err := safety.CheckApp(env, a.Path, a.BundleID); err != nil {
		return nil, err
	}
	found, err := leftover.Find(ctx, env, a, leftover.Options{
		Dotfiles:     opts.Dotfiles,
		CrashReports: opts.CrashReports,
		Run:          opts.Run,
	})
	if err != nil {
		return nil, err
	}
	p := &Plan{App: a, Mode: opts.Mode, BrewZap: a.Cask != ""}
	for _, l := range found {
		p.Items = append(p.Items, Item{Leftover: l, Selected: true})
	}
	for _, extra := range opts.Also {
		if err := p.Add(env, extra, safety.Options{AllowDotfiles: opts.Dotfiles}); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Add appends a user-chosen path after validating it. Missing paths are an error.
func (p *Plan) Add(env fsx.Env, path string, so safety.Options) error {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return fmt.Errorf("plan: --also %q: path must be absolute", path)
	}
	if err := safety.Validate(env, path, so); err != nil {
		return fmt.Errorf("plan: --also: %w", err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("plan: --also %q: %w", path, err)
	}
	for _, it := range p.Items {
		if it.Path == path {
			return nil
		}
	}
	var size int64
	if fi.IsDir() {
		size = dirSize(path)
	} else {
		size = fi.Size()
	}
	p.Items = append(p.Items, Item{
		Leftover: leftover.Leftover{Path: path, Kind: leftover.AppSupport, MatchedBy: ByManual, SizeBytes: size, NeedsRoot: !writable(path)},
		Selected: true,
	})
	return nil
}

// Selected returns the items the user kept.
func (p *Plan) Selected() []Item {
	var out []Item
	for _, it := range p.Items {
		if it.Selected {
			out = append(out, it)
		}
	}
	return out
}

// Totals summarises the selection for confirmation screens.
func (p *Plan) Totals() (count int, bytes int64, needsRoot int) {
	for _, it := range p.Selected() {
		if !isPath(it.Leftover) {
			continue
		}
		count++
		bytes += it.SizeBytes
		if it.NeedsRoot {
			needsRoot++
		}
	}
	return count, bytes, needsRoot
}

// Validate re-checks every selected filesystem path. Execute calls it so the
// check happens both at build time and immediately before deletion.
func (p *Plan) Validate(env fsx.Env, dotfiles bool) error {
	so := safety.Options{AllowDotfiles: dotfiles}
	for _, it := range p.Selected() {
		if !isPath(it.Leftover) {
			continue
		}
		if err := safety.Validate(env, it.Path, so); err != nil {
			return fmt.Errorf("plan: refusing to continue: %w", err)
		}
	}
	return nil
}

// isPath reports whether a leftover is a real filesystem path (receipts are
// "pkg:<id>" pseudo-paths and login items are report-only).
func isPath(l leftover.Leftover) bool {
	return l.Kind != leftover.Receipt && l.Kind != leftover.LoginItem
}

var labelRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// launchdLabel derives the job label from a plist path (the label is the
// file name by convention). Returns "" when the name is not a safe label.
func launchdLabel(plistPath string) string {
	l := strings.TrimSuffix(filepath.Base(plistPath), ".plist")
	if !labelRe.MatchString(l) {
		return ""
	}
	return l
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
		}
		return nil
	})
	return n
}
