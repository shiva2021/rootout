package ext

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shiva2021/purge/internal/fsx"
	"github.com/shiva2021/purge/internal/testutil"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// fakeHome builds the three-editor fixture: VS Code and Cursor bundles with
// product.json, Windsurf by extension dir only. extensions.json location.path
// keeps a literal "HOME" prefix; matching goes by relativeLocation, or by the
// basename of location.path for the Cursor fixture which omits it.
func fakeHome(t *testing.T) fsx.Env {
	t.Helper()
	spec := testutil.Spec{
		Apps: []testutil.App{
			{Path: "/Applications/Visual Studio Code.app", BundleID: "com.microsoft.VSCode", Exec: "Electron"},
			{Path: "~/Applications/Cursor.app", BundleID: "com.todesktop.230313mzl4w4u92", Exec: "Cursor"},
		},
		Dirs: []string{
			"~/.windsurf/extensions",
			"~/.vscode/extensions/leetcode.vscode-leetcode-0.18.4/node_modules/underscore",
			"~/.vscode/extensions/broken.ext-1.0.0/dist",
		},
		Files: map[string]string{
			"/Applications/Visual Studio Code.app/Contents/Resources/app/product.json": `{"nameShort":"Code","dataFolderName":".vscode"}`,
			"~/Applications/Cursor.app/Contents/Resources/app/product.json":            `{"nameShort":"Cursor","dataFolderName":".cursor"}`,

			"~/.vscode/extensions/extensions.json": fixture(t, "vscode-extensions.json"),
			"~/.vscode/extensions/.obsolete":       `{"heisebaiyun.kiro-for-cc-0.2.9": true, "github.copilot-1.275.0": true}`,

			"~/.vscode/extensions/ms-python.python-2026.10.0/package.json":            fixture(t, "python-package.json"),
			"~/.vscode/extensions/ms-python.python-2026.10.0/.vsixmanifest":           "<PackageManifest/>",
			"~/.vscode/extensions/ms-python.python-2026.10.0/out/client/extension.js": "module.exports = {}; // 32 bytes....",
			"~/.vscode/extensions/ms-python.python-2026.10.0/README.md":               "# Python\n",

			"~/.vscode/extensions/heisebaiyun.kiro-for-cc-0.2.9/package.json": `{"name":"kiro-for-cc","publisher":"heisebaiyun","version":"0.2.9","displayName":"Kiro for CC","activationEvents":["onStartupFinished"],"repository":"github:heisebaiyun/kiro-for-cc","main":"./dist/extension.js"}`,

			"~/.vscode/extensions/leetcode.vscode-leetcode-0.18.4/package.json":                          `{"name":"vscode-leetcode","publisher":"LeetCode","version":"0.18.4","repository":"git+https://github.com/LeetCode-OpenSource/vscode-leetcode.git"}`,
			"~/.vscode/extensions/leetcode.vscode-leetcode-0.18.4/node_modules/underscore/underscore.js": "x",

			"~/.vscode/extensions/broken.ext-1.0.0/dist/extension.js": "x",

			"~/.cursor/extensions/extensions.json":                             fixture(t, "cursor-extensions.json"),
			"~/.cursor/extensions/some.ext-1.0.0-darwin-arm64/package.json":    `{"name":"ext","publisher":"Some","version":"1.0.0","browser":"./web.js","contributes":{"themes":[]},"categories":["Themes"]}`,
			"~/.cursor/extensions/some.ext-1.0.0-darwin-arm64/.signature.p7s":  "sig",
			"~/.cursor/extensions/some.ext-1.0.0-darwin-arm64/.vsixmanifest":   "<PackageManifest/>",
			"~/.cursor/extensions/some.ext-1.0.0-darwin-arm64/bin/native.node": "bytes",
			"~/.cursor/extensions/some.ext-1.0.0-darwin-arm64/not-an-ext.txt":  "ignored because it is a file, not a folder",
			"~/.cursor/extensions/a-file-not-a-folder.txt":                     "skipped by Enumerate",
		},
		Symlinks: map[string]string{
			// Points outside the folder; must be counted as a link, never followed.
			"~/.vscode/extensions/ms-python.python-2026.10.0/escape": "/",
		},
	}
	return testutil.FakeHome(t, spec)
}

