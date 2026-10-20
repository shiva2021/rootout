// Package priv runs the small, fixed set of root-requiring operations the
// removal plan needs (rm under /Library, pkgutil --forget, launchctl bootout)
// behind a single administrator prompt. The tool itself never runs as root.
//
// Every operation is a fixed argv ([]string). The only place a shell line is
// built is Exec's osascript path, and there every argv element is single-quoted
// with ShellQuote; no user data is ever interpolated unquoted.
package priv

import (
	"context"
	"errors"
	"fmt"
	"github.com/shiva2021/purge/internal/run"
	"regexp"
	"strings"

	"github.com/shiva2021/purge/internal/fsx"
	"github.com/shiva2021/purge/internal/safety"
)

// Options controls how privileged commands are run.
type Options struct {
	Run      run.Runner // nil means run.Default
	UseSudo  bool       // --sudo: use `sudo` (needs a TTY) instead of the GUI osascript prompt
	Dotfiles bool       // passed through to safety.Options.AllowDotfiles
}

// Cmd is one argv to run as root. No shell strings anywhere in the API.
type Cmd []string

// ErrCancelled is returned when the user dismisses the administrator prompt.
var ErrCancelled = errors.New("priv: administrator prompt cancelled")

// maxArgBytes caps the argument bytes of one rm command so a single chunk
// stays well under the kernel's ARG_MAX.
const maxArgBytes = 64 << 10

// idPattern is the only shape accepted for package ids and launchd labels.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// plainPattern matches tokens that carry no shell meaning. Only the program
// name and option tokens are ever left bare, and only when they match it.
var plainPattern = regexp.MustCompile(`^[A-Za-z0-9_./=-]+$`)

// Remove deletes paths as root. Every path is re-validated with
// safety.Validate first; any violation aborts the whole call before anything
// runs. Paths are passed to /bin/rm -rf -- <path...>, chunked so one command
// never exceeds ~64 KB of arguments, and all chunks run in one elevation.
func Remove(ctx context.Context, env fsx.Env, paths []string, opts Options) error {
	if len(paths) == 0 {
		return nil
	}
	sopts := safety.Options{AllowDotfiles: opts.Dotfiles}
	for _, p := range paths {
		if err := safety.Validate(env, p, sopts); err != nil {
			return fmt.Errorf("priv: remove: %w", err)
		}
	}
	return Exec(ctx, rmCmds(paths), opts)
}

// rmCmds splits paths into /bin/rm -rf -- commands of at most maxArgBytes
// argument bytes each (counting one separator per path).
func rmCmds(paths []string) []Cmd {
	base := Cmd{"/bin/rm", "-rf", "--"}
	var cmds []Cmd
	cur := append(Cmd{}, base...)
	size := 0
	for _, p := range paths {
		n := len(p) + 1
		if size+n > maxArgBytes && len(cur) > len(base) {
			cmds = append(cmds, cur)
			cur = append(Cmd{}, base...)
			size = 0
		}
		cur = append(cur, p)
		size += n
	}
	return append(cmds, cur)
}

// Forget runs `pkgutil --forget <id>` for each package id as root.
func Forget(ctx context.Context, pkgIDs []string, opts Options) error {
	cmds := make([]Cmd, 0, len(pkgIDs))
	for _, id := range pkgIDs {
		if !idPattern.MatchString(id) {
			return fmt.Errorf("priv: forget: invalid package id %q", id)
		}
		cmds = append(cmds, Cmd{"pkgutil", "--forget", id})
	}
	return Exec(ctx, cmds, opts)
}

// Bootout runs `launchctl bootout system/<label>` for each label as root.
func Bootout(ctx context.Context, labels []string, opts Options) error {
	cmds := make([]Cmd, 0, len(labels))
	for _, l := range labels {
		if !idPattern.MatchString(l) {
			return fmt.Errorf("priv: bootout: invalid launchd label %q", l)
		}
		cmds = append(cmds, Cmd{"launchctl", "bootout", "system/" + l})
	}
	return Exec(ctx, cmds, opts)
}

// Exec runs cmds as root in one elevation.
//
// With UseSudo each cmd is exec'd directly as `sudo -- <argv...>` (no shell),
// stopping at the first error. Otherwise every argv is quoted with ShellQuote,
// the commands are joined with " && ", and the resulting line is run once via
// `osascript -e 'do shell script "<line>" with administrator privileges'`,
// escaped with AppleScriptQuote. Arguments containing a newline, carriage
// return or NUL are rejected before anything runs. Failures carry the command
// output; a dismissed GUI prompt returns ErrCancelled.
func Exec(ctx context.Context, cmds []Cmd, opts Options) error {
	if len(cmds) == 0 {
		return nil
	}
	for _, c := range cmds {
		if err := checkCmd(c); err != nil {
			return err
		}
	}
	runner := opts.Run
	if runner == nil {
		runner = run.Default
	}

	if opts.UseSudo {
		for _, c := range cmds {
			args := append([]string{"--"}, c...)
			out, err := runner(ctx, "sudo", args...)
			if err != nil {
				return fmt.Errorf("priv: sudo %s: %w: %s", c[0], err, trimOutput(out))
			}
		}
		return nil
	}

	lines := make([]string, len(cmds))
	for i, c := range cmds {
		lines[i] = ShellQuote(c)
	}
	script := `do shell script "` + AppleScriptQuote(strings.Join(lines, " && ")) + `" with administrator privileges`
	out, err := runner(ctx, "osascript", "-e", script)
	if err != nil {
		if strings.Contains(string(out), "User canceled") {
			return ErrCancelled
		}
		return fmt.Errorf("priv: osascript: %w: %s", err, trimOutput(out))
	}
	return nil
}

// checkCmd rejects empty commands and arguments that cannot be quoted safely.
func checkCmd(c Cmd) error {
	if len(c) == 0 {
		return errors.New("priv: empty command")
	}
	for _, a := range c {
		if strings.ContainsAny(a, "\n\r\x00") {
			return fmt.Errorf("priv: argument %q contains a newline or NUL", a)
		}
	}
	return nil
}

// ShellQuote renders args as one POSIX shell line. Operands (anything that is
// not the program name or an option starting with "-") are always wrapped in
// single quotes with embedded quotes written as '\”. The program name and
// options come from this package's own tables, so they are left bare when
// they contain only plain characters; otherwise they are quoted too.
func ShellQuote(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if (i == 0 || strings.HasPrefix(a, "-")) && plainPattern.MatchString(a) {
			q[i] = a
			continue
		}
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// AppleScriptQuote escapes s for use inside an AppleScript double-quoted
// string literal: backslash becomes \\ and double quote becomes \".
func AppleScriptQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

func trimOutput(out []byte) string {
	return strings.TrimSpace(string(out))
}
