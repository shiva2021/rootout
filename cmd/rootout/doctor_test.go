package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/update"
)

func TestUpgradeHint(t *testing.T) {
	tests := []struct {
		exe  string
		want string
	}{
		{"/opt/homebrew/Caskroom/rootout/0.7.0/rootout", brewUpgrade},
		{"/usr/local/Caskroom/rootout/0.7.0/rootout", brewUpgrade},
		{"/Users/me/.local/bin/rootout", "https://example.test/v0.8.0"},
		{"", "https://example.test/v0.8.0"},
	}
	for _, tt := range tests {
		t.Run(tt.exe, func(t *testing.T) {
			if got := upgradeHint(tt.exe, "https://example.test/v0.8.0"); got != tt.want {
				t.Errorf("upgradeHint(%q) = %q, want %q", tt.exe, got, tt.want)
			}
		})
	}
}

func fakeGitHub(t *testing.T, status int, body string) (*update.Client, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cl := update.New("v0.7.0")
	cl.URL = srv.URL
	return cl, &hits
}

const releaseV080 = `{"tag_name":"v0.8.0","html_url":"https://github.com/shiva2021/rootout/releases/tag/v0.8.0","published_at":"2026-11-05T01:02:03Z"}`

func TestUpdateCheck(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		version   string
		status    int
		body      string
		wantOK    bool
		wantHits  int
		wantText  string
		wantFix   string
		wantWrite bool
	}{
		{"off", false, "v0.7.0", 200, releaseV080, true, 0, "off (rootout config set updates.check true)", "", false},
		{"dev build", true, "dev", 200, releaseV080, true, 0, "dev build; cannot compare with the latest release", "", false},
		{"unreachable", true, "v0.7.0", 500, "", true, 1, "could not reach GitHub", "", false},
		{"newer", true, "v0.7.0", 200, releaseV080, false, 1, "v0.8.0 available (installed v0.7.0, released 2026-11-05)", brewUpgrade, true},
		{"up to date", true, "v0.8.0-2-gabc1234", 200, releaseV080, true, 1, "up to date (latest v0.8.0)", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl, hits := fakeGitHub(t, tt.status, tt.body)
			path := filepath.Join(t.TempDir(), "rootout", "update.json")
			c := updateCheck(context.Background(), cl, tt.enabled, tt.version, path, "/opt/homebrew/Caskroom/rootout/0.7.0/rootout")
			if c.Name != "Update" || c.OK != tt.wantOK || !strings.Contains(c.Detail, tt.wantText) || c.Fix != tt.wantFix {
				t.Errorf("check = %+v; want ok=%v detail containing %q fix %q", c, tt.wantOK, tt.wantText, tt.wantFix)
			}
			if *hits != tt.wantHits {
				t.Errorf("GitHub hits = %d, want %d", *hits, tt.wantHits)
			}
			_, ok := update.ReadRecord(path, time.Now())
			if ok != tt.wantWrite {
				t.Errorf("record written = %v, want %v", ok, tt.wantWrite)
			}
		})
	}
}
