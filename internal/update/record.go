package update

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shiva2021/rootout/internal/fsx"
)

// MaxAge is how long a record counts as fresh: the home screen asks GitHub
// at most once per MaxAge.
const MaxAge = 24 * time.Hour

// recordVersion is the on-disk layout; a file with another version is
// treated as never checked and replaced on the next write.
const recordVersion = 1

// record is the file under ~/.cache/rootout. An empty Tag records a check
// that found nothing (GitHub unreachable), so the next launch waits too.
type record struct {
	Version   int       `json:"version"`
	CheckedAt time.Time `json:"checked_at"`
	Release
}

// Path returns $XDG_CACHE_HOME/rootout/update.json when XDG_CACHE_HOME is
// set, else ~/.cache/rootout/update.json. ROOTOUT_CACHE is not consulted:
// it names the assessments file, not a directory (D-038).
func Path(env fsx.Env, getenv func(string) string) string {
	if getenv != nil {
		if x := getenv("XDG_CACHE_HOME"); x != "" {
			return filepath.Join(x, "rootout", "update.json")
		}
	}
	return env.User(".cache", "rootout", "update.json")
}

// ReadRecord returns the recorded release and true when the file exists,
// parses, has this layout and was checked within MaxAge before now. A
// check stamped in the future counts as stale, so a clock set back does
// not freeze the record.
func ReadRecord(path string, now time.Time) (Release, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Release{}, false
	}
	var r record
	if err := json.Unmarshal(raw, &r); err != nil || r.Version != recordVersion || r.CheckedAt.IsZero() {
		return Release{}, false
	}
	age := now.Sub(r.CheckedAt)
	if age < 0 || age >= MaxAge {
		return Release{}, false
	}
	return r.Release, true
}

// WriteRecord saves r as checked at now, atomically (temp file + rename),
// 0600 in a 0700 directory.
func WriteRecord(path string, r Release, now time.Time) error {
	raw, err := json.MarshalIndent(record{Version: recordVersion, CheckedAt: now.UTC(), Release: r}, "", "  ")
	if err != nil {
		return fmt.Errorf("update: encode record: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("update: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".update-*.json")
	if err != nil {
		return fmt.Errorf("update: create temp in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("update: chmod %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("update: write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("update: close %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("update: rename to %s: %w", path, err)
	}
	return nil
}
