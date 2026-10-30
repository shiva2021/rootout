package license

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/testutil"
)

var testNow = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

const otherMID = "ffffffffffffffffffffffffffffffff"

func fakeRun(context.Context, string, ...string) ([]byte, error) { return []byte(ioregSample), nil }

func testMID(t *testing.T) string {
	t.Helper()
	mid, err := MachineID(context.Background(), fakeRun)
	if err != nil {
		t.Fatal(err)
	}
	return mid
}

// signKey builds a key of kind for mid expiring at now+exp (exp 0 = no expiry).
func signKey(t *testing.T, priv ed25519.PrivateKey, kind Kind, mid string, exp time.Duration) string {
	t.Helper()
	c := Claims{V: 1, Kind: kind, Sub: "12345678", MID: mid, IAT: testNow.Add(-time.Hour).Unix()}
	if exp != 0 {
		c.EXP = testNow.Add(exp).Unix()
		if c.EXP <= c.IAT {
			c.IAT = c.EXP - 3600
		}
	}
	k, err := Encode(c, priv)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// fakeService answers every request with status and body and counts calls.
type fakeService struct {
	srv   *httptest.Server
	calls atomic.Int32
}

func newService(t *testing.T, status int, body string) *fakeService {
	t.Helper()
	f := &fakeService{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.calls.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func keyBody(key string) string {
	b, _ := json.Marshal(map[string]string{"key": key})
	return string(b)
}

type fixture struct {
	env   fsx.Env
	store Store
	svc   *fakeService
	deps  Deps
}

// setup writes key (if any) to a fake home and points Deps at svc; down closes the server first.
func setup(t *testing.T, key string, svc *fakeService, down bool) *fixture {
	t.Helper()
	env := testutil.FakeHome(t, testutil.Spec{})
	testutil.RequireSandbox(t, env)
	s := Store{Path: Path(env, noEnv)}
	if key != "" {
		if err := s.Save(key); err != nil {
			t.Fatal(err)
		}
	}
	if svc == nil {
		svc = newService(t, http.StatusOK, `{}`)
	}
	if down {
		svc.srv.Close()
	}
	return &fixture{env: env, store: s, svc: svc, deps: Deps{
		Env: env, Getenv: noEnv, Run: fakeRun, Now: func() time.Time { return testNow }, Pub: testPub,
		Client: &Client{BaseURL: svc.srv.URL, HTTP: &http.Client{Timeout: time.Second}}, StartTrial: true,
	}}
}

func TestResolve(t *testing.T) {
	mid := testMID(t)
	day := 24 * time.Hour
	patron := func(exp time.Duration) string { return signKey(t, testPriv, KindPatron, mid, exp) }
	renewed := patron(35 * day)
	trialKey := signKey(t, testPriv, KindTrial, mid, 7*day)
	tampered := func() string {
		p := strings.Split(patron(20*day), ".")
		b := []byte(p[1])
		b[5] ^= 1
		return p[0] + "." + string(b) + "." + p[2]
	}()

	tests := []struct {
		name       string
		key        string
		stampAge   time.Duration // 0 = no stamp
		status     int
		body       string
		down       bool
		noTrial    bool
		breakMID   bool
		want       Status
		allowed    bool
		daysLeft   int
		calls      int32
		replaced   string // key expected on disk afterwards; "" = unchanged
		stamp      bool   // a renewal stamp is expected at testNow
		reasonHas  string
		warning    bool
		started    bool
		wantNoFile bool
	}{
		{name: "Admin", key: signKey(t, testPriv, KindAdmin, mid, 0), down: true, want: StatusAdmin, allowed: true},
		{name: "AdminWrongMID", key: signKey(t, testPriv, KindAdmin, otherMID, 0), down: true, want: StatusInvalid, reasonHas: "another Mac"},
		{name: "MemberFresh", key: patron(20 * day), down: true, want: StatusMember, allowed: true, daysLeft: 20},
		{name: "MemberRenewsAhead", key: patron(3 * day), status: 200, body: keyBody(renewed), want: StatusMember, allowed: true, daysLeft: 35, calls: 1, replaced: renewed, stamp: true},
		{name: "MemberRenewThrottled", key: patron(3 * day), stampAge: time.Hour, status: 200, body: keyBody(renewed), want: StatusMember, allowed: true, daysLeft: 3},
		{name: "ExpiredRenewOK", key: patron(-60 * day), status: 200, body: keyBody(renewed), want: StatusMember, allowed: true, daysLeft: 35, calls: 1, replaced: renewed, stamp: true},
		{name: "ExpiredGraceNet", key: patron(-3 * day), down: true, want: StatusGrace, allowed: true, daysLeft: 11, stamp: true, warning: true},
		{name: "ExpiredGrace503", key: patron(-3 * day), status: 503, want: StatusGrace, allowed: true, daysLeft: 11, calls: 1, stamp: true, warning: true},
		{name: "ExpiredDenied403", key: patron(-3 * day), status: 403, body: `{"reason":"declined_patron"}`, want: StatusExpired, calls: 1, stamp: true, reasonHas: "declined"},
		{name: "ExpiredPastGrace", key: patron(-20 * day), down: true, want: StatusExpired, stamp: true, reasonHas: "expired"},
		{name: "TrialActive", key: signKey(t, testPriv, KindTrial, mid, 2*day), down: true, want: StatusTrial, allowed: true, daysLeft: 2},
		{name: "TrialExpiredNoGrace", key: signKey(t, testPriv, KindTrial, mid, -time.Hour), status: 200, body: keyBody(renewed), want: StatusExpired, reasonHas: "trial"},
		{name: "NoKeyStartTrial", status: 200, body: keyBody(trialKey), want: StatusTrial, allowed: true, daysLeft: 7, calls: 1, replaced: trialKey, started: true},
		{name: "NoKeyTrialUsed", status: 409, want: StatusTrialUsed, calls: 1, reasonHas: "already used", wantNoFile: true},
		{name: "NoKeyOffline", down: true, want: StatusNone, reasonHas: "network", wantNoFile: true},
		{name: "NoKeyNotAllowed", noTrial: true, status: 200, body: keyBody(trialKey), want: StatusNone, wantNoFile: true},
		{name: "InvalidSignature", key: tampered, down: true, want: StatusInvalid, reasonHas: "rootout license remove"},
		{name: "NoMachineID", key: patron(20 * day), down: true, breakMID: true, want: StatusNoMachine},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var svc *fakeService
			if !tt.down {
				svc = newService(t, tt.status, tt.body)
			}
			f := setup(t, tt.key, svc, tt.down)
			if tt.stampAge != 0 {
				if err := f.store.MarkRenew(testNow.Add(-tt.stampAge), ""); err != nil {
					t.Fatal(err)
				}
			}
			f.deps.StartTrial = !tt.noTrial
			if tt.breakMID {
				f.deps.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no ioreg") }
			}

			got := Resolve(context.Background(), f.deps)

			if got.Status != tt.want || got.Allowed != tt.allowed {
				t.Errorf("status, allowed = %s, %v; want %s, %v (reason %q)", got.Status, got.Allowed, tt.want, tt.allowed, got.Reason)
			}
			if got.DaysLeft != tt.daysLeft {
				t.Errorf("DaysLeft = %d, want %d", got.DaysLeft, tt.daysLeft)
			}
			if n := f.svc.calls.Load(); n != tt.calls {
				t.Errorf("service calls = %d, want %d", n, tt.calls)
			}
			if !strings.Contains(got.Reason, tt.reasonHas) {
				t.Errorf("Reason = %q, want it to contain %q", got.Reason, tt.reasonHas)
			}
			if (got.Warning != "") != tt.warning {
				t.Errorf("Warning = %q, want present=%v", got.Warning, tt.warning)
			}
			if got.Started != tt.started {
				t.Errorf("Started = %v, want %v", got.Started, tt.started)
			}
			if tt.want != StatusNoMachine && got.MID != mid {
				t.Errorf("MID = %q, want %q", got.MID, mid)
			}
			onDisk, err := f.store.Load()
			switch {
			case tt.wantNoFile:
				if !errors.Is(err, ErrNoKey) {
					t.Errorf("key file exists after %s: %q, %v", tt.name, onDisk, err)
				}
			case tt.replaced != "":
				if onDisk != tt.replaced {
					t.Error("key file not replaced with the service's key")
				}
			default:
				if onDisk != tt.key {
					t.Errorf("key file changed: %q", onDisk)
				}
			}
			last, _, ok := f.store.LastRenew()
			switch {
			case tt.stamp && (!ok || !last.Equal(testNow)):
				t.Errorf("stamp = %v, %v; want %v", last, ok, testNow)
			case !tt.stamp && tt.stampAge == 0 && ok:
				t.Errorf("unexpected stamp %v", last)
			}
		})
	}
}

func TestResolveMalformedClaims(t *testing.T) {
	mid := testMID(t)
	sign := func(c Claims) string {
		k, err := Encode(c, testPriv)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	exp := testNow.Add(20 * 24 * time.Hour).Unix()
	tests := []struct {
		name string
		c    Claims
		want Status
	}{
		{"patron without exp", Claims{V: 1, Kind: KindPatron, Sub: "1", MID: mid, IAT: testNow.Unix()}, StatusInvalid},
		{"iat two days ahead", Claims{V: 1, Kind: KindPatron, Sub: "1", MID: mid, IAT: testNow.Add(48 * time.Hour).Unix(), EXP: exp}, StatusInvalid},
		{"iat five minutes ahead", Claims{V: 1, Kind: KindPatron, Sub: "1", MID: mid, IAT: testNow.Add(5 * time.Minute).Unix(), EXP: exp}, StatusMember},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := setup(t, sign(tt.c), nil, true)
			got := Resolve(context.Background(), f.deps)
			if got.Status != tt.want {
				t.Errorf("Status = %s, want %s (reason %q)", got.Status, tt.want, got.Reason)
			}
			if tt.want == StatusInvalid && (got.Allowed || !strings.Contains(got.Reason, "rootout license remove")) {
				t.Errorf("invalid key: allowed %v, reason %q", got.Allowed, got.Reason)
			}
		})
	}
}

