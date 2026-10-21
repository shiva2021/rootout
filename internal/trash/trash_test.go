package trash

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
}

// fakeRunner records every invocation and fails when fail returns an error.
type fakeRunner struct {
	calls []call
	fail  func(c call) ([]byte, error)
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	c := call{name: name, args: append([]string(nil), args...)}
	f.calls = append(f.calls, c)
	if f.fail != nil {
		return f.fail(c)
	}
	return nil, nil
}

func newMover(t *testing.T, backend string) (*Mover, *fakeRunner) {
	t.Helper()
	fr := &fakeRunner{}
	m := &Mover{Run: fr.run, LookPath: func(string) (string, error) {
		if backend == BackendTrash {
			return trashBin, nil
		}
		return "", errors.New("not found")
	}}
	return m, fr
}

func mkfiles(t *testing.T, dir string, n int) []string {
	t.Helper()
	paths := make([]string, n)
	for i := range paths {
		p := filepath.Join(dir, fmt.Sprintf("f%03d", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths[i] = p
	}
	return paths
}

func TestAvailable(t *testing.T) {
	tests := []struct {
		name string
		look func(string) (string, error)
		want string
	}{
		{"system trash present", func(string) (string, error) { return trashBin, nil }, BackendTrash},
		{"absent", func(string) (string, error) { return "", errors.New("no") }, BackendFinder},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &Mover{Run: (&fakeRunner{}).run, LookPath: tc.look}
			if got := m.Available(); got != tc.want {
				t.Errorf("Available() = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("asks only for the system binary", func(t *testing.T) {
		var asked string
		m := &Mover{LookPath: func(s string) (string, error) { asked = s; return "", errors.New("no") }}
		m.Available()
		if asked != "/usr/bin/trash" {
			t.Errorf("LookPath asked for %q, want /usr/bin/trash", asked)
		}
	})
}

func TestMoveTrashBackendBatches(t *testing.T) {
	m, fr := newMover(t, BackendTrash)
	paths := mkfiles(t, t.TempDir(), 150)

	res, err := m.Move(context.Background(), paths)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(fr.calls))
	}
	wantLens := []int{100, 50}
	for i, c := range fr.calls {
		if c.name != "/usr/bin/trash" {
			t.Errorf("call %d name = %q, want /usr/bin/trash", i, c.name)
		}
		if len(c.args) == 0 || c.args[0] != "-s" {
			t.Fatalf("call %d args = %v, want -s first", i, c.args)
		}
		got := c.args[1:]
		if len(got) != wantLens[i] {
			t.Errorf("call %d has %d paths, want %d", i, len(got), wantLens[i])
		}
		for _, p := range got {
			if !filepath.IsAbs(p) {
				t.Errorf("call %d non-absolute path %q", i, p)
			}
		}
		want := paths[i*100 : i*100+wantLens[i]]
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("call %d paths differ from input order", i)
		}
	}
	if len(res.Moved) != 150 || len(res.Skipped) != 0 || len(res.Failed) != 0 {
		t.Errorf("result moved=%d skipped=%d failed=%d", len(res.Moved), len(res.Skipped), len(res.Failed))
	}
}

func TestMoveFinderBackendScript(t *testing.T) {
	m, fr := newMover(t, BackendFinder)
	dir := t.TempDir()
	quoted := filepath.Join(dir, `say "hi"`)
	slashed := filepath.Join(dir, `back\slash`)
	for _, p := range []string{quoted, slashed} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res, err := m.Move(context.Background(), []string{quoted, slashed})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(fr.calls))
	}
	c := fr.calls[0]
	want := `tell application "Finder" to delete {POSIX file "` + dir + `/say \"hi\"", POSIX file "` + dir + `/back\\slash"}`
	if c.name != "osascript" || len(c.args) != 2 || c.args[0] != "-e" {
		t.Fatalf("call = %q %v, want osascript -e <script>", c.name, c.args)
	}
	if c.args[1] != want {
		t.Errorf("script:\n got %s\nwant %s", c.args[1], want)
	}
	if len(res.Moved) != 2 {
		t.Errorf("moved = %v", res.Moved)
	}
}

