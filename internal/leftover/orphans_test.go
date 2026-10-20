package leftover

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/progress"
	"github.com/shiva2021/rootout/internal/safety"
	"github.com/shiva2021/rootout/internal/testutil"
)

const oldapp = "com.oldvendor.oldapp"

// oldappPaths are the six leftovers of the uninstalled app, with the Kind
// their rule row assigns.
var oldappPaths = map[string]Kind{
	"~/Library/Preferences/" + oldapp + ".plist":                  Preferences,
	"~/Library/Caches/" + oldapp:                                  Cache,
	"~/Library/Application Support/" + oldapp:                     AppSupport,
	"~/Library/Containers/ABCDE12345." + oldapp:                   Container,
	"~/Library/Group Containers/group." + oldapp:                  GroupContainer,
	"~/Library/Saved Application State/" + oldapp + ".savedState": SavedState,
}

func infoPlist(id string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>` + id + `</string>
<key>CFBundleName</key><string>Thing</string>
</dict></plist>
`
}

// age sets the mtime of the file at spec and of every directory between it
// and the item root, so a directory item reads as old as its contents.
func age(t *testing.T, env fsx.Env, root, spec string, at time.Time) {
	t.Helper()
	stop := testutil.Resolve(env, root)
	for p := testutil.Resolve(env, spec); ; p = filepath.Dir(p) {
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatalf("age %s: %v", p, err)
		}
		if p == stop || p == filepath.Dir(p) {
			return
		}
	}
}

// orphanHome builds the fixture: Spotify installed, one old app's leftovers
// aged 400 days, and the ids each guard should claim.
func orphanHome(t *testing.T) (fsx.Env, app.App) {
	t.Helper()
	env := testutil.FakeHome(t, testutil.Spec{
		Apps: []testutil.App{{Path: "/Applications/Spotify.app", BundleID: spotifyID, Name: "Spotify"}},
		Dirs: []string{
			"~/Library/Caches/" + spotifyID + ".helper",
			"~/Library/Application Support/com.spotify",
			"~/Library/Application Support/OldApp",
			"~/Library/Caches/Spotify",
			"~/Library/Caches/com.downloaded.app",
			"~/Library/Caches/com.proc.tool",
		},
		Files: map[string]string{
			"~/Library/Preferences/" + oldapp + ".plist":                    "prefs",
			"~/Library/Caches/" + oldapp + "/x":                             strings.Repeat("x", 10),
			"~/Library/Application Support/" + oldapp + "/y":                "yy",
			"~/Library/Containers/ABCDE12345." + oldapp + "/Data/z":         "z",
			"~/Library/Group Containers/group." + oldapp + "/w":             "w",
			"~/Library/Saved Application State/" + oldapp + ".savedState/v": "v",
			"~/Library/Preferences/com.apple.finder.plist":                  "prefs",
			"~/Library/Preferences/com.running.agent.plist":                 "prefs",
			"~/Library/Caches/com.recent.app/f":                             "f",
			"~/Downloads/Thing.app/Contents/Info.plist":                     infoPlist("com.downloaded.app"),
		},
	})
	old := time.Now().Add(-400 * 24 * time.Hour)
	age(t, env, "~/Library/Preferences/"+oldapp+".plist", "~/Library/Preferences/"+oldapp+".plist", old)
	age(t, env, "~/Library/Caches/"+oldapp, "~/Library/Caches/"+oldapp+"/x", old)
	age(t, env, "~/Library/Application Support/"+oldapp, "~/Library/Application Support/"+oldapp+"/y", old)
	age(t, env, "~/Library/Containers/ABCDE12345."+oldapp, "~/Library/Containers/ABCDE12345."+oldapp+"/Data/z", old)
	age(t, env, "~/Library/Group Containers/group."+oldapp, "~/Library/Group Containers/group."+oldapp+"/w", old)
	age(t, env, "~/Library/Saved Application State/"+oldapp+".savedState", "~/Library/Saved Application State/"+oldapp+".savedState/v", old)
	a, err := app.Read(env, env.Sys("Applications", "Spotify.app"))
	if err != nil {
		t.Fatal(err)
	}
	return env, a
}

// orphanRunner answers each guard with canned output and records the calls.
func orphanRunner(env fsx.Env, calls *[]string) Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, strings.Join(append([]string{name}, args...), " "))
		switch name {
		case "mdfind":
			return []byte(testutil.Resolve(env, "~/Downloads/Thing.app") + "\n" +
				testutil.Resolve(env, "~/Downloads/Missing.app") + "\n"), nil
		case "launchctl":
			return []byte("PID\tStatus\tLabel\n123\t0\tcom.running.agent\n-\t0\tcom.apple.Finder\n"), nil
		case "ps":
			return []byte("/sbin/launchd\n/opt/x/tool\n"), nil
		case "pkgutil":
			return []byte("com.apple.pkg.Core\n" + oldapp + ".pkg\ncom.gone.pkg\n\n"), nil
		}
		return nil, errors.New("unexpected command " + name)
	}
}

func findOrphans(t *testing.T, env fsx.Env, opts OrphanOptions) []Orphan {
	t.Helper()
	got, err := FindOrphans(context.Background(), env, opts)
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}
	checkOrphanInvariants(t, env, got)
	return got
}

// checkOrphanInvariants asserts the contract every FindOrphans result honours.
func checkOrphanInvariants(t *testing.T, env fsx.Env, got []Orphan) {
	t.Helper()
	if !sort.SliceIsSorted(got, func(i, j int) bool {
		if got[i].SizeBytes != got[j].SizeBytes {
			return got[i].SizeBytes > got[j].SizeBytes
		}
		return strings.ToLower(got[i].Owner) < strings.ToLower(got[j].Owner)
	}) {
		t.Errorf("orphans not sorted by size desc then owner")
	}
	for _, o := range got {
		if len(o.Items) == 0 || o.Reasons[0] != reasonNotInstalled {
			t.Errorf("%s: items=%d reasons=%v", o.Owner, len(o.Items), o.Reasons)
		}
		for _, l := range o.Items {
			if l.MatchedBy != ByOrphan {
				t.Errorf("%s: MatchedBy = %q", l.Path, l.MatchedBy)
			}
			if l.Kind == Receipt {
				continue
			}
			if err := safety.Validate(env, l.Path, safety.Options{}); err != nil {
				t.Errorf("returned path fails safety.Validate: %v", err)
			}
		}
	}
}

func byOwner(got []Orphan, owner string) (Orphan, bool) {
	for _, o := range got {
		if strings.EqualFold(o.Owner, owner) {
			return o, true
		}
	}
	return Orphan{}, false
}

func TestFindOrphans(t *testing.T) {
	env, spotify := orphanHome(t)
	var calls []string
	got := findOrphans(t, env, OrphanOptions{Installed: []app.App{spotify}, Run: orphanRunner(env, &calls)})

	wantCalls := []string{
		"mdfind kMDItemContentType == 'com.apple.application-bundle'",
		"launchctl list",
		"ps -axo comm=",
		"pkgutil --pkgs",
	}
	if strings.Join(calls, "|") != strings.Join(wantCalls, "|") {
		t.Errorf("calls = %q", calls)
	}

	t.Run("old app grouped", func(t *testing.T) {
		o, ok := byOwner(got, oldapp)
		if !ok {
			t.Fatalf("missing %s in %+v", oldapp, got)
		}
		if o.Owner != oldapp || len(o.Items) != 7 {
			t.Fatalf("owner=%q items=%d: %+v", o.Owner, len(o.Items), o.Items)
		}
		for spec, kind := range oldappPaths {
			l, ok := lookup(o.Items, testutil.Resolve(env, spec))
			if !ok {
				t.Errorf("missing %s", spec)
				continue
			}
			if l.Kind != kind {
				t.Errorf("%s: kind=%s want %s", spec, l.Kind, kind)
			}
		}
		if o.SizeBytes != 20 {
			t.Errorf("SizeBytes = %d, want 20", o.SizeBytes)
		}
		if o.Recent || time.Since(o.LastUsed) < 399*24*time.Hour {
			t.Errorf("recent=%v lastUsed=%v", o.Recent, o.LastUsed)
		}
		want := []string{reasonNotInstalled, reasonNoBundle, reasonNotLoaded, reasonNoProcess}
		if strings.Join(o.Reasons, "|") != strings.Join(want, "|") {
			t.Errorf("reasons = %v", o.Reasons)
		}
		r := o.Items[len(o.Items)-1]
		if r.Path != "pkg:"+oldapp+".pkg" || r.Kind != Receipt || r.Note != receiptNote || r.SizeBytes != 0 {
			t.Errorf("receipt item = %+v", r)
		}
	})
	t.Run("receipt without files", func(t *testing.T) {
		o, ok := byOwner(got, "com.gone.pkg")
		if !ok || len(o.Items) != 1 || o.Items[0].Path != "pkg:com.gone.pkg" || o.Items[0].Kind != Receipt {
			t.Errorf("com.gone.pkg = %+v (found %v)", o, ok)
		}
		if o.Recent || o.SizeBytes != 0 {
			t.Errorf("recent=%v size=%d", o.Recent, o.SizeBytes)
		}
	})
	t.Run("recent", func(t *testing.T) {
		o, ok := byOwner(got, "com.recent.app")
		if !ok || !o.Recent || len(o.Items) != 1 || o.Items[0].Kind != Cache {
			t.Errorf("com.recent.app = %+v (found %v)", o, ok)
		}
	})
	t.Run("owned and non-id entries are absent", func(t *testing.T) {
		for _, owner := range []string{
			"com.apple.finder", spotifyID + ".helper", "com.spotify", "OldApp", "Spotify",
			"com.running.agent", "com.downloaded.app", "com.proc.tool", "com.apple.pkg.Core",
		} {
			if o, ok := byOwner(got, owner); ok {
				t.Errorf("must not return %s: %+v", owner, o)
			}
		}
		if len(got) != 3 {
			t.Errorf("got %d orphans, want 3: %+v", len(got), got)
		}
	})
	t.Run("custom window and clock", func(t *testing.T) {
		far := func() time.Time { return time.Now().Add(10 * 24 * time.Hour) }
		got := findOrphans(t, env, OrphanOptions{Installed: []app.App{spotify}, RecentWindow: 24 * time.Hour, Now: far})
		if o, _ := byOwner(got, "com.recent.app"); o.Recent {
			t.Errorf("recent with a 1-day window seen from 10 days ahead: %+v", o)
		}
	})
}

func TestFindOrphansWithoutRunner(t *testing.T) {
	env, spotify := orphanHome(t)
	failing := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		return nil, errors.New(name + ": not found")
	}
	for name, run := range map[string]Runner{"nil runner": nil, "failing runner": failing} {
		t.Run(name, func(t *testing.T) {
			got := findOrphans(t, env, OrphanOptions{Installed: []app.App{spotify}, Run: run})
			for _, owner := range []string{oldapp, "com.recent.app", "com.running.agent", "com.downloaded.app", "com.proc.tool"} {
				o, ok := byOwner(got, owner)
				if !ok {
					t.Errorf("missing %s", owner)
					continue
				}
				if len(o.Reasons) != 1 {
					t.Errorf("%s: reasons = %v", owner, o.Reasons)
				}
				for _, l := range o.Items {
					if l.Kind == Receipt {
						t.Errorf("unexpected receipt %+v", l)
					}
				}
			}
			if o, _ := byOwner(got, oldapp); len(o.Items) != 6 {
				t.Errorf("%s has %d items, want 6", oldapp, len(o.Items))
			}
			if len(got) != 5 {
				t.Errorf("got %d orphans, want 5", len(got))
			}
		})
	}
}

func TestFindOrphansProgress(t *testing.T) {
	env, spotify := orphanHome(t)
	var calls []string
	rec := &progress.Recorder{}
	findOrphans(t, env, OrphanOptions{Installed: []app.App{spotify}, Run: orphanRunner(env, &calls), Progress: rec})
	rows := len(orphanRows(env))
	if rec.Label != "Looking for orphaned leftovers" || rec.Total != rows+orphanGuards || !rec.Finished {
		t.Errorf("recorder = %+v, want total %d", rec, rows+orphanGuards)
	}
	if len(rec.Steps) != rec.Total {
		t.Errorf("steps = %v (%d), want %d", rec.Steps, len(rec.Steps), rec.Total)
	}
	for _, s := range rec.Steps {
		if s == "" || strings.HasPrefix(s, "/") {
			t.Errorf("step detail %q should be a short name", s)
		}
	}
}

func TestFindOrphansErrors(t *testing.T) {
	env, spotify := orphanHome(t)
	t.Run("installed required", func(t *testing.T) {
		if _, err := FindOrphans(context.Background(), env, OrphanOptions{}); err == nil {
			t.Error("want error without Installed")
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var calls []string
		_, err := FindOrphans(ctx, env, OrphanOptions{Installed: []app.App{spotify}, Run: orphanRunner(env, &calls)})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}

func TestOrphanRows(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	for _, r := range orphanRows(env) {
		dir := r.dir(env)
		if !under(env.Library(), dir) || r.when != nil {
			t.Errorf("row %s must be an ungated user-Library row", dir)
		}
	}
}

func TestParseOwner(t *testing.T) {
	cases := map[string]string{
		"com.foo.bar.plist":                                      "com.foo.bar",
		"ABCDE12345.com.foo.bar":                                 "com.foo.bar",
		"group.com.foo.bar":                                      "com.foo.bar",
		"ABCDE12345.group.com.foo.bar":                           "com.foo.bar",
		"com.foo.bar.savedState":                                 "com.foo.bar",
		"com.foo.bar.binarycookies":                              "com.foo.bar",
		"com.foo.bar.0123ABCD-1234-5678-9ABC-DEF012345678.plist": "com.foo.bar",
		"com.foo.bar.helper":                                     "com.foo.bar.helper",
		"io.github.user.tool":                                    "io.github.user.tool",
		"Com.Foo.Bar":                                            "Com.Foo.Bar",
		"Spotify":                                                "",
		"com.foo":                                                "",
		"My App.plist":                                           "",
		"com.foo bar.baz":                                        "",
		".DS_Store":                                              "",
		"":                                                       "",
	}
	for in, want := range cases {
		if got := ParseOwner(in); got != want {
			t.Errorf("ParseOwner(%q) = %q, want %q", in, got, want)
		}
	}
}
