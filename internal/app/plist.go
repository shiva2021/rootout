package app

import (
	"fmt"
	"os"
	"path/filepath"

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
