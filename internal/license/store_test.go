package license

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/testutil"
)

func noEnv(string) string { return "" }

func TestPath(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	if got, want := Path(env, noEnv), env.User(".config", "rootout", "license"); got != want {
		t.Errorf("default Path = %q, want %q", got, want)
	}
	custom := filepath.Join(env.Root, "elsewhere", "key")
	getenv := func(k string) string {
		if k == "ROOTOUT_LICENSE_FILE" {
			return custom
		}
		return ""
	}
	if got := Path(env, getenv); got != custom {
		t.Errorf("override Path = %q, want %q", got, custom)
	}
}

func TestStoreSaveLoad(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	s := Store{Path: Path(env, noEnv)}

	if _, err := s.Load(); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Load on missing = %v, want ErrNoKey", err)
	}
	if err := s.Save("ro1.abc.def"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(s.Path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", di.Mode().Perm())
	}
	if err := os.WriteFile(s.Path, []byte("  ro1.abc.def\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || got != "ro1.abc.def" {
		t.Errorf("Load = %q, %v; want trimmed key", got, err)
	}
	// Save replaces an existing key.
	if err := s.Save("ro1.new.key"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load(); got != "ro1.new.key" {
		t.Errorf("Load after replace = %q", got)
	}
}

func TestStoreLoadEmptyFile(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{Files: map[string]string{"~/.config/rootout/license": " \n"}})
	if _, err := (Store{Path: Path(env, noEnv)}).Load(); !errors.Is(err, ErrNoKey) {
		t.Errorf("Load on blank file = %v, want ErrNoKey", err)
	}
}

func TestStoreLoadError(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{Dirs: []string{"~/.config/rootout/license"}})
	_, err := (Store{Path: Path(env, noEnv)}).Load()
	if err == nil || errors.Is(err, ErrNoKey) {
		t.Errorf("Load on a directory = %v, want a read error", err)
	}
}

func TestStoreRemove(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	s := Store{Path: Path(env, noEnv)}
	if err := s.Remove(); err != nil {
		t.Fatalf("Remove on missing = %v, want nil", err)
	}
	if err := s.Save("ro1.a.b"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRenew(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNoKey) {
		t.Errorf("Load after Remove = %v, want ErrNoKey", err)
	}
	if _, ok := s.LastRenew(); ok {
		t.Error("stamp survived Remove")
	}
	if err := s.Remove(); err != nil {
		t.Errorf("second Remove = %v, want nil", err)
	}
}

func TestStoreLastRenew(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	s := Store{Path: Path(env, noEnv)}
	if _, ok := s.LastRenew(); ok {
		t.Error("LastRenew ok with no stamp")
	}
	when := time.Date(2026, 11, 1, 10, 30, 0, 0, time.UTC)
	if err := s.MarkRenew(when); err != nil {
		t.Fatal(err)
	}
	got, ok := s.LastRenew()
	if !ok || !got.Equal(when) {
		t.Errorf("LastRenew = %v, %v; want %v", got, ok, when)
	}
	if fi, err := os.Stat(s.Path + ".renew"); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("stamp stat = %v, %v; want mode 0600", fi, err)
	}
	if err := os.WriteFile(s.Path+".renew", []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LastRenew(); ok {
		t.Error("LastRenew ok with a garbage stamp")
	}
}
