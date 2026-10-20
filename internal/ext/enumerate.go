package ext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shiva2021/purge/internal/fsx"
	"github.com/shiva2021/purge/internal/run"
)

// Options injects the two things this package touches outside the
// filesystem: PATH lookups and external commands. Zero values mean production.
type Options struct {
	LookPath func(string) (string, error) // default exec.LookPath
	Run      run.Runner                   // default run.Default (used by Uninstall and IsRunning)
}

func (o Options) lookPath() func(string) (string, error) {
	if o.LookPath != nil {
		return o.LookPath
	}
	return exec.LookPath
}

func (o Options) runner() run.Runner {
	if o.Run != nil {
		return o.Run
	}
	return run.Default
}

// Manifest file names inside an extensions directory.
const (
	extensionsJSON = "extensions.json"
	obsoleteFile   = ".obsolete"
	productJSON    = "Contents/Resources/app/product.json"
)

// signatureFiles are the marketplace signature artefacts an install may
// leave behind. Unverified: on the audited machine (VS Code 1.123, 12 gallery
// installs) none of these exist; the installer verifies the signature and
// keeps only .vsixmanifest. HasVsixManifest records that separately.
var signatureFiles = []string{".signature.p7s", ".signature.sig", ".signature.manifest"}

const vsixManifest = ".vsixmanifest"

// editorSpec is the static knowledge about one supported editor.
type editorSpec struct {
	id, name, app, cli, fallback string
}

var editorSpecs = []editorSpec{
	{"vscode", "Visual Studio Code", "Visual Studio Code.app", "code", ".vscode"},
	{"vscode-insiders", "Visual Studio Code - Insiders", "Visual Studio Code - Insiders.app", "code-insiders", ".vscode-insiders"},
	{"cursor", "Cursor", "Cursor.app", "cursor", ".cursor"},
	{"vscodium", "VSCodium", "VSCodium.app", "codium", ".vscode-oss"},
	{"windsurf", "Windsurf", "Windsurf.app", "windsurf", ".windsurf"},
}

// Editors returns the supported editors found on this machine. An editor
// counts when its app bundle exists under /Applications or ~/Applications, or
// when its extensions directory exists. ExtDir comes from the bundle's
// product.json "dataFolderName" when readable, else a per-editor fallback.
func Editors(env fsx.Env, opts Options) []Editor {
	lookPath := opts.lookPath()
	var out []Editor
	for _, spec := range editorSpecs {
		ed := Editor{ID: spec.id, Name: spec.name}
		for _, p := range []string{env.Sys("Applications", spec.app), env.User("Applications", spec.app)} {
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				ed.AppPath = p
				break
			}
		}
		folder := spec.fallback
		if ed.AppPath != "" {
			if f := dataFolderName(filepath.Join(ed.AppPath, productJSON)); f != "" {
				folder = f
			}
		}
		ed.ExtDir = env.User(folder, "extensions")
		dirExists := false
		if fi, err := os.Stat(ed.ExtDir); err == nil && fi.IsDir() {
			dirExists = true
		}
		if ed.AppPath == "" && !dirExists {
			continue
		}
		if _, err := lookPath(spec.cli); err == nil {
			ed.CLI = spec.cli
		}
		out = append(out, ed)
	}
	return out
}

