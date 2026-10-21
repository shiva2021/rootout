package main

import (
	"errors"

	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/tui"
)

// tuiConfirmer is the full-screen review used on a terminal.
func tuiConfirmer() plan.Confirmer { return tui.Confirmer{} }

// declinedOnQuit turns tui.ErrQuit into the declined exit so ctrl+x and esc
// look the same to scripts; any other error passes through.
func declinedOnQuit(err error, msg string) error {
	if errors.Is(err, tui.ErrQuit) {
		return &exitErr{code: exitDeclined, msg: msg}
	}
	return err
}
