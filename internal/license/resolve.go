package license

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/run"
)

const (
	patronLife = 35 * 24 * time.Hour // how long the Worker signs patron keys for
	trialLife  = 7 * 24 * time.Hour  // how long the Worker signs trial keys for
	renewAhead = 7 * 24 * time.Hour  // renew a patron key this long before it expires
	grace      = 14 * 24 * time.Hour // a patron key that could not be renewed still works this long
	renewEvery = 24 * time.Hour      // at most one renewal attempt per this interval
	iatSkew    = 24 * time.Hour      // tolerated clock difference for a key's issue time
)

const removeHint = "; run: rootout license remove, then install a new key"

// Status names the outcome of Resolve.
type Status string

const (
	StatusAdmin     Status = "admin"
	StatusMember    Status = "member"
	StatusTrial     Status = "trial"
	StatusGrace     Status = "grace"
	StatusExpired   Status = "expired"
	StatusTrialUsed Status = "trial-used"
	StatusNone      Status = "none"
	StatusInvalid   Status = "invalid"
	StatusNoMachine Status = "no-machine-id"
)

// Deps are Resolve's inputs, injectable for tests.
type Deps struct {
	Env        fsx.Env
	Getenv     func(string) string
	Run        run.Runner
	Now        func() time.Time
	Pub        ed25519.PublicKey // nil means the embedded PublicKeyHex
	Client     *Client           // nil means never renew or start a trial
	StartTrial bool              // may call /trial when no key is installed
}

