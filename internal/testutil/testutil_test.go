package testutil

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shiva2021/purge/internal/fsx"
)

func TestFakeHome(t *testing.T) {
	env := FakeHome(t, Spec{
		Dirs:  []string{"~/Library/Caches/com.foo.app", "/Library/LaunchDaemons"},
		Files: map[string]string{"~/Library/Preferences/com.foo.app.plist": "prefs", "/etc/hosts": "127.0.0.1"},
		Symlinks: map[string]string{
			"~/link-rel":  "Library/Caches",
			"/opt/absent": "/nowhere",
		},
		Apps: []App{
			{Path: "/Applications/Foo.app", BundleID: "com.foo.app"},
			{Path: "~/Applications/Bar & Co.app", BundleID: "com.bar", Name: "Bar", Version: "2.3", Exec: "bar-bin"},
		},
	})

	if env.Home != filepath.Join(env.Root, "home") {
		t.Fatalf("Home = %q, want <root>/home (root %q)", env.Home, env.Root)
	}

	tests := []struct {
		name string
		path string
		dir  bool
	}{
		{"user dir", "~/Library/Caches/com.foo.app", true},
		{"sys dir", "/Library/LaunchDaemons", true},
		{"user file", "~/Library/Preferences/com.foo.app.plist", false},
		{"sys file", "/etc/hosts", false},
		{"sys app plist", "/Applications/Foo.app/Contents/Info.plist", false},
		{"sys app exec", "/Applications/Foo.app/Contents/MacOS/Foo", false},
		{"sys app resources", "/Applications/Foo.app/Contents/Resources", true},
		{"user app exec", "~/Applications/Bar & Co.app/Contents/MacOS/bar-bin", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !Exists(env, tc.path) {
				t.Fatalf("%s does not exist", tc.path)
			}
			if got := IsDir(env, tc.path); got != tc.dir {
				t.Fatalf("IsDir(%s) = %v, want %v", tc.path, got, tc.dir)
			}
		})
	}

	t.Run("file content", func(t *testing.T) {
		if got := Read(t, env, "~/Library/Preferences/com.foo.app.plist"); got != "prefs" {
			t.Fatalf("content = %q, want %q", got, "prefs")
		}
	})

	t.Run("symlinks", func(t *testing.T) {
		for link, want := range map[string]string{"~/link-rel": "Library/Caches", "/opt/absent": "/nowhere"} {
			got, err := os.Readlink(Resolve(env, link))
			if err != nil {
				t.Fatalf("readlink %s: %v", link, err)
			}
			if got != want {
				t.Fatalf("%s -> %q, want %q", link, got, want)
			}
		}
		if !Exists(env, "/opt/absent") {
			t.Fatal("dangling symlink should still Exist")
		}
	})

	t.Run("executable mode", func(t *testing.T) {
		fi, err := os.Stat(Resolve(env, "/Applications/Foo.app/Contents/MacOS/Foo"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("executable mode = %v, want +x", fi.Mode())
		}
	})
}

// plistDoc pairs <key> and <string> children of the top-level <dict> by position.
type plistDoc struct {
	Keys    []string `xml:"dict>key"`
	Strings []string `xml:"dict>string"`
}

func (d plistDoc) get(key string) string {
	for i, k := range d.Keys {
		if k == key && i < len(d.Strings) {
			return d.Strings[i]
		}
	}
	return ""
}

func TestInfoPlist(t *testing.T) {
	env := FakeHome(t, Spec{Apps: []App{
		{Path: "/Applications/Foo.app", BundleID: "com.foo.app"},
		{Path: "~/Applications/Bar & Co.app", BundleID: "com.bar", Name: "Bar", Version: "2.3", Exec: "bar-bin"},
	}})

	tests := []struct {
		name                 string
		path                 string
		id, bundleName, exec string
		version              string
	}{
		{"defaults", "/Applications/Foo.app", "com.foo.app", "Foo", "Foo", "1.0"},
		{"explicit", "~/Applications/Bar & Co.app", "com.bar", "Bar", "bar-bin", "2.3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := Read(t, env, tc.path+"/Contents/Info.plist")
			if !strings.HasPrefix(raw, `<?xml version="1.0" encoding="UTF-8"?>`) || !strings.Contains(raw, "<!DOCTYPE plist PUBLIC") {
				t.Fatalf("missing Apple plist header/DOCTYPE:\n%s", raw)
			}
			if !strings.Contains(raw, "<string>"+tc.id+"</string>") {
				t.Fatalf("bundle id %q not in plist:\n%s", tc.id, raw)
			}
			var doc plistDoc
			if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
				t.Fatalf("xml parse: %v", err)
			}
			for key, want := range map[string]string{
				"CFBundleIdentifier":         tc.id,
				"CFBundleName":               tc.bundleName,
				"CFBundleExecutable":         tc.exec,
				"CFBundleShortVersionString": tc.version,
				"CFBundlePackageType":        "APPL",
			} {
				if got := doc.get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
		})
	}

	t.Run("escapes xml", func(t *testing.T) {
		got, err := infoPlist("com.x", "A & <B>", "1", "a")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "A &amp; &lt;B&gt;") {
			t.Fatalf("not escaped:\n%s", got)
		}
	})
}

func TestResolve(t *testing.T) {
	env := fsx.Env{Root: "/tmp/r", Home: "/tmp/r/home"}
	tests := []struct{ in, want string }{
		{"~/Library/Caches", "/tmp/r/home/Library/Caches"},
		{"~", "/tmp/r/home"},
		{"/Applications/Foo.app", "/tmp/r/Applications/Foo.app"},
		{"/", "/tmp/r"},
		{"relative/thing", "relative/thing"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := Resolve(env, tc.in); got != tc.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSandboxed(t *testing.T) {
	tmp := t.TempDir()
	realHome := "/Users/someone"
	tests := []struct {
		name string
		env  fsx.Env
		ok   bool
	}{
		{"temp dir", fsx.Env{Root: tmp, Home: filepath.Join(tmp, "home")}, true},
		{"root is /", fsx.Env{Root: "/", Home: filepath.Join(tmp, "home")}, false},
		{"home is real home", fsx.Env{Root: tmp, Home: realHome}, false},
		{"root contains real home", fsx.Env{Root: "/Users", Home: filepath.Join(tmp, "home")}, false},
		{"root under real home", fsx.Env{Root: filepath.Join(realHome, "x"), Home: filepath.Join(tmp, "home")}, false},
		{"empty", fsx.Env{}, false},
		{"relative", fsx.Env{Root: "rel", Home: "rel/home"}, false},
		{"dotdot escape", fsx.Env{Root: tmp, Home: filepath.Join(tmp, strings.Repeat("../", 32), "etc")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := sandboxed(tc.env, realHome)
			if (err == nil) != tc.ok {
				t.Fatalf("sandboxed(%+v) err = %v, want ok=%v", tc.env, err, tc.ok)
			}
		})
	}

	t.Run("refuses the actual real home", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip(err)
		}
		if err := sandboxed(fsx.Env{Root: "/", Home: home}, home); err == nil {
			t.Fatal("expected error for the real home")
		}
	})
}
