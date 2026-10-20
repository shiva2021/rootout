package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shiva2021/rootout/internal/fsx"
)

// caskIndex maps installed Homebrew casks to the .app bundles they ship, built
// once per Discover so lookups stay constant time. Detection only; uninstall
// via brew lives in a later package.
type caskIndex struct {
	rooms  []string          // existing Caskroom directories
	byBase map[string]string // "Ghostty.app" -> "ghostty"
}

// caskrooms are the Caskroom locations for Apple-silicon and Intel Homebrew.
var caskrooms = [][]string{
	{"opt", "homebrew", "Caskroom"},
	{"usr", "local", "Caskroom"},
}

// loadCasks scans Caskroom/<token>/<version>/*.app in every existing Caskroom.
func loadCasks(env fsx.Env) caskIndex {
	idx := caskIndex{byBase: map[string]string{}}
	for _, parts := range caskrooms {
		room := env.Sys(parts...)
		tokens, err := os.ReadDir(room)
		if err != nil {
			continue
		}
		// Resolve the room itself so symlinked bundles compare against the
		// same canonical prefix that EvalSymlinks yields for them.
		if real, err := filepath.EvalSymlinks(room); err == nil {
			room = real
		}
		idx.rooms = append(idx.rooms, room)
		for _, tok := range tokens {
			if !tok.IsDir() {
				continue
			}
			versions, err := os.ReadDir(filepath.Join(room, tok.Name()))
			if err != nil {
				continue
			}
			for _, ver := range versions {
				entries, err := os.ReadDir(filepath.Join(room, tok.Name(), ver.Name()))
				if err != nil {
					continue
				}
				for _, e := range entries {
					if isBundleName(e.Name()) {
						idx.byBase[e.Name()] = tok.Name()
					}
				}
			}
		}
	}
	return idx
}

// lookup returns the cask token for the bundle at path, or "" when it was not
// installed by Homebrew. A bundle matches when a Caskroom entry shares its
// basename, or when path is a symlink that resolves into a Caskroom.
func (c caskIndex) lookup(path string) string {
	if len(c.rooms) == 0 {
		return ""
	}
	if tok, ok := c.byBase[filepath.Base(path)]; ok {
		return tok
	}
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	for _, room := range c.rooms {
		if real == room || !under(room, real) {
			continue
		}
		rel, _ := filepath.Rel(room, real)
		return strings.SplitN(rel, string(filepath.Separator), 2)[0]
	}
	return ""
}
