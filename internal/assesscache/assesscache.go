// Package assesscache keeps model assessments across runs so a paid
// provider is not asked twice about the same extension. It is a plain JSON
// file under the user's cache directory, keyed by what the model saw
// (extension, version, provider, model, prompt version and a fingerprint of
// the prompt). It never holds keys; it is derived data and safe to delete.
// Only cmd/ uses it: the TUI gets preloaded results and never persists.
package assesscache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/llm"
)

// EnvVar overrides the cache file path, like ROOTOUT_CONFIG does for config.
const EnvVar = "ROOTOUT_CACHE"

// fileVersion is the on-disk layout; a file with another version is
// treated as empty and replaced on the next store.
const fileVersion = 1

// Path returns $ROOTOUT_CACHE if set, else ~/.cache/rootout/assessments.json.
func Path(env fsx.Env, getenv func(string) string) string {
	if getenv != nil {
		if p := getenv(EnvVar); p != "" {
			return p
		}
	}
	return env.User(".cache", "rootout", "assessments.json")
}

// EntryID is what a saved assessment is keyed by: any change to it means
// the model would see something else and must be asked again.
type EntryID struct {
	Editor, ExtID, Version string
	Provider, Model        string
	PromptVersion          int
	Fingerprint            string
}

// IDFor builds the id for r assessed by provider's model.
func IDFor(r heur.Report, provider, model string) EntryID {
	return EntryID{
		Editor:        r.Ext.Editor,
		ExtID:         r.Ext.ID,
		Version:       r.Ext.Version,
		Provider:      provider,
		Model:         model,
		PromptVersion: llm.PromptVersion,
		Fingerprint:   llm.Fingerprint(r),
	}
}

// String is the map key, "editor|ext|version|provider|model|p<N>|fingerprint".
func (id EntryID) String() string {
	return strings.Join([]string{id.Editor, id.ExtID, id.Version, id.Provider, id.Model, fmt.Sprintf("p%d", id.PromptVersion), id.Fingerprint}, "|")
}

// Entry is one saved assessment and when it was made.
type Entry struct {
	Assessment llm.Assessment `json:"assessment"`
	At         time.Time      `json:"at"`
}

type file struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// Cache is the in-memory view of the file. Store writes through, so a
// quit mid-scan loses nothing; a mutex guards concurrent workers.
type Cache struct {
	path    string
	mu      sync.Mutex
	entries map[string]Entry
}

// Open reads path. A missing file is an empty cache; an unreadable or
// foreign file is logged and treated as empty (it is replaced on the next
// Store). Open never creates the file.
func Open(path string) (*Cache, error) {
	c := &Cache{path: path, entries: map[string]Entry{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assesscache: read %s: %w", path, err)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != fileVersion {
		slog.Warn("assesscache: ignoring unreadable cache file", "path", path, "err", err, "version", f.Version)
		return c, nil
	}
	if f.Entries != nil {
		c.entries = f.Entries
	}
	return c, nil
}

// Path is the file the cache reads and writes.
func (c *Cache) Path() string { return c.path }

// Len is the number of saved assessments.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Lookup returns the saved assessment for id, marked Cached.
func (c *Cache) Lookup(id EntryID) (llm.Assessment, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id.String()]
	if !ok {
		return llm.Assessment{}, false
	}
	a := e.Assessment
	a.Cached = true
	return a, true
}

// Store saves a for id and writes the file atomically (0600 in a 0700
// directory). Cached is cleared on disk: it describes a lookup, not an
// answer.
func (c *Cache) Store(id EntryID, a llm.Assessment) error {
	a.Cached = false
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id.String()] = Entry{Assessment: a, At: time.Now().UTC()}
	return c.write()
}

// write is the atomic save; the caller holds mu.
func (c *Cache) write() error {
	raw, err := json.MarshalIndent(file{Version: fileVersion, Entries: c.entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("assesscache: encode: %w", err)
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("assesscache: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".assessments-*.json")
	if err != nil {
		return fmt.Errorf("assesscache: create temp in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("assesscache: chmod %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("assesscache: write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("assesscache: close %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), c.path); err != nil {
		return fmt.Errorf("assesscache: rename to %s: %w", c.path, err)
	}
	return nil
}

// Clear deletes the file; removed is false when there was none.
func Clear(path string) (removed bool, err error) {
	err = os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("assesscache: remove %s: %w", path, err)
	}
	return true, nil
}
