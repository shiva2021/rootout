// Package fsx holds the small filesystem helpers shared by every package that
// reads or deletes files. Everything takes an Env so tests can point the whole
// tool at a temporary directory instead of the real home.
package fsx

import (
	"os"
	"path/filepath"
	"strings"
)

// Env describes where the tool looks for files.
//
// Home is the user's home directory (normally os.UserHomeDir()).
// Root is prepended to every absolute system path such as /Applications or
// /Library; it is "/" in production and a temp dir in tests.
type Env struct {
	Home string
	Root string
}

// Real returns the production Env.
func Real() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	return Env{Home: home, Root: "/"}, nil
}

// Sys resolves a system-absolute path such as "/Library/Preferences" under Root.
func (e Env) Sys(parts ...string) string {
	return filepath.Join(append([]string{e.Root}, parts...)...)
}

// User resolves a path under Home, e.g. e.User("Library", "Caches").
func (e Env) User(parts ...string) string {
	return filepath.Join(append([]string{e.Home}, parts...)...)
}

// Library is shorthand for the per-user ~/Library.
func (e Env) Library(parts ...string) string {
	return e.User(append([]string{"Library"}, parts...)...)
}

// UnderHome reports whether p (already cleaned and absolute) is inside Home.
func (e Env) UnderHome(p string) bool {
	rel, err := filepath.Rel(e.Home, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
