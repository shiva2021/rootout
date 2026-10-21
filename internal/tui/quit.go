package tui

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// ErrQuit is returned by the screen entry points when the user asked to leave
// rootout (ctrl+x or ctrl+c) rather than just this screen. esc and q end a
// screen with ok=false and the caller decides what "back" means.
var ErrQuit = errors.New("tui: quit")

// isQuitKey reports whether k is one of the keys that quit rootout.
func isQuitKey(k string) bool { return k == "ctrl+x" || k == "ctrl+c" }

// runErr turns the error from tea.Program.Run into the screen's error:
// nil stays nil, an interrupt (SIGINT/SIGTERM) becomes ErrQuit, anything
// else is wrapped as "tui: <what>: ...".
func runErr(err error, what string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, tea.ErrInterrupted) {
		return ErrQuit
	}
	return fmt.Errorf("tui: %s: %w", what, err)
}