// dataFolderName reads product.json and returns its dataFolderName, or ""
// when the file is missing, unparseable or the value is not a plain folder.
func dataFolderName(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var p struct {
		DataFolderName string `json:"dataFolderName"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		slog.Debug("ext: parse product.json", "path", path, "err", err)
		return ""
	}
	f := p.DataFolderName
	if f == "" || f != filepath.Base(f) || f == "." || f == ".." {
		return ""
	}
	return f
}

// Enumerate lists every extension folder of every editor found, sorted by
// Editor then ID then Dir.
func Enumerate(env fsx.Env, opts Options) ([]Extension, error) {
	var out []Extension
	for _, ed := range Editors(env, opts) {
		exts, err := enumerateDir(ed.ID, ed.ExtDir)
		if err != nil {
			return nil, err
		}
		out = append(out, exts...)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Editor != b.Editor {
			return a.Editor < b.Editor
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Dir < b.Dir
	})
	return out, nil
}

// enumerateDir lists the extension folders directly inside extDir.
func enumerateDir(editorID, extDir string) ([]Extension, error) {
	entries, err := os.ReadDir(extDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ext: read %s: %w", extDir, err)
	}
	installed, err := readExtensionsJSON(filepath.Join(extDir, extensionsJSON))
	if err != nil {
		return nil, err
	}
	obsolete, err := readObsolete(filepath.Join(extDir, obsoleteFile))
	if err != nil {
		return nil, err
	}

	var out []Extension
	for _, de := range entries {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		dir := filepath.Join(extDir, de.Name())
		e := Extension{Editor: editorID, Dir: dir}
		e.Publisher, e.Name, e.Version, e.Platform = parseFolderName(de.Name())

		manifestErr := readPackageJSON(filepath.Join(dir, "package.json"), &e)
		if manifestErr != nil {
			slog.Debug("ext: package.json", "dir", dir, "err", manifestErr)
		}
		e.ID = strings.ToLower(e.Publisher + "." + e.Name)

		meta, listed := installed[de.Name()]
		e.Orphaned = !listed || obsolete[de.Name()] || manifestErr != nil
		if listed {
			if meta.Metadata.InstalledTimestamp > 0 {
				e.InstalledAt = time.UnixMilli(meta.Metadata.InstalledTimestamp)
			}
			e.Source = meta.Metadata.Source
			e.Builtin = meta.Metadata.IsBuiltin
			e.PreRelease = meta.Metadata.IsPreReleaseVersion
			if e.Platform == "" && meta.Metadata.TargetPlatform != "" && meta.Metadata.TargetPlatform != "undefined" {
				e.Platform = meta.Metadata.TargetPlatform
			}
			// "updated" is a bool in current VS Code builds; only a number is a timestamp.
			var ms int64
			if json.Unmarshal(meta.Metadata.Updated, &ms) == nil && ms > 0 {
				e.UpdatedAt = time.UnixMilli(ms)
			}
		}

		newest := walkStats(dir, &e)
		if e.UpdatedAt.IsZero() {
			e.UpdatedAt = newest
		}
		if fi, err := os.Stat(filepath.Join(dir, "node_modules")); err == nil && fi.IsDir() {
			e.HasNodeModules = true
		}
		for _, name := range signatureFiles {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				e.Signed = true
				break
			}
		}
		if _, err := os.Lstat(filepath.Join(dir, vsixManifest)); err == nil {
			e.HasVsixManifest = true
		}
		out = append(out, e)
	}
	return out, nil
}

// Folder names are <publisher>.<name>-<version>[-<platform>]. The platform
// form is tried first so a bare "-universal" is not read as a prerelease tag.
const (
	folderHead     = `^([^.]+)\.(.+?)-(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+?)?)`
	folderPlatform = `-(darwin-x64|darwin-arm64|linux-x64|linux-arm64|linux-armhf|alpine-x64|alpine-arm64|win32-x64|win32-arm64|web|universal)$`
)

var (
	folderPlatformRE = regexp.MustCompile(folderHead + folderPlatform)
	folderRE         = regexp.MustCompile(folderHead + `$`)
)

// parseFolderName splits an extension folder name into its parts. Names that
// do not follow the convention yield whatever can be salvaged.
func parseFolderName(folder string) (publisher, name, version, platform string) {
	if m := folderPlatformRE.FindStringSubmatch(folder); m != nil {
		return m[1], m[2], m[3], m[4]
	}
	if m := folderRE.FindStringSubmatch(folder); m != nil {
		return m[1], m[2], m[3], ""
	}
	publisher, name, ok := strings.Cut(folder, ".")
	if !ok {
		return "", folder, "", ""
	}
	return publisher, name, "", ""
}

// packageJSON is the subset of an extension manifest purge reads.
type packageJSON struct {
	Publisher        string                     `json:"publisher"`
	Name             string                     `json:"name"`
	DisplayName      string                     `json:"displayName"`
	Version          string                     `json:"version"`
	Main             string                     `json:"main"`
	Browser          string                     `json:"browser"`
	Description      string                     `json:"description"`
	Repository       json.RawMessage            `json:"repository"`
	Homepage         string                     `json:"homepage"`
	ActivationEvents []string                   `json:"activationEvents"`
	Scripts          map[string]string          `json:"scripts"`
	Dependencies     map[string]string          `json:"dependencies"`
	Contributes      map[string]json.RawMessage `json:"contributes"`
	Categories       []string                   `json:"categories"`
}

// readPackageJSON fills e from the manifest at path. A missing or
// syntactically broken manifest is an error; a field of the wrong type is
// skipped and the rest is kept.
func readPackageJSON(path string, e *Extension) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ext: read %s: %w", path, err)
	}
	var p packageJSON
	if err := json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &p); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return fmt.Errorf("ext: parse %s: %w", path, err)
		}
		slog.Debug("ext: package.json field skipped", "path", path, "err", err)
	}
	if p.Publisher != "" {
		e.Publisher = p.Publisher
	}
	if p.Name != "" {
		e.Name = p.Name
	}
	if p.Version != "" {
		e.Version = p.Version
	}
	e.DisplayName = p.DisplayName
	e.Main = p.Main
	if e.Main == "" {
		e.Main = p.Browser
	}
	e.Description = p.Description
	e.Repository = repositoryURL(p.Repository)
	e.Homepage = p.Homepage
	e.ActivationEvents = p.ActivationEvents
	e.Scripts = p.Scripts
	e.Dependencies = p.Dependencies
	e.Categories = p.Categories
	if len(p.Contributes) > 0 {
		e.Contributes = make([]string, 0, len(p.Contributes))
		for k := range p.Contributes {
			e.Contributes = append(e.Contributes, k)
		}
		sort.Strings(e.Contributes)
	}
	return nil
}

// repositoryURL accepts the string and {url} forms of package.json
// "repository" and normalises npm shorthands and git schemes to https.
func repositoryURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var obj struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(raw, &obj) != nil {
			return ""
		}
		s = obj.URL
	}
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "git+")
	s = strings.TrimSuffix(s, ".git")
	switch {
	case strings.HasPrefix(s, "github:"):
		return "https://github.com/" + strings.TrimPrefix(s, "github:")
	case strings.HasPrefix(s, "gitlab:"):
		return "https://gitlab.com/" + strings.TrimPrefix(s, "gitlab:")
	case strings.HasPrefix(s, "bitbucket:"):
		return "https://bitbucket.org/" + strings.TrimPrefix(s, "bitbucket:")
	case strings.HasPrefix(s, "git://"):
		return "https://" + strings.TrimPrefix(s, "git://")
	case strings.HasPrefix(s, "git@github.com:"):
		return "https://github.com/" + strings.TrimPrefix(s, "git@github.com:")
	case s != "" && !strings.Contains(s, ":") && strings.Count(s, "/") == 1:
		return "https://github.com/" + s // npm "user/repo" shorthand
	}
	return s
}

// walkStats sums sizes and counts files under dir without following
// symlinks, and returns the newest modification time seen.
func walkStats(dir string, e *Extension) time.Time {
	var newest time.Time
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Debug("ext: walk", "path", path, "err", err)
			if d != nil && d.IsDir() && path != dir {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info() // Lstat semantics: symlinks are counted, never followed
		if err != nil {
			return nil //nolint:nilerr // an entry vanishing mid-walk is not worth failing the scan
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		if d.IsDir() {
			return nil
		}
		e.FileCount++
		e.SizeBytes += info.Size()
		return nil
	})
	if err != nil {
		slog.Debug("ext: walk", "dir", dir, "err", err)
	}
	return newest
}

// extensionEntry is one element of extensions.json.
type extensionEntry struct {
	Identifier struct {
		ID string `json:"id"`
	} `json:"identifier"`
	Version  string `json:"version"`
	Location struct {
		Path string `json:"path"`
	} `json:"location"`
	RelativeLocation string `json:"relativeLocation"`
	Metadata         struct {
		InstalledTimestamp  int64           `json:"installedTimestamp"`
		Updated             json.RawMessage `json:"updated"`
		Source              string          `json:"source"`
		IsBuiltin           bool            `json:"isBuiltin"`
		IsPreReleaseVersion bool            `json:"isPreReleaseVersion"`
		TargetPlatform      string          `json:"targetPlatform"`
	} `json:"metadata"`
}

// folder returns the extension folder name an entry refers to.
func (x extensionEntry) folder() string {
	if x.RelativeLocation != "" {
		return x.RelativeLocation
	}
	if x.Location.Path != "" {
		return filepath.Base(x.Location.Path)
	}
	return ""
}

// readExtensionsJSON returns the entries keyed by folder name. A missing or
// empty file yields an empty map.
func readExtensionsJSON(path string) (map[string]extensionEntry, error) {
	out := map[string]extensionEntry{}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ext: read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return out, nil
	}
	var entries []extensionEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("ext: parse %s: %w", path, err)
	}
	for _, x := range entries {
		if f := x.folder(); f != "" {
			out[f] = x
		}
	}
	return out, nil
}

// readObsolete returns the folder names listed in .obsolete, a JSON object
// whose keys are folder names. A missing or empty file yields an empty set.
func readObsolete(path string) (map[string]bool, error) {
	out := map[string]bool{}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ext: read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return out, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("ext: parse %s: %w", path, err)
	}
	for k := range m {
		out[k] = true
	}
	return out, nil
}
