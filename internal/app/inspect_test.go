package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/testutil"
)

// fakeRunner answers each command from a testdata file by command name and
// records what was asked. A name in block waits for the context instead,
// which is how a hung spctl is simulated.
type fakeRunner struct {
	t     *testing.T
	out   map[string]string // command name -> testdata file
	err   map[string]error  // command name -> error returned alongside the output
	block map[string]bool
	dir   string // where out files live; "" = testdata

	mu    sync.Mutex
	calls [][]string
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()
	if f.block[name] {
		<-ctx.Done()
		// What a killed command leaves behind: a plausible first line.
		return []byte("Identifier=com.partial\n/x: accepted\nkMDItemDateAdded = 2026-01-01 00:00:00 +0000\n"), ctx.Err()
	}
	var out []byte
	if file, ok := f.out[name]; ok {
		dir := f.dir
		if dir == "" {
			dir = "testdata"
		}
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			f.t.Fatal(err)
		}
		out = data
	}
	return out, f.err[name]
}

// argsOf returns the arguments of the first call to name, nil when it was
// never run.
func (f *fakeRunner) argsOf(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c[0] == name {
			return c[1:]
		}
	}
	return nil
}

// signedRunner is a notarised Developer ID app with Spotlight metadata.
func signedRunner(t *testing.T) *fakeRunner {
	return &fakeRunner{t: t,
		out: map[string]string{"codesign": "codesign_devid.txt", "spctl": "spctl_notarized.txt", "mdls": "mdls_full.txt"},
	}
}

func inspectSpec() testutil.Spec {
	return testutil.Spec{
		Apps: []testutil.App{
			{Path: "/Applications/Foo.app", BundleID: "com.example.foo", Version: "1.2"},
			{Path: "/Applications/Real.app", BundleID: "com.example.real"},
			{Path: "/Applications/Store.app", BundleID: "com.example.store"},
		},
		Symlinks: map[string]string{"/Applications/Link.app": "Real.app"},
		Dirs:     []string{"/Applications/Broken.app/Contents"},
		Files: map[string]string{
			"/Applications/Store.app/Contents/_MASReceipt/receipt": "receipt",
			"/Applications/Rich.app/Contents/Info.plist":           richPlist,
			"~/Library/LaunchAgents/com.example.foo.helper.plist":  "<plist/>",
			"/Library/LaunchDaemons/com.example.foo.plist":         "<plist/>",
			"/Library/LaunchAgents/com.example.foo2.plist":         "<plist/>",
			"/Library/LaunchAgents/com.other.plist":                "<plist/>",
			"/Library/LaunchAgents/com.example.foo.txt":            "not a plist",
		},
	}
}

const richPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.example.rich</string>
	<key>CFBundleName</key>
	<string>Rich</string>
	<key>CFBundleExecutable</key>
	<string>RichBinary</string>
	<key>NSHumanReadableCopyright</key>
	<string>© 2026 Example Pty Ltd</string>
	<key>LSMinimumSystemVersion</key>
	<string>13.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSMicrophoneUsageDescription</key>
	<string>Records audio</string>
	<key>NSCameraUsageDescription</key>
	<string>Video calls</string>
	<key>CFBundleURLTypes</key>
	<array>
		<dict>
			<key>CFBundleURLName</key>
			<string>Rich link</string>
			<key>CFBundleURLSchemes</key>
			<array>
				<string>rich</string>
				<string>richapp</string>
			</array>
		</dict>
	</array>
	<key>SMPrivilegedExecutables</key>
	<dict>
		<key>com.example.rich.helper</key>
		<string>identifier "com.example.rich.helper"</string>
	</dict>
