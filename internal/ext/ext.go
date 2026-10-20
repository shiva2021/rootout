// Package ext enumerates the extensions installed in VS Code and its forks
// (Insiders, Cursor, VSCodium, Windsurf) and can uninstall them.
package ext

import "time"

// Editor is one supported editor installation.
type Editor struct {
	ID      string `json:"id"`       // "vscode" | "vscode-insiders" | "cursor" | "vscodium" | "windsurf"
	Name    string `json:"name"`     // "Visual Studio Code"
	ExtDir  string `json:"ext_dir"`  // ~/.vscode/extensions
	CLI     string `json:"cli"`      // "code" | "code-insiders" | "cursor" | "codium" | "windsurf"; "" when not on PATH
	AppPath string `json:"app_path"` // /Applications/Visual Studio Code.app when found
}

// Extension is one installed extension folder plus what its manifests say.
type Extension struct {
	Editor      string `json:"editor"` // Editor.ID
	ID          string `json:"id"`     // "publisher.name", lower-cased
	Publisher   string `json:"publisher"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Version     string `json:"version"`
	Dir         string `json:"dir"`            // absolute folder
	Main        string `json:"main,omitempty"` // package.json "main" / "browser", relative to Dir
	Description string `json:"description,omitempty"`
	Repository  string `json:"repository,omitempty"` // normalised URL from package.json
	Homepage    string `json:"homepage,omitempty"`

	ActivationEvents []string          `json:"activation_events,omitempty"`
	Scripts          map[string]string `json:"scripts,omitempty"`      // package.json scripts (postinstall, ...)
	Dependencies     map[string]string `json:"dependencies,omitempty"` // package.json dependencies
	Contributes      []string          `json:"contributes,omitempty"`  // keys of package.json "contributes"
	Categories       []string          `json:"categories,omitempty"`

	InstalledAt    time.Time `json:"installed_at,omitempty"` // extensions.json metadata.installedTimestamp
	UpdatedAt      time.Time `json:"updated_at,omitempty"`   // extensions.json metadata.updated, else newest file mtime
	Source         string    `json:"source,omitempty"`       // extensions.json metadata.source, e.g. "gallery", "vsix"
	Builtin        bool      `json:"builtin,omitempty"`
	PreRelease     bool      `json:"pre_release,omitempty"`
	Orphaned       bool      `json:"orphaned"` // folder exists but is listed in .obsolete or missing from extensions.json
	SizeBytes      int64     `json:"size_bytes"`
	FileCount      int       `json:"file_count"`
	HasNodeModules bool      `json:"has_node_modules"`
	Signed         bool      `json:"signed"` // a marketplace signature file is present in the folder
}
