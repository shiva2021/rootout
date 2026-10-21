package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/priv"
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

func TestUninstallErr(t *testing.T) {
	if err := uninstallErr(0); err != nil {
		t.Fatalf("uninstallErr(0) = %v, want nil", err)
	}
	err := uninstallErr(2)
	var ee *exitErr
	if !errors.As(err, &ee) || ee.code != exitError || ee.msg != "2 extension(s) could not be uninstalled" {
		t.Fatalf("uninstallErr(2) = %#v", err)
	}
}

func TestRemovalFlash(t *testing.T) {
	mk := func(mode plan.Mode) *plan.Plan {
		items := []plan.Item{
			{Leftover: leftover.Leftover{Path: "/a", SizeBytes: 5_000_000}, Selected: true},
			{Leftover: leftover.Leftover{Path: "/b", SizeBytes: 2_000_000}, Selected: true},
			{Leftover: leftover.Leftover{Path: "/c", SizeBytes: 1_000_000}, Selected: true},
		}
		return plan.New("x", items, mode, false)
	}
	tests := []struct {
		name    string
		mode    plan.Mode
		res     *plan.Result
		err     error
		want    string
		wantErr bool
	}{
		{"partial failure", plan.Trash,
			&plan.Result{Removed: []string{"/a", "/b"}, Failed: map[string]string{"/c": "x"}},
			nil, "Removed Spotify · 2 items · 7 MB → Trash · 1 failed", true},
		{"singular clean", plan.Trash,
			&plan.Result{Removed: []string{"/c"}}, nil,
			"Removed Spotify · 1 item · 1 MB → Trash", false},
		{"permanent", plan.Permanent,
			&plan.Result{Removed: []string{"/a"}}, nil,
			"Removed Spotify · 1 item · 5 MB deleted", false},
		{"brew failed", plan.Trash,
			&plan.Result{Removed: []string{"/a"}, BrewError: "boom"}, nil,
			"Removed Spotify · 1 item · 5 MB → Trash · brew failed", true},
		{"cancelled", plan.Trash,
			&plan.Result{}, priv.ErrCancelled,
			"Spotify: admin prompt cancelled", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := removalFlash("Spotify", mk(tc.mode), tc.res, tc.err)
			if got.Text != tc.want || got.Err != tc.wantErr {
				t.Fatalf("got %+v, want {%q %v}", got, tc.want, tc.wantErr)
			}
			if tc.mode == plan.Permanent && !strings.Contains(got.Text, "deleted") {
				t.Fatalf("missing deleted: %q", got.Text)
			}
		})
	}
}

func TestScanFlash(t *testing.T) {
	tests := []struct {
		done, failed int
		want         string
		wantErr      bool
	}{
		{1, 0, "Uninstalled 1 extension", false},
		{2, 0, "Uninstalled 2 extensions", false},
		{2, 1, "Uninstalled 2 extensions · 1 failed", true},
	}
	for _, tc := range tests {
		got := scanFlash(tc.done, tc.failed)
		if got.Text != tc.want || got.Err != tc.wantErr {
			t.Errorf("scanFlash(%d,%d) = %+v, want {%q %v}", tc.done, tc.failed, got, tc.want, tc.wantErr)
		}
	}
}
