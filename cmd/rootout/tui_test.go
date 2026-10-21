package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/shiva2021/rootout/internal/progress"
	"github.com/shiva2021/rootout/internal/testutil"
	"github.com/shiva2021/rootout/internal/tui"
)

func TestDeclinedOnQuit(t *testing.T) {
	other := errors.New("boom")
	tests := []struct {
		name     string
		in       error
		wantCode int // 0 = expect the input back unchanged
	}{
		{"nil", nil, 0},
		{"quit", tui.ErrQuit, exitDeclined},
		{"wrapped quit", fmt.Errorf("x: %w", tui.ErrQuit), exitDeclined},
		{"other", other, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := declinedOnQuit(tc.in, "nothing removed")
			if tc.wantCode == 0 {
				if !errors.Is(got, tc.in) {
					t.Fatalf("got %v, want %v unchanged", got, tc.in)
				}
				return
			}
			var ee *exitErr
			if !errors.As(got, &ee) || ee.code != tc.wantCode || ee.msg != "nothing removed" {
				t.Fatalf("got %#v, want exitErr{%d, nothing removed}", got, tc.wantCode)
			}
		})
	}
}

func TestIsExit(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"match", &exitErr{code: exitDeclined}, true},
		{"other code", &exitErr{code: exitError}, false},
		{"nil", nil, false},
		{"plain error", errors.New("x"), false},
		{"wrapped", fmt.Errorf("w: %w", &exitErr{code: exitDeclined}), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isExit(tc.err, exitDeclined); got != tc.want {
				t.Fatalf("isExit = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScanReportsEmptyHome(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	testutil.RequireSandbox(t, env)
	shown, total, err := scanReports(context.Background(), env, scanFlags{minScore: 30}, progress.Nop())
	if err != nil || total != 0 || len(shown) != 0 {
		t.Fatalf("got shown=%d total=%d err=%v", len(shown), total, err)
	}
}

// findOrphans has no smoke test: it hard-wires run.Default, which would exec
// launchctl and ps against the real machine.
