package leftover

import (
	"os"
	"path/filepath"

	"howett.net/plist"
)

// launchdJob is the subset of a launchd plist the matcher needs.
type launchdJob struct {
	Label            string   `plist:"Label"`
	Program          string   `plist:"Program"`
	ProgramArguments []string `plist:"ProgramArguments"`
}

// readLaunchd parses a launchd plist (XML or binary). Unparsable files yield
// nil: a plist we cannot read can still match by name, never by program path.
func readLaunchd(path string) *launchdJob {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var job launchdJob
	if _, err := plist.Unmarshal(data, &job); err != nil {
		return nil
	}
	return &job
}

// program returns the executable the job runs: Program, else ProgramArguments[0].
func (j *launchdJob) program() string {
	if j == nil {
		return ""
	}
	if j.Program != "" {
		return j.Program
	}
	if len(j.ProgramArguments) > 0 {
		return j.ProgramArguments[0]
	}
	return ""
}

// matchLaunchd matches a .plist in a LaunchAgents/LaunchDaemons directory by
// bundle-id prefix ("bundle_prefix") or because the job's program lives inside
// the app bundle ("program_path"). Labels of matched LaunchDaemons are kept so
// the PrivilegedHelperTools rule can match the helpers they launch.
func (s *scan) matchLaunchd(kind Kind, path, base string) (string, bool) {
	if !hasSuffix(base, ".plist") {
		return "", false
	}
	job := readLaunchd(path)
	var by string
	switch {
	case s.app.BundleID != "" && hasPrefix(base, s.app.BundleID+"."):
		by = ByBundlePrefix
	case s.app.Path != "" && under(filepath.Clean(s.app.Path), filepath.Clean(job.program())):
		by = ByProgramPath
	default:
		return "", false
	}
	if kind == LaunchDaemon && job != nil && job.Label != "" {
		s.labels[job.Label] = by
	}
	return by, true
}
