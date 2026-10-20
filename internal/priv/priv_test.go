package priv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shiva2021/purge/internal/fsx"
	"github.com/shiva2021/purge/internal/testutil"
)

// newEnv builds a sandboxed Env: Root is a temp dir and Home is <root>/home.
func newEnv(t *testing.T) fsx.Env {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	env := fsx.Env{Home: home, Root: root}
	testutil.RequireSandbox(t, env)
	return env
}

type call struct {
	name string
	args []string
}

// fakeRunner records every call and never executes anything.
type fakeRunner struct {
	calls []call
	out   []byte
	err   error
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{name, args})
	return f.out, f.err
}

// adminScript wraps a shell line the way Exec does for osascript.
func adminScript(line string) string {
	return `do shell script "` + AppleScriptQuote(line) + `" with administrator privileges`
}

// unAppleScript reverses AppleScriptQuote on the body of a `do shell script`
// literal and returns the embedded shell line.
func unAppleScript(t *testing.T, script string) string {
	t.Helper()
	const pre, suf = `do shell script "`, `" with administrator privileges`
	if !strings.HasPrefix(script, pre) || !strings.HasSuffix(script, suf) {
		t.Fatalf("script not wrapped as expected: %q", script)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(script, pre), suf)
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' {
			i++
			if i >= len(body) {
				t.Fatalf("dangling backslash in %q", body)
			}
			switch body[i] {
			case '\\', '"':
				b.WriteByte(body[i])
			default:
				t.Fatalf("unexpected escape \\%c in %q", body[i], body)
			}
			continue
		}
		if body[i] == '"' {
			t.Fatalf("unescaped double quote in %q", body)
		}
		b.WriteByte(body[i])
	}
	return b.String()
}

// shellWords is a tiny POSIX word splitter that understands single quotes and
// backslash escapes outside quotes, which is all ShellQuote produces.
func shellWords(t *testing.T, line string) []string {
	t.Helper()
	var words []string
	var cur strings.Builder
	inWord, inQuote := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inQuote:
			if c == '\'' {
				inQuote = false
			} else {
				cur.WriteByte(c)
			}
		case c == '\'':
			inQuote, inWord = true, true
		case c == '\\':
			i++
			if i >= len(line) {
				t.Fatalf("dangling backslash in %q", line)
			}
			cur.WriteByte(line[i])
			inWord = true
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			if c == '$' || c == ';' || c == '"' || c == '&' || c == '|' || c == '`' {
				t.Fatalf("unquoted shell metacharacter %q in %q", c, line)
			}
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inQuote {
		t.Fatalf("unterminated single quote in %q", line)
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

func TestRemoveBuildsOsascript(t *testing.T) {
	env := newEnv(t)
	p1 := env.Sys("Library", "LaunchDaemons", "com.foo.plist")
	p2 := env.Sys("Library", "PrivilegedHelperTools", "com.foo.helper")
	fr := &fakeRunner{}

	if err := Remove(context.Background(), env, []string{p1, p2}, Options{Run: fr.run}); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(fr.calls))
	}
	want := call{"osascript", []string{"-e", fmt.Sprintf(`do shell script "/bin/rm -rf -- '%s' '%s'" with administrator privileges`, p1, p2)}}
	if got := fr.calls[0]; got.name != want.name || strings.Join(got.args, " ") != strings.Join(want.args, " ") {
		t.Errorf("call = %s %q\nwant   %s %q", got.name, got.args, want.name, want.args)
	}
}

func TestRemoveHostileNamesRoundTrip(t *testing.T) {
	env := newEnv(t)
	hostile := env.Sys("Library", "Application Support", `it's "weird" $HOME; a\b && rm`)
	plain := env.Sys("Library", "Caches", "com.foo")
	fr := &fakeRunner{}

	if err := Remove(context.Background(), env, []string{hostile, plain}, Options{Run: fr.run}); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 1 || fr.calls[0].name != "osascript" || fr.calls[0].args[0] != "-e" {
		t.Fatalf("unexpected calls %+v", fr.calls)
	}
	line := unAppleScript(t, fr.calls[0].args[1])
	got := shellWords(t, line)
	want := []string{"/bin/rm", "-rf", "--", hostile, plain}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("parsed words = %q\nwant %q", got, want)
	}
}

