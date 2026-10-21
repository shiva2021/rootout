package tcc

import (
	"os"
	"testing"

	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/testutil"
)

func TestFullDiskAccess(t *testing.T) {
	t.Run("granted when a protected folder is readable", func(t *testing.T) {
		env := testutil.FakeHome(t, testutil.Spec{Dirs: []string{"~/Library/Safari"}})
		if got := FullDiskAccess(env); got != Granted {
			t.Fatalf("got %v, want Granted", got)
		}
	})
	t.Run("denied when the folder cannot be listed", func(t *testing.T) {
		env := testutil.FakeHome(t, testutil.Spec{Dirs: []string{"~/Library/Safari"}})
		dir := env.Library("Safari")
		if err := os.Chmod(dir, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if got := FullDiskAccess(env); got != Denied {
			t.Fatalf("got %v, want Denied", got)
		}
	})
	t.Run("falls back to the next folder", func(t *testing.T) {
		env := testutil.FakeHome(t, testutil.Spec{Dirs: []string{"~/Library/Mail"}})
		if got := FullDiskAccess(env); got != Granted {
			t.Fatalf("got %v, want Granted", got)
		}
	})
	t.Run("unknown when no protected folder exists", func(t *testing.T) {
		env := testutil.FakeHome(t, testutil.Spec{Dirs: []string{"~/Library/Caches"}})
		if got := FullDiskAccess(env); got != Unknown {
			t.Fatalf("got %v, want Unknown", got)
		}
	})
}

func TestAccessString(t *testing.T) {
	for a, want := range map[Access]string{Unknown: "unknown", Granted: "granted", Denied: "denied", Access(9): "unknown"} {
		if got := a.String(); got != want {
			t.Errorf("%d: got %q, want %q", int(a), got, want)
		}
	}
}

func TestNeedsFullDiskAccess(t *testing.T) {
	env := fsx.Env{Home: "/Users/me", Root: "/"}
	cases := map[string]bool{
		"/Users/me/Library/Containers/com.foo.app":       true,
		"/Users/me/Library/Containers/com.foo.app/Data":  false,
		"/Users/me/Library/Containers":                   false,
		"/Users/me/Library/Group Containers/ABC.com.foo": false,
		"/Users/me/Library/Caches/com.foo.app":           false,
		"/Users/other/Library/Containers/com.foo.app":    false,
		"/Users/me/Library/Containers/../Caches/com.foo": false,
		"pkg:com.foo.pkg":                                false,
	}
	for path, want := range cases {
		if got := NeedsFullDiskAccess(env, path); got != want {
			t.Errorf("%s: got %v, want %v", path, got, want)
		}
	}
}

func TestTerminalApp(t *testing.T) {
	cases := map[string]string{
		"WarpTerminal":   "Warp",
		"Apple_Terminal": "Terminal",
		"iTerm.app":      "iTerm",
		"vscode":         "Visual Studio Code",
		"ghostty":        "Ghostty",
		"":               "your terminal app",
		"something-else": "your terminal app",
	}
	for v, want := range cases {
		getenv := func(string) string { return v }
		if got := TerminalApp(getenv); got != want {
			t.Errorf("TERM_PROGRAM=%q: got %q, want %q", v, got, want)
		}
	}
}
