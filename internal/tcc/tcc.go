// Package tcc probes the macOS privacy permissions (Transparency, Consent and
// Control) that rootout depends on. Today that is Full Disk Access: without
// it the terminal cannot remove app containers directly under
// ~/Library/Containers, and neither can Finder, /usr/bin/trash or root.
package tcc

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/shiva2021/rootout/internal/fsx"
)

// Access is the result of a permission probe.
type Access int

const (
	Unknown Access = iota // no protected folder exists to probe
	Granted
	Denied
)

func (a Access) String() string {
	switch a {
	case Granted:
		return "granted"
	case Denied:
		return "denied"
	}
	return "unknown"
}

// Note is the item note set on paths that need Full Disk Access.
const Note = "needs Full Disk Access"

// SettingsPath is where the user grants the permission.
const SettingsPath = "System Settings › Privacy & Security › Full Disk Access"

// probeDirs are folders under ~/Library that macOS lets only Full Disk
// Access apps list; the first one that exists decides.
var probeDirs = [][]string{
	{"Safari"},
	{"Mail"},
	{"Application Support", "com.apple.TCC"},
}

// FullDiskAccess reports whether this process can read the folders macOS
// reserves for Full Disk Access. A permission error on an existing folder
// means Denied; a successful listing means Granted; when no probe folder
// exists the answer is Unknown and callers should not warn.
func FullDiskAccess(env fsx.Env) Access {
	for _, d := range probeDirs {
		_, err := os.ReadDir(env.Library(d...))
		switch {
		case err == nil:
			return Granted
		case errors.Is(err, fs.ErrPermission):
			return Denied
		}
	}
	return Unknown
}

// NeedsFullDiskAccess reports whether removing path needs Full Disk Access:
// a direct child of ~/Library/Containers. Deeper paths and other folders are
// removable by the user alone.
func NeedsFullDiskAccess(env fsx.Env, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(env.Library("Containers"), filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	return !strings.Contains(rel, string(filepath.Separator))
}

// terminalNames maps TERM_PROGRAM values to the app name shown in System
// Settings.
var terminalNames = map[string]string{
	"Apple_Terminal": "Terminal",
	"iTerm.app":      "iTerm",
	"WarpTerminal":   "Warp",
	"vscode":         "Visual Studio Code",
	"ghostty":        "Ghostty",
	"Hyper":          "Hyper",
	"kitty":          "kitty",
	"WezTerm":        "WezTerm",
	"tmux":           "your terminal app",
}

// TerminalApp names the terminal app to grant the permission to, from the
// TERM_PROGRAM variable, or "your terminal app" when it is not recognised.
func TerminalApp(getenv func(string) string) string {
	if name, ok := terminalNames[getenv("TERM_PROGRAM")]; ok {
		return name
	}
	return "your terminal app"
}
