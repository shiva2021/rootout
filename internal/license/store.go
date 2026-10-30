package license

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shiva2021/rootout/internal/fsx"
)

// ErrNoKey means no licence key is installed.
var ErrNoKey = errors.New("license: no key installed")

// Path returns where the key lives: $ROOTOUT_LICENSE_FILE, else ~/.config/rootout/license.
func Path(env fsx.Env, getenv func(string) string) string {
	if p := getenv("ROOTOUT_LICENSE_FILE"); p != "" {
		return p
	}
	return env.User(".config", "rootout", "license")
}

// Store reads and writes the key file and the renewal stamp beside it.
type Store struct{ Path string }

func (s Store) stamp() string { return s.Path + ".renew" }

// Load returns the installed key with surrounding whitespace trimmed.
func (s Store) Load() (string, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoKey
	}
	if err != nil {
		return "", fmt.Errorf("license: read %s: %w", s.Path, err)
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return "", ErrNoKey
	}
	return key, nil
}

// Save writes key with mode 0600, replacing any existing key atomically.
func (s Store) Save(key string) error {
	return writePrivate(s.Path, key+"\n")
}

// Remove deletes the key and the renewal stamp. Missing files are not an error.
func (s Store) Remove() error {
	for _, p := range []string{s.Path, s.stamp()} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("license: remove %s: %w", p, err)
		}
	}
	return nil
}

// LastRenew returns when renewal was last attempted; ok is false when unknown.
func (s Store) LastRenew() (time.Time, bool) {
	b, err := os.ReadFile(s.stamp())
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// MarkRenew records a renewal attempt at t.
func (s Store) MarkRenew(t time.Time) error {
	return writePrivate(s.stamp(), t.UTC().Format(time.RFC3339)+"\n")
}

// writePrivate writes data to path via a 0600 temp file and a rename, creating
// the directory with mode 0700 when needed.
func writePrivate(path, data string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("license: create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".license-*")
	if err != nil {
		return fmt.Errorf("license: write %s: %w", path, err)
	}
	tmp := f.Name()
	_, werr := f.WriteString(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("license: write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("license: write %s: %w", path, err)
	}
	return nil
}
