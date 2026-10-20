package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/shiva2021/purge/internal/testutil"
)

const displayPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.example.display</string>
	<key>CFBundleName</key>
	<string>Display</string>
	<key>CFBundleDisplayName</key>
	<string>Display Name Pro</string>
	<key>CFBundleVersion</key>
	<string>42</string>
</dict>
</plist>
`

func discoverSpec() testutil.Spec {
	return testutil.Spec{
		Apps: []testutil.App{
			{Path: "/Applications/Zed.app", BundleID: "com.example.zed", Version: "2.1"},
			{Path: "/Applications/Alpha.app", BundleID: "com.example.alpha"},
			{Path: "/Applications/Google Chrome.app", BundleID: "com.google.Chrome", Version: "120.0"},
			{Path: "/Applications/Utilities/Hex Fiend.app", BundleID: "com.ridiculousfish.HexFiend"},
			{Path: "/Applications/Epson Software/Epson Scan 2.app", BundleID: "com.epson.scan2"},
			{Path: "/Applications/Safari.app", BundleID: "com.apple.Safari"},
			{Path: "/Applications/Ghostty.app", BundleID: "com.mitchellh.ghostty"},
			{Path: "~/Applications/Foo.app", BundleID: "com.example.foo"},
			{Path: "/System/Applications/Mail.app", BundleID: "com.apple.mail"},
			{Path: "/System/Applications/Utilities/Disk Utility.app", BundleID: "com.apple.DiskUtility"},
		},
		Dirs: []string{
			"/Applications/Broken.app",
			"/Applications/Not An App/Contents",
			"/opt/homebrew/Caskroom/ghostty/1.0.0/Ghostty.app",
		},
		Files: map[string]string{
			"/Applications/Display.app/Contents/Info.plist": displayPlist,
			"/Applications/Not An App/Contents/Info.plist":  displayPlist,
			"/Applications/README.txt":                      "ignored",
		},
	}
}

func TestDiscover(t *testing.T) {
	env := testutil.FakeHome(t, discoverSpec())
	apps, err := Discover(env)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	wantOrder := []string{
		"Alpha", "Broken", "Disk Utility", "Display Name Pro", "Epson Scan 2",
		"Foo", "Ghostty", "Google Chrome", "Hex Fiend", "Mail", "Safari", "Zed",
	}
	if len(apps) != len(wantOrder) {
		t.Fatalf("got %d apps, want %d: %+v", len(apps), len(wantOrder), appNames(apps))
	}
	for i, name := range wantOrder {
		if apps[i].Name != name {
			t.Fatalf("order: got %v, want %v", appNames(apps), wantOrder)
		}
	}

	byName := map[string]App{}
	for _, a := range apps {
		byName[a.Name] = a
	}

	tests := []struct {
		name      string
		path      string // spec-form path
		bundleID  string
		version   string
		protected bool
		cask      string
		sized     bool
	}{
		{"Alpha", "/Applications/Alpha.app", "com.example.alpha", "1.0", false, "", true},
		{"Zed", "/Applications/Zed.app", "com.example.zed", "2.1", false, "", true},
		{"Google Chrome", "/Applications/Google Chrome.app", "com.google.Chrome", "120.0", false, "", true},
		{"Hex Fiend", "/Applications/Utilities/Hex Fiend.app", "com.ridiculousfish.HexFiend", "1.0", false, "", true},
		{"Epson Scan 2", "/Applications/Epson Software/Epson Scan 2.app", "com.epson.scan2", "1.0", false, "", true},
		{"Safari", "/Applications/Safari.app", "com.apple.Safari", "1.0", true, "", true},
		{"Ghostty", "/Applications/Ghostty.app", "com.mitchellh.ghostty", "1.0", false, "ghostty", true},
		{"Foo", "~/Applications/Foo.app", "com.example.foo", "1.0", false, "", true},
		{"Mail", "/System/Applications/Mail.app", "com.apple.mail", "1.0", true, "", true},
		{"Disk Utility", "/System/Applications/Utilities/Disk Utility.app", "com.apple.DiskUtility", "1.0", true, "", true},
		{"Broken", "/Applications/Broken.app", "", "", false, "", false},
		{"Display Name Pro", "/Applications/Display.app", "com.example.display", "42", false, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, ok := byName[tc.name]
			if !ok {
				t.Fatalf("missing app %q", tc.name)
			}
			if want := testutil.Resolve(env, tc.path); a.Path != want {
				t.Errorf("Path = %q, want %q", a.Path, want)
			}
			if a.BundleID != tc.bundleID {
				t.Errorf("BundleID = %q, want %q", a.BundleID, tc.bundleID)
			}
			if a.Version != tc.version {
				t.Errorf("Version = %q, want %q", a.Version, tc.version)
			}
			if a.Protected != tc.protected {
				t.Errorf("Protected = %v, want %v", a.Protected, tc.protected)
			}
			if a.Cask != tc.cask {
				t.Errorf("Cask = %q, want %q", a.Cask, tc.cask)
			}
			if tc.sized && a.SizeBytes <= 0 {
				t.Errorf("SizeBytes = %d, want > 0", a.SizeBytes)
			}
			if a.TeamID != "" {
				t.Errorf("TeamID = %q, want empty", a.TeamID)
			}
		})
	}
}

func TestDiscoverCaskSymlink(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{
		Apps: []testutil.App{
			{Path: "/usr/local/Caskroom/linkapp/2.0/Other.app", BundleID: "com.example.other"},
		},
		Symlinks: map[string]string{
			"/Applications/Link.app": "../usr/local/Caskroom/linkapp/2.0/Other.app",
		},
	})
	apps, err := Discover(env)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("got %d apps, want 1: %v", len(apps), appNames(apps))
	}
	a := apps[0]
	if a.Path != testutil.Resolve(env, "/Applications/Link.app") {
		t.Errorf("Path = %q", a.Path)
	}
	if a.Name != "Other" || a.BundleID != "com.example.other" {
		t.Errorf("Name/BundleID = %q/%q", a.Name, a.BundleID)
	}
	if a.Cask != "linkapp" {
		t.Errorf("Cask = %q, want linkapp", a.Cask)
	}
}

func TestDiscoverEmpty(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	apps, err := Discover(env)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("got %v, want none", appNames(apps))
	}
}

func TestRead(t *testing.T) {
	env := testutil.FakeHome(t, discoverSpec())

	t.Run("ok", func(t *testing.T) {
		a, err := Read(env, testutil.Resolve(env, "/Applications/Ghostty.app"))
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if a.Name != "Ghostty" || a.BundleID != "com.mitchellh.ghostty" || a.Cask != "ghostty" {
			t.Errorf("got %+v", a)
		}
	})
	t.Run("no .app suffix", func(t *testing.T) {
		_, err := Read(env, testutil.Resolve(env, "/Applications/Not An App"))
		if !errors.Is(err, ErrNotBundle) {
			t.Errorf("err = %v, want ErrNotBundle", err)
		}
	})
	t.Run("broken bundle", func(t *testing.T) {
		_, err := Read(env, testutil.Resolve(env, "/Applications/Broken.app"))
		if err == nil || !strings.Contains(err.Error(), "Info.plist") {
			t.Errorf("err = %v, want Info.plist read error", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		if _, err := Read(env, testutil.Resolve(env, "/Applications/Nope.app")); err == nil {
			t.Error("err = nil, want error")
		}
	})
}

func appNames(apps []App) []string {
	out := make([]string, len(apps))
	for i, a := range apps {
		out[i] = a.Name
	}
	return out
}
