// Package update tells whether a newer rootout release exists: it asks
// GitHub for the latest release, compares it with the installed version
// and keeps a small daily record so the home screen does not ask twice.
package update

import (
	"strconv"
	"strings"
)

// Installed parses the leading vMAJOR.MINOR.PATCH of a version string as
// the Makefile and GoReleaser produce it: "v0.7.0", "v0.7.0-3-gabc-dirty"
// (git describe past a tag) or "0.7.0". ok is false for "dev", "" or
// anything else that does not start with three numbers, which means an
// unknown build that cannot be compared with a release.
func Installed(version string) (v [3]int, ok bool) {
	s := strings.TrimPrefix(version, "v")
	// Only the dotted numbers before the first "-" matter; a trailing
	// "-N-ghash" or "-dirty" says how far past the tag the build is.
	s, _, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

// Newer reports whether latest is a higher release than installed. It is
// false when either side cannot be parsed, so an unknown build never
// produces an update notice.
func Newer(latest, installed string) bool {
	l, ok := Installed(latest)
	if !ok {
		return false
	}
	i, ok := Installed(installed)
	if !ok {
		return false
	}
	for n := range l {
		if l[n] != i[n] {
			return l[n] > i[n]
		}
	}
	return false
}
