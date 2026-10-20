// Package trash moves paths to the macOS Trash without cgo (D-011).
//
// It prefers /usr/bin/trash (macOS 15+) and falls back to telling Finder to
// delete the items through osascript (macOS 14). Both run through an
// injectable Runner so tests never exec anything. Nothing in this package
// deletes permanently.
package trash

import (
	"context"
	"errors"
	"fmt"
	"github.com/shiva2021/rootout/internal/run"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Backend names returned by Mover.Available.
const (
	BackendTrash  = "trash"
	BackendFinder = "finder"
)

const (
	trashBin    = "/usr/bin/trash"
	osascript   = "osascript"
	trashBatch  = 100
	finderBatch = 50
)

// ErrInvalidPath is returned (wrapped) when an input path cannot be passed
// safely to either backend, e.g. it contains a newline.
var ErrInvalidPath = errors.New("trash: invalid path")

// ErrAllFailed is returned (wrapped) when every batch failed.
var ErrAllFailed = errors.New("trash: every batch failed")

// Mover sends paths to the Trash. The zero value is not usable; call New.
type Mover struct {
	Run      run.Runner                   // default run.Default
	LookPath func(string) (string, error) // default exec.LookPath; tests override
}

// Result reports what happened to each input path.
type Result struct {
	Moved   []string         // confirmed sent to the Trash
	Skipped []string         // did not exist (Lstat reported not-exist)
	Failed  map[string]error // path -> error, including the command's output
}

// New returns a Mover that execs real commands.
func New() *Mover {
	return &Mover{Run: run.Default, LookPath: exec.LookPath}
}

// Available reports which backend Move will use: BackendTrash when
// /usr/bin/trash exists and is executable, otherwise BackendFinder. Only the
// system binary is considered; a third-party `trash` on PATH has different
// flags and is ignored.
func (m *Mover) Available() string {
	if _, err := m.LookPath(trashBin); err == nil {
		return BackendTrash
	}
	return BackendFinder
}

// Move sends every path to the Trash.
//
// Paths are made absolute (never symlink-resolved, so links move as links).
// Paths that do not exist are listed in Result.Skipped. The rest are sent to
// the backend in batches: `/usr/bin/trash -s <paths...>` (100 per call) or
// `osascript -e 'tell application "Finder" to delete {POSIX file "...", ...}'`
// (50 per call).
//
// Failures are per batch. When a batch command fails, each of its paths is
// re-checked: ones that are already gone count as Moved, the others land in
// Result.Failed with the command's output in the error. A non-nil error is
// returned only when the input was invalid (ErrInvalidPath; nothing has run
// yet) or when every batch failed (ErrAllFailed). Partial failures return a
// nil error so callers can report per item from Result.Failed.
func (m *Mover) Move(ctx context.Context, paths []string) (Result, error) {
	res := Result{Failed: map[string]error{}}

	abs := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || strings.ContainsAny(p, "\n\r\x00") {
			return Result{}, fmt.Errorf("%w: %q", ErrInvalidPath, p)
		}
		a, err := filepath.Abs(p)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %q: %w", ErrInvalidPath, p, err)
		}
		abs = append(abs, a)
	}

	var pending []string
	for _, p := range abs {
		switch _, err := os.Lstat(p); {
		case err == nil:
			pending = append(pending, p)
		case errors.Is(err, os.ErrNotExist):
			res.Skipped = append(res.Skipped, p)
		default:
			res.Failed[p] = fmt.Errorf("trash: lstat %s: %w", p, err)
		}
	}
	if len(pending) == 0 {
		return res, nil
	}

	backend := m.Available()
	size := finderBatch
	if backend == BackendTrash {
		size = trashBatch
	}

	batches, failed := 0, 0
	var firstErr error
	for start := 0; start < len(pending); start += size {
		end := min(start+size, len(pending))
		batch := pending[start:end]
		batches++

		err := ctx.Err()
		if err == nil {
			err = m.runBatch(ctx, backend, batch)
		}
		if err == nil {
			res.Moved = append(res.Moved, batch...)
			continue
		}
		failed++
		if firstErr == nil {
			firstErr = err
		}
		for _, p := range batch {
			if _, lerr := os.Lstat(p); errors.Is(lerr, os.ErrNotExist) {
				res.Moved = append(res.Moved, p)
				continue
			}
			res.Failed[p] = err
		}
	}
	if failed == batches {
		return res, fmt.Errorf("%w (%d): %w", ErrAllFailed, batches, firstErr)
	}
	return res, nil
}

// runBatch runs one backend command for a batch of absolute paths.
func (m *Mover) runBatch(ctx context.Context, backend string, batch []string) error {
	var name string
	var args []string
	switch backend {
	case BackendTrash:
		// /usr/bin/trash does not accept "--"; paths are absolute so none
		// can be mistaken for an option. -s stops at the first failure.
		name = trashBin
		args = append([]string{"-s"}, batch...)
	default:
		name = osascript
		args = []string{"-e", finderScript(batch)}
	}
	out, err := m.Run(ctx, name, args...)
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("trash: %s batch: %s: %w", backend, msg, err)
	}
	return fmt.Errorf("trash: %s batch: %w", backend, err)
}

// finderScript builds the AppleScript that asks Finder to trash the paths.
func finderScript(batch []string) string {
	var b strings.Builder
	b.WriteString(`tell application "Finder" to delete {`)
	for i, p := range batch {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`POSIX file "`)
		b.WriteString(appleEscape(p))
		b.WriteString(`"`)
	}
	b.WriteString("}")
	return b.String()
}

var appleEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// appleEscape escapes s for use inside an AppleScript string literal.
func appleEscape(s string) string { return appleEscaper.Replace(s) }
