package ext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/shiva2021/purge/internal/fsx"
	"github.com/shiva2021/purge/internal/safety"
)

// ErrEditorRunning is returned when the editor is open, no CLI is available,
// and removing the folder behind its back would be undone or corrupt state.
var ErrEditorRunning = errors.New("ext: editor is running; quit it or install its command-line tool")

// Uninstall removes e. With an editor CLI it runs
// `<cli> --uninstall-extension <id>` and then removes whatever is left of the
// folder. Without one it refuses while the editor is running, else removes the
// folder and drops the entry from extensions.json and .obsolete. e.Dir must be
// directly inside the editor's ExtDir.
func Uninstall(ctx context.Context, env fsx.Env, e Extension, editors []Editor, opts Options) error {
	ed, ok := findEditor(editors, e.Editor)
	if !ok {
		return fmt.Errorf("ext: unknown editor %q", e.Editor)
	}
	dir, err := validateDir(env, ed, e.Dir)
	if err != nil {
		return err
	}
	runner := opts.runner()

	if ed.CLI != "" {
		if out, err := runner(ctx, ed.CLI, "--uninstall-extension", e.ID); err != nil {
			// The CLI refuses extensions it does not consider installed
			// (orphans); the folder removal below still applies.
			slog.Debug("ext: cli uninstall", "cli", ed.CLI, "id", e.ID, "err", err, "out", string(out))
		}
	} else {
		running, err := IsRunning(ctx, ed, opts)
		if err != nil {
			return err
		}
		if running {
			return ErrEditorRunning
		}
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("ext: remove %s: %w", dir, err)
	}
	folder := filepath.Base(dir)
	if err := dropFromExtensionsJSON(filepath.Join(ed.ExtDir, extensionsJSON), folder); err != nil {
		return err
	}
	return dropFromObsolete(filepath.Join(ed.ExtDir, obsoleteFile), folder)
}

// IsRunning reports whether the editor's app bundle has a live process. It
// matches on the bundle's MacOS directory because every Electron app reports
// its process name as "Electron" or the product name. Without an AppPath the
// check is skipped and false is returned.
func IsRunning(ctx context.Context, ed Editor, opts Options) (bool, error) {
	if ed.AppPath == "" {
		return false, nil
	}
	pattern := filepath.Join(ed.AppPath, "Contents", "MacOS") + string(filepath.Separator)
	_, err := opts.runner()(ctx, "pgrep", "-f", pattern)
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exitErr):
		return false, nil // exit 1: no match
	case errors.Is(err, exec.ErrNotFound):
		slog.Debug("ext: pgrep not found; skipping running check")
		return false, nil
	default:
		return false, fmt.Errorf("ext: pgrep: %w", err)
	}
}

func findEditor(editors []Editor, id string) (Editor, bool) {
	for _, ed := range editors {
		if ed.ID == id {
			return ed, true
		}
	}
	return Editor{}, false
}

// validateDir returns the cleaned dir once it is known to be a direct child of
// ed.ExtDir (itself under home), not a manifest file, and accepted by safety.
func validateDir(env fsx.Env, ed Editor, dir string) (string, error) {
	extDir := filepath.Clean(ed.ExtDir)
	if !filepath.IsAbs(extDir) || !env.UnderHome(extDir) {
		return "", fmt.Errorf("ext: extension dir %s is not under home", ed.ExtDir)
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("ext: %s is not absolute", dir)
	}
	clean := filepath.Clean(dir)
	base := filepath.Base(clean)
	if filepath.Dir(clean) != extDir || base == "." || base == ".." || base == string(filepath.Separator) {
		return "", fmt.Errorf("ext: %s is not directly inside %s", dir, extDir)
	}
	if base == extensionsJSON || base == obsoleteFile {
		return "", fmt.Errorf("ext: %s is a manifest, not an extension", dir)
	}
	if err := safety.Validate(env, clean, safety.Options{AllowDotfiles: true}); err != nil {
		return "", err
	}
	return clean, nil
}

// dropFromExtensionsJSON rewrites path without the entries for folder. Other
// entries are kept byte-for-byte (compacted); nothing is written when the
// file is missing or already lacks the entry.
func dropFromExtensionsJSON(path, folder string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ext: read %s: %w", path, err)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("ext: parse %s: %w", path, err)
	}
	kept := make([]json.RawMessage, 0, len(raw))
	for _, r := range raw {
		var x extensionEntry
		if err := json.Unmarshal(r, &x); err != nil {
			return fmt.Errorf("ext: parse %s entry: %w", path, err)
		}
		if x.folder() == folder {
			continue
		}
		kept = append(kept, r)
	}
	if len(kept) == len(raw) {
		return nil
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return fmt.Errorf("ext: encode %s: %w", path, err)
	}
	return writeAtomic(path, out)
}

// dropFromObsolete removes folder from the .obsolete object, if present.
func dropFromObsolete(path, folder string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ext: read %s: %w", path, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("ext: parse %s: %w", path, err)
	}
	if _, ok := m[folder]; !ok {
		return nil
	}
	delete(m, folder)
	out, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("ext: encode %s: %w", path, err)
	}
	return writeAtomic(path, out)
}

// writeAtomic writes data to a temp file beside path and renames it over,
// keeping the original's permission bits.
func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".purge-*")
	if err != nil {
		return fmt.Errorf("ext: temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("ext: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("ext: close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return fmt.Errorf("ext: chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("ext: rename %s: %w", path, err)
	}
	return nil
}