func TestRemoveRejectsUnsafePathBeforeRunning(t *testing.T) {
	env := newEnv(t)
	fr := &fakeRunner{}
	paths := []string{env.Sys("Library", "LaunchDaemons", "com.foo.plist"), env.Sys("System", "x")}

	err := Remove(context.Background(), env, paths, Options{Run: fr.run})
	if err == nil {
		t.Fatal("expected a safety violation")
	}
	if !strings.Contains(err.Error(), "protected_prefix") {
		t.Errorf("err = %v, want protected_prefix violation", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner was called: %+v", fr.calls)
	}
}

func TestRemoveUseSudo(t *testing.T) {
	env := newEnv(t)
	p1 := env.Sys("Library", "LaunchDaemons", "com.foo.plist")
	p2 := env.Sys("Library", "Application Support", "Foo Bar")
	fr := &fakeRunner{}

	if err := Remove(context.Background(), env, []string{p1, p2}, Options{Run: fr.run, UseSudo: true}); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(fr.calls))
	}
	want := []string{"--", "/bin/rm", "-rf", "--", p1, p2}
	if got := fr.calls[0]; got.name != "sudo" || strings.Join(got.args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("call = %s %q, want sudo %q", got.name, got.args, want)
	}
}

func TestRemoveChunksLargeArgLists(t *testing.T) {
	env := newEnv(t)
	var paths []string
	total := 0
	for i := 0; total < maxArgBytes+1024; i++ {
		p := env.Sys("Library", "Caches", fmt.Sprintf("com.foo.%04d.%s", i, strings.Repeat("x", 200)))
		paths = append(paths, p)
		total += len(p) + 1
	}
	fr := &fakeRunner{}

	if err := Remove(context.Background(), env, paths, Options{Run: fr.run}); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("got %d calls, want 1 (one elevation)", len(fr.calls))
	}
	line := unAppleScript(t, fr.calls[0].args[1])
	parts := strings.Split(line, " && ")
	if len(parts) < 2 {
		t.Fatalf("expected >1 command joined with \" && \", got %d", len(parts))
	}
	var got []string
	for _, part := range parts {
		words := shellWords(t, part)
		if len(words) < 4 || words[0] != "/bin/rm" || words[1] != "-rf" || words[2] != "--" {
			t.Fatalf("bad command prefix %q", words[:3])
		}
		n := 0
		for _, w := range words[3:] {
			n += len(w) + 1
		}
		if n > maxArgBytes {
			t.Errorf("chunk carries %d argument bytes, exceeds %d", n, maxArgBytes)
		}
		got = append(got, words[3:]...)
	}
	if strings.Join(got, "\x00") != strings.Join(paths, "\x00") {
		t.Errorf("chunks do not reassemble to the input (%d vs %d paths)", len(got), len(paths))
	}
}

func TestExecUserCancelled(t *testing.T) {
	fr := &fakeRunner{out: []byte("execution error: User canceled. (-128)"), err: errors.New("exit status 1")}
	err := Exec(context.Background(), []Cmd{{"/bin/rm", "-rf", "--", "/x"}}, Options{Run: fr.run})
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("err = %v, want ErrCancelled", err)
	}
}

func TestExecFailureIncludesOutput(t *testing.T) {
	fr := &fakeRunner{out: []byte("rm: /x: Operation not permitted"), err: errors.New("exit status 1")}
	err := Exec(context.Background(), []Cmd{{"/bin/rm", "-rf", "--", "/x"}}, Options{Run: fr.run})
	if err == nil || errors.Is(err, ErrCancelled) || !strings.Contains(err.Error(), "Operation not permitted") {
		t.Errorf("err = %v, want wrapped output", err)
	}
}

func TestExecRejectsNewlineAndNUL(t *testing.T) {
	for _, bad := range []string{"a\nb", "a\rb", "a\x00b"} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			fr := &fakeRunner{}
			for _, sudo := range []bool{false, true} {
				err := Exec(context.Background(), []Cmd{{"/bin/rm", "-rf", "--", bad}}, Options{Run: fr.run, UseSudo: sudo})
				if err == nil {
					t.Errorf("sudo=%v: expected error", sudo)
				}
			}
			if len(fr.calls) != 0 {
				t.Errorf("runner was called: %+v", fr.calls)
			}
		})
	}
}

