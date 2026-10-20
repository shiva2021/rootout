// Package run is the single place purge executes external commands. Every
// command is an argv list; nothing is ever passed through a shell. Packages
// accept a Runner so tests inject fakes and never exec anything real.
package run

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Runner executes name with args and returns the combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Default executes the command for real and logs it at debug level.
func Default(ctx context.Context, name string, args ...string) ([]byte, error) {
	start := time.Now()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // argv only, never a shell string; callers validate inputs
	slog.Debug("exec", "cmd", name+" "+strings.Join(args, " "), "err", err, "ms", time.Since(start).Milliseconds())
	return out, err
}
