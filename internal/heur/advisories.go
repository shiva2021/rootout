package heur

import (
	"strconv"
	"strings"
)

// hygAdvisory says that versions of a package below fixedIn carry a known
// vulnerability. fixedIn may list one fixed version per release line,
// space-separated, for advisories that were backported ("5.7.2 6.3.1 7.5.2").
// An empty fixedIn means every version is affected (deprecated, no fix).
type hygAdvisory struct {
	name     string
	fixedIn  string
	advisory string
}

// hygAdvisories is a floor, not a scanner: it holds the dependencies the
// manual audit (docs/research/vscode-extension-audit/REPORT.md) found in
// stale extensions plus a handful of well-known npm advisories. It will
// miss anything not listed here; it exists so the common offenders are
// caught offline. Written 2026-10-20.
var hygAdvisories = []hygAdvisory{
	// From the audit (leetcode.vscode-leetcode 0.18.4).
	{"underscore", "1.12.1", "CVE-2021-23358"},
	{"moment", "2.29.4", "CVE-2022-31129"},
	{"qs", "6.5.3", "CVE-2022-24999"},
	{"request", "", "CVE-2023-28155"}, // deprecated; no fixed release
	{"axios", "1.7.4", "CVE-2024-39338"},
	{"axios", "0.21.1", "CVE-2020-28168"},
	{"form-data", "2.5.4", "CVE-2025-7783"},
	// Well-known.
	{"lodash", "4.17.21", "CVE-2021-23337"},
	{"minimist", "1.2.6", "CVE-2021-44906"},
	{"node-fetch", "2.6.7 3.1.1", "CVE-2022-0235"},
	{"ws", "5.2.3 6.2.2 7.4.6", "CVE-2021-32640"},
	{"tar", "4.4.18 5.0.10 6.1.9", "CVE-2021-37713"},
	{"shelljs", "0.8.5", "CVE-2022-0144"},
	{"xml2js", "0.5.0", "CVE-2023-0842"},
	{"semver", "5.7.2 6.3.1 7.5.2", "CVE-2022-25883"},
	{"ejs", "3.1.7", "CVE-2022-29078"},
	{"jsonwebtoken", "9.0.0", "CVE-2022-23529"},
	{"node-forge", "1.3.0", "CVE-2022-24771"},
}

// hygSemver is a parsed major.minor.patch.
type hygSemver [3]int

// hygParseSemver turns a package.json version range into the lowest version
// it names. Range operators (^ ~ >= > =) and a leading v are stripped, as
// are pre-release and build suffixes. Anything else (tags like "latest",
// git URLs, "*", complex ranges) is reported as unparseable.
func hygParseSemver(s string) (hygSemver, bool) {
	var v hygSemver
	s = strings.TrimSpace(s)
	for _, p := range []string{">=", "^", "~", ">", "=", "v"} {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " -+|<"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return v, false
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// hygLess reports whether a sorts before b.
func hygLess(a, b hygSemver) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// hygVulnerable returns the advisories that apply to name@version. Versions
// that cannot be parsed are skipped unless the advisory covers every version.
func hygVulnerable(name, version string) []hygAdvisory {
	var hits []hygAdvisory
	for _, a := range hygAdvisories {
		if a.name != name {
			continue
		}
		if a.fixedIn == "" {
			hits = append(hits, a)
			continue
		}
		have, ok := hygParseSemver(version)
		if !ok {
			continue
		}
		fixed, ok := hygFixFor(have, a.fixedIn)
		if ok && hygLess(have, fixed) {
			hits = append(hits, a)
		}
	}
	return hits
}

// hygFixFor picks the fixed version that applies to have: the fix on the same
// major.minor line, else the same major line, else the lowest fix above have
// (an older line with no backport). ok is false when have is newer than every
// listed line.
func hygFixFor(have hygSemver, fixedIn string) (hygSemver, bool) {
	var fixes []hygSemver
	for _, f := range strings.Fields(fixedIn) {
		if v, ok := hygParseSemver(f); ok {
			fixes = append(fixes, v)
		}
	}
	for _, f := range fixes {
		if f[0] == have[0] && f[1] == have[1] {
			return f, true
		}
	}
	for _, f := range fixes {
		if f[0] == have[0] {
			return f, true
		}
	}
	var best hygSemver
	found := false
	for _, f := range fixes {
		if hygLess(have, f) && (!found || hygLess(f, best)) {
			best, found = f, true
		}
	}
	return best, found
}
