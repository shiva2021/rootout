package safety

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/shiva2021/purge/internal/fsx"
)

// newEnv builds a sandboxed Env: Root is a temp dir and Home is <root>/home.
func newEnv(t *testing.T) fsx.Env {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return fsx.Env{Home: home, Root: root}
}

// rule returns the Violation rule in err, "" for nil, or "<err>" for a
// non-Violation error.
func rule(err error) string {
	if err == nil {
		return ""
	}
	var v *Violation
	if errors.As(err, &v) {
		return v.Rule
	}
	return "<" + err.Error() + ">"
}

func TestValidate(t *testing.T) {
	dot := Options{AllowDotfiles: true}
	tests := []struct {
		name string
		path func(e fsx.Env) string
		opts Options
		want string // expected rule, "" for accepted
	}{
		// allowlist and prefixes themselves
		{"user cache entry ok", func(e fsx.Env) string { return e.Library("Caches", "com.foo") }, Options{}, ""},
		{"user cache dir is top level", func(e fsx.Env) string { return e.Library("Caches") }, Options{}, "library_top_level"},
		{"user library itself", func(e fsx.Env) string { return e.Library() }, Options{}, "allowed_prefix_itself"},
		{"user library trailing slash", func(e fsx.Env) string { return e.Library() + "/" }, Options{}, "allowed_prefix_itself"},
		{"sys applications itself", func(e fsx.Env) string { return e.Sys("Applications") }, Options{}, "allowed_prefix_itself"},
		{"sys library itself", func(e fsx.Env) string { return e.Sys("Library") }, Options{}, "allowed_prefix_itself"},
		{"user applications itself", func(e fsx.Env) string { return e.User("Applications") }, Options{}, "allowed_prefix_itself"},
		{"sys app ok", func(e fsx.Env) string { return e.Sys("Applications", "Foo.app") }, Options{}, ""},
		{"sys app contents ok", func(e fsx.Env) string { return e.Sys("Applications", "Foo.app", "Contents") }, Options{}, ""},
		{"utilities as a whole", func(e fsx.Env) string { return e.Sys("Applications", "Utilities") }, Options{}, "protected_prefix"},
		{"app inside utilities ok", func(e fsx.Env) string { return e.Sys("Applications", "Utilities", "Foo.app") }, Options{}, ""},
		{"sys launch daemon plist ok", func(e fsx.Env) string { return e.Sys("Library", "LaunchDaemons", "com.foo.plist") }, Options{}, ""},
		{"sys library top level", func(e fsx.Env) string { return e.Sys("Library", "LaunchDaemons") }, Options{}, "library_top_level"},
		{"sys library app support entry ok", func(e fsx.Env) string { return e.Sys("Library", "Application Support", "Foo") }, Options{}, ""},
		{"user preferences plist ok", func(e fsx.Env) string { return e.Library("Preferences", "com.foo.plist") }, Options{}, ""},
		{"user app ok at depth 2", func(e fsx.Env) string { return e.User("Applications", "Foo.app") }, Options{}, ""},
		{"user applications non-app too shallow", func(e fsx.Env) string { return e.User("Applications", "foo") }, Options{}, "too_shallow"},
		{"user app contents ok", func(e fsx.Env) string { return e.User("Applications", "Foo.app", "Contents") }, Options{}, ""},

		// outside the allowlist
		{"home itself", func(e fsx.Env) string { return e.Home }, Options{}, "outside_allowlist"},
		{"documents", func(e fsx.Env) string { return e.User("Documents", "a", "b") }, Options{}, "outside_allowlist"},
		{"root", func(e fsx.Env) string { return e.Root }, Options{}, "outside_allowlist"},
		{"volumes", func(e fsx.Env) string { return e.Sys("Volumes", "Data", "x") }, Options{}, "outside_allowlist"},
		{"sibling of allowed prefix", func(e fsx.Env) string { return e.Sys("ApplicationsX", "Foo.app") }, Options{}, "outside_allowlist"},
		{"sibling of user library", func(e fsx.Env) string { return e.User("LibraryX", "Caches", "x") }, Options{}, "outside_allowlist"},

		// protected prefixes
		{"system app", func(e fsx.Env) string { return e.Sys("System", "Applications", "Mail.app") }, Options{}, "protected_prefix"},
		{"system root", func(e fsx.Env) string { return e.Sys("System") }, Options{}, "protected_prefix"},
		{"usr", func(e fsx.Env) string { return e.Sys("usr", "local", "bin", "x") }, Options{}, "protected_prefix"},
		{"bin", func(e fsx.Env) string { return e.Sys("bin", "ls") }, Options{}, "protected_prefix"},
		{"sbin", func(e fsx.Env) string { return e.Sys("sbin", "x") }, Options{}, "protected_prefix"},
		{"private receipts", func(e fsx.Env) string { return e.Sys("private", "var", "db", "receipts", "x") }, Options{}, "protected_prefix"},
		{"library apple", func(e fsx.Env) string { return e.Sys("Library", "Apple", "x") }, Options{}, "protected_prefix"},
		{"command line tools", func(e fsx.Env) string { return e.Sys("Library", "Developer", "CommandLineTools", "usr") }, Options{}, "protected_prefix"},
		{"other developer entry ok", func(e fsx.Env) string { return e.Sys("Library", "Developer", "Foo") }, Options{}, ""},
		{"keychain", func(e fsx.Env) string { return e.Library("Keychains", "login.keychain-db") }, Options{}, "protected_prefix"},
		{"mail", func(e fsx.Env) string { return e.Library("Mail", "V10") }, Options{}, "protected_prefix"},
		{"messages", func(e fsx.Env) string { return e.Library("Messages", "chat.db") }, Options{}, "protected_prefix"},
		{"photos", func(e fsx.Env) string { return e.Library("Photos", "Libraries", "x") }, Options{}, "protected_prefix"},
		{"safari", func(e fsx.Env) string { return e.Library("Safari", "History.db") }, Options{}, "protected_prefix"},

		// depth under home
		{"user library child", func(e fsx.Env) string { return e.Library("x") }, Options{}, "library_top_level"},
		{"user library grandchild ok", func(e fsx.Env) string { return e.Library("Logs", "foo.log") }, Options{}, ""},
		{"deep user path ok", func(e fsx.Env) string { return e.Library("Application Support", "Foo", "a", "b", "c") }, Options{}, ""},

		// dotfiles
		{"dot config entry without opt-in", func(e fsx.Env) string { return e.User(".config", "foo") }, Options{}, "outside_allowlist"},
		{"dot config entry with opt-in", func(e fsx.Env) string { return e.User(".config", "foo") }, dot, ""},
		{"dot config nested with opt-in", func(e fsx.Env) string { return e.User(".config", "foo", "bar") }, dot, ""},
		{"dot config itself with opt-in", func(e fsx.Env) string { return e.User(".config") }, dot, "allowed_prefix_itself"},
		{"dot dir without opt-in", func(e fsx.Env) string { return e.User(".foo") }, Options{}, "outside_allowlist"},
		{"dot dir with opt-in", func(e fsx.Env) string { return e.User(".foo") }, dot, ""},
		{"dot dir child with opt-in", func(e fsx.Env) string { return e.User(".foo", "bar") }, dot, ""},
		{"ssh with opt-in", func(e fsx.Env) string { return e.User(".ssh") }, dot, "protected_prefix"},
		{"ssh key with opt-in", func(e fsx.Env) string { return e.User(".ssh", "id_ed25519") }, dot, "protected_prefix"},
		{"gnupg with opt-in", func(e fsx.Env) string { return e.User(".gnupg") }, dot, "protected_prefix"},
		{"aws with opt-in", func(e fsx.Env) string { return e.User(".aws", "credentials") }, dot, "protected_prefix"},
		{"trash with opt-in", func(e fsx.Env) string { return e.User(".Trash", "x") }, dot, "protected_prefix"},
		{"non-dot home entry with opt-in", func(e fsx.Env) string { return e.User("foo") }, dot, "outside_allowlist"},

		// shape of the path
		{"relative path", func(e fsx.Env) string { return "Library/Caches/com.foo" }, Options{}, "not_absolute"},
		{"empty path", func(e fsx.Env) string { return "" }, Options{}, "not_absolute"},
		// built with string concatenation: filepath.Join would clean ".." away.
		{"dot dot escape", func(e fsx.Env) string { return e.Library("Caches") + "/../../Documents" }, Options{}, "relative_component"},
		{"dot dot staying inside", func(e fsx.Env) string { return e.Library("Caches") + "/a/../b" }, Options{}, "relative_component"},
		{"single dot ok", func(e fsx.Env) string { return e.Library("Caches") + "/./com.foo" }, Options{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t)
			got := rule(Validate(env, tc.path(env), tc.opts))
			if got != tc.want {
				t.Errorf("Validate(%q) rule = %q, want %q", tc.path(env), got, tc.want)
			}
		})
	}
}