func TestResolveRenewReturnsBadKey(t *testing.T) {
	mid := testMID(t)
	_, otherPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	day := 24 * time.Hour
	foreign := signKey(t, otherPriv, KindPatron, mid, 35*day)
	wrongMac := signKey(t, testPriv, KindPatron, otherMID, 35*day)
	tests := []struct {
		name string
		exp  time.Duration
		body string
		want Status
	}{
		{"other keypair, expired", -3 * day, keyBody(foreign), StatusGrace},
		{"other keypair, not expired", 3 * day, keyBody(foreign), StatusMember},
		{"other mac, expired", -3 * day, keyBody(wrongMac), StatusGrace},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := signKey(t, testPriv, KindPatron, mid, tt.exp)
			f := setup(t, old, newService(t, http.StatusOK, tt.body), false)
			got := Resolve(context.Background(), f.deps)
			if got.Status != tt.want {
				t.Errorf("Status = %s, want %s", got.Status, tt.want)
			}
			if f.svc.calls.Load() != 1 {
				t.Errorf("calls = %d, want 1", f.svc.calls.Load())
			}
			if onDisk, _ := f.store.Load(); onDisk != old {
				t.Error("old key was replaced by a bad one")
			}
		})
	}
}

func TestResolveTrialReturnsBadKey(t *testing.T) {
	_, otherPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	bad := signKey(t, otherPriv, KindTrial, testMID(t), 7*24*time.Hour)
	f := setup(t, "", newService(t, http.StatusOK, keyBody(bad)), false)
	got := Resolve(context.Background(), f.deps)
	if got.Status != StatusNone || got.Allowed || got.Started {
		t.Errorf("got %+v, want StatusNone, denied", got)
	}
	if _, err := f.store.Load(); !errors.Is(err, ErrNoKey) {
		t.Error("bad trial key was saved")
	}
}

