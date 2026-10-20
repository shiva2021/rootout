// Package testutil builds fake home/root trees under a temp dir so filesystem
// tests never touch the real home. Every test that receives an fsx.Env must
// call RequireSandbox (FakeHome does so itself).
package testutil

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/shiva2021/rootout/internal/fsx"
)

// Spec declares the fake filesystem. Paths use two prefixes:
//
//	"~/..."  -> under env.Home   (e.g. "~/Library/Caches/com.foo.app/x.db")
//	"/..."   -> under env.Root   (e.g. "/Applications/Foo.app", "/Library/LaunchDaemons/com.foo.plist")
type Spec struct {
	Dirs     []string          // created empty (parents too)
	Files    map[string]string // path -> content (parents created)
	Symlinks map[string]string // linkPath -> target (target is taken literally; may be relative or absolute)
	Apps     []App             // .app bundles to synthesize
}

// App describes a .app bundle to synthesize.
type App struct {
	Path     string // "/Applications/Foo.app" or "~/Applications/Foo.app"
	BundleID string // CFBundleIdentifier
	Name     string // CFBundleName; defaults to basename without .app
	Version  string // CFBundleShortVersionString; default "1.0"
	Exec     string // CFBundleExecutable; default = Name
}

// FakeHome creates the tree under t.TempDir() and returns an fsx.Env whose
// Root is the temp dir and Home is <tempdir>/home. It calls RequireSandbox
// before writing anything.
func FakeHome(t *testing.T, spec Spec) fsx.Env {
	t.Helper()
	root := t.TempDir()
	env := fsx.Env{Root: root, Home: filepath.Join(root, "home")}
	RequireSandbox(t, env)

	mkdir := func(p string) {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("testutil: mkdir %s: %v", p, err)
		}
	}
	write := func(p, content string, mode os.FileMode) {
		mkdir(filepath.Dir(p))
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatalf("testutil: write %s: %v", p, err)
		}
	}

	mkdir(env.Home)
	for _, d := range spec.Dirs {
		mkdir(Resolve(env, d))
	}
	for p, content := range spec.Files {
		write(Resolve(env, p), content, 0o644)
	}
	for _, app := range spec.Apps {
		dir := Resolve(env, app.Path)
		name := app.Name
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(dir), ".app")
		}
		version := app.Version
		if version == "" {
			version = "1.0"
		}
		exec := app.Exec
		if exec == "" {
			exec = name
		}
		plist, err := infoPlist(app.BundleID, name, version, exec)
		if err != nil {
			t.Fatalf("testutil: render Info.plist for %s: %v", app.Path, err)
		}
		write(filepath.Join(dir, "Contents", "Info.plist"), plist, 0o644)
		write(filepath.Join(dir, "Contents", "MacOS", exec), machOMagic, 0o755)
		mkdir(filepath.Join(dir, "Contents", "Resources"))
	}
	// Symlinks go last so targets inside the spec already exist.
	for link, target := range spec.Symlinks {
		lp := Resolve(env, link)
		mkdir(filepath.Dir(lp))
		if err := os.Symlink(target, lp); err != nil {
			t.Fatalf("testutil: symlink %s -> %s: %v", lp, target, err)
		}
	}
	return env
}

// RequireSandbox fails the test immediately unless env.Root and env.Home are
// both inside os.TempDir() and neither equals or contains the real home
// directory or "/".
func RequireSandbox(t *testing.T, env fsx.Env) {
	t.Helper()
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("testutil: real home: %v", err)
	}
	if err := sandboxed(env, realHome); err != nil {
		t.Fatalf("testutil: refusing to run outside the sandbox: %v", err)
	}
}

// sandboxed is the predicate behind RequireSandbox, split out so it can be
// tested without failing the calling test.
func sandboxed(env fsx.Env, realHome string) error {
	tmp := os.TempDir()
	for _, dir := range []struct{ name, path string }{
		{"Root", env.Root},
		{"Home", env.Home},
	} {
		p := dir.path
		if p == "" || !filepath.IsAbs(p) {
			return fmt.Errorf("%s %q is not an absolute path", dir.name, p)
		}
		p = filepath.Clean(p)
		if p == string(filepath.Separator) {
			return fmt.Errorf("%s is /", dir.name)
		}
		if within(p, realHome) {
			return fmt.Errorf("%s %q equals or contains the real home %q", dir.name, p, realHome)
		}
		if !within(tmp, p) && !within(canon(tmp), canon(p)) {
			return fmt.Errorf("%s %q is outside the temp dir %q", dir.name, p, tmp)
		}
	}
	return nil
}

// within reports whether child equals parent or sits beneath it.
func within(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// canon resolves symlinks when the path exists, else returns the cleaned path.
// macOS puts temp dirs under /var, which is a symlink to /private/var.
func canon(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// Resolve maps a spec path onto env: "~/x" -> env.User("x"), "/x" -> env.Sys("x").
// Any other form is returned unchanged; callers that need a prefixed path
// should fail the test themselves.
func Resolve(env fsx.Env, p string) string {
	switch {
	case p == "~":
		return env.User()
	case strings.HasPrefix(p, "~/"):
		return env.User(strings.TrimPrefix(p, "~/"))
	case strings.HasPrefix(p, "/"):
		return env.Sys(strings.TrimPrefix(p, "/"))
	}
	return p
}

// Exists reports whether the prefixed path exists under env (symlinks are not
// followed, so a dangling link still counts).
func Exists(env fsx.Env, p string) bool {
	_, err := os.Lstat(Resolve(env, p))
	return err == nil
}

// IsDir reports whether the prefixed path is a directory under env.
func IsDir(env fsx.Env, p string) bool {
	fi, err := os.Stat(Resolve(env, p))
	return err == nil && fi.IsDir()
}

// Read returns the content of the prefixed path under env, failing the test
// if it cannot be read.
func Read(t *testing.T, env fsx.Env, p string) string {
	t.Helper()
	b, err := os.ReadFile(Resolve(env, p))
	if err != nil {
		t.Fatalf("testutil: read %s: %v", p, err)
	}
	return string(b)
}

// machOMagic is the 64-bit Mach-O magic number; enough for an executable
// placeholder that is clearly not a text file.
const machOMagic = "\xcf\xfa\xed\xfe"

// plistTemplate is the standard Apple XML property list layout, including the
// header and DOCTYPE that howett.net/plist emits and expects.
var plistTemplate = template.Must(template.New("plist").Funcs(template.FuncMap{"esc": escapeXML}).Parse(
	`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>{{esc .BundleID}}</string>
	<key>CFBundleName</key>
	<string>{{esc .Name}}</string>
	<key>CFBundleShortVersionString</key>
	<string>{{esc .Version}}</string>
	<key>CFBundleExecutable</key>
	<string>{{esc .Exec}}</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
</dict>
</plist>
`))

func infoPlist(bundleID, name, version, exec string) (string, error) {
	if bundleID == "" {
		return "", errors.New("empty BundleID")
	}
	var sb strings.Builder
	err := plistTemplate.Execute(&sb, App{BundleID: bundleID, Name: name, Version: version, Exec: exec})
	return sb.String(), err
}

func escapeXML(s string) (string, error) {
	var sb strings.Builder
	if err := xml.EscapeText(&sb, []byte(s)); err != nil {
		return "", err
	}
	return sb.String(), nil
}