func TestValidateSymlinks(t *testing.T) {
	env := newEnv(t)
	caches := env.Library("Caches")
	support := env.Library("Application Support", "foo")
	outside := filepath.Join(env.Root, "outside")
	for _, d := range []string{caches, support, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// link escapes ~/Library, inner stays within it, dangling points nowhere.
	if err := os.Symlink(outside, filepath.Join(caches, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(support, filepath.Join(caches, "inner")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(env.Root, "missing"), filepath.Join(caches, "dangling")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, want string
	}{
		{"parent symlink escapes", filepath.Join(caches, "link", "sub"), "symlink_escape"},
		{"deep under escaping parent", filepath.Join(caches, "link", "a", "b"), "symlink_escape"},
		{"parent symlink stays inside", filepath.Join(caches, "inner", "sub"), ""},
		{"dangling parent", filepath.Join(caches, "dangling", "sub"), "symlink_escape"},
		{"the link itself is fine", filepath.Join(caches, "link"), ""},
		{"nonexistent parent is fine", filepath.Join(caches, "nope", "sub"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rule(Validate(env, tc.path, Options{})); got != tc.want {
				t.Errorf("Validate(%q) rule = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestValidateAll(t *testing.T) {
	env := newEnv(t)
	ok := []string{env.Library("Caches", "a"), env.Sys("Applications", "Foo.app")}
	if err := ValidateAll(env, ok, Options{}); err != nil {
		t.Fatalf("ValidateAll(ok) = %v", err)
	}
	bad := append(ok, env.Library("Caches"), env.Sys("System"))
	if got := rule(ValidateAll(env, bad, Options{})); got != "library_top_level" {
		t.Errorf("ValidateAll(bad) rule = %q, want first violation library_top_level", got)
	}
	if err := ValidateAll(env, nil, Options{}); err != nil {
		t.Errorf("ValidateAll(nil) = %v", err)
	}
}

func TestViolationError(t *testing.T) {
	env := newEnv(t)
	err := Validate(env, "relative", Options{})
	var v *Violation
	if !errors.As(err, &v) {
		t.Fatalf("want *Violation, got %T", err)
	}
	if v.Path != "relative" || v.Rule != "not_absolute" || v.Detail == "" {
		t.Errorf("unexpected violation %+v", v)
	}
	if !strings.Contains(err.Error(), "not_absolute") || !strings.Contains(err.Error(), "relative") {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestCheckApp(t *testing.T) {
	env := newEnv(t)
	normal := env.Sys("Applications", "Foo.app")
	if err := os.MkdirAll(normal, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, bundleID, want string
	}{
		{"apple bundle id", env.Sys("Applications", "Safari.app"), "com.apple.Safari", "apple_bundle_id"},
		{"system path", env.Sys("System", "Applications", "Mail.app"), "org.example.mail", "system_path"},
		{"system path wins over empty id", env.Sys("System", "Applications", "X.app"), "", "system_path"},
		{"normal existing app", normal, "com.foo.app", ""},
		{"normal missing app", env.Sys("Applications", "Gone.app"), "com.gone.app", ""},
		{"com.apple prefix only", env.Sys("Applications", "Foo.app"), "com.applesauce.foo", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rule(CheckApp(env, tc.path, tc.bundleID)); got != tc.want {
				t.Errorf("CheckApp(%q, %q) rule = %q, want %q", tc.path, tc.bundleID, got, tc.want)
			}
		})
	}
}

func TestIsRestricted(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "plain")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{f, dir, filepath.Join(dir, "missing")} {
		got, err := isRestricted(p)
		if err != nil || got {
			t.Errorf("isRestricted(%q) = %v, %v; want false, nil", p, got, err)
		}
	}
}

func TestNameOK(t *testing.T) {
	tests := []struct {
		name, bundleID string
		want           bool
	}{
		{"Spotify", "com.spotify.client", true},
		{"Mail", "com.apple.mail", false}, // denylist
		{"R", "org.R-project.R", false},   // too short
		// Strict rule: "visualstudiocode" is not in the id and the last segment
		// "vscode" is not in the name. Users reach such leftovers with --also.
		{"Visual Studio Code", "com.microsoft.VSCode", false},
		{"Ghostty", "com.mitchellh.ghostty", true},
		{"1Password", "com.1password.1password", true},
		{"Notion", "notion.id", true},
		{"Warp", "dev.warp.Warp-Stable", true},     // last segment "warpstable" is not in name, but name is in id
		{"Data Viewer", "com.example.dv", false},   // two-rune last segment must not corroborate
		{"Hello World", "com.example.hello", true}, // last segment >= 3 runes contained in name
		{"Code", "com.microsoft.VSCode", false},    // denylist
		{"", "x", false},
		{"Foo Bar", "", false},
		{"foo bar", "com.foo-bar.app", true}, // separators ignored
		{"Alfred", "com.runningwithcrayons.Alfred", true},
		{"Obsidian", "md.obsidian", true},             // last segment in name
		{"Docker Desktop", "com.docker.docker", true}, // last segment in name
		{"Zoom", "us.zoom.xos", false},                // denylist (vendor)
		{"caches", "com.foo.caches", false},           // denylist, case-insensitive
		{"Foo Bar", "com.unrelated.thing", false},     // no corroboration
		{"日本語アプリ", "com.example.other", false},        // 6 runes but no corroboration
		{"日本語アプリ", "com.example.日本語アプリ", true},
		{"日本", "com.example.日本", false},   // 2 runes
		{"- - - -", "com.foo.bar", false}, // normalizes to empty
	}
	for _, tc := range tests {
		t.Run(tc.name+"/"+tc.bundleID, func(t *testing.T) {
			if got := NameOK(tc.name, tc.bundleID); got != tc.want {
				t.Errorf("NameOK(%q, %q) = %v, want %v", tc.name, tc.bundleID, got, tc.want)
			}
		})
	}
}

func TestDenylist(t *testing.T) {
	got := Denylist()
	if !sort.StringsAreSorted(got) {
		t.Error("Denylist() is not sorted")
	}
	seen := map[string]bool{}
	for _, n := range got {
		if seen[strings.ToLower(n)] {
			t.Errorf("duplicate denylist entry %q", n)
		}
		seen[strings.ToLower(n)] = true
	}
	for _, want := range []string{"Application Support", "Caches", "Mail", "Xcode", "Go", "Java", "R", "Google", "System"} {
		if !seen[strings.ToLower(want)] {
			t.Errorf("denylist missing %q", want)
		}
	}
	// Denylist returns a copy: mutating it must not affect the package.
	got[0] = "mutated"
	if Denylist()[0] == "mutated" {
		t.Error("Denylist() returned its backing slice")
	}
	// Each group in the source is sorted case-insensitively.
	for i := 1; i < len(denylist); i++ {
		a, b := strings.ToLower(denylist[i-1]), strings.ToLower(denylist[i])
		if a > b && !groupBoundary(denylist[i]) {
			t.Errorf("denylist group out of order: %q before %q", denylist[i-1], denylist[i])
		}
	}
}

// groupBoundary reports whether name starts a new comment group in denylist.go.
func groupBoundary(name string) bool {
	switch name {
	case "Calendar", "App", "Adobe":
		return true
	}
	return false
}