func TestResolveRenewDeniedWhileActive(t *testing.T) {
	key := signKey(t, testPriv, KindPatron, testMID(t), 3*24*time.Hour)
	f := setup(t, key, newService(t, http.StatusForbidden, `{"reason":"membership_ended"}`), false)
	got := Resolve(context.Background(), f.deps)
	if got.Status != StatusMember || !got.Allowed || !strings.Contains(got.Warning, "membership_ended") {
		t.Errorf("got %+v, want member with a warning naming the reason", got)
	}
}

func TestResolveFutureStampDoesNotThrottle(t *testing.T) {
	key := signKey(t, testPriv, KindPatron, testMID(t), 3*24*time.Hour)
	f := setup(t, key, newService(t, http.StatusServiceUnavailable, ""), false)
	if err := f.store.MarkRenew(testNow.Add(48*time.Hour), ""); err != nil {
		t.Fatal(err)
	}
	Resolve(context.Background(), f.deps)
	if f.svc.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", f.svc.calls.Load())
	}
}

func TestResolveNilClient(t *testing.T) {
	expired := signKey(t, testPriv, KindPatron, testMID(t), -3*24*time.Hour)
	f := setup(t, expired, nil, false)
	f.deps.Client = nil
	if got := Resolve(context.Background(), f.deps); got.Status != StatusGrace || strings.Contains(got.Warning, "once a day") ||
		!strings.HasSuffix(got.Warning, "could not be renewed") {
		t.Errorf("expired, nil client: %+v, want grace with no retry hint", got)
	}
	if _, _, ok := f.store.LastRenew(); ok {
		t.Error("stamp written with no client")
	}

	g := setup(t, "", nil, false)
	g.deps.Client = nil
	if got := Resolve(context.Background(), g.deps); got.Status != StatusNone {
		t.Errorf("no key, nil client: %s, want none", got.Status)
	}
}

