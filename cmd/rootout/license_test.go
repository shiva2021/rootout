package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/heur"
	"github.com/shiva2021/rootout/internal/license"
	"github.com/shiva2021/rootout/internal/testutil"
)

func licensePath() string { return os.Getenv("ROOTOUT_LICENSE_FILE") }

func TestLicenseInstallOK(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	key := h.sign(license.KindPatron, 30*24*time.Hour)
	code, out, stderr := runCLI(t, "", "license", key)
	if code != 0 || !strings.Contains(out, "member · renews 2026-12-01") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	fi, err := os.Stat(licensePath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v, mode %v", err, fi)
	}
	if h.renew.Load()+h.trial.Load() != 0 {
		t.Fatal("installing a key must not reach the network")
	}
}

func TestLicenseInstallWrongMID(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	key := signFor(license.KindPatron, "ffffffffffffffffffffffffffffffff", 30*24*time.Hour)
	code, _, stderr := runCLI(t, "", "license", key)
	if code != exitBadInput || !strings.Contains(stderr, "another Mac") || !strings.Contains(stderr, keysURL(h.mid)) {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(licensePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a key for another Mac was saved: %v", err)
	}
}

func TestLicenseInstallBadKey(t *testing.T) {
	newHarness(t, testutil.Spec{})
	for _, key := range []string{"ro1.nope.nope", "hello", signFor(license.KindAdmin, "0123456789abcdef0123456789abcdef", 0) + "x"} {
		code, _, stderr := runCLI(t, "", "license", key)
		if code != exitBadInput || !strings.Contains(stderr, "not a valid licence key") {
			t.Fatalf("%q: exit %d, stderr %q", key, code, stderr)
		}
	}
	if _, err := os.Stat(licensePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a bad key was saved: %v", err)
	}
}

func TestLicenseStatusJSON(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.install(t, h.sign(license.KindTrial, 5*24*time.Hour))
	code, out, stderr := runCLI(t, "", "license", "status", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if got["status"] != "trial" || got["allowed"] != true || got["days_left"] != float64(5) || got["expires"] == nil || got["machine_id"] != h.mid {
		t.Fatalf("status JSON = %v", got)
	}
	if h.renew.Load()+h.trial.Load() != 0 {
		t.Fatal("license status must not reach the network")
	}
}

func TestLicenseStatusText(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.install(t, h.sign(license.KindAdmin, 0))
	code, out, _ := runCLI(t, "", "license", "status")
	if code != 0 || !strings.Contains(out, "admin") || !strings.Contains(out, "machine code: "+h.mid) {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
}

func TestLicenseRemove(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.install(t, h.sign(license.KindAdmin, 0))
	code, out, stderr := runCLI(t, "", "license", "remove")
	if code != 0 || !strings.Contains(out, "licence key removed") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	if _, err := os.Stat(licensePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key still there: %v", err)
	}
	code, out, _ = runCLI(t, "", "license", "remove")
	if code != 0 || !strings.Contains(out, "no licence key installed") {
		t.Fatalf("second remove: exit %d, stdout %q", code, out)
	}
}

func TestLicenseNoArgsOffersTrial(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	code, out, stderr := runCLI(t, "y\n", "license")
	if code != 0 || !strings.Contains(out, "Start your free 7-day trial? [y/N] ") || !strings.Contains(out, "7-day trial started · ends 2026-11-08") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	if h.trial.Load() != 1 {
		t.Fatalf("/trial called %d times, want 1", h.trial.Load())
	}
	if _, err := os.Stat(licensePath()); err != nil {
		t.Fatalf("trial key not saved: %v", err)
	}
}

func TestLicenseNoArgsDeclined(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	code, out, _ := runCLI(t, "n\n", "license")
	if code != 0 || h.trial.Load() != 0 || strings.Contains(out, "trial started") {
		t.Fatalf("exit %d, /trial %d, stdout %q", code, h.trial.Load(), out)
	}
}

func TestLicenseNoArgsTrialUsed(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.trialStatus = 409
	code, _, stderr := runCLI(t, "", "license", "--yes")
	if code != exitLicensing || !strings.Contains(stderr, deniedTrialUsed) {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestLicenseNoArgsShowsURL(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.install(t, h.sign(license.KindAdmin, 0))
	code, out, _ := runCLI(t, "", "license")
	for _, want := range []string{"admin", "machine code: " + h.mid, "get a key: " + license.ServiceURL + "/?m=" + h.mid} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout lacks %q:\n%s", want, out)
		}
	}
	if code != 0 || strings.Contains(out, "Start your free") || h.trial.Load() != 0 {
		t.Fatalf("exit %d; a key holder must not be offered the trial:\n%s", code, out)
	}
}

func TestDoctorMembership(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		name   string
		key    func(h *harness) string
		runErr bool
		ok     bool
		detail string
	}{
		{"none", func(*harness) string { return "" }, false, true, "no key · 7-day trial available (rootout license)"},
		{"admin", func(h *harness) string { return h.sign(license.KindAdmin, 0) }, false, true, "admin"},
		{"trial", func(h *harness) string { return h.sign(license.KindTrial, 5*day) }, false, true, "trial · 5 days left"},
		{"expired", func(h *harness) string { return h.sign(license.KindTrial, -day) }, false, false, "expired 2026-10-31"},
		{"grace", func(h *harness) string { return h.sign(license.KindPatron, -2*day) }, false, false, "grace · expired 2026-10-30 · membership key expired 2 days ago"},
		{"invalid", func(*harness) string { return "ro1.x.y" }, false, false, "invalid key: "},
		{"no machine id", func(*harness) string { return "" }, true, false, "could not read this Mac's id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, testutil.Spec{})
			if k := tc.key(h); k != "" {
				h.install(t, k)
			}
			if tc.runErr {
				h.runErr = errors.New("boom")
			}
			c := membershipCheck(context.Background(), h.env)
			if c.Name != "Membership" || c.OK != tc.ok || !strings.HasPrefix(c.Detail, tc.detail) {
				t.Fatalf("check = %+v", c)
			}
			if !c.OK && c.Fix != "rootout license" {
				t.Fatalf("fix = %q", c.Fix)
			}
			if h.renew.Load()+h.trial.Load() != 0 {
				t.Fatal("doctor must not reach the network")
			}
		})
	}
}

func TestBuildAssessorDenied(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	c := config.Default()
	c.LLM.Provider, c.LLM.Ollama.Model, c.LLM.Ollama.BaseURL = "ollama", "llama3.2", "http://127.0.0.1:1"
	ctx := context.Background()
	a, err := buildAssessor(ctx, h.env, c, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != aiMembersLabel || a.Assess == nil {
		t.Fatalf("assessor = %+v", a)
	}
	if _, err := a.Assess(ctx, heur.Report{}); !errors.Is(err, ErrMembership) {
		t.Fatalf("Assess err = %v, want ErrMembership", err)
	}
	// One offline Resolve for the label, one gated Resolve for the assessment.
	if h.trial.Load() != 0 || h.ioreg.Load() != 2 {
		t.Fatalf("/trial %d, Resolve %d; want 0 and 2", h.trial.Load(), h.ioreg.Load())
	}
}

func TestBuildAssessorAllowed(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.install(t, h.sign(license.KindAdmin, 0))
	c := config.Default()
	c.LLM.Provider, c.LLM.Ollama.Model, c.LLM.Ollama.BaseURL = "ollama", "llama3.2", "http://127.0.0.1:1"
	a, err := buildAssessor(context.Background(), h.env, c, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "ollama · llama3.2" {
		t.Fatalf("label = %q", a.Label)
	}
}

func TestBuildAssessorNoProviderNoCheck(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	quietStderr(t)
	a, err := buildAssessor(context.Background(), h.env, config.Default(), "", "", false)
	if err != nil || a.Assess != nil || a.Label != "" {
		t.Fatalf("assessor %+v err %v", a, err)
	}
	if h.sawIoreg() {
		t.Fatal("no provider means no licence check")
	}
}

// quietStderr sends os.Stderr to a temp file for the rest of the test.
func quietStderr(t *testing.T) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = old; _ = f.Close() })
}

