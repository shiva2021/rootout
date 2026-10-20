// Package app discovers installed macOS applications and lets the user find
// one by fuzzy search.
package app

// App is one installed application bundle.
type App struct {
	Name      string `json:"name"`              // CFBundleName, else the directory name without .app
	BundleID  string `json:"bundle_id"`         // CFBundleIdentifier; may be empty for broken bundles
	Path      string `json:"path"`              // /Applications/Foo.app
	Version   string `json:"version,omitempty"` // CFBundleShortVersionString
	SizeBytes int64  `json:"size_bytes"`        // on-disk size of the bundle
	TeamID    string `json:"team_id,omitempty"` // from code signature; filled lazily, may be empty
	Cask      string `json:"cask,omitempty"`    // Homebrew cask token when installed via brew
	Protected bool   `json:"protected"`         // system app; listed but never removable
}
