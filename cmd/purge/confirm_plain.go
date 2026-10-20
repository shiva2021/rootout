package main

import (
	"github.com/shiva2021/purge/internal/plan"
	"github.com/shiva2021/purge/internal/tui"
)

// tuiConfirmer is the full-screen review used on a terminal.
func tuiConfirmer() plan.Confirmer { return tui.Confirmer{} }