func TestLicenseJSONOnlyJSON(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	key := h.sign(license.KindAdmin, 0)
	for _, args := range [][]string{{"license", "--json"}, {"license", key, "--json"}} {
		code, out, stderr := runCLI(t, "y\n", args...)
		var st map[string]any
		if code != 0 || json.Unmarshal([]byte(out), &st) != nil || strings.Contains(out+stderr, "Start your free") {
			t.Fatalf("%v: exit %d, stdout %q, stderr %q", args[1:], code, out, stderr)
		}
	}
	if h.trial.Load() != 0 {
		t.Fatal("--json must never start the trial")
	}
}

func TestLicenseInstallRejectsExpiredAndFutureKeys(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	future, err := license.Encode(license.Claims{V: 1, Kind: license.KindPatron, Sub: "12345678", MID: h.mid,
		IAT: testNow.Add(48 * time.Hour).Unix(), EXP: testNow.Add(60 * 24 * time.Hour).Unix()}, testPriv)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, key, want string }{
		{"expired", h.sign(license.KindPatron, -time.Hour), "this key has expired"},
		{"future", future, "this key is not valid yet; check your clock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, "", "license", tc.key)
			if code != exitBadInput || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			if _, err := os.Stat(licensePath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the key was saved: %v", err)
			}
		})
	}
}

func TestBuildAssessorOpenMakesNoRenewCall(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.install(t, h.sign(license.KindPatron, 3*24*time.Hour)) // renewal due
	c := config.Default()
	c.LLM.Provider, c.LLM.Ollama.Model, c.LLM.Ollama.BaseURL = "ollama", "llama3.2", "http://127.0.0.1:1"
	a, err := buildAssessor(context.Background(), h.env, c, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "ollama · llama3.2" {
		t.Fatalf("label = %q", a.Label)
	}
	if n := h.renew.Load(); n != 0 {
		t.Fatalf("opening the scan made %d /renew call(s), want 0", n)
	}
	_, _ = a.Assess(context.Background(), heur.Report{})
	if n := h.renew.Load(); n != 1 {
		t.Fatalf("the first assessment made %d /renew call(s), want 1", n)
	}
}

func TestBuildAssessorLabelCarriesWarning(t *testing.T) {
	h := newHarness(t, testutil.Spec{})
	h.install(t, h.sign(license.KindPatron, -2*24*time.Hour))
	c := config.Default()
	c.LLM.Provider, c.LLM.Ollama.Model, c.LLM.Ollama.BaseURL = "ollama", "llama3.2", "http://127.0.0.1:1"
	a, err := buildAssessor(context.Background(), h.env, c, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	// Offline, so no retry hint: the warning fits in labelWarningMax.
	const want = "ollama · llama3.2 · membership key expired 2 days ago and could not be renewed"
	if a.Label != want {
		t.Fatalf("label = %q, want %q", a.Label, want)
	}
}