</dict>
</plist>
`

func fooApp(env fsx.Env) App {
	return App{Name: "Foo", BundleID: "com.example.foo", Version: "1.2", Path: env.Sys("Applications", "Foo.app")}
}

func TestInspectSignedNotarised(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	r := signedRunner(t)
	f := Inspect(context.Background(), env, fooApp(env), r.run)

	wantSig := Signature{Signed: true, Identifier: "com.google.Chrome", TeamID: "EQHXZ8M8AV",
		Authority: []string{"Developer ID Application: Google LLC (EQHXZ8M8AV)", "Developer ID Certification Authority", "Apple Root CA"},
		Timestamp: "29 Oct 2026 at 13:45:15"}
	if !reflect.DeepEqual(f.Signature, wantSig) {
		t.Errorf("Signature = %+v, want %+v", f.Signature, wantSig)
	}
	if f.App.TeamID != "EQHXZ8M8AV" {
		t.Errorf("App.TeamID = %q, want filled from the signature", f.App.TeamID)
	}
	if want := (Gatekeeper{Accepted: true, Source: "Notarized Developer ID"}); f.Gatekeeper != want {
		t.Errorf("Gatekeeper = %+v, want %+v", f.Gatekeeper, want)
	}
	wantFroms := []string{"https://dl.example.com/Foo-1.2.dmg", "https://example.com/download"}
	if !slices.Equal(f.Origin.WhereFroms, wantFroms) {
		t.Errorf("WhereFroms = %q, want %q", f.Origin.WhereFroms, wantFroms)
	}
	if want := time.Date(2026, 11, 1, 12, 37, 41, 0, time.UTC); !f.Origin.DateAdded.Equal(want) {
		t.Errorf("DateAdded = %v, want %v", f.Origin.DateAdded, want)
	}
	if want := time.Date(2026, 11, 4, 8, 16, 47, 0, time.UTC); !f.Origin.LastUsed.Equal(want) {
		t.Errorf("LastUsed = %v, want %v", f.Origin.LastUsed, want)
	}
	if f.AppStore {
		t.Error("AppStore = true without a receipt")
	}
	if len(f.Unavailable) != 0 {
		t.Errorf("Unavailable = %q", f.Unavailable)
	}
	// Every probe is given the resolved bundle path as its last argument
	// (the temp dir itself sits behind the /var -> /private/var link).
	path, err := filepath.EvalSymlinks(env.Sys("Applications", "Foo.app"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codesign", "spctl", "mdls"} {
		args := r.argsOf(name)
		if len(args) == 0 || args[len(args)-1] != path {
			t.Errorf("%s args = %q, want last %q", name, args, path)
		}
	}
	if args := r.argsOf("codesign"); !slices.Equal(args[:len(args)-1], []string{"-dv", "--verbose=4"}) {
		t.Errorf("codesign args = %q", args)
	}
	if args := r.argsOf("spctl"); !slices.Equal(args[:len(args)-1], []string{"--assess", "--type", "execute", "-vv"}) {
		t.Errorf("spctl args = %q", args)
	}
}

func TestInspectAdHoc(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	r := &fakeRunner{t: t, out: map[string]string{"codesign": "codesign_adhoc.txt", "spctl": "spctl_rejected.txt", "mdls": "mdls_null.txt"},
		err: map[string]error{"spctl": errors.New("exit status 3")}}
	f := Inspect(context.Background(), env, fooApp(env), r.run)
	if !f.Signature.Signed || !f.Signature.AdHoc || f.Signature.TeamID != "" || f.Signature.Identifier != "com.example.foo" {
		t.Errorf("Signature = %+v, want signed ad-hoc with no team", f.Signature)
	}
	if f.App.TeamID != "" {
		t.Errorf("App.TeamID = %q, want empty", f.App.TeamID)
	}
	if want := (Gatekeeper{Accepted: false, Source: "no usable signature"}); f.Gatekeeper != want {
		t.Errorf("Gatekeeper = %+v, want %+v", f.Gatekeeper, want)
	}
	if f.Origin.WhereFroms != nil || !f.Origin.DateAdded.IsZero() || !f.Origin.LastUsed.IsZero() {
		t.Errorf("Origin = %+v, want zero for (null) values", f.Origin)
	}
	if len(f.Unavailable) != 0 {
		t.Errorf("Unavailable = %q, want none: a rejected verdict is an answer", f.Unavailable)
	}
}

func TestInspectUnsigned(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	r := &fakeRunner{t: t, out: map[string]string{"codesign": "codesign_unsigned.txt", "spctl": "spctl_rejected.txt", "mdls": "mdls_null.txt"},
		err: map[string]error{"codesign": errors.New("exit status 1"), "spctl": errors.New("exit status 3")}}
	f := Inspect(context.Background(), env, fooApp(env), r.run)
	if f.Signature.Signed || f.Signature.AdHoc || f.Signature.TeamID != "" || len(f.Signature.Authority) != 0 {
		t.Errorf("Signature = %+v, want unsigned", f.Signature)
	}
	if f.Gatekeeper.Accepted || f.Gatekeeper.Source != "no usable signature" {
		t.Errorf("Gatekeeper = %+v", f.Gatekeeper)
	}
	if len(f.Unavailable) != 0 {
		t.Errorf("Unavailable = %q, want none: 'not signed at all' is an answer", f.Unavailable)
	}
}

func TestInspectProbesUnavailable(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	boom := errors.New("exec: not found")
	r := &fakeRunner{t: t, err: map[string]error{"codesign": boom, "spctl": boom, "mdls": boom}}
	f := Inspect(context.Background(), env, fooApp(env), r.run)
	if want := []string{"codesign", "mdls", "spctl"}; !slices.Equal(f.Unavailable, want) {
		t.Errorf("Unavailable = %q, want %q", f.Unavailable, want)
	}
	if f.Signature.Signed || f.Gatekeeper.Accepted || f.Gatekeeper.Source != "" {
		t.Errorf("facts filled from empty output: %+v %+v", f.Signature, f.Gatekeeper)
	}
}

func TestInspectTimeout(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	old := spctlTimeout
	spctlTimeout = 20 * time.Millisecond
	t.Cleanup(func() { spctlTimeout = old })
	r := signedRunner(t)
	r.block = map[string]bool{"spctl": true}
	start := time.Now()
	f := Inspect(context.Background(), env, fooApp(env), r.run)
	if time.Since(start) > time.Second {
		t.Fatal("Inspect waited past the spctl timeout")
	}
	if !slices.Contains(f.Unavailable, "spctl") {
		t.Errorf("Unavailable = %q, want spctl", f.Unavailable)
	}
	if f.Gatekeeper.Accepted || f.Gatekeeper.Source != "" {
		t.Errorf("Gatekeeper = %+v, want zero: partial output from a killed probe is not an answer", f.Gatekeeper)
	}
	if !f.Signature.Signed || f.Origin.DateAdded.IsZero() {
		t.Error("the other probes should still have answered")
	}
}

// TestInspectUnrecognisedOutput: error text from a probe is missing
// evidence, never a verdict.
func TestInspectUnrecognisedOutput(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	dir := t.TempDir()
	for name, text := range map[string]string{
		"codesign.txt": "/Applications/Foo.app: bundle format unrecognized, invalid, or unsuitable\n",
		"spctl.txt":    "/Applications/Foo.app: unknown error -67062\n",
		"mdls.txt":     "/Applications/Foo.app: could not find /Applications/Foo.app.\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := &fakeRunner{t: t, dir: dir, out: map[string]string{"codesign": "codesign.txt", "spctl": "spctl.txt", "mdls": "mdls.txt"},
		err: map[string]error{"codesign": errors.New("exit status 1"), "spctl": errors.New("exit status 1"), "mdls": errors.New("exit status 1")}}
	f := Inspect(context.Background(), env, fooApp(env), r.run)
	if want := []string{"codesign", "mdls", "spctl"}; !slices.Equal(f.Unavailable, want) {
		t.Errorf("Unavailable = %q, want %q", f.Unavailable, want)
	}
	if f.Signature.Signed || f.Gatekeeper.Source != "" || f.Gatekeeper.Accepted {
		t.Errorf("error text became a verdict: %+v %+v", f.Signature, f.Gatekeeper)
	}
}

func TestInspectCancelled(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	r := signedRunner(t)
	r.block = map[string]bool{"codesign": true, "spctl": true, "mdls": true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := Inspect(ctx, env, fooApp(env), r.run)
	if want := []string{"codesign", "mdls", "spctl"}; !slices.Equal(f.Unavailable, want) {
		t.Errorf("Unavailable = %q, want %q", f.Unavailable, want)
	}
}

func TestInspectResolvesSymlink(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	r := signedRunner(t)
	link := env.Sys("Applications", "Link.app")
	f := Inspect(context.Background(), env, App{Name: "Link", BundleID: "com.example.real", Path: link}, r.run)
	real, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codesign", "spctl", "mdls"} {
		args := r.argsOf(name)
		if len(args) == 0 || args[len(args)-1] != real {
			t.Errorf("%s got %q, want the resolved bundle %q", name, args, real)
		}
	}
	if f.App.Path != link {
		t.Errorf("App.Path = %q, want the link kept for display", f.App.Path)
	}
}

func TestInspectAppStoreReceipt(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	r := signedRunner(t)
	f := Inspect(context.Background(), env, App{Name: "Store", BundleID: "com.example.store", Path: env.Sys("Applications", "Store.app")}, r.run)
	if !f.AppStore {
		t.Error("AppStore = false with Contents/_MASReceipt/receipt present")
	}
}

func TestInspectLaunchItems(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	f := Inspect(context.Background(), env, fooApp(env), signedRunner(t).run)
	// Prefix match on the bundle id, so com.example.foo2 counts too (a
	// sibling id that shares the prefix is rare and worth a look anyway).
	want := []string{
		env.Sys("Library", "LaunchAgents", "com.example.foo2.plist"),
		env.Sys("Library", "LaunchDaemons", "com.example.foo.plist"),
		env.User("Library", "LaunchAgents", "com.example.foo.helper.plist"),
	}
	if !slices.Equal(f.LaunchItems, want) {
		t.Errorf("LaunchItems = %q, want %q", f.LaunchItems, want)
	}
	// No bundle id, no prefix to match: nothing is listed.
	g := Inspect(context.Background(), env, App{Name: "Broken", Path: env.Sys("Applications", "Broken.app")}, signedRunner(t).run)
	if len(g.LaunchItems) != 0 {
		t.Errorf("LaunchItems without a bundle id = %q", g.LaunchItems)
	}
}

func TestInspectPlistFacts(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	f := Inspect(context.Background(), env, App{Name: "Rich", BundleID: "com.example.rich", Path: env.Sys("Applications", "Rich.app")}, signedRunner(t).run)
	p := f.Plist
	if p.Copyright != "© 2026 Example Pty Ltd" || p.MinSystem != "13.0" || p.Executable != "RichBinary" {
		t.Errorf("strings = %+v", p)
	}
	if !p.UIElement || p.BackgroundOnly {
		t.Errorf("UIElement=%v BackgroundOnly=%v", p.UIElement, p.BackgroundOnly)
	}
	if want := []string{"rich", "richapp"}; !slices.Equal(p.URLSchemes, want) {
		t.Errorf("URLSchemes = %q, want %q", p.URLSchemes, want)
	}
	if want := []string{"NSCameraUsageDescription", "NSMicrophoneUsageDescription"}; !slices.Equal(p.UsageKeys, want) {
		t.Errorf("UsageKeys = %q, want %q", p.UsageKeys, want)
	}
	if want := []string{"com.example.rich.helper"}; !slices.Equal(p.PrivilegedHelpers, want) {
		t.Errorf("PrivilegedHelpers = %q, want %q", p.PrivilegedHelpers, want)
	}
	if slices.Contains(f.Unavailable, "plist") {
		t.Errorf("Unavailable = %q", f.Unavailable)
	}
	// The synthesised bundle has none of the optional keys.
	g := Inspect(context.Background(), env, fooApp(env), signedRunner(t).run)
	if g.Plist.Executable != "Foo" || g.Plist.UIElement || len(g.Plist.UsageKeys) != 0 || len(g.Plist.URLSchemes) != 0 {
		t.Errorf("plain bundle Plist = %+v", g.Plist)
	}
}

func TestInspectBrokenPlist(t *testing.T) {
	env := testutil.FakeHome(t, inspectSpec())
	f := Inspect(context.Background(), env, App{Name: "Broken", Path: env.Sys("Applications", "Broken.app")}, signedRunner(t).run)
	if !slices.Contains(f.Unavailable, "plist") {
		t.Errorf("Unavailable = %q, want plist", f.Unavailable)
	}
}