// State is the membership outcome for this Mac.
type State struct {
	Status   Status    `json:"status"`
	Allowed  bool      `json:"allowed"`
	Kind     Kind      `json:"kind,omitempty"`
	MID      string    `json:"machine_id,omitempty"`
	Expires  time.Time `json:"expires,omitzero"`
	DaysLeft int       `json:"days_left"`
	Warning  string    `json:"warning,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Started  bool      `json:"trial_started,omitempty"` // a trial key was just issued
}

// Resolve reads, verifies and when needed renews the installed key, or starts
// a trial, and reports whether gated features are allowed. It never panics and
// fails closed.
func Resolve(ctx context.Context, d Deps) State {
	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	getenv := d.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	mid, err := MachineID(ctx, d.Run)
	if err != nil {
		slog.Debug("license: machine id", "err", err)
		return State{Status: StatusNoMachine, Reason: "could not read this Mac's id"}
	}
	pub, pubErr := d.Pub, error(nil)
	if pub == nil {
		pub, pubErr = EmbeddedPublicKey()
	}
	store := Store{Path: Path(d.Env, getenv)}
	key, err := store.Load()
	switch {
	case errors.Is(err, ErrNoKey):
		return startTrial(ctx, d, store, mid, pub, pubErr, now)
	case err != nil:
		return State{Status: StatusInvalid, MID: mid, Reason: err.Error() + removeHint}
	case pubErr != nil:
		return State{Status: StatusInvalid, MID: mid, Reason: "this build cannot verify keys (no public key embedded)"}
	}
	c, err := verify(key, pub, mid, now)
	if err != nil {
		return State{Status: StatusInvalid, MID: mid, Reason: err.Error() + removeHint}
	}
	if c.Kind == KindAdmin {
		return State{Status: StatusAdmin, Allowed: true, Kind: KindAdmin, MID: mid}
	}
	var warning string
	var denied *DeniedError
	if c.Kind == KindPatron && d.Client != nil && dueForRenewal(c, store, now) {
		c, warning, denied = renew(ctx, *d.Client, store, key, c, pub, mid, now)
	}
	s := evaluate(c, now, denied)
	s.MID = mid
	switch {
	case warning == "":
	case s.Warning == "":
		s.Warning = warning
	default:
		s.Warning += "; " + warning
	}
	return s
}

// verify decodes key and checks it belongs to this Mac and was not issued in the future.
func verify(key string, pub ed25519.PublicKey, mid string, now time.Time) (Claims, error) {
	c, err := Decode(key, pub)
	if err != nil {
		return Claims{}, err
	}
	if c.MID != mid {
		return Claims{}, fmt.Errorf("%w: the key belongs to another Mac", ErrBadKey)
	}
	if c.IAT > now.Add(iatSkew).Unix() {
		return Claims{}, fmt.Errorf("%w: issued in the future; check the clock", ErrBadKey)
	}
	return c, nil
}

// dueForRenewal reports whether a patron key is near or past expiry and the
// last attempt is at least renewEvery old. A stamp in the future does not throttle.
func dueForRenewal(c Claims, store Store, now time.Time) bool {
	exp, _ := c.Expires()
	if exp.Sub(now) >= renewAhead {
		return false
	}
	if last, ok := store.LastRenew(); ok {
		if age := now.Sub(last); age >= 0 && age < renewEvery {
			return false
		}
	}
	return true
}

// renew calls /renew and returns the claims to evaluate, a warning for the
// user, and the 403 answer if there was one. Service failures keep c.
func renew(ctx context.Context, cl Client, store Store, key string, c Claims, pub ed25519.PublicKey, mid string, now time.Time) (Claims, string, *DeniedError) {
	if err := store.MarkRenew(now); err != nil {
		slog.Debug("license: renewal stamp", "err", err)
	}
	fresh, err := cl.Renew(ctx, key)
	var denied *DeniedError
	if errors.As(err, &denied) {
		return c, "", denied
	}
	if err != nil {
		slog.Debug("license: renew", "err", err)
		return c, "", nil
	}
	nc, err := verify(fresh, pub, mid, now)
	if err != nil {
		slog.Debug("license: renew returned a bad key", "err", err)
		return c, "", nil
	}
	if err := store.Save(fresh); err != nil {
		return nc, "could not save the renewed key: " + err.Error(), nil
	}
	return nc, "", nil
}

// evaluate turns verified patron or trial claims into a State.
func evaluate(c Claims, now time.Time, denied *DeniedError) State {
	exp, _ := c.Expires()
	s := State{Kind: c.Kind, Expires: exp}
	switch {
	case now.Before(exp):
		s.Status, s.Allowed, s.DaysLeft = StatusMember, true, daysLeft(exp, now)
		if c.Kind == KindTrial {
			s.Status = StatusTrial
		}
		if denied != nil {
			s.Warning = fmt.Sprintf("%v; the key stops working on %s", denied, exp.Format(time.DateOnly))
		}
	case c.Kind == KindPatron && denied == nil && now.Before(exp.Add(grace)):
		s.Status, s.Allowed, s.DaysLeft = StatusGrace, true, daysLeft(exp.Add(grace), now)
		s.Warning = fmt.Sprintf("membership key expired %d days ago and could not be renewed; check your connection",
			max(1, daysLeft(now, exp)))
	default:
		s.Status = StatusExpired
		s.Reason = "membership expired " + exp.Format(time.DateOnly)
		if c.Kind == KindTrial {
			s.Reason = "trial ended " + exp.Format(time.DateOnly)
		}
		if denied != nil {
			s.Reason += ": " + denied.Error()
		}
	}
	return s
}

// startTrial handles a Mac with no key: it asks /trial only when allowed and
// when the answer can be verified.
func startTrial(ctx context.Context, d Deps, store Store, mid string, pub ed25519.PublicKey, pubErr error, now time.Time) State {
	none := State{Status: StatusNone, MID: mid, Reason: "no key installed"}
	if !d.StartTrial || d.Client == nil {
		return none
	}
	if pubErr != nil {
		none.Reason = "this build cannot verify keys (no public key embedded)"
		return none
	}
	key, err := d.Client.Trial(ctx, mid)
	if errors.Is(err, ErrTrialUsed) {
		return State{Status: StatusTrialUsed, MID: mid, Reason: "the trial was already used on this Mac"}
	}
	var c Claims
	if err == nil {
		c, err = verify(key, pub, mid, now)
	}
	if err != nil {
		slog.Debug("license: trial", "err", err)
		none.Reason = "starting the trial needs the network once"
		return none
	}
	s := evaluate(c, now, nil)
	s.MID, s.Started = mid, true
	if err := store.Save(key); err != nil {
		s.Warning = "could not save the trial key: " + err.Error()
	}
	return s
}

// daysLeft is ceil((exp-now)/24h), never negative.
func daysLeft(exp, now time.Time) int {
	d := exp.Sub(now)
	if d <= 0 {
		return 0
	}
	return int((d + 24*time.Hour - 1) / (24 * time.Hour))
}

// Summary is a one-line status for doctor, the home title and `license status`.
func (s State) Summary() string {
	switch s.Status {
	case StatusAdmin:
		return "admin"
	case StatusMember:
		return "member · renews " + s.Expires.Format(time.DateOnly)
	case StatusTrial:
		if s.DaysLeft == 1 {
			return "trial · 1 day left"
		}
		return fmt.Sprintf("trial · %d days left", s.DaysLeft)
	case StatusGrace:
		return "grace · expired " + s.Expires.Format(time.DateOnly)
	case StatusExpired:
		return "expired " + s.Expires.Format(time.DateOnly)
	case StatusTrialUsed:
		return "trial used on this Mac"
	case StatusNone:
		return "no key"
	case StatusInvalid:
		return "invalid key: " + s.Reason
	}
	return "could not read this Mac's id"
}