func TestResolveNoEmbeddedKey(t *testing.T) {
	if PublicKeyHex != "" {
		t.Skip("key embedded")
	}
	key := signKey(t, testPriv, KindAdmin, testMID(t), 0)
	f := setup(t, key, nil, false)
	f.deps.Pub = nil
	if got := Resolve(context.Background(), f.deps); got.Status != StatusInvalid || got.Allowed || got.Reason == "" {
		t.Errorf("key on disk, no public key: %+v", got)
	}

	trialKey := signKey(t, testPriv, KindTrial, testMID(t), 7*24*time.Hour)
	g := setup(t, "", newService(t, http.StatusOK, keyBody(trialKey)), false)
	g.deps.Pub = nil
	got := Resolve(context.Background(), g.deps)
	if got.Status != StatusNone || got.Allowed || got.Reason == "" {
		t.Errorf("no key, no public key: %+v", got)
	}
	if g.svc.calls.Load() != 0 {
		t.Error("called /trial without a public key to verify the answer")
	}
}

func TestResolveUnreadableKeyFile(t *testing.T) {
	f := setup(t, "", nil, false)
	if err := os.MkdirAll(f.store.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	got := Resolve(context.Background(), f.deps)
	if got.Status != StatusInvalid || got.Allowed {
		t.Errorf("got %+v, want invalid", got)
	}
	if f.svc.calls.Load() != 0 {
		t.Error("service called for an unreadable key")
	}
}

// readOnlyDir makes the key's directory unwritable for the rest of the test.
func readOnlyDir(t *testing.T, s Store) {
	t.Helper()
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}

func TestResolveSaveFails(t *testing.T) {
	mid := testMID(t)
	day := 24 * time.Hour

	t.Run("renewed key", func(t *testing.T) {
		old := signKey(t, testPriv, KindPatron, mid, 3*day)
		f := setup(t, old, newService(t, http.StatusOK, keyBody(signKey(t, testPriv, KindPatron, mid, 35*day))), false)
		readOnlyDir(t, f.store)
		got := Resolve(context.Background(), f.deps)
		if got.Status != StatusMember || got.DaysLeft != 35 || !strings.Contains(got.Warning, "could not save") {
			t.Errorf("got %+v, want member on the new key with a save warning", got)
		}
	})
	t.Run("renewed key already expired", func(t *testing.T) {
		old := signKey(t, testPriv, KindPatron, mid, -3*day)
		f := setup(t, old, newService(t, http.StatusOK, keyBody(signKey(t, testPriv, KindPatron, mid, -2*day))), false)
		readOnlyDir(t, f.store)
		got := Resolve(context.Background(), f.deps)
		if got.Status != StatusGrace || !strings.Contains(got.Warning, "expired") || !strings.Contains(got.Warning, "could not save") {
			t.Errorf("got %+v, want grace with both warnings", got)
		}
	})
	t.Run("trial key", func(t *testing.T) {
		f := setup(t, "", newService(t, http.StatusOK, keyBody(signKey(t, testPriv, KindTrial, mid, 7*day))), false)
		readOnlyDir(t, f.store)
		got := Resolve(context.Background(), f.deps)
		if got.Status != StatusTrial || !got.Allowed || !got.Started || !strings.Contains(got.Warning, "could not save") {
			t.Errorf("got %+v, want trial started with a save warning", got)
		}
	})
}

func TestResolveDefaults(t *testing.T) {
	// Nil Now and Getenv fall back to the real clock and no overrides.
	f := setup(t, "", nil, false)
	f.deps.Now, f.deps.Getenv, f.deps.Client = nil, nil, nil
	if got := Resolve(context.Background(), f.deps); got.Status != StatusNone {
		t.Errorf("Status = %s, want none", got.Status)
	}
}

func TestResolveDenialPersists(t *testing.T) {
	mid := testMID(t)
	day := 24 * time.Hour
	tests := []struct {
		name    string
		exp     time.Duration
		want    Status
		allowed bool
		field   func(State) string
	}{
		{"ExpiredDenied403Persists", -3 * day, StatusExpired, false, func(s State) string { return s.Reason }},
		{"ActiveDenied403Persists", 3 * day, StatusMember, true, func(s State) string { return s.Warning }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := signKey(t, testPriv, KindPatron, mid, tt.exp)
			f := setup(t, key, newService(t, http.StatusForbidden, `{"reason":"declined_patron"}`), false)
			for run := 1; run <= 2; run++ {
				got := Resolve(context.Background(), f.deps)
				if got.Status != tt.want || got.Allowed != tt.allowed || !strings.Contains(tt.field(got), "declined_patron") {
					t.Errorf("run %d: got %+v, want %s naming the 403 reason", run, got, tt.want)
				}
				f.svc.srv.Close() // the second run must not need the service
			}
			if n := f.svc.calls.Load(); n != 1 {
				t.Errorf("service calls = %d, want 1", n)
			}
		})
	}

	t.Run("nil client still honours the stamp", func(t *testing.T) {
		key := signKey(t, testPriv, KindPatron, mid, -3*day)
		f := setup(t, key, nil, true)
		if err := f.store.MarkRenew(testNow.Add(-2*day), "membership_ended"); err != nil {
			t.Fatal(err)
		}
		f.deps.Client = nil
		if got := Resolve(context.Background(), f.deps); got.Status != StatusExpired || !strings.Contains(got.Reason, "membership_ended") {
			t.Errorf("got %+v, want expired naming the stamped reason", got)
		}
	})
}

