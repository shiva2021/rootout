package leftover

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/safety"
	"github.com/shiva2021/rootout/internal/testutil"
)

const (
	spotifyID   = "com.spotify.client"
	spotifyTeam = "2FNC3A47ZF"
)

// launchdPlist renders a minimal launchd job plist.
func launchdPlist(label string, args ...string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + label + `</string>
<key>ProgramArguments</key><array>`)
	for _, a := range args {
		sb.WriteString("<string>" + a + "</string>")
	}
	sb.WriteString("</array></dict></plist>\n")
	return sb.String()
}

// spotifyHome builds the fixture shared by the rule-table tests.
func spotifyHome(t *testing.T) (fsx.Env, app.App) {
	t.Helper()
	env := testutil.FakeHome(t, testutil.Spec{
		Apps: []testutil.App{{Path: "/Applications/Spotify.app", BundleID: spotifyID, Name: "Spotify"}},
		Dirs: []string{
			"~/Library/Caches/" + spotifyID,
			"~/Library/Caches/" + spotifyID + ".helper",
			"~/Library/Caches/Spotify",
			"~/Library/Caches/spotify-cache",
			"~/Library/Caches/com.spotify.clientx",
			"~/Library/Logs/Spotify",
			"~/Library/Application Support/Spotify",
			"~/Library/Application Support/SpotifyExtras",
			"~/Library/Containers/" + spotifyID,
			"~/Library/Containers/" + spotifyTeam + "." + spotifyID,
			"~/Library/Containers/com.other.app",
			"~/Library/Group Containers/" + spotifyTeam + ".group." + spotifyID,
			"~/Library/Group Containers/group." + spotifyID,
			"~/Library/Group Containers/Spotify",
			"~/Library/Saved Application State/" + spotifyID + ".savedState",
			"~/Library/Saved Application State/" + spotifyID,
			"~/Library/HTTPStorages/" + spotifyID,
			"~/Library/WebKit/" + spotifyID,
			"~/Library/WebKit/com.other.app",
			"~/Library/Application Scripts/" + spotifyID,
			"~/Library/Cookies/" + spotifyID,
			"/Library/Caches/" + spotifyID,
			"/Library/Caches/Spotify",
			"/Library/Application Support/Spotify",
			"/Library/Logs/Spotify",
		},
		Files: map[string]string{
			"~/Library/Preferences/" + spotifyID + ".plist":                  "prefs",
			"~/Library/Preferences/" + spotifyID + ".helper.plist":           "prefs",
			"~/Library/Preferences/com.spotify.clientx.plist":                "prefs",
			"~/Library/Preferences/com.other.plist":                          "prefs",
			"~/Library/Preferences/ByHost/" + spotifyID + ".ABCD-1234.plist": "prefs",
			"~/Library/Preferences/ByHost/com.other.ABCD-1234.plist":         "prefs",
			"/Library/Preferences/" + spotifyID + ".plist":                   "prefs",
			"~/Library/HTTPStorages/" + spotifyID + ".binarycookies":         "cookies",
			"~/Library/Cookies/" + spotifyID + ".binarycookies":              "cookies",
			"~/Library/LaunchAgents/" + spotifyID + ".startup.plist":         "not a plist at all",
			"~/Library/LaunchAgents/com.other.agent.plist":                   launchdPlist("com.other.agent", "/usr/bin/true"),
			"/Library/LaunchDaemons/" + spotifyID + ".helperd.plist":         launchdPlist("SpotifyHelperDaemon", "/Library/PrivilegedHelperTools/SpotifyHelperDaemon"),
			"/Library/LaunchDaemons/com.other.daemon.plist":                  launchdPlist("OtherDaemon", "/Library/PrivilegedHelperTools/OtherDaemon"),
			"/Library/PrivilegedHelperTools/SpotifyHelperDaemon":             "bin",
			"/Library/PrivilegedHelperTools/OtherDaemon":                     "bin",
			"/Library/PrivilegedHelperTools/" + spotifyID + ".helper":        "bin",
			"~/Library/Logs/DiagnosticReports/Spotify-2026-10-01.ips":        "crash",
			"~/Library/Logs/DiagnosticReports/Spotify_2026-10-02.crash":      "crash",
			"~/Library/Logs/DiagnosticReports/Spotify-2026-10-03.txt":        "not a report",
			"~/Library/Logs/DiagnosticReports/SpotifyHelper-2026-10-04.ips":  "other app",
			"~/Library/Caches/" + spotifyID + "/data.db":                     strings.Repeat("x", 100),
			"~/Library/Caches/" + spotifyID + "/sub/more.db":                 strings.Repeat("y", 50),
		},
	})
	a := app.App{Name: "Spotify", BundleID: spotifyID, Path: env.Sys("Applications", "Spotify.app"), TeamID: spotifyTeam}
	// The program-path agent needs the absolute bundle path, known only now.
	agent := testutil.Resolve(env, "~/Library/LaunchAgents/com.example.autostart.plist")
	prog := filepath.Join(a.Path, "Contents", "MacOS", "Spotify")
	if err := os.WriteFile(agent, []byte(launchdPlist("com.example.autostart", prog, "--autostart")), 0o644); err != nil {
		t.Fatal(err)
	}
	return env, a
}