func TestExecEmpty(t *testing.T) {
	fr := &fakeRunner{}
	if err := Exec(context.Background(), nil, Options{Run: fr.run}); err != nil {
		t.Fatal(err)
	}
	if err := Exec(context.Background(), []Cmd{{}}, Options{Run: fr.run}); err == nil {
		t.Error("expected error for empty command")
	}
	if len(fr.calls) != 0 {
		t.Errorf("runner was called: %+v", fr.calls)
	}
}

func TestForgetAndBootout(t *testing.T) {
	t.Run("forget osascript", func(t *testing.T) {
		fr := &fakeRunner{}
		if err := Forget(context.Background(), []string{"com.foo.pkg", "com.foo.helper-1_0"}, Options{Run: fr.run}); err != nil {
			t.Fatal(err)
		}
		want := adminScript("pkgutil --forget 'com.foo.pkg' && pkgutil --forget 'com.foo.helper-1_0'")
		if len(fr.calls) != 1 || fr.calls[0].name != "osascript" || fr.calls[0].args[1] != want {
			t.Errorf("calls = %+v\nwant osascript -e %q", fr.calls, want)
		}
	})
	t.Run("bootout sudo", func(t *testing.T) {
		fr := &fakeRunner{}
		if err := Bootout(context.Background(), []string{"com.foo.daemon", "com.foo.agent"}, Options{Run: fr.run, UseSudo: true}); err != nil {
			t.Fatal(err)
		}
		if len(fr.calls) != 2 {
			t.Fatalf("got %d calls, want 2", len(fr.calls))
		}
		for i, label := range []string{"com.foo.daemon", "com.foo.agent"} {
			want := []string{"--", "launchctl", "bootout", "system/" + label}
			if got := fr.calls[i]; got.name != "sudo" || strings.Join(got.args, "\x00") != strings.Join(want, "\x00") {
				t.Errorf("call %d = %s %q, want sudo %q", i, got.name, got.args, want)
			}
		}
	})
	t.Run("rejects bad ids", func(t *testing.T) {
		for _, bad := range []string{"", "com.foo pkg", "../etc", "com.foo;rm", "system/com.foo", "com.foo$"} {
			fr := &fakeRunner{}
			if err := Forget(context.Background(), []string{"com.ok", bad}, Options{Run: fr.run}); err == nil {
				t.Errorf("Forget(%q): expected error", bad)
			}
			if err := Bootout(context.Background(), []string{bad}, Options{Run: fr.run}); err == nil {
				t.Errorf("Bootout(%q): expected error", bad)
			}
			if len(fr.calls) != 0 {
				t.Errorf("%q: runner was called: %+v", bad, fr.calls)
			}
		}
	})
}

func TestQuoteHelpers(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"a", "b c"}, `a 'b c'`},
		{[]string{"/bin/rm", "-rf", "--", "/Library/Caches/com.foo"}, `/bin/rm -rf -- '/Library/Caches/com.foo'`},
		{[]string{"it's"}, `'it'\''s'`},
		{[]string{"it's", "-x"}, `'it'\''s' -x`},
		{[]string{"rm", "-r f"}, `rm '-r f'`},
		{[]string{"rm", ""}, `rm ''`},
		{[]string{`$HOME;"x"\y`}, `'$HOME;"x"\y'`},
	}
	for _, tc := range tests {
		if got := ShellQuote(tc.in); got != tc.want {
			t.Errorf("ShellQuote(%q) = %s, want %s", tc.in, got, tc.want)
		}
		if got := shellWords(t, ShellQuote(tc.in)); strings.Join(got, "\x00") != strings.Join(tc.in, "\x00") {
			t.Errorf("round trip %q -> %q", tc.in, got)
		}
	}
	as := []struct{ in, want string }{
		{`plain`, `plain`},
		{`a"b`, `a\"b`},
		{`a\b`, `a\\b`},
		{`\"`, `\\\"`},
	}
	for _, tc := range as {
		if got := AppleScriptQuote(tc.in); got != tc.want {
			t.Errorf("AppleScriptQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