func lookPathWith(found ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, f := range found {
			if f == name {
				return "/usr/local/bin/" + name, nil
			}
		}
		return "", exec.ErrNotFound
	}
}

func TestEditors(t *testing.T) {
	env := fakeHome(t)
	testutil.RequireSandbox(t, env)
	got := Editors(env, Options{LookPath: lookPathWith("code", "windsurf")})

	want := []Editor{
		{ID: "vscode", Name: "Visual Studio Code", ExtDir: env.User(".vscode", "extensions"), CLI: "code", AppPath: env.Sys("Applications", "Visual Studio Code.app")},
		{ID: "cursor", Name: "Cursor", ExtDir: env.User(".cursor", "extensions"), CLI: "", AppPath: env.User("Applications", "Cursor.app")},
		{ID: "windsurf", Name: "Windsurf", ExtDir: env.User(".windsurf", "extensions"), CLI: "windsurf", AppPath: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Editors mismatch\n got: %+v\nwant: %+v", got, want)
	}

	t.Run("product.json overrides fallback", func(t *testing.T) {
		env := testutil.FakeHome(t, testutil.Spec{
			Apps:  []testutil.App{{Path: "/Applications/VSCodium.app", BundleID: "com.vscodium"}},
			Files: map[string]string{"/Applications/VSCodium.app/Contents/Resources/app/product.json": `{"dataFolderName":".codium-custom"}`},
		})
		eds := Editors(env, Options{LookPath: lookPathWith()})
		if len(eds) != 1 || eds[0].ID != "vscodium" || eds[0].ExtDir != env.User(".codium-custom", "extensions") {
			t.Fatalf("got %+v", eds)
		}
	})
	t.Run("bad product.json falls back", func(t *testing.T) {
		env := testutil.FakeHome(t, testutil.Spec{
			Apps:  []testutil.App{{Path: "/Applications/VSCodium.app", BundleID: "com.vscodium"}},
			Files: map[string]string{"/Applications/VSCodium.app/Contents/Resources/app/product.json": `{"dataFolderName":"../etc"}`},
		})
		eds := Editors(env, Options{LookPath: lookPathWith()})
		if len(eds) != 1 || eds[0].ExtDir != env.User(".vscode-oss", "extensions") {
			t.Fatalf("got %+v", eds)
		}
	})
	t.Run("nothing installed", func(t *testing.T) {
		env := testutil.FakeHome(t, testutil.Spec{})
		if eds := Editors(env, Options{LookPath: lookPathWith("code")}); len(eds) != 0 {
			t.Fatalf("got %+v, want none", eds)
		}
	})
}

func TestEnumerate(t *testing.T) {
	env := fakeHome(t)
	testutil.RequireSandbox(t, env)
	exts, err := Enumerate(env, Options{LookPath: lookPathWith()})
	if err != nil {
		t.Fatal(err)
	}

	var ids []string
	for _, e := range exts {
		ids = append(ids, e.Editor+"/"+e.ID)
	}
	wantIDs := []string{
		"cursor/some.ext",
		"vscode/broken.ext",
		"vscode/heisebaiyun.kiro-for-cc",
		"vscode/leetcode.vscode-leetcode",
		"vscode/ms-python.python",
	}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("order/ids = %v, want %v", ids, wantIDs)
	}
	byID := map[string]Extension{}
	for _, e := range exts {
		byID[e.ID] = e
	}

	t.Run("full manifest", func(t *testing.T) {
		e := byID["ms-python.python"]
		checks := []struct {
			name      string
			got, want any
		}{
			{"Publisher", e.Publisher, "ms-python"},
			{"Name", e.Name, "python"},
			{"DisplayName", e.DisplayName, "Python"},
			{"Version", e.Version, "2026.10.0"},
			{"Platform", e.Platform, ""},
			{"Dir", e.Dir, env.User(".vscode", "extensions", "ms-python.python-2026.10.0")},
			{"Main", e.Main, "./out/client/extension"},
			{"Repository", e.Repository, "https://github.com/Microsoft/vscode-python"},
			{"Homepage", e.Homepage, "https://github.com/Microsoft/vscode-python"},
			{"ActivationEvents", e.ActivationEvents, []string{"onLanguage:python", "onDebugResolve:python", "workspaceContains:mspythonconfig.json"}},
			{"Scripts", e.Scripts, map[string]string{"postinstall": "node ./build/ci/postInstall.js", "compile": "tsc -p ./"}},
			{"Dependencies", e.Dependencies, map[string]string{"vscode-languageclient": "^9.0.1", "semver": "^7.5.4"}},
			{"Contributes", e.Contributes, []string{"commands", "configuration", "debuggers", "languages"}},
			{"Categories", e.Categories, []string{"Programming Languages", "Debuggers", "Linters"}},
			{"InstalledAt", e.InstalledAt, time.UnixMilli(1717903599687)},
			{"Source", e.Source, "gallery"},
			{"Builtin", e.Builtin, false},
			{"PreRelease", e.PreRelease, false},
			{"Orphaned", e.Orphaned, false},
			{"HasNodeModules", e.HasNodeModules, false},
			{"Signed", e.Signed, false},
			{"HasVsixManifest", e.HasVsixManifest, true},
			// package.json + .vsixmanifest + extension.js + README.md + symlink
			{"FileCount", e.FileCount, 5},
		}
		for _, c := range checks {
			if !reflect.DeepEqual(c.got, c.want) {
				t.Errorf("%s = %#v, want %#v", c.name, c.got, c.want)
			}
		}
		if !strings.HasPrefix(e.Description, "Python language support") {
			t.Errorf("Description = %q", e.Description)
		}
		// Size: the four regular files plus the symlink's own length ("/" = 1).
		var want int64
		for _, rel := range []string{"package.json", ".vsixmanifest", "out/client/extension.js", "README.md"} {
			fi, err := os.Stat(filepath.Join(e.Dir, rel))
			if err != nil {
				t.Fatal(err)
			}
			want += fi.Size()
		}
		want++
		if e.SizeBytes != want {
			t.Errorf("SizeBytes = %d, want %d (symlink to / must not be followed)", e.SizeBytes, want)
		}
		// metadata.updated is a bool, so UpdatedAt falls back to the newest mtime.
		if e.UpdatedAt.IsZero() || time.Since(e.UpdatedAt) > time.Minute {
			t.Errorf("UpdatedAt = %v, want a recent mtime", e.UpdatedAt)
		}
	})

	t.Run("obsolete is orphaned", func(t *testing.T) {
		e := byID["heisebaiyun.kiro-for-cc"]
		if !e.Orphaned {
			t.Error("want Orphaned")
		}
		if e.Repository != "https://github.com/heisebaiyun/kiro-for-cc" {
			t.Errorf("Repository = %q", e.Repository)
		}
		if !e.PreRelease || e.InstalledAt != time.UnixMilli(1740000000000) {
			t.Errorf("metadata not merged: %+v", e)
		}
		if e.Main != "./dist/extension.js" || e.DisplayName != "Kiro for CC" {
			t.Errorf("manifest not parsed: %+v", e)
		}
	})

	t.Run("unlisted is orphaned with node_modules", func(t *testing.T) {
		e := byID["leetcode.vscode-leetcode"]
		if !e.Orphaned || !e.HasNodeModules {
			t.Errorf("Orphaned=%v HasNodeModules=%v", e.Orphaned, e.HasNodeModules)
		}
		if e.Publisher != "LeetCode" || e.ID != "leetcode.vscode-leetcode" {
			t.Errorf("ID should be lower-cased: %q (%q)", e.ID, e.Publisher)
		}
		if e.Repository != "https://github.com/LeetCode-OpenSource/vscode-leetcode" {
			t.Errorf("Repository = %q", e.Repository)
		}
		if e.FileCount != 2 { // package.json + underscore.js
			t.Errorf("FileCount = %d", e.FileCount)
		}
		if e.InstalledAt.IsZero() != true || e.Source != "" {
			t.Errorf("unexpected metadata: %+v", e)
		}
	})

	t.Run("no package.json", func(t *testing.T) {
		e := byID["broken.ext"]
		if !e.Orphaned || e.Publisher != "broken" || e.Name != "ext" || e.Version != "1.0.0" {
			t.Errorf("got %+v", e)
		}
		if e.FileCount != 1 || e.SizeBytes != 1 {
			t.Errorf("FileCount=%d SizeBytes=%d", e.FileCount, e.SizeBytes)
		}
	})

	t.Run("platform suffix and signature", func(t *testing.T) {
		e := byID["some.ext"]
		if e.Editor != "cursor" || e.Platform != "darwin-arm64" || e.Version != "1.0.0" {
			t.Errorf("got %+v", e)
		}
		if !e.Signed || !e.HasVsixManifest {
			t.Errorf("Signed=%v HasVsixManifest=%v", e.Signed, e.HasVsixManifest)
		}
		if e.Main != "./web.js" {
			t.Errorf("Main should fall back to browser: %q", e.Main)
		}
		if e.Orphaned || !e.Builtin || e.Source != "vsix" {
			t.Errorf("metadata: %+v", e)
		}
		if e.UpdatedAt != time.UnixMilli(1750000000000) || e.InstalledAt != time.UnixMilli(1749000000000) {
			t.Errorf("timestamps: %v %v", e.UpdatedAt, e.InstalledAt)
		}
		if !reflect.DeepEqual(e.Contributes, []string{"themes"}) || !reflect.DeepEqual(e.Categories, []string{"Themes"}) {
			t.Errorf("contributes/categories: %v %v", e.Contributes, e.Categories)
		}
	})

	t.Run("bad extensions.json is an error", func(t *testing.T) {
		env := testutil.FakeHome(t, testutil.Spec{Files: map[string]string{
			"~/.vscode/extensions/extensions.json": "not json",
		}})
		if _, err := Enumerate(env, Options{LookPath: lookPathWith()}); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestParseFolderName(t *testing.T) {
	cases := []struct {
		in                                 string
		publisher, name, version, platform string
	}{
		{"ms-python.python-2026.10.0", "ms-python", "python", "2026.10.0", ""},
		{"ms-python.python-2026.10.0-darwin-arm64", "ms-python", "python", "2026.10.0", "darwin-arm64"},
		{"ms-toolsai.vscode-jupyter-cell-tags-0.1.9", "ms-toolsai", "vscode-jupyter-cell-tags", "0.1.9", ""},
		{"pub.name-1.2.3-beta.1", "pub", "name", "1.2.3-beta.1", ""},
		{"pub.name-1.2.3-beta.1-linux-x64", "pub", "name", "1.2.3-beta.1", "linux-x64"},
		{"pub.name-1.2.3-universal", "pub", "name", "1.2.3", "universal"},
		{"pub.weird", "pub", "weird", "", ""},
		{"noversion", "", "noversion", "", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			p, n, v, pl := parseFolderName(c.in)
			if p != c.publisher || n != c.name || v != c.version || pl != c.platform {
				t.Errorf("got (%q,%q,%q,%q) want (%q,%q,%q,%q)", p, n, v, pl, c.publisher, c.name, c.version, c.platform)
			}
		})
	}
}

func TestRepositoryURL(t *testing.T) {
	cases := map[string]string{
		`"https://github.com/a/b.git"`:           "https://github.com/a/b",
		`"git+ssh://git@github.com/a/b.git"`:     "ssh://git@github.com/a/b",
		`"git://github.com/a/b.git"`:             "https://github.com/a/b",
		`"git@github.com:a/b.git"`:               "https://github.com/a/b",
		`"github:a/b"`:                           "https://github.com/a/b",
		`"a/b"`:                                  "https://github.com/a/b",
		`{"type":"git","url":"https://x.y/a/b"}`: "https://x.y/a/b",
		`{"type":"git"}`:                         "",
		`42`:                                     "",
		``:                                       "",
	}
	for in, want := range cases {
		if got := repositoryURL(json.RawMessage(in)); got != want {
			t.Errorf("repositoryURL(%s) = %q, want %q", in, got, want)
		}
	}
}

// call records one Runner invocation.
type call struct {
	name string
	args []string
}

// fakeRunner returns a Runner that records calls; pgrep reports running or not.
func fakeRunner(calls *[]call, running bool) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, call{name, args})
		if name == "pgrep" {
			if running {
				return []byte("4242\n"), nil
			}
			return nil, &exec.ExitError{}
		}
		return nil, nil
	}
}

