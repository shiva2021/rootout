package app

import "strings"

// Tone says how a fact line should read: plain, a warning worth a glance,
// or dim (missing or unremarkable).
type Tone int

const (
	Plain Tone = iota
	Warn
	Dim
)

// FactLine is one labelled fact, ready for a table or a styled pane.
type FactLine struct {
	Label string
	Value string
	Tone  Tone
}

// Summary renders the facts as labelled lines, the same for the command
// line and the screens: signature, Gatekeeper, origin, App Store, dates,
// Dock visibility, permissions, privileged helpers, launch items and the
// probes that could not run.
func (f Facts) Summary() []FactLine {
	unavailable := map[string]bool{}
	for _, u := range f.Unavailable {
		unavailable[u] = true
	}
	var out []FactLine
	add := func(label, value string, tone Tone) { out = append(out, FactLine{label, value, tone}) }

	s := f.Signature
	switch {
	case unavailable["codesign"]:
		add("Signature", "not available", Dim)
	case !s.Signed:
		add("Signature", "unsigned", Warn)
	case s.AdHoc:
		add("Signature", "ad-hoc (no developer identity)", Warn)
	default:
		who := orNone(s.TeamID)
		if len(s.Authority) > 0 {
			who = s.Authority[0] + " · team " + orNone(s.TeamID)
		}
		add("Signature", who, Plain)
	}
	switch {
	case unavailable["spctl"]:
		add("Gatekeeper", "not available", Dim)
	case f.Gatekeeper.Accepted:
		add("Gatekeeper", "accepted · "+f.Gatekeeper.Source, Plain)
	default:
		add("Gatekeeper", "rejected · "+f.Gatekeeper.Source, Warn)
	}
	switch {
	case unavailable["mdls"]:
		add("Origin", "not available", Dim)
	case len(f.Origin.WhereFroms) > 0:
		add("Where from", strings.Join(f.Origin.WhereFroms, ", "), Plain)
	default:
		add("Where from", "unknown", Dim)
	}
	if f.AppStore {
		add("App Store", "receipt present", Plain)
	}
	if !f.Origin.DateAdded.IsZero() {
		add("Added", f.Origin.DateAdded.Local().Format("2006-01-02"), Plain)
	}
	if !f.Origin.LastUsed.IsZero() {
		add("Last used", f.Origin.LastUsed.Local().Format("2006-01-02"), Plain)
	}
	if f.Plist.UIElement || f.Plist.BackgroundOnly {
		add("Runs", "hidden from the Dock", Plain)
	}
	if len(f.Plist.UsageKeys) > 0 {
		add("Permissions", strings.Join(PermissionNames(f.Plist.UsageKeys), ", "), Plain)
	}
	if len(f.Plist.PrivilegedHelpers) > 0 {
		add("Helper", strings.Join(f.Plist.PrivilegedHelpers, ", "), Warn)
	}
	if len(f.LaunchItems) > 0 {
		add("Launch items", strings.Join(f.LaunchItems, ", "), Plain)
	}
	if len(f.Unavailable) > 0 {
		add("Could not run", strings.Join(f.Unavailable, ", "), Dim)
	}
	return out
}

// Source names where the app came from: "system", "brew cask <token>",
// "app store", or "" when unknown.
func (f Facts) Source() string {
	switch {
	case f.App.Protected:
		return "system"
	case f.App.Cask != "":
		return "brew cask " + f.App.Cask
	case f.AppStore:
		return "app store"
	}
	return ""
}

// PermissionNames turns NSCameraUsageDescription into "Camera".
func PermissionNames(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(k, "NS"), "UsageDescription"))
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
