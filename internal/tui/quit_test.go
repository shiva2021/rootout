package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRunErr(t *testing.T) {
	other := errors.New("boom")
	if got := runErr(nil, "x"); got != nil {
		t.Fatalf("nil: got %v", got)
	}
	if got := runErr(tea.ErrInterrupted, "x"); !errors.Is(got, ErrQuit) {
		t.Fatalf("interrupt: got %v, want ErrQuit", got)
	}
	got := runErr(other, "pick app")
	if !errors.Is(got, other) || got.Error() != "tui: pick app: boom" {
		t.Fatalf("other: got %v", got)
	}
}