func find(t *testing.T, exts []Extension, id string) Extension {
	t.Helper()
	for _, e := range exts {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("extension %s not enumerated", id)
	return Extension{}
}

func readExtensionIDs(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []extensionEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		t.Fatalf("extensions.json no longer valid: %v\n%s", err, b)
	}
	var ids []string
	for _, x := range entries {
		ids = append(ids, x.Identifier.ID)
	}
	return ids
}

func TestUninstall(t *testing.T) {
	ctx := context.Background()

	t.Run("with CLI", func(t *testing.T) {
		env := fakeHome(t)
		opts := Options{LookPath: lookPathWith("code")}
		editors := Editors(env, opts)
		exts, err := Enumerate(env, opts)
		if err != nil {
			t.Fatal(err)
		}
		e := find(t, exts, "ms-python.python")
		var calls []call
		opts.Run = fakeRunner(&calls, true) // running does not matter with a CLI
		if err := Uninstall(ctx, env, e, editors, opts); err != nil {
			t.Fatal(err)
		}
		want := []call{{"code", []string{"--uninstall-extension", "ms-python.python"}}}
		if !reflect.DeepEqual(calls, want) {
			t.Errorf("calls = %+v, want %+v", calls, want)
		}
		if _, err := os.Lstat(e.Dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("folder still exists: %v", err)
		}
		if ids := readExtensionIDs(t, env.User(".vscode", "extensions", "extensions.json")); !reflect.DeepEqual(ids, []string{"heisebaiyun.kiro-for-cc"}) {
			t.Errorf("extensions.json ids = %v", ids)
		}
	})

	t.Run("without CLI, not running", func(t *testing.T) {
		env := fakeHome(t)
		opts := Options{LookPath: lookPathWith()}
		editors := Editors(env, opts)
		exts, err := Enumerate(env, opts)
		if err != nil {
			t.Fatal(err)
		}
		e := find(t, exts, "heisebaiyun.kiro-for-cc")
		var calls []call
		opts.Run = fakeRunner(&calls, false)
		if err := Uninstall(ctx, env, e, editors, opts); err != nil {
			t.Fatal(err)
		}
		wantPgrep := call{"pgrep", []string{"-f", env.Sys("Applications", "Visual Studio Code.app", "Contents", "MacOS") + "/"}}
		if !reflect.DeepEqual(calls, []call{wantPgrep}) {
			t.Errorf("calls = %+v, want %+v", calls, wantPgrep)
		}
		if _, err := os.Lstat(e.Dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("folder still exists: %v", err)
		}
		extJSON := env.User(".vscode", "extensions", "extensions.json")
		if ids := readExtensionIDs(t, extJSON); !reflect.DeepEqual(ids, []string{"ms-python.python"}) {
			t.Errorf("extensions.json ids = %v", ids)
		}
		// The surviving entry keeps every original field.
		b, _ := os.ReadFile(extJSON)
		for _, needle := range []string{`"uuid":"f1f59ae4-9318-4f3c-a9b5-81b2eaa5f8a5"`, `"installedTimestamp":1717903599687`, `"$mid":1`, `"pinned":false`} {
			if !strings.Contains(string(b), needle) {
				t.Errorf("extensions.json lost %s:\n%s", needle, b)
			}
		}
		var obs map[string]bool
		if err := json.Unmarshal([]byte(testutil.Read(t, env, "~/.vscode/extensions/.obsolete")), &obs); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(obs, map[string]bool{"github.copilot-1.275.0": true}) {
			t.Errorf(".obsolete = %v", obs)
		}
		// Idempotent on manifests: a second pass on the same folder changes nothing.
		if err := Uninstall(ctx, env, e, editors, opts); err != nil {
			t.Fatal(err)
		}
		// Neighbours untouched.
		for _, p := range []string{"~/.vscode/extensions/ms-python.python-2026.10.0", "~/.vscode/extensions/leetcode.vscode-leetcode-0.18.4"} {
			if !testutil.IsDir(env, p) {
				t.Errorf("%s was removed", p)
			}
		}
	})

	t.Run("without CLI, running", func(t *testing.T) {
		env := fakeHome(t)
		opts := Options{LookPath: lookPathWith()}
		editors := Editors(env, opts)
		exts, err := Enumerate(env, opts)
		if err != nil {
			t.Fatal(err)
		}
		e := find(t, exts, "ms-python.python")
		var calls []call
		opts.Run = fakeRunner(&calls, true)
		before := testutil.Read(t, env, "~/.vscode/extensions/extensions.json")
		err = Uninstall(ctx, env, e, editors, opts)
		if !errors.Is(err, ErrEditorRunning) {
			t.Fatalf("err = %v, want ErrEditorRunning", err)
		}
		if !testutil.IsDir(env, "~/.vscode/extensions/ms-python.python-2026.10.0") {
			t.Error("folder removed despite running editor")
		}
		if after := testutil.Read(t, env, "~/.vscode/extensions/extensions.json"); after != before {
			t.Error("extensions.json modified despite running editor")
		}
	})

	t.Run("no AppPath skips the running check", func(t *testing.T) {
		env := testutil.FakeHome(t, testutil.Spec{Dirs: []string{"~/.windsurf/extensions/pub.name-1.0.0"}})
		opts := Options{LookPath: lookPathWith()}
		editors := Editors(env, opts)
		exts, err := Enumerate(env, opts)
		if err != nil {
			t.Fatal(err)
		}
		var calls []call
		opts.Run = fakeRunner(&calls, true)
		if err := Uninstall(ctx, env, find(t, exts, "pub.name"), editors, opts); err != nil {
			t.Fatal(err)
		}
		if len(calls) != 0 {
			t.Errorf("unexpected calls %+v", calls)
		}
		if testutil.Exists(env, "~/.windsurf/extensions/pub.name-1.0.0") {
			t.Error("folder not removed")
		}
	})

	t.Run("refuses paths outside ExtDir", func(t *testing.T) {
		env := fakeHome(t)
		opts := Options{LookPath: lookPathWith()}
		editors := Editors(env, opts)
		var calls []call
		opts.Run = fakeRunner(&calls, false)
		victim := env.User("Documents", "important")
		if err := os.MkdirAll(victim, 0o755); err != nil {
			t.Fatal(err)
		}
		extDir := env.User(".vscode", "extensions")
		bad := []string{
			victim,
			extDir,
			filepath.Join(extDir, "..", "extensions"),
			filepath.Join(extDir, "ms-python.python-2026.10.0", "..", "..", "..", "Documents", "important"),
			filepath.Join(extDir, "ms-python.python-2026.10.0", "out"),
			filepath.Join(extDir, "extensions.json"),
			filepath.Join(extDir, ".obsolete"),
			env.User(".cursor", "extensions", "some.ext-1.0.0-darwin-arm64"), // wrong editor
			"relative/path",
			"",
		}
		for _, dir := range bad {
			e := Extension{Editor: "vscode", ID: "x.y", Dir: dir}
			if err := Uninstall(ctx, env, e, editors, opts); err == nil {
				t.Errorf("Uninstall(%q) succeeded, want error", dir)
			}
		}
		if err := Uninstall(ctx, env, Extension{Editor: "nope", Dir: filepath.Join(extDir, "ms-python.python-2026.10.0")}, editors, opts); err == nil {
			t.Error("unknown editor accepted")
		}
		if len(calls) != 0 {
			t.Errorf("runner called for rejected paths: %+v", calls)
		}
		for _, p := range []string{victim, filepath.Join(extDir, "ms-python.python-2026.10.0"), filepath.Join(extDir, "extensions.json"), filepath.Join(extDir, ".obsolete"), env.User(".cursor", "extensions", "some.ext-1.0.0-darwin-arm64")} {
			if _, err := os.Lstat(p); err != nil {
				t.Errorf("%s was removed", p)
			}
		}
	})
}
