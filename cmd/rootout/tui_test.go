package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/license"
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
			"Nothing removed from Spotify · admin prompt cancelled", true},
		{"cancelled after removals", plan.Trash,
			&plan.Result{Removed: []string{"/a", "/b"}}, priv.ErrCancelled,
			"Removed Spotify · 2 items · 7 MB → Trash · admin prompt cancelled", true},
		{"all failed", plan.Trash,
			&plan.Result{Failed: map[string]string{"/a": "x", "/b": "x", "/c": "x"}}, nil,
			"Nothing removed from Spotify · 3 failed", true},
		{"only skipped", plan.Trash,
			&plan.Result{Skipped: []string{"/a"}}, nil,
			"Spotify was already gone", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := removalFlash("Spotify", mk(tc.mode), tc.res, tc.err)
			if got.Text != tc.want || got.Err != tc.wantErr {
				t.Fatalf("got %+v, want {%q %v}", got, tc.want, tc.wantErr)
			}
		})
	}
}

func TestErrorFlash(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain", "boom", "boom"},
		{"multi-line", "first\nsecond", "first"},
		{"tui prefix", "tui: pick app: boom", "pick app: boom"},
		{"both", "tui: home: x\ny", "home: x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := errorFlash(errors.New(tc.in))
			if got.Text != tc.want || !got.Err {
				t.Fatalf("got %+v, want {%q true}", got, tc.want)
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

// fakeConfirmer answers Confirm with ok and counts calls.
type fakeConfirmer struct {
	ok    bool
	calls int
}

func (c *fakeConfirmer) Confirm(context.Context, *plan.Plan) (bool, error) {
	c.calls++
	return c.ok, nil
}

// testHomeDeps drives the home actions against h: the picker returns Foo
// once and then backs out, the gate is the real one over h's fakes.
func testHomeDeps(t *testing.T, h *harness, c *fakeConfirmer) homeDeps {
	t.Helper()
	picks := 0
	return homeDeps{
		pick: func(_ context.Context, load tui.LoadFunc) (app.App, bool, error) {
			picks++
			if picks > 1 {
				return app.App{}, false, nil
			}
			apps, err := load(progress.Nop())
			if err != nil {
				return app.App{}, false, err
			}
			for _, a := range apps {
				if a.BundleID == "com.foo.app" {
					return a, true, nil
				}
			}
			t.Fatal("Foo.app not discovered in the fake home")
			return app.App{}, false, nil
		},
		confirm: c,
		run:     h.run,
		gate:    gate,
		execute: func(context.Context, fsx.Env, *plan.Plan, plan.Deps) (*plan.Result, error) {
			h.execs.Add(1)
			return nil, nil
		},
	}
}

func TestHomeUninstallCancelKeepsTrial(t *testing.T) {
	h := newHarness(t, appSpec())
	c := &fakeConfirmer{ok: false}
	f, err := homeUninstall(context.Background(), h.env, testHomeDeps(t, h, c))
	if err != nil || f.Err || f.Text != "" {
		t.Fatalf("flash %+v err %v; cancel goes back to the picker, which backs out", f, err)
	}
	if c.calls != 1 || h.trial.Load() != 0 || h.execs.Load() != 0 {
		t.Fatalf("confirm %d, /trial %d, execs %d; want 1, 0, 0", c.calls, h.trial.Load(), h.execs.Load())
	}
}

func TestHomeOrphansCancelKeepsTrial(t *testing.T) {
	h := newHarness(t, appSpec())
	c := &fakeConfirmer{ok: false}
	f, err := homeOrphans(context.Background(), h.env, testHomeDeps(t, h, c))
	if err != nil || f.Text != "Nothing removed" {
		t.Fatalf("flash %+v err %v", f, err)
	}
	if c.calls != 1 || h.trial.Load() != 0 || h.execs.Load() != 0 {
		t.Fatalf("confirm %d, /trial %d, execs %d; want 1, 0, 0", c.calls, h.trial.Load(), h.execs.Load())
	}
}

func TestHomeUninstallConfirmStartsTrial(t *testing.T) {
	h := newHarness(t, appSpec())
	c := &fakeConfirmer{ok: true}
	f, err := homeUninstall(context.Background(), h.env, testHomeDeps(t, h, c))
	if err != nil || !strings.HasPrefix(f.Text, "7-day trial started · ") {
		t.Fatalf("flash %+v err %v", f, err)
	}
	if h.trial.Load() != 1 || h.execs.Load() != 1 {
		t.Fatalf("/trial %d, execs %d; want 1, 1", h.trial.Load(), h.execs.Load())
	}
}

func TestHomeUninstallTrialUsedDeniedBeforeConfirm(t *testing.T) {
	h := newHarness(t, appSpec())
	h.trialStatus = http.StatusConflict
	// A previous action in this session already learnt the trial is used.
	if _, err := gate(context.Background(), h.env, true); err == nil {
		t.Fatal("setup: want a denial")
	}
	c := &fakeConfirmer{ok: true}
	d := testHomeDeps(t, h, c)
	for name, run := range map[string]func(context.Context, fsx.Env, homeDeps) (tui.Flash, error){
		"uninstall": homeUninstall, "orphans": homeOrphans,
	} {
		f, err := run(context.Background(), h.env, d)
		if err != nil || !f.Err || f.Text != deniedTrialUsedLine {
			t.Fatalf("%s: flash %+v err %v", name, f, err)
		}
	}
	if c.calls != 0 || h.execs.Load() != 0 || h.trial.Load() != 1 {
		t.Fatalf("confirm %d, execs %d, /trial %d; want 0, 0, 1", c.calls, h.execs.Load(), h.trial.Load())
	}
}

func TestHomeUninstallTrialRefusedAfterConfirm(t *testing.T) {
	h := newHarness(t, appSpec())
	h.trialStatus = http.StatusConflict
	c := &fakeConfirmer{ok: true}
	f, err := homeUninstall(context.Background(), h.env, testHomeDeps(t, h, c))
	if err != nil || !f.Err || f.Text != deniedTrialUsedLine {
		t.Fatalf("flash %+v err %v", f, err)
	}
	if c.calls != 1 || h.execs.Load() != 0 {
		t.Fatalf("confirm %d, execs %d; want 1, 0", c.calls, h.execs.Load())
	}
}

func TestHomeGateFakeDenied(t *testing.T) {
	h := newHarness(t, appSpec())
	c := &fakeConfirmer{ok: true}
	d := testHomeDeps(t, h, c)
	d.gate = func(context.Context, fsx.Env, bool) (license.State, error) {
		st := license.State{Status: license.StatusExpired}
		return st, &membershipError{st: st}
	}
	f, err := homeUninstall(context.Background(), h.env, d)
	if err != nil || !f.Err || !strings.HasPrefix(f.Text, "deletion and AI features are for members (expired") || !strings.HasSuffix(f.Text, ") · rootout license") {
		t.Fatalf("flash %+v err %v", f, err)
	}
	if c.calls != 0 {
		t.Fatal("confirm opened after a denial")
	}
}

func TestHomeUninstallFlashCarriesWarning(t *testing.T) {
	h := newHarness(t, appSpec())
	c := &fakeConfirmer{ok: true}
	d := testHomeDeps(t, h, c)
	d.gate = func(context.Context, fsx.Env, bool) (license.State, error) {
		return license.State{Status: license.StatusGrace, Allowed: true, Warning: graceWarning}, nil
	}
	f, err := homeUninstall(context.Background(), h.env, d)
	if err != nil || !strings.HasSuffix(f.Text, " · "+graceWarning) {
		t.Fatalf("flash %+v err %v", f, err)
	}
	if h.execs.Load() != 1 {
		t.Fatalf("execs %d, want 1", h.execs.Load())
	}
}
