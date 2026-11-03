package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/priv"
	"github.com/shiva2021/rootout/internal/progress"
	"github.com/shiva2021/rootout/internal/testutil"
	"github.com/shiva2021/rootout/internal/tui"
	"github.com/shiva2021/rootout/internal/update"
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

func TestUpdateNotice(t *testing.T) {
	const exe = "/opt/homebrew/Caskroom/rootout/0.7.0/rootout"
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	tests := []struct {
		name     string
		enabled  bool
		version  string
		record   *update.Release // nil = no record on disk
		status   int
		body     string
		want     string
		wantHits int
		wantTag  string // tag in the record afterwards
	}{
		{"fresh record, newer", true, "v0.7.0", &update.Release{Tag: "v0.8.0"}, 200, releaseV080, "v0.8.0 available · " + brewUpgrade, 0, "v0.8.0"},
		{"fresh record, up to date", true, "v0.8.0", &update.Release{Tag: "v0.8.0"}, 200, releaseV080, "", 0, "v0.8.0"},
		{"fresh record, empty tag", true, "v0.7.0", &update.Release{}, 200, releaseV080, "", 0, ""},
		{"no record, fetch", true, "v0.7.0", nil, 200, releaseV080, "v0.8.0 available · " + brewUpgrade, 1, "v0.8.0"},
		{"no record, server down", true, "v0.7.0", nil, 500, "", "", 1, ""},
		{"disabled", false, "v0.7.0", nil, 200, releaseV080, "", 0, "-"},
		{"dev build", true, "dev", nil, 200, releaseV080, "", 0, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, hits := fakeGitHub(t, tt.status, tt.body)
			path := filepath.Join(t.TempDir(), "rootout", "update.json")
			if tt.record != nil {
				if err := update.WriteRecord(path, *tt.record, now.Add(-time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			notice := updateNotice(context.Background(), cl, tt.enabled, tt.version, path, exe, clock)
			if got := notice(); got != tt.want {
				t.Errorf("notice() = %q, want %q", got, tt.want)
			}
			if got := notice(); got != tt.want {
				t.Errorf("second notice() = %q, want %q", got, tt.want)
			}
			if *hits != tt.wantHits {
				t.Errorf("GitHub hits = %d, want %d (memoised, at most one)", *hits, tt.wantHits)
			}
			rec, ok := update.ReadRecord(path, now)
			switch {
			case tt.wantTag == "-" && ok:
				t.Errorf("record written = %+v, want none", rec)
			case tt.wantTag != "-" && (!ok || rec.Tag != tt.wantTag):
				t.Errorf("record = %+v, %v; want tag %q", rec, ok, tt.wantTag)
			}
		})
	}
}

func TestUpdateNoticeHintWithoutBrew(t *testing.T) {
	cl, _ := fakeGitHub(t, 200, releaseV080)
	path := filepath.Join(t.TempDir(), "update.json")
	got := updateNotice(context.Background(), cl, true, "v0.7.0", path, "/Users/me/.local/bin/rootout", time.Now)()
	if want := "v0.8.0 available · github.com/shiva2021/rootout/releases"; got != want {
		t.Errorf("notice = %q, want %q", got, want)
	}
}

func TestUpdateNoticeCancelledContextIsNotRecorded(t *testing.T) {
	cl, hits := fakeGitHub(t, 200, releaseV080)
	path := filepath.Join(t.TempDir(), "update.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := updateNotice(ctx, cl, true, "v0.7.0", path, "", time.Now)(); got != "" {
		t.Errorf("notice = %q, want empty", got)
	}
	if _, ok := update.ReadRecord(path, time.Now()); ok {
		t.Error("a cancelled context must not be recorded as a check")
	}
	if *hits != 0 {
		t.Errorf("GitHub hits = %d, want 0", *hits)
	}
}

func TestUpdateNoticeRendersParsedTag(t *testing.T) {
	cl, _ := fakeGitHub(t, 200, `{"tag_name":"v9.9.9-日本語\u001b[31m","html_url":"https://example.test/r"}`)
	got := updateNotice(context.Background(), cl, true, "v0.8.0", filepath.Join(t.TempDir(), "update.json"), "", time.Now)()
	if want := "v9.9.9 available · " + strings.TrimPrefix(update.ReleasesPage, "https://"); got != want {
		t.Errorf("notice = %q, want %q", got, want)
	}
}