func TestMoveFinderBatchesAt50(t *testing.T) {
	m, fr := newMover(t, BackendFinder)
	paths := mkfiles(t, t.TempDir(), 120)
	if _, err := m.Move(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 3 {
		t.Fatalf("got %d calls, want 3", len(fr.calls))
	}
	for i, n := range []int{50, 50, 20} {
		if got := strings.Count(fr.calls[i].args[1], "POSIX file"); got != n {
			t.Errorf("call %d has %d items, want %d", i, got, n)
		}
	}
}

func TestMoveSkipsNonexistent(t *testing.T) {
	for _, backend := range []string{BackendTrash, BackendFinder} {
		t.Run(backend, func(t *testing.T) {
			m, fr := newMover(t, backend)
			dir := t.TempDir()
			missing := filepath.Join(dir, "missing")
			present := mkfiles(t, dir, 1)[0]

			res, err := m.Move(context.Background(), []string{missing, present})
			if err != nil {
				t.Fatalf("Move: %v", err)
			}
			if len(res.Skipped) != 1 || res.Skipped[0] != missing {
				t.Errorf("Skipped = %v, want [%s]", res.Skipped, missing)
			}
			if len(fr.calls) != 1 {
				t.Fatalf("got %d calls, want 1", len(fr.calls))
			}
			for _, a := range fr.calls[0].args {
				if strings.Contains(a, "missing") {
					t.Errorf("nonexistent path reached the runner: %v", fr.calls[0].args)
				}
			}
			if len(res.Moved) != 1 || res.Moved[0] != present {
				t.Errorf("Moved = %v", res.Moved)
			}
		})
	}

	t.Run("only nonexistent runs nothing", func(t *testing.T) {
		m, fr := newMover(t, BackendTrash)
		res, err := m.Move(context.Background(), []string{filepath.Join(t.TempDir(), "nope")})
		if err != nil || len(fr.calls) != 0 || len(res.Skipped) != 1 {
			t.Errorf("err=%v calls=%d skipped=%d", err, len(fr.calls), len(res.Skipped))
		}
	})
}

func TestMoveDanglingSymlinkIsNotSkipped(t *testing.T) {
	m, fr := newMover(t, BackendTrash)
	link := filepath.Join(t.TempDir(), "dangling")
	if err := os.Symlink("/nonexistent/target", link); err != nil {
		t.Fatal(err)
	}
	res, err := m.Move(context.Background(), []string{link})
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 1 || fr.calls[0].args[1] != link {
		t.Errorf("link not passed as-is: %v", fr.calls)
	}
	if len(res.Skipped) != 0 || len(res.Moved) != 1 {
		t.Errorf("result = %+v", res)
	}
}

func TestMoveFailingBatchRetriesEachPath(t *testing.T) {
	m, fr := newMover(t, BackendTrash)
	paths := mkfiles(t, t.TempDir(), 150)
	bad := paths[120] // in the second batch
	fr.fail = func(c call) ([]byte, error) {
		for _, a := range c.args[1:] {
			if a == bad {
				return []byte("Error attempting to move " + bad + " ... afpAccessDenied: Insufficient access privileges for operation\n"), errors.New("exit status 5")
			}
		}
		return nil, nil
	}

	res, err := m.Move(context.Background(), paths)
	if err != nil {
		t.Fatalf("partial failure must return nil error, got %v", err)
	}
	if len(res.Moved) != 149 {
		t.Errorf("moved = %d, want 149", len(res.Moved))
	}
	if len(res.Failed) != 1 {
		t.Fatalf("failed = %v, want only %s", res.Failed, bad)
	}
	ferr := res.Failed[bad]
	for _, want := range []string{"Error attempting to move", "exit status 5", "Full Disk Access"} {
		if ferr == nil || !strings.Contains(ferr.Error(), want) {
			t.Errorf("error lacks %q: %v", want, ferr)
		}
	}
	// Two batch calls, then the 50 paths of the failed batch one by one.
	if got := len(fr.calls); got != 52 {
		t.Errorf("calls = %d, want 52", got)
	}
	single := 0
	for _, c := range fr.calls {
		if len(c.args)-1 == 1 {
			single++
		}
	}
	if single != 50 {
		t.Errorf("single-path retries = %d, want 50", single)
	}
}

func TestMoveNoHintForOtherErrors(t *testing.T) {
	m, fr := newMover(t, BackendTrash)
	paths := mkfiles(t, t.TempDir(), 1)
	fr.fail = func(call) ([]byte, error) { return []byte("disk full"), errors.New("exit status 1") }
	res, _ := m.Move(context.Background(), paths)
	if e := res.Failed[paths[0]]; e == nil || strings.Contains(e.Error(), "Full Disk Access") {
		t.Fatalf("unexpected hint: %v", e)
	}
}

func TestMoveAllBatchesFailed(t *testing.T) {
	m, fr := newMover(t, BackendFinder)
	paths := mkfiles(t, t.TempDir(), 3)
	fr.fail = func(call) ([]byte, error) {
		return []byte("execution error: Finder got an error"), errors.New("exit status 1")
	}

	res, err := m.Move(context.Background(), paths)
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
	if len(res.Failed) != 3 || len(res.Moved) != 0 {
		t.Errorf("result = %+v", res)
	}
	if !strings.Contains(err.Error(), "Finder got an error") {
		t.Errorf("err lacks output: %v", err)
	}
}

func TestMoveRejectsNewline(t *testing.T) {
	for _, backend := range []string{BackendTrash, BackendFinder} {
		t.Run(backend, func(t *testing.T) {
			m, fr := newMover(t, backend)
			good := mkfiles(t, t.TempDir(), 1)[0]
			bad := "/tmp/evil\nname"

			_, err := m.Move(context.Background(), []string{good, bad})
			if !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("err = %v, want ErrInvalidPath", err)
			}
			if len(fr.calls) != 0 {
				t.Errorf("runner was called %d times before validation failed", len(fr.calls))
			}
		})
	}
}

func TestMoveEmptyInput(t *testing.T) {
	m, fr := newMover(t, BackendTrash)
	res, err := m.Move(context.Background(), nil)
	if err != nil || len(fr.calls) != 0 || len(res.Moved)+len(res.Skipped)+len(res.Failed) != 0 {
		t.Errorf("err=%v calls=%d res=%+v", err, len(fr.calls), res)
	}
}

func TestMoveCancelledContext(t *testing.T) {
	m, fr := newMover(t, BackendTrash)
	paths := mkfiles(t, t.TempDir(), 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := m.Move(ctx, paths)
	if !errors.Is(err, ErrAllFailed) {
		t.Fatalf("err = %v, want ErrAllFailed", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner ran %d times after cancellation", len(fr.calls))
	}
	for _, p := range paths {
		if !errors.Is(res.Failed[p], context.Canceled) {
			t.Errorf("%s: %v, want context.Canceled", p, res.Failed[p])
		}
	}
}

func TestNewUsesRealDefaults(t *testing.T) {
	m := New()
	if m.Run == nil || m.LookPath == nil {
		t.Fatal("New() left a nil hook")
	}
}
