package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runReleaseNotes runs scripts/release_notes.sh against a temporary CHANGELOG.
func runReleaseNotes(t *testing.T, version, changelog string) (string, int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte(changelog), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", "../../scripts/release_notes.sh", version, path).Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exitErr):
		return string(out), exitErr.ExitCode()
	default:
		t.Fatal(err)
		return "", -1
	}
}

func TestReleaseNotes(t *testing.T) {
	const log = "## [Unreleased]\n\n## [0.4.0] - 2026-10-22\n\n### Added\n- Release pipeline (D-032).\n\n## [0.3.1] - 2026-10-22\n\n- old\n"
	tests := []struct {
		name, version, changelog, want string
		code                           int
	}{
		{"v-prefix", "v0.4.0", log, "### Added\n- Release pipeline (D-032).", 0},
		{"bare", "0.4.0", log, "### Added\n- Release pipeline (D-032).", 0},
		{"last-section", "0.3.1", log, "- old", 0},
		{"no-partial-match", "0.4", log, "", 1},
		{"no-prefix-match", "0.4.01", log, "", 1},
		{"missing", "9.9.9", log, "", 1},
		{"empty-section", "0.5.0", "## [0.5.0] - 2026-10-23\n\n## [0.4.0] - 2026-10-22\n\n- x\n", "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, code := runReleaseNotes(t, tt.version, tt.changelog)
			if code != tt.code {
				t.Errorf("exit = %d, want %d", code, tt.code)
			}
			if got = strings.TrimRight(got, "\n"); got != tt.want {
				t.Errorf("stdout = %q, want %q", got, tt.want)
			}
		})
	}
}