func find(t *testing.T, env fsx.Env, a app.App, opts Options) []Leftover {
	t.Helper()
	got, err := Find(context.Background(), env, a, opts)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	checkInvariants(t, env, got, opts)
	return got
}

// checkInvariants asserts the contract every Find result must honour.
func checkInvariants(t *testing.T, env fsx.Env, got []Leftover, opts Options) {
	t.Helper()
	if len(got) == 0 || got[0].Kind != Bundle || got[0].MatchedBy != ByBundleID {
		t.Fatalf("first item must be the Bundle matched by bundle_id, got %+v", got)
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool {
		if got[i].Kind != got[j].Kind {
			return got[i].Kind < got[j].Kind
		}
		return got[i].Path < got[j].Path
	}) {
		t.Errorf("results not sorted by Kind then Path")
	}
	for _, l := range got {
		if l.Kind == Receipt || l.Kind == LoginItem {
			continue
		}
		if err := safety.Validate(env, l.Path, safety.Options{AllowDotfiles: opts.Dotfiles}); err != nil {
			t.Errorf("returned path fails safety.Validate: %v", err)
		}
	}
}

func lookup(got []Leftover, path string) (Leftover, bool) {
	for _, l := range got {
		if l.Path == path {
			return l, true
		}
	}
	return Leftover{}, false
}

func want(t *testing.T, env fsx.Env, got []Leftover, spec string, kind Kind, by string) Leftover {
	t.Helper()
	path := testutil.Resolve(env, spec)
	l, ok := lookup(got, path)
	if !ok {
		t.Fatalf("missing %s", spec)
	}
	if l.Kind != kind || l.MatchedBy != by {
		t.Errorf("%s: got kind=%s by=%s, want kind=%s by=%s", spec, l.Kind, l.MatchedBy, kind, by)
	}
	return l
}

func reject(t *testing.T, env fsx.Env, got []Leftover, spec string) {
	t.Helper()
	if l, ok := lookup(got, testutil.Resolve(env, spec)); ok {
		t.Errorf("must not return %s (kind=%s by=%s)", spec, l.Kind, l.MatchedBy)
	}
}

