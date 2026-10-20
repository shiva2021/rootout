package heur

import (
	"strconv"
	"strings"
)

// hygAdvisory says that versions of a package below fixedIn carry a known
// vulnerability. An empty fixedIn means every version is affected (the
// package is deprecated with no fix).
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
	{"node-fetch", "2.6.7", "CVE-2022-0235"},
	{"ws", "7.4.6", "CVE-2021-32640"},
	{"tar", "6.1.9", "CVE-2021-37713"},
	{"shelljs", "0.8.5", "CVE-2022-0144"},
	{"xml2js", "0.5.0", "CVE-2023-0842"},
	{"semver", "7.5.2", "CVE-2022-25883"},
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
		fixed, ok := hygParseSemver(a.fixedIn)
		if !ok {
			continue
		}
		if hygLess(have, fixed) {
			hits = append(hits, a)
		}
	}
	return hits
}
