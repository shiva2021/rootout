package app

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/progress"
)

// ErrNotBundle is returned by Read when the path is not a .app directory.
var ErrNotBundle = errors.New("app: not an application bundle")

// defaultWorkers is the size-walk concurrency when Options.Workers is 0.
const defaultWorkers = 4

// Options tunes DiscoverWith.
type Options struct {
	Progress  progress.Reporter // nil = progress.Nop()
	SkipSizes bool              // leave SizeBytes 0; used when only ids are needed
	Workers   int               // size-walk concurrency; 0 = 4
}

// Discover lists application bundles under env.Sys("Applications") (one level
// deep, plus one level inside subfolders that are not themselves .app bundles,
// e.g. /Applications/Utilities/*.app and /Applications/Epson Software/*.app)
// and env.User("Applications") (same rule), and env.Sys("System","Applications")
// (top level + Utilities, marked Protected). Results are sorted by Name
// (case-insensitive) and deduplicated by Path. Bundles whose Info.plist cannot
// be read are still returned with Name = dir name sans ".app" and an empty
// BundleID; the error is logged via slog.Debug.
//
// Discover(env) is DiscoverWith(env, Options{}).
func Discover(env fsx.Env) ([]App, error) {
	return DiscoverWith(env, Options{})
}

// DiscoverWith is Discover with progress reporting, optional size walks and
// a configurable worker count. Bundle paths are collected first, then each
// bundle is read (Info.plist and size walk) on a pool of opts.Workers
// goroutines; opts.Progress sees Start once, one Step per bundle (its Name)
// and Done at the end. The result is sorted, so the order is deterministic
// regardless of worker scheduling.
func DiscoverWith(env fsx.Env, opts Options) ([]App, error) {
	p := opts.Progress
	if p == nil {
		p = progress.Nop()
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	casks := loadCasks(env)

	var paths []string
	seen := map[string]bool{}
	add := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, root := range []string{env.Sys("Applications"), env.User("Applications")} {
		for _, b := range bundlesIn(root, true) {
			add(b)
		}
	}
	sys := env.Sys("System", "Applications")
	for _, b := range bundlesIn(sys, false) {
		add(b)
	}
	for _, b := range bundlesIn(filepath.Join(sys, "Utilities"), false) {
		add(b)
	}

	p.Start("Scanning applications", len(paths))
	defer p.Done()

	apps := make([]App, len(paths))
	idx := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, len(paths)) {
		wg.Go(func() {
			for i := range idx {
				apps[i] = read(env, paths[i], casks, !opts.SkipSizes)
				p.Step(apps[i].Name)
			}
		})
	}
	for i := range paths {
		idx <- i
	}
	close(idx)
	wg.Wait()

	sort.SliceStable(apps, func(i, j int) bool {
		a, b := strings.ToLower(apps[i].Name), strings.ToLower(apps[j].Name)
		if a != b {
			return a < b
		}
		return apps[i].Path < apps[j].Path
	})
	return apps, nil
}

// Read loads one bundle at path (must end in .app and contain
// Contents/Info.plist).
func Read(env fsx.Env, path string) (App, error) {
	path = filepath.Clean(path)
	if !isBundleName(filepath.Base(path)) {
		return App{}, fmt.Errorf("%w: %s", ErrNotBundle, path)
	}
	if fi, err := os.Stat(path); err != nil {
		return App{}, fmt.Errorf("app: stat %s: %w", path, err)
	} else if !fi.IsDir() {
		return App{}, fmt.Errorf("%w: %s is not a directory", ErrNotBundle, path)
	}
	if _, err := readInfo(path); err != nil {
		return App{}, err
	}
	return read(env, path, loadCasks(env), true), nil
}

// read builds an App for the bundle at path, tolerating a missing or broken
// Info.plist. With sizes false the size walk is skipped and SizeBytes stays 0.
func read(env fsx.Env, path string, casks caskIndex, sizes bool) App {
	a := App{
		Path: path,
		Name: strings.TrimSuffix(filepath.Base(path), ".app"),
	}
	in, err := readInfo(path)
	if err != nil {
		slog.Debug("app: unreadable Info.plist", "path", path, "err", err)
	} else {
		if in.DisplayName != "" {
			a.Name = in.DisplayName
		} else if in.Name != "" {
			a.Name = in.Name
		}
		a.BundleID = in.BundleID
		a.Version = in.ShortVer
		if a.Version == "" {
			a.Version = in.Version
		}
	}
	if sizes {
		a.SizeBytes = bundleSize(path)
	}
	a.Protected = under(env.Sys("System"), path) ||
		strings.HasPrefix(a.BundleID, "com.apple.") ||
		restricted(path)
	a.Cask = casks.lookup(path)
	return a
}

// bundlesIn returns the .app directories directly inside dir. With recurse,
// it also looks one level inside subdirectories that are not bundles. Entries
// that are symlinks count when they resolve to a directory. A missing dir
// yields nil.
func bundlesIn(dir string, recurse bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if !isDir(p, e) {
			continue
		}
		switch {
		case isBundleName(e.Name()):
			out = append(out, p)
		case recurse:
			out = append(out, bundlesIn(p, false)...)
		}
	}
	return out
}

// isDir reports whether entry e at p is a directory, following symlinks.
func isDir(p string, e fs.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// isBundleName reports whether name looks like an application bundle.
func isBundleName(name string) bool {
	return strings.HasSuffix(name, ".app") && len(name) > len(".app")
}

// bundleSize sums the Lstat sizes of every regular file under root without
// following symlinks. Errors on individual entries are ignored.
func bundleSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

// under reports whether p equals parent or sits beneath it.
func under(parent, p string) bool {
	rel, err := filepath.Rel(parent, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
