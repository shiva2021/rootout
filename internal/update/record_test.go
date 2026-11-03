package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/testutil"
)

var now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func TestPath(t *testing.T) {
	env := fsx.Env{Home: "/h", Root: "/"}
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default", nil, "/h/.cache/rootout/update.json"},
		{"xdg", map[string]string{"XDG_CACHE_HOME": "/x"}, "/x/rootout/update.json"},
		{"ROOTOUT_CACHE is the assessments file, not a directory", map[string]string{"ROOTOUT_CACHE": "/elsewhere/a.json"}, "/h/.cache/rootout/update.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Path(env, func(k string) string { return tt.env[k] }); got != tt.want {
				t.Errorf("Path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecordRoundTrip(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	testutil.RequireSandbox(t, env)
	path := Path(env, nil)
	r := Release{Tag: "v0.8.0", URL: "https://example.test/v0.8.0", PublishedAt: now.Add(-time.Hour)}
	if err := WriteRecord(path, r, now); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadRecord(path, now.Add(time.Hour))
	if !ok || got != r {
		t.Errorf("ReadRecord = %+v, %v; want %+v, true", got, ok, r)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", di.Mode().Perm())
	}
}

func TestRecordEmptyTagIsFreshWithoutRelease(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	testutil.RequireSandbox(t, env)
	path := Path(env, nil)
	if err := WriteRecord(path, Release{}, now); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadRecord(path, now.Add(time.Hour))
	if !ok || got.Tag != "" {
		t.Errorf("ReadRecord = %+v, %v; want empty release, true", got, ok)
	}
}

func TestReadRecordNotOK(t *testing.T) {
	tests := []struct {
		name    string
		content string
		at      time.Time
	}{
		{"missing", "", now},
		{"corrupt", "{nope", now},
		{"foreign version", `{"version":2,"checked_at":"2026-11-05T12:00:00Z","latest":"v0.8.0"}`, now},
		{"stale", `{"version":1,"checked_at":"2026-11-05T12:00:00Z","latest":"v0.8.0"}`, now.Add(MaxAge + time.Second)},
		{"future checked_at", `{"version":1,"checked_at":"2026-11-05T12:00:00Z","latest":"v0.8.0"}`, now.Add(-time.Minute)},
		{"no checked_at", `{"version":1,"latest":"v0.8.0"}`, now},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.FakeHome(t, testutil.Spec{})
			testutil.RequireSandbox(t, env)
			path := Path(env, nil)
			if tt.content != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if r, ok := ReadRecord(path, tt.at); ok {
				t.Errorf("ReadRecord = %+v, true; want ok=false", r)
			}
		})
	}
}

func TestReadRecordJustInsideMaxAge(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	testutil.RequireSandbox(t, env)
	path := Path(env, nil)
	if err := WriteRecord(path, Release{Tag: "v0.8.0"}, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadRecord(path, now.Add(MaxAge-time.Second)); !ok {
		t.Error("a record just inside MaxAge should be fresh")
	}
}
