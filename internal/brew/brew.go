// Package brew runs the Homebrew side of an uninstall. Cask detection lives
// in package app (read-only, done during discovery); this package only acts.
package brew

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Runner executes an external command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Brew wraps the brew CLI.
type Brew struct {
	Run      Runner
	LookPath func(string) (string, error)
}

// New returns a Brew that executes the real CLI.
func New() *Brew {
	return &Brew{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
		LookPath: exec.LookPath,
	}
}

// ErrNotInstalled is returned when brew is not on PATH.
var ErrNotInstalled = errors.New("brew: homebrew is not installed")

var tokenRe = regexp.MustCompile(`^[a-z0-9@._+-]+$`)

// Available reports whether the brew CLI can be found.
func (b *Brew) Available() bool {
	_, err := b.LookPath("brew")
	return err == nil
}

// Zap runs `brew uninstall --cask --zap <token>`, which removes the cask and
// the leftover paths its formula declares. The token is validated against the
// cask naming rules so nothing user-controlled reaches the shell.
func (b *Brew) Zap(ctx context.Context, token string) error {
	if !tokenRe.MatchString(token) {
		return fmt.Errorf("brew: invalid cask token %q", token)
	}
	if !b.Available() {
		return ErrNotInstalled
	}
	out, err := b.Run(ctx, "brew", "uninstall", "--cask", "--zap", token)
	if err != nil {
		return fmt.Errorf("brew: uninstall --zap %s: %w: %s", token, err, strings.TrimSpace(string(out)))
	}
	return nil
}