func TestFindSpotify(t *testing.T) {
	env, a := spotifyHome(t)
	got := find(t, env, a, Options{CrashReports: true})

	cases := map[string]func(t *testing.T){
		"bundle": func(t *testing.T) {
			if got[0].Path != a.Path || got[0].SizeBytes == 0 {
				t.Errorf("bundle item %+v", got[0])
			}
		},
		"preferences": func(t *testing.T) {
			want(t, env, got, "~/Library/Preferences/"+spotifyID+".plist", Preferences, ByBundleID)
			want(t, env, got, "~/Library/Preferences/"+spotifyID+".helper.plist", Preferences, ByBundlePrefix)
			reject(t, env, got, "~/Library/Preferences/com.spotify.clientx.plist")
			reject(t, env, got, "~/Library/Preferences/com.other.plist")
		},
		"preferences ByHost": func(t *testing.T) {
			want(t, env, got, "~/Library/Preferences/ByHost/"+spotifyID+".ABCD-1234.plist", Preferences, ByBundlePrefix)
			reject(t, env, got, "~/Library/Preferences/ByHost/com.other.ABCD-1234.plist")
		},
		"preferences /Library": func(t *testing.T) {
			want(t, env, got, "/Library/Preferences/"+spotifyID+".plist", Preferences, ByBundleID)
		},
		"caches": func(t *testing.T) {
			l := want(t, env, got, "~/Library/Caches/"+spotifyID, Cache, ByBundleID)
			if l.SizeBytes != 150 {
				t.Errorf("directory size = %d, want 150", l.SizeBytes)
			}
			want(t, env, got, "~/Library/Caches/"+spotifyID+".helper", Cache, ByBundlePrefix)
			want(t, env, got, "~/Library/Caches/Spotify", Cache, ByName)
			reject(t, env, got, "~/Library/Caches/spotify-cache")
			reject(t, env, got, "~/Library/Caches/com.spotify.clientx")
		},
		"caches /Library is id only": func(t *testing.T) {
			want(t, env, got, "/Library/Caches/"+spotifyID, Cache, ByBundleID)
			reject(t, env, got, "/Library/Caches/Spotify")
		},
		"logs": func(t *testing.T) {
			want(t, env, got, "~/Library/Logs/Spotify", Log, ByName)
			want(t, env, got, "/Library/Logs/Spotify", Log, ByName)
			reject(t, env, got, "~/Library/Logs/DiagnosticReports")
		},
		"application support": func(t *testing.T) {
			want(t, env, got, "~/Library/Application Support/Spotify", AppSupport, ByName)
			want(t, env, got, "/Library/Application Support/Spotify", AppSupport, ByName)
			reject(t, env, got, "~/Library/Application Support/SpotifyExtras")
		},
		"containers": func(t *testing.T) {
			want(t, env, got, "~/Library/Containers/"+spotifyID, Container, ByBundleID)
			want(t, env, got, "~/Library/Containers/"+spotifyTeam+"."+spotifyID, Container, ByTeamBundle)
			reject(t, env, got, "~/Library/Containers/com.other.app")
		},
		"group containers": func(t *testing.T) {
			want(t, env, got, "~/Library/Group Containers/"+spotifyTeam+".group."+spotifyID, GroupContainer, ByTeamBundle)
			want(t, env, got, "~/Library/Group Containers/group."+spotifyID, GroupContainer, ByTeamBundle)
			reject(t, env, got, "~/Library/Group Containers/Spotify") // never by name
		},
		"saved state": func(t *testing.T) {
			want(t, env, got, "~/Library/Saved Application State/"+spotifyID+".savedState", SavedState, ByBundleID)
			reject(t, env, got, "~/Library/Saved Application State/"+spotifyID)
		},
		"http storages": func(t *testing.T) {
			want(t, env, got, "~/Library/HTTPStorages/"+spotifyID, HTTPStorage, ByBundleID)
			want(t, env, got, "~/Library/HTTPStorages/"+spotifyID+".binarycookies", HTTPStorage, ByBundleID)
		},
		"cookies": func(t *testing.T) {
			want(t, env, got, "~/Library/Cookies/"+spotifyID+".binarycookies", Cookies, ByBundleID)
			reject(t, env, got, "~/Library/Cookies/"+spotifyID)
		},
		"webkit and application scripts": func(t *testing.T) {
			want(t, env, got, "~/Library/WebKit/"+spotifyID, WebKit, ByBundleID)
			reject(t, env, got, "~/Library/WebKit/com.other.app")
			want(t, env, got, "~/Library/Application Scripts/"+spotifyID, AppScripts, ByBundleID)
		},
		"launch agents": func(t *testing.T) {
			want(t, env, got, "~/Library/LaunchAgents/"+spotifyID+".startup.plist", LaunchAgent, ByBundlePrefix)
			want(t, env, got, "~/Library/LaunchAgents/com.example.autostart.plist", LaunchAgent, ByProgramPath)
			reject(t, env, got, "~/Library/LaunchAgents/com.other.agent.plist")
		},
		"launch daemons and privileged helpers": func(t *testing.T) {
			want(t, env, got, "/Library/LaunchDaemons/"+spotifyID+".helperd.plist", LaunchDaemon, ByBundlePrefix)
			reject(t, env, got, "/Library/LaunchDaemons/com.other.daemon.plist")
			want(t, env, got, "/Library/PrivilegedHelperTools/"+spotifyID+".helper", PrivilegedHelper, ByBundlePrefix)
			want(t, env, got, "/Library/PrivilegedHelperTools/SpotifyHelperDaemon", PrivilegedHelper, ByBundlePrefix)
			reject(t, env, got, "/Library/PrivilegedHelperTools/OtherDaemon")
		},
		"crash reports": func(t *testing.T) {
			l := want(t, env, got, "~/Library/Logs/DiagnosticReports/Spotify-2026-10-01.ips", CrashReport, ByName)
			if l.SizeBytes != int64(len("crash")) {
				t.Errorf("file size = %d", l.SizeBytes)
			}
			want(t, env, got, "~/Library/Logs/DiagnosticReports/Spotify_2026-10-02.crash", CrashReport, ByName)
			reject(t, env, got, "~/Library/Logs/DiagnosticReports/Spotify-2026-10-03.txt")
			reject(t, env, got, "~/Library/Logs/DiagnosticReports/SpotifyHelper-2026-10-04.ips")
		},
		"no root needed in the sandbox": func(t *testing.T) {
			for _, l := range got {
				if l.NeedsRoot {
					t.Errorf("%s: NeedsRoot set although everything is user-owned", l.Path)
				}
			}
		},
		"no receipts or login items without a Runner": func(t *testing.T) {
			for _, l := range got {
				if l.Kind == Receipt || l.Kind == LoginItem {
					t.Errorf("unexpected %+v", l)
				}
			}
		},
	}
	for name, fn := range cases {
		t.Run(name, fn)
	}
}