func TestResolveDeniedThenRetryFails(t *testing.T) {
	mid := testMID(t)
	for _, tt := range []struct {
		name      string
		closeThen bool // close the server after the 403; otherwise it answers 503
		calls     int32
	}{
		{"server closed", true, 1},
		{"503", false, 2},
	} {
		t.Run("DeniedThenRetryFails/"+tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"reason":"declined_patron"}`))
					return
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			t.Cleanup(srv.Close)
			f := setup(t, signKey(t, testPriv, KindPatron, mid, -3*24*time.Hour), nil, false)
			f.deps.Client = &Client{BaseURL: srv.URL, HTTP: &http.Client{Timeout: time.Second}}

			first := Resolve(context.Background(), f.deps)
			if first.Status != StatusExpired {
				t.Fatalf("day 0: %+v, want expired", first)
			}
			if tt.closeThen {
				srv.Close()
			}
			later := testNow.Add(25 * time.Hour)
			f.deps.Now = func() time.Time { return later }
			got := Resolve(context.Background(), f.deps)
			if got.Status != StatusExpired || got.Allowed || !strings.Contains(got.Reason, "declined_patron") {
				t.Errorf("day 1: %+v, want expired naming the original reason", got)
			}
			if n := calls.Load(); n != tt.calls {
				t.Errorf("service calls = %d, want %d", n, tt.calls)
			}
			if last, denied, ok := f.store.LastRenew(); !ok || !last.Equal(later) || denied != "declined_patron" {
				t.Errorf("stamp = %v, %q, %v; want the retry time and the kept reason", last, denied, ok)
			}
		})
	}
}

func TestResolveFailureWording(t *testing.T) {
	mid := testMID(t)
	day := 24 * time.Hour
	_, otherPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		exp     time.Duration
		status  int
		body    string
		down    bool
		want    string
		wantNot string
	}{
		{"network", -3 * day, 0, "", true, "expired 3 days ago and could not be renewed; check your connection", "service"},
		{"503", -3 * day, 503, "", false, "the key service returned an error", "connection"},
		{"429", -3 * day, 429, "", false, "the key service returned an error", "connection"},
		{"bad key", -3 * day, 200, keyBody(signKey(t, otherPriv, KindPatron, mid, 35*day)), false, "the key service returned an error", "connection"},
		{"one day", -time.Hour, 503, "", false, "expired 1 day ago", "1 days"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var svc *fakeService
			if !tt.down {
				svc = newService(t, tt.status, tt.body)
			}
			f := setup(t, signKey(t, testPriv, KindPatron, mid, tt.exp), svc, tt.down)
			got := Resolve(context.Background(), f.deps)
			if got.Status != StatusGrace || !strings.Contains(got.Warning, tt.want) || strings.Contains(got.Warning, tt.wantNot) {
				t.Errorf("Warning = %q, want %q and not %q", got.Warning, tt.want, tt.wantNot)
			}
		})
	}

	t.Run("throttled", func(t *testing.T) {
		f := setup(t, signKey(t, testPriv, KindPatron, mid, -3*day), nil, true)
		if err := f.store.MarkRenew(testNow.Add(-time.Hour), ""); err != nil {
			t.Fatal(err)
		}
		if got := Resolve(context.Background(), f.deps); !strings.Contains(got.Warning, "once a day") {
			t.Errorf("Warning = %q, want the retry note", got.Warning)
		}
	})

	for _, tt := range []struct {
		name, body, wantNot string
		status              int
	}{
		{"trial 503", "", "network", 503},
		{"trial bad key", keyBody(signKey(t, otherPriv, KindTrial, mid, 7*day)), "network", 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := setup(t, "", newService(t, tt.status, tt.body), false)
			got := Resolve(context.Background(), f.deps)
			if got.Status != StatusNone || !strings.Contains(got.Reason, "the key service returned an error") || strings.Contains(got.Reason, tt.wantNot) {
				t.Errorf("got %+v, want a service-error reason", got)
			}
		})
	}
}

func TestResolveRenewClearsRememberedDenial(t *testing.T) {
	mid := testMID(t)
	renewed := signKey(t, testPriv, KindPatron, mid, 35*24*time.Hour)
	f := setup(t, signKey(t, testPriv, KindPatron, mid, -3*24*time.Hour), newService(t, http.StatusOK, keyBody(renewed)), false)
	if err := f.store.MarkRenew(testNow.Add(-25*time.Hour), "declined_patron"); err != nil {
		t.Fatal(err)
	}
	got := Resolve(context.Background(), f.deps)
	if got.Status != StatusMember || !got.Allowed || got.Warning != "" {
		t.Errorf("got %+v, want member with no warning", got)
	}
	if last, denied, ok := f.store.LastRenew(); !ok || !last.Equal(testNow) || denied != "" {
		t.Errorf("stamp = %v, %q, %v; want testNow with no reason", last, denied, ok)
	}
}
