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

// PatronLife and TrialLife are how long issued keys last. The issuer
// (rootout-keys, the Worker) uses them; nothing in this package does.
const (
	PatronLife = 35 * 24 * time.Hour
	TrialLife  = 7 * 24 * time.Hour
)

const (
	renewAhead = 7 * 24 * time.Hour  // renew a patron key this long before it expires
	grace      = 14 * 24 * time.Hour // a patron key that could not be renewed still works this long
	renewEvery = 24 * time.Hour      // at most one renewal attempt per this interval
	iatSkew    = 24 * time.Hour      // tolerated clock difference for a key's issue time
)

const (
	removeHint   = "; run: rootout license remove, then install a new key"
	noPubKey     = "this build cannot verify keys (no public key embedded)"
	hintNetwork  = "check your connection"
	hintService  = "the key service returned an error; try again later"
	hintNotTried = "rootout tries again once a day"
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

// renewal is what a renewal attempt, or the stamp of an earlier one, tells evaluate.
type renewal struct {
	denied  *DeniedError // a 403, now or remembered from the stamp
	hint    string       // why a renewal failed, for the grace warning
	warning string       // extra warning, e.g. the renewed key could not be saved
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
		return State{Status: StatusInvalid, MID: mid, Reason: noPubKey}
	}
	c, err := verify(key, pub, mid, now)
	if err != nil {
		return State{Status: StatusInvalid, MID: mid, Reason: err.Error() + removeHint}
	}
	if c.Kind == KindAdmin {
		return State{Status: StatusAdmin, Allowed: true, Kind: KindAdmin, MID: mid}
	}
	r := renewal{hint: hintNotTried}
	if c.Kind == KindPatron {
		due, remembered := renewalDue(c, store, now)
		switch {
		case due && d.Client != nil:
			c, r = renew(ctx, *d.Client, store, key, c, pub, mid, now)
		case remembered != "":
			r.denied = &DeniedError{Reason: remembered}
		}
	}
	s := evaluate(c, now, r)
	s.MID = mid
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

// renewalDue reports whether a patron key is near or past expiry and the last
// attempt is at least renewEvery old (a stamp in the future does not throttle).
// It also returns the 403 reason the stamp remembers, if any.
func renewalDue(c Claims, store Store, now time.Time) (bool, string) {
	last, denied, ok := store.LastRenew()
	exp, _ := c.Expires()
	if exp.Sub(now) >= renewAhead {
		return false, denied
	}
	if age := now.Sub(last); ok && age >= 0 && age < renewEvery {
		return false, denied
	}
	return true, denied
}

// renew calls /renew and returns the claims to evaluate and what happened.
// Every attempt is stamped; a 403 is stamped with its reason. Failures keep c.
func renew(ctx context.Context, cl Client, store Store, key string, c Claims, pub ed25519.PublicKey, mid string, now time.Time) (Claims, renewal) {
	mark := func(denied string) {
		if err := store.MarkRenew(now, denied); err != nil {
			slog.Debug("license: renewal stamp", "err", err)
		}
	}
	mark("")
	fresh, err := cl.Renew(ctx, key)
	var denied *DeniedError
	switch {
	case errors.As(err, &denied):
		mark(denied.Reason)
		return c, renewal{denied: denied}
	case errors.Is(err, errUnreachable):
		slog.Debug("license: renew", "err", err)
		return c, renewal{hint: hintNetwork}
	case err != nil:
		slog.Debug("license: renew", "err", err)
		return c, renewal{hint: hintService}
	}
	nc, err := verify(fresh, pub, mid, now)
	if err != nil {
		slog.Debug("license: renew returned a bad key", "err", err)
		return c, renewal{hint: hintService}
	}
	if err := store.Save(fresh); err != nil {
		return nc, renewal{hint: hintService, warning: "could not save the renewed key: " + err.Error()}
	}
	mark("") // Save cleared the stamp; keep the throttle.
	return nc, renewal{}
}

// evaluate turns verified patron or trial claims into a State.
func evaluate(c Claims, now time.Time, r renewal) State {
	exp, _ := c.Expires()
	s := State{Kind: c.Kind, Expires: exp}
	switch {
	case now.Before(exp):
		s.Status, s.Allowed, s.DaysLeft = StatusMember, true, daysLeft(exp, now)
		if c.Kind == KindTrial {
			s.Status = StatusTrial
		}
		if r.denied != nil {
			s.Warning = fmt.Sprintf("%v; the key stops working on %s", r.denied, exp.Format(time.DateOnly))
		}
	case c.Kind == KindPatron && r.denied == nil && now.Before(exp.Add(grace)):
		s.Status, s.Allowed, s.DaysLeft = StatusGrace, true, daysLeft(exp.Add(grace), now)
		s.Warning = fmt.Sprintf("membership key expired %s ago and could not be renewed; %s",
			days(max(1, daysLeft(now, exp))), r.hint)
	default:
		s.Status = StatusExpired
		s.Reason = "membership expired " + exp.Format(time.DateOnly)
		if c.Kind == KindTrial {
			s.Reason = "trial ended " + exp.Format(time.DateOnly)
		}
		if r.denied != nil {
			s.Reason += ": " + r.denied.Error()
		}
	}
	switch {
	case r.warning == "":
	case s.Warning == "":
		s.Warning = r.warning
	default:
		s.Warning += "; " + r.warning
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
		none.Reason = noPubKey
		return none
	}
	key, err := d.Client.Trial(ctx, mid)
	if errors.Is(err, ErrTrialUsed) {
		return State{Status: StatusTrialUsed, MID: mid, Reason: "the trial was already used on this Mac"}
	}
	if errors.Is(err, errUnreachable) {
		slog.Debug("license: trial", "err", err)
		none.Reason = "starting the trial needs the network once"
		return none
	}
	var c Claims
	if err == nil {
		c, err = verify(key, pub, mid, now)
	}
	if err != nil {
		slog.Debug("license: trial", "err", err)
		none.Reason = "could not start the trial: " + hintService
		return none
	}
	s := evaluate(c, now, renewal{})
	s.MID, s.Started = mid, true
	if err := store.Save(key); err != nil {
		s.Warning = "could not save the trial key: " + err.Error()
	}
	return s
}