func TestFindCrashReportsOff(t *testing.T) {
	env, a := spotifyHome(t)
	got := find(t, env, a, Options{CrashReports: false})
	reject(t, env, got, "~/Library/Logs/DiagnosticReports/Spotify-2026-10-01.ips")
	for _, l := range got {
		if l.Kind == CrashReport {
			t.Errorf("unexpected crash report %s", l.Path)
		}
	}
}

func TestFindNoTeamID(t *testing.T) {
	env, a := spotifyHome(t)
	a.TeamID = ""
	got := find(t, env, a, Options{})
	reject(t, env, got, "~/Library/Containers/"+spotifyTeam+"."+spotifyID)
	reject(t, env, got, "~/Library/Group Containers/"+spotifyTeam+".group."+spotifyID)
	want(t, env, got, "~/Library/Group Containers/group."+spotifyID, GroupContainer, ByTeamBundle)
}

// TestFindNameGuard covers apps whose name must never drive a match.
func TestFindNameGuard(t *testing.T) {
	cases := []struct {
		name, id string
		dirs     []string
	}{
		// "Mail" is on the denylist and com.apple.* never corroborates a name.
		{"Mail", "com.apple.mail", []string{"~/Library/Mail", "~/Library/Application Support/Mail", "~/Library/Caches/Mail"}},
		// "R" is too short and on the denylist.
		{"R", "org.R-project.R", []string{"~/Library/R", "~/Library/Application Support/R", "~/Library/Caches/R"}},
		// "Code" is on the denylist (generic word); users pass --also for
		// ~/Library/Application Support/Code when they really mean it.
		{"Code", "com.microsoft.VSCode", []string{"~/Library/Application Support/Code", "~/Library/Caches/Code", "~/Library/Logs/Code"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := testutil.FakeHome(t, testutil.Spec{
				Apps: []testutil.App{{Path: "/Applications/" + c.name + ".app", BundleID: c.id, Name: c.name}},
				Dirs: c.dirs,
			})
			a := app.App{Name: c.name, BundleID: c.id, Path: env.Sys("Applications", c.name+".app")}
			got := find(t, env, a, Options{CrashReports: true, Dotfiles: true})
			for _, d := range c.dirs {
				reject(t, env, got, d)
			}
			for _, l := range got {
				if l.MatchedBy == ByName {
					t.Errorf("name match leaked: %+v", l)
				}
			}
		})
	}
}

