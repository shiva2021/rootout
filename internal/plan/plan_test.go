package plan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/brew"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/priv"
	"github.com/shiva2021/rootout/internal/safety"
	"github.com/shiva2021/rootout/internal/tcc"
	"github.com/shiva2021/rootout/internal/testutil"
	"github.com/shiva2021/rootout/internal/trash"
)

const spotifyID = "com.spotify.client"

func fixture(t *testing.T) (fsx.Env, app.App) {
	t.Helper()
	env := testutil.FakeHome(t, testutil.Spec{
		Apps: []testutil.App{{Path: "/Applications/Spotify.app", BundleID: spotifyID, Name: "Spotify"}},
		Files: map[string]string{
			"~/Library/Preferences/" + spotifyID + ".plist":         "p",
			"~/Library/Caches/" + spotifyID + "/a.db":               "cache",
			"~/Library/Application Support/Spotify/prefs":           "x",
			"~/Library/LaunchAgents/" + spotifyID + ".helper.plist": "<plist/>",
			"~/Library/Application Support/Other/keep":              "k",
		},
	})
	a, err := app.Read(env, env.Sys("Applications", "Spotify.app"))
	if err != nil {
		t.Fatal(err)
	}
	return env, a
}

func TestBuild(t *testing.T) {
	env, a := fixture(t)
	p, err := Build(context.Background(), env, a, Options{CrashReports: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) < 4 || p.Items[0].Kind != leftover.Bundle {
		t.Fatalf("items = %+v", p.Items)
	}
	for _, it := range p.Items {
		if !it.Selected {
			t.Errorf("%s not selected", it.Path)
		}
		if strings.Contains(it.Path, "Other") {
			t.Errorf("unrelated path included: %s", it.Path)
		}
	}
	n, size, root := p.Totals()
	if n != len(p.Items) || size == 0 || root != 0 {
		t.Fatalf("totals = %d %d %d", n, size, root)
	}
}

func TestBuildRefusesProtected(t *testing.T) {
	env, a := fixture(t)
	a.BundleID = "com.apple.Music"
	_, err := Build(context.Background(), env, a, Options{})
	var v *safety.Violation
	if !errors.As(err, &v) {
		t.Fatalf("err = %v, want safety violation", err)
	}
}

func TestBuildAlso(t *testing.T) {
	env, a := fixture(t)
	extra := env.Library("Application Support", "Other")
	p, err := Build(context.Background(), env, a, Options{Also: []string{extra}})
	if err != nil {
		t.Fatal(err)
	}
	last := p.Items[len(p.Items)-1]
	if last.Path != extra || last.MatchedBy != ByManual || last.SizeBytes != 1 {
		t.Fatalf("manual item = %+v", last)
	}
	for _, bad := range []string{env.Sys("System", "x"), "relative/path", env.Library("Caches")} {
		if _, err := Build(context.Background(), env, a, Options{Also: []string{bad}}); err == nil {
			t.Errorf("--also %q accepted", bad)
		}
	}
	if _, err := Build(context.Background(), env, a, Options{Also: []string{env.Library("Caches", "does-not-exist")}}); err == nil {
		t.Error("missing --also path accepted")
	}
}

// recorder is a run.Runner that logs every command.
type recorder struct {
	calls []string
	fail  map[string]string // substring -> output when it should fail
}

func (r *recorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	for sub, out := range r.fail {
		if strings.Contains(line, sub) {
			return []byte(out), errors.New("exit status 1")
		}
	}
	return nil, nil
}

func (r *recorder) has(sub string) int {
	for i, c := range r.calls {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

func deps(rec *recorder) Deps {
	return Deps{
		Trash: &trash.Mover{Run: rec.run, LookPath: func(string) (string, error) { return "/usr/bin/trash", nil }},
		Brew:  &brew.Brew{Run: rec.run, LookPath: func(string) (string, error) { return "/opt/homebrew/bin/brew", nil }},
		Priv:  priv.Options{Run: rec.run},
		Run:   rec.run,
		UID:   501,
	}
}

func buildWithRoot(t *testing.T) (fsx.Env, *Plan) {
	t.Helper()
	env, a := fixture(t)
	a.Cask = "spotify"
	p, err := Build(context.Background(), env, a, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate what a real machine would report for /Library items.
	p.Items = append(p.Items,
		Item{Leftover: leftover.Leftover{Path: env.Sys("Library", "LaunchDaemons", spotifyID+".daemon.plist"), Kind: leftover.LaunchDaemon, MatchedBy: leftover.ByBundlePrefix, NeedsRoot: true}, Selected: true},
		Item{Leftover: leftover.Leftover{Path: "pkg:" + spotifyID + ".pkg", Kind: leftover.Receipt, MatchedBy: leftover.ByReceipt}, Selected: true},
		Item{Leftover: leftover.Leftover{Path: a.Path, Kind: leftover.LoginItem, MatchedBy: leftover.ByLoginItem, Note: "report only"}, Selected: true},
	)
	return env, p
}

func TestExecuteTrash(t *testing.T) {
	env, p := buildWithRoot(t)
	rec := &recorder{}
	res, err := Execute(context.Background(), env, p, deps(rec))
	if err != nil {
		t.Fatal(err)
	}
	quit, agent, zap, tr, admin := rec.has("osascript -e tell application id"), rec.has("launchctl bootout gui/501/"+spotifyID+".helper"),
		rec.has("brew uninstall --cask --zap spotify"), rec.has("/usr/bin/trash"), rec.has("with administrator privileges")
	for name, i := range map[string]int{"quit": quit, "agent": agent, "zap": zap, "trash": tr, "admin": admin} {
		if i < 0 {
			t.Fatalf("%s step not run; calls:\n%s", name, strings.Join(rec.calls, "\n"))
		}
	}
	if quit >= agent || agent >= zap || zap >= tr || tr >= admin {
		t.Fatalf("wrong order: quit=%d agent=%d zap=%d trash=%d admin=%d", quit, agent, zap, tr, admin)
	}
	adminLine := rec.calls[admin]
	for _, want := range []string{"'system/" + spotifyID + ".daemon'", "/bin/rm -rf --", "pkgutil --forget '" + spotifyID + ".pkg'"} {
		if !strings.Contains(adminLine, want) {
			t.Errorf("admin command missing %q:\n%s", want, adminLine)
		}
	}
	if !res.OK() || !res.BrewZapped || !res.AdminUsed || len(res.Reported) != 1 || len(res.Forgotten) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Removed) != 6 { // bundle, prefs, cache, app support, agent, daemon
		t.Fatalf("removed %d: %v", len(res.Removed), res.Removed)
	}
}

func TestExecutePermanent(t *testing.T) {
	env, p := buildWithRoot(t)
	p.Mode = Permanent
	rec := &recorder{}
	var removed []string
	d := deps(rec)
	d.RemoveAll = func(s string) error { removed = append(removed, s); return nil }
	if _, err := Execute(context.Background(), env, p, d); err != nil {
		t.Fatal(err)
	}
	if rec.has("/usr/bin/trash") >= 0 || len(removed) != 5 {
		t.Fatalf("permanent mode used trash or removed %d", len(removed))
	}
}

func TestExecuteRefusals(t *testing.T) {
	env, p := buildWithRoot(t)
	rec := &recorder{}
	p.Mode = DryRun
	if _, err := Execute(context.Background(), env, p, deps(rec)); !errors.Is(err, ErrDryRun) || len(rec.calls) != 0 {
		t.Fatalf("dry run: err=%v calls=%v", err, rec.calls)
	}
	p.Mode = Trash
	p.Items[1].Path = env.Sys("System", "Library", "x") // tampered after Build
	if _, err := Execute(context.Background(), env, p, deps(rec)); err == nil || len(rec.calls) != 0 {
		t.Fatalf("tampered path: err=%v calls=%v", err, rec.calls)
	}
}

func TestExecuteAdminCancelled(t *testing.T) {
	env, p := buildWithRoot(t)
	rec := &recorder{fail: map[string]string{"administrator privileges": "execution error: User canceled. (-128)"}}
	res, err := Execute(context.Background(), env, p, deps(rec))
	if !errors.Is(err, priv.ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
	if res.OK() || len(res.Removed) == 0 {
		t.Fatalf("user-level work should still be reported: %+v", res)
	}
}

func TestDescribeAndPrompt(t *testing.T) {
	env, p := buildWithRoot(t)
	_ = env
	var out bytes.Buffer
	ok, err := Prompt{In: strings.NewReader("delete\n"), Out: &out}.Confirm(context.Background(), p)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	for _, want := range []string{"Spotify (com.spotify.client)", "brew uninstall --zap", "[admin]", "[name]", "report only", "need admin rights", "Type delete to continue (anything else cancels): "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("describe missing %q:\n%s", want, out.String())
		}
	}
	ok, _ = Prompt{In: strings.NewReader("\n"), Out: &bytes.Buffer{}}.Confirm(context.Background(), p)
	if ok {
		t.Fatal("empty answer must decline")
	}
}

func TestIsConfirmWord(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"delete", true},
		{"DELETE", true},
		{" delete\n", true},
		{"del", false},
		{"delete now", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			if got := IsConfirmWord(tc.in); got != tc.want {
				t.Fatalf("IsConfirmWord(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPromptRequiresWord(t *testing.T) {
	_, p := buildWithRoot(t)
	cases := []struct {
		name, in string
		want     bool
	}{
		{"word", "delete\n", true},
		{"upper", "DELETE\n", true},
		{"yes", "y\n", false},
		{"prefix", "del\n", false},
		{"empty", "\n", false},
		{"eof", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			ok, err := Prompt{In: strings.NewReader(tc.in), Out: &out}.Confirm(context.Background(), p)
			if err != nil || ok != tc.want {
				t.Fatalf("ok=%v err=%v, want ok=%v", ok, err, tc.want)
			}
			if !strings.HasSuffix(out.String(), "Type delete to continue (anything else cancels): ") {
				t.Fatalf("prompt text missing:\n%s", out.String())
			}
			if strings.Contains(out.String(), "[y/N]") {
				t.Fatal("old y/N prompt still printed")
			}
		})
	}
}

// orphanFixture is a FakeHome with two user-level leftovers of a gone app.
func orphanFixture(t *testing.T) (fsx.Env, []Item) {
	t.Helper()
	const oldID = "com.old.app"
	env := testutil.FakeHome(t, testutil.Spec{
		Files: map[string]string{
			"~/Library/Preferences/" + oldID + ".plist": "p",
			"~/Library/Caches/" + oldID + "/a.db":       "cache",
		},
	})
	items := []Item{
		{Leftover: leftover.Leftover{Path: env.Library("Preferences", oldID+".plist"), Kind: leftover.Preferences, MatchedBy: leftover.ByBundleID, SizeBytes: 1}, Selected: true, Group: oldID},
		{Leftover: leftover.Leftover{Path: env.Library("Caches", oldID), Kind: leftover.Cache, MatchedBy: leftover.ByBundleID, SizeBytes: 5}, Selected: true, Group: oldID},
	}
	return env, items
}

func TestNew(t *testing.T) {
	cases := []struct {
		name            string
		startUnselected bool
		wantCount       int
	}{
		{"selected", false, 2},
		{"unselected", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, items := orphanFixture(t)
			p := New("Orphaned leftovers", items, Trash, tc.startUnselected)
			if p.Label != "Orphaned leftovers" || p.Mode != Trash || p.StartUnselected != tc.startUnselected {
				t.Fatalf("plan = %+v", p)
			}
			if p.App.BundleID != "" || p.App.Name != "" || p.BrewZap {
				t.Fatalf("New must leave App zero and BrewZap false: %+v", p)
			}
			for _, it := range p.Items {
				if it.Selected == tc.startUnselected {
					t.Errorf("%s selected=%v", it.Path, it.Selected)
				}
			}
			n, size, root := p.Totals()
			if n != tc.wantCount || root != 0 || (tc.startUnselected && size != 0) {
				t.Fatalf("totals = %d %d %d", n, size, root)
			}
			if got := p.Groups(); len(got) != 1 || got[0] != "com.old.app" {
				t.Fatalf("groups = %v", got)
			}
		})
	}
}

func TestGroups(t *testing.T) {
	item := func(group string) Item { return Item{Group: group} }
	cases := []struct {
		name  string
		items []Item
		want  []string
	}{
		{"none", []Item{item(""), item("")}, nil},
		{"empty plan", nil, nil},
		{"first seen order", []Item{item("b"), item("a"), item("b"), item(""), item("c")}, []string{"b", "a", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := New("x", tc.items, Trash, false).Groups()
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("groups = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDescribeGroups(t *testing.T) {
	_, items := orphanFixture(t)
	p := New("Orphaned leftovers", items, Trash, false)
	var out bytes.Buffer
	Describe(&out, p)
	got := out.String()
	if !strings.HasPrefix(got, "Orphaned leftovers\n") {
		t.Errorf("label missing as first line:\n%s", got)
	}
	for _, want := range []string{"\ncom.old.app\n", "[Preferences]", "[Cache]", "2 paths"} {
		if !strings.Contains(got, want) {
			t.Errorf("describe missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "\ncom.old.app\n") != 1 {
		t.Errorf("group heading should appear once:\n%s", got)
	}
	if strings.Contains(got, "brew") || strings.Contains(got, "(") {
		t.Errorf("app-only lines printed for a labelled plan:\n%s", got)
	}

	// Without groups Describe still groups by Kind.
	for i := range p.Items {
		p.Items[i].Group = ""
	}
	out.Reset()
	Describe(&out, p)
	if !strings.Contains(out.String(), "\nPreferences\n") || strings.Contains(out.String(), "[Preferences]") {
		t.Errorf("kind grouping broken:\n%s", out.String())
	}
}

func TestExecuteNewPlan(t *testing.T) {
	env, items := orphanFixture(t)
	p := New("Orphaned leftovers", items, Trash, false)
	rec := &recorder{}
	res, err := Execute(context.Background(), env, p, deps(rec))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || len(res.Removed) != 2 || len(res.Skipped) != 0 || res.BrewZapped || res.AdminUsed {
		t.Fatalf("result = %+v", res)
	}
	if rec.has("/usr/bin/trash") < 0 {
		t.Fatalf("trash not called:\n%s", strings.Join(rec.calls, "\n"))
	}
	for _, forbidden := range []string{"tell application", "osascript", "brew", "administrator privileges"} {
		if i := rec.has(forbidden); i >= 0 {
			t.Errorf("unexpected call %q: %s", forbidden, rec.calls[i])
		}
	}
	for _, it := range items {
		if testutil.Exists(env, it.Path) {
			// The fake runner does not actually move files; only assert the command carried the path.
			if rec.has(it.Path) < 0 {
				t.Errorf("trash command missing %s", it.Path)
			}
		}
	}
}

func TestMarkAccess(t *testing.T) {
	env := fsx.Env{Home: "/Users/me", Root: "/"}
	mk := func() *Plan {
		return &Plan{Items: []Item{
			{Leftover: leftover.Leftover{Path: "/Users/me/Library/Containers/com.foo.app", Kind: leftover.Container}, Selected: true},
			{Leftover: leftover.Leftover{Path: "/Users/me/Library/Containers/com.foo.helper", Kind: leftover.Container, Note: "recently used (3 days)"}},
			{Leftover: leftover.Leftover{Path: "/Users/me/Library/Caches/com.foo.app", Kind: leftover.Cache}, Selected: true},
		}}
	}

	t.Run("denied tags container entries and sets the warning", func(t *testing.T) {
		p := mk()
		markAccess(env, p, tcc.Denied, "Warp")
		if p.Items[0].Note != tcc.Note {
			t.Errorf("item 0 note = %q", p.Items[0].Note)
		}
		if want := "recently used (3 days); " + tcc.Note; p.Items[1].Note != want {
			t.Errorf("item 1 note = %q, want %q", p.Items[1].Note, want)
		}
		if p.Items[2].Note != "" {
			t.Errorf("cache item must stay untagged, got %q", p.Items[2].Note)
		}
		for _, want := range []string{"2 paths need Full Disk Access", "Warp", tcc.SettingsPath} {
			if !strings.Contains(p.Warning, want) {
				t.Errorf("warning lacks %q: %q", want, p.Warning)
			}
		}
	})
	t.Run("granted and unknown leave the plan alone", func(t *testing.T) {
		for _, a := range []tcc.Access{tcc.Granted, tcc.Unknown} {
			p := mk()
			markAccess(env, p, a, "Warp")
			if p.Warning != "" || p.Items[0].Note != "" || p.Items[1].Note != "recently used (3 days)" {
				t.Errorf("%v: plan was changed: %+v", a, p)
			}
		}
	})
	t.Run("denied without container entries sets no warning", func(t *testing.T) {
		p := mk()
		p.Items = p.Items[2:]
		markAccess(env, p, tcc.Denied, "Warp")
		if p.Warning != "" {
			t.Errorf("unexpected warning %q", p.Warning)
		}
	})
	t.Run("singular wording", func(t *testing.T) {
		p := mk()
		p.Items = p.Items[:1]
		markAccess(env, p, tcc.Denied, "Terminal")
		if !strings.HasPrefix(p.Warning, "1 path needs Full Disk Access") {
			t.Errorf("warning = %q", p.Warning)
		}
	})
}

func TestDescribeShowsWarning(t *testing.T) {
	p := &Plan{Label: "Orphaned leftovers", Warning: "1 path needs Full Disk Access; grant it to Warp"}
	var out bytes.Buffer
	Describe(&out, p)
	if !strings.Contains(out.String(), "! "+p.Warning) {
		t.Errorf("describe lacks warning:\n%s", out.String())
	}
}
