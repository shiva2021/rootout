package tui

import "errors"

// ErrQuit is returned by the screen entry points when the user asked to leave
// rootout (ctrl+x or ctrl+c) rather than just this screen. esc and q end a
// screen with ok=false and the caller decides what "back" means.
var ErrQuit = errors.New("tui: quit")

// isQuitKey reports whether k is one of the keys that quit rootout.
func isQuitKey(k string) bool { return k == "ctrl+x" || k == "ctrl+c" }