func TestFindRunner(t *testing.T) {
	env, a := spotifyHome(t)
	var calls [][]string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		switch name {
		case "pkgutil":
			return []byte("com.apple.pkg.Core\ncom.spotify.client.pkg\n" + spotifyID + "\ncom.spotifyx.pkg\n\n"), nil
		case "sfltool":
			return []byte("items:\n  uuid: 1 name: Spotify bundle id: " + spotifyID + "\n"), nil
		}
		return nil, errors.New("unexpected command " + name)
	}
	got := find(t, env, a, Options{Run: run})

	if len(calls) != 2 || strings.Join(calls[0], " ") != "pkgutil --pkgs" || strings.Join(calls[1], " ") != "sfltool dumpbtm" {
		t.Errorf("calls = %v", calls)
	}
	var receipts []string
	var login []Leftover
	for _, l := range got {
		switch l.Kind {
		case Receipt:
			receipts = append(receipts, l.Path)
			if l.MatchedBy != ByReceipt || l.Note == "" {
				t.Errorf("receipt %+v", l)
			}
		case LoginItem:
			login = append(login, l)
		}
	}
	if strings.Join(receipts, ",") != "pkg:"+spotifyID+",pkg:"+spotifyID+".pkg" {
		t.Errorf("receipts = %v", receipts)
	}
	if len(login) != 1 || login[0].Path != a.Path || login[0].MatchedBy != ByLoginItem || !strings.Contains(login[0].Note, "report only") {
		t.Errorf("login items = %+v", login)
	}

	t.Run("no match and failing commands", func(t *testing.T) {
		run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "sfltool" {
				return []byte("items:\n  uuid: 2 bundle id: com.other.app\n"), nil
			}
			return nil, errors.New("pkgutil: not found")
		}
		got := find(t, env, a, Options{Run: run})
		for _, l := range got {
			if l.Kind == Receipt || l.Kind == LoginItem {
				t.Errorf("unexpected %+v", l)
			}
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Find(ctx, env, a, Options{Run: run}); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}

func TestFindDotfiles(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{
		Apps: []testutil.App{{Path: "/Applications/Ghostty.app", BundleID: "com.mitchellh.ghostty", Name: "Ghostty"}},
		Dirs: []string{"~/.config/ghostty", "~/.ghostty", "~/.config/ghostty-old", "~/.config/code", "~/.gnupg"},
	})
	a := app.App{Name: "Ghostty", BundleID: "com.mitchellh.ghostty", Path: env.Sys("Applications", "Ghostty.app")}

	t.Run("on", func(t *testing.T) {
		got := find(t, env, a, Options{Dotfiles: true})
		want(t, env, got, "~/.config/ghostty", Dotfile, ByName)
		want(t, env, got, "~/.ghostty", Dotfile, ByName)
		reject(t, env, got, "~/.config/ghostty-old")
		reject(t, env, got, "~/.gnupg")
	})
	t.Run("off", func(t *testing.T) {
		got := find(t, env, a, Options{})
		for _, l := range got {
			if l.Kind == Dotfile {
				t.Errorf("unexpected dotfile %s", l.Path)
			}
		}
	})
	t.Run("needs NameOK", func(t *testing.T) {
		code := app.App{Name: "Code", BundleID: "com.microsoft.VSCode", Path: env.Sys("Applications", "Ghostty.app")}
		got := find(t, env, code, Options{Dotfiles: true})
		reject(t, env, got, "~/.config/code")
	})
}

// TestFindDropsInvalidPaths points ~/Library/Caches at a directory outside the
// home via a symlink; the match is found but must be dropped by Validate.
func TestFindDropsInvalidPaths(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{
		Apps: []testutil.App{{Path: "/Applications/Spotify.app", BundleID: spotifyID, Name: "Spotify"}},
		Dirs: []string{"/outside/" + spotifyID, "~/Library/Preferences"},
	})
	if err := os.Symlink(env.Sys("outside"), env.Library("Caches")); err != nil {
		t.Fatal(err)
	}
	a := app.App{Name: "Spotify", BundleID: spotifyID, Path: env.Sys("Applications", "Spotify.app")}
	got := find(t, env, a, Options{})
	reject(t, env, got, "~/Library/Caches/"+spotifyID)
	if len(got) != 1 {
		t.Errorf("want only the bundle, got %+v", got)
	}
}

func TestFindRefusesInvalidBundle(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{})
	a := app.App{Name: "Finder", BundleID: "com.apple.finder", Path: env.Sys("System", "Library", "CoreServices", "Finder.app")}
	var v *safety.Violation
	if _, err := Find(context.Background(), env, a, Options{}); !errors.As(err, &v) {
		t.Fatalf("err = %v, want a safety.Violation", err)
	}
}

func TestFindEmptyBundleID(t *testing.T) {
	env := testutil.FakeHome(t, testutil.Spec{
		Apps: []testutil.App{{Path: "/Applications/Broken.app", BundleID: "x", Name: "Broken"}},
		Dirs: []string{"~/Library/Caches/.hidden", "~/Library/Application Support/Broken", "~/Library/Preferences/.plist"},
	})
	a := app.App{Name: "Broken", BundleID: "", Path: env.Sys("Applications", "Broken.app")}
	got := find(t, env, a, Options{CrashReports: true})
	if len(got) != 1 {
		t.Errorf("want only the bundle for an app without a bundle id, got %+v", got)
	}
}

