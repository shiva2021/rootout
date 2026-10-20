package plan

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/brew"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/priv"
	"github.com/shiva2021/rootout/internal/safety"
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
	ok, err := Prompt{In: strings.NewReader("y\n"), Out: &out}.Confirm(context.Background(), p)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	for _, want := range []string{"Spotify (com.spotify.client)", "brew uninstall --zap", "[admin]", "[name]", "report only", "need admin rights"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("describe missing %q:\n%s", want, out.String())
		}
	}
	ok, _ = Prompt{In: strings.NewReader("\n"), Out: &bytes.Buffer{}}.Confirm(context.Background(), p)
	if ok {
		t.Fatal("empty answer must decline")
	}
}
