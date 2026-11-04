package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"howett.net/plist"
)

// info is the subset of Contents/Info.plist that Discover needs.
type info struct {
	DisplayName string `plist:"CFBundleDisplayName"`
	Name        string `plist:"CFBundleName"`
	BundleID    string `plist:"CFBundleIdentifier"`
	ShortVer    string `plist:"CFBundleShortVersionString"`
	Version     string `plist:"CFBundleVersion"`
}

// readInfo parses <bundle>/Contents/Info.plist. Both XML and binary plists
// are accepted.
func readInfo(bundle string) (info, error) {
	p := filepath.Join(bundle, "Contents", "Info.plist")
	data, err := os.ReadFile(p)
	if err != nil {
		return info{}, fmt.Errorf("app: read %s: %w", p, err)
	}
	var in info
	if _, err := plist.Unmarshal(data, &in); err != nil {
		return info{}, fmt.Errorf("app: parse %s: %w", p, err)
	}
	return in, nil
}

// PlistFacts are the Info.plist keys that say what an app does on the
// system: whether it hides from the Dock, which URL schemes it claims,
// which privacy permissions it declares and whether it installs a
// privileged helper. Usage keys carry the key names only, never the text.
type PlistFacts struct {
	Copyright         string   `json:"copyright,omitempty"`  // NSHumanReadableCopyright
	MinSystem         string   `json:"min_system,omitempty"` // LSMinimumSystemVersion
	Executable        string   `json:"executable,omitempty"` // CFBundleExecutable
	UIElement         bool     `json:"ui_element,omitempty"` // LSUIElement: no Dock icon
	BackgroundOnly    bool     `json:"background_only,omitempty"`
	URLSchemes        []string `json:"url_schemes,omitempty"`        // CFBundleURLTypes[].CFBundleURLSchemes
	UsageKeys         []string `json:"usage_keys,omitempty"`         // NS*UsageDescription key names
	PrivilegedHelpers []string `json:"privileged_helpers,omitempty"` // SMPrivilegedExecutables keys
}

// readPlistFacts parses Info.plist into a map and picks the keys above;
// ok is false when the file is missing or unreadable.
func readPlistFacts(bundle string) (PlistFacts, bool) {
	data, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return PlistFacts{}, false
	}
	var m map[string]any
	if _, err := plist.Unmarshal(data, &m); err != nil {
		return PlistFacts{}, false
	}
	str := func(key string) string {
		s, _ := m[key].(string)
		return s
	}
	p := PlistFacts{
		Copyright:      str("NSHumanReadableCopyright"),
		MinSystem:      str("LSMinimumSystemVersion"),
		Executable:     str("CFBundleExecutable"),
		UIElement:      truthy(m["LSUIElement"]),
		BackgroundOnly: truthy(m["LSBackgroundOnly"]),
	}
	if types, ok := m["CFBundleURLTypes"].([]any); ok {
		for _, t := range types {
			entry, _ := t.(map[string]any)
			schemes, _ := entry["CFBundleURLSchemes"].([]any)
			for _, s := range schemes {
				if scheme, ok := s.(string); ok {
					p.URLSchemes = append(p.URLSchemes, scheme)
				}
			}
		}
	}
	for key := range m {
		if strings.HasSuffix(key, "UsageDescription") {
			p.UsageKeys = append(p.UsageKeys, key)
		}
	}
	if helpers, ok := m["SMPrivilegedExecutables"].(map[string]any); ok {
		for key := range helpers {
			p.PrivilegedHelpers = append(p.PrivilegedHelpers, key)
		}
	}
	sort.Strings(p.URLSchemes)
	sort.Strings(p.UsageKeys)
	sort.Strings(p.PrivilegedHelpers)
	return p, true
}

// truthy reads a plist boolean that may have been written as <true/>, a
// string ("1", "YES", "true") or a number.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(x) {
		case "1", "yes", "true":
			return true
		}
	case uint64:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	}
	return false
}