func TestNeedsRoot(t *testing.T) {
	env, a := spotifyHome(t)
	t.Run("foreign uid", func(t *testing.T) {
		old := uidOf
		uidOf = func(string) (int, bool) { return os.Getuid() + 1, true }
		t.Cleanup(func() { uidOf = old })
		for _, l := range find(t, env, a, Options{}) {
			if !l.NeedsRoot {
				t.Errorf("%s: NeedsRoot false for a foreign owner", l.Path)
			}
		}
	})
	t.Run("unwritable system dir", func(t *testing.T) {
		old := dirWritable
		dirWritable = func(string) bool { return false }
		t.Cleanup(func() { dirWritable = old })
		got := find(t, env, a, Options{})
		if l, _ := lookup(got, env.Sys("Library", "Caches", spotifyID)); !l.NeedsRoot {
			t.Errorf("/Library/Caches item should need root")
		}
		if l, _ := lookup(got, env.Library("Caches", spotifyID)); l.NeedsRoot {
			t.Errorf("~/Library item must not need root")
		}
		if !got[0].NeedsRoot {
			t.Errorf("/Applications bundle should need root")
		}
	})
}

// TestRulesHonourNameLimits guards the table: no name matching for Group
// Containers, and under /Library only for Application Support and Logs.
func TestRulesHonourNameLimits(t *testing.T) {
	env := fsx.Env{Home: "/h", Root: "/r"}
	for _, r := range rules {
		dir := r.dir(env)
		hasName := false
		for _, m := range r.matchers {
			hasName = hasName || m == matchName || m == matchDotName
		}
		if r.kind == GroupContainer && hasName {
			t.Errorf("%s: Group Containers must not match by name", dir)
		}
		if under(env.Sys("Library"), dir) && hasName && r.kind != AppSupport && r.kind != Log {
			t.Errorf("%s: name matching under /Library is limited to Application Support and Logs", dir)
		}
	}
}

func TestKindJSON(t *testing.T) {
	b, err := json.Marshal(Leftover{Path: "/x", Kind: LaunchAgent, MatchedBy: ByBundlePrefix})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"kind":"Launch agent"`) {
		t.Errorf("json = %s", b)
	}
	if Kind(99).String() != "Kind(99)" || Bundle.String() != "Bundle" {
		t.Errorf("String() = %q / %q", Kind(99), Bundle)
	}
}

// TestHelperSiblingsAndSystemCrashReports covers gaps found by comparing a
// real dry run against `find`: helper ids under HTTPStorages/WebKit and the
// system-wide DiagnosticReports folder.
func TestHelperSiblingsAndSystemCrashReports(t *testing.T) {
	const id = "com.spotify.client"
	env := testutil.FakeHome(t, testutil.Spec{
		Apps: []testutil.App{{Path: "/Applications/Spotify.app", BundleID: id, Name: "Spotify"}},
		Files: map[string]string{
			"~/Library/HTTPStorages/" + id + ".helper/x":                                          "1",
			"~/Library/WebKit/" + id + ".helper/x":                                                "1",
			"~/Library/Saved Application State/" + id + ".helper.savedState/x":                    "1",
			"~/Library/HTTPStorages/com.spotify.installer/x":                                      "1", // sibling id: must NOT match
			"/Library/Logs/DiagnosticReports/Spotify_2026-10-18-173425_Shivangs-MacBook-Air.diag": "1",
			"/Library/Logs/DiagnosticReports/Other-2026-10-18.ips":                                "1",
		},
	})
	a, err := app.Read(env, env.Sys("Applications", "Spotify.app"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Find(context.Background(), env, a, Options{CrashReports: true})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, l := range got {
		paths[l.Path] = l.MatchedBy
	}
	for _, want := range []string{
		env.Library("HTTPStorages", id+".helper"),
		env.Library("WebKit", id+".helper"),
		env.Library("Saved Application State", id+".helper.savedState"),
		env.Sys("Library", "Logs", "DiagnosticReports", "Spotify_2026-10-18-173425_Shivangs-MacBook-Air.diag"),
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("missing %s; got %v", want, paths)
		}
	}
	for _, bad := range []string{
		env.Library("HTTPStorages", "com.spotify.installer"),
		env.Sys("Library", "Logs", "DiagnosticReports", "Other-2026-10-18.ips"),
	} {
		if _, ok := paths[bad]; ok {
			t.Errorf("must not match %s", bad)
		}
	}
}
