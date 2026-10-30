package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/license"
	"github.com/shiva2021/rootout/internal/run"
	"github.com/shiva2021/rootout/internal/tui"
)

// ErrMembership is matched by every denial from gate.
var ErrMembership = errors.New("membership required")

// membersOnly is the home-title status when gated actions are locked.
const membersOnly = tui.StatusMembersOnly

// noKeyReason is Resolve's Reason for a plain "no key, no trial attempted".
const noKeyReason = "no key installed"

// deniedMark precedes the service's reason in an expired key's Reason
// (license.DeniedError's text).
const deniedMark = "membership check failed: "

// membershipError is a denial: why, and the two routes to a key.
type membershipError struct{ st license.State }

// first is the opening of the message without its full stop.
func (e *membershipError) first() string {
	detail := e.st.Summary()
	switch e.st.Status {
	case license.StatusNone:
		if e.st.Reason != "" && e.st.Reason != noKeyReason {
			detail += " · " + e.st.Reason
		}
	case license.StatusExpired:
		if _, why, ok := strings.Cut(e.st.Reason, deniedMark); ok && why != "" {
			detail += " · " + plainReason(why)
		}
	}
	return "deletion and AI features are for members (" + detail + ")"
}

// plainReason puts a /renew 403 reason in the spec's words; an unknown one passes through.
func plainReason(r string) string {
	switch r {
	case "declined_patron":
		return "your last payment was declined"
	case "former_patron":
		return "your membership has ended"
	case "device_replaced":
		return "this Mac was replaced by a newer one on your account"
	case "free_member":
		return "a paid tier is needed"
	}
	return r
}

// warnLine is st.Warning for a person: its first line, with a 403 reason
// after deniedMark in plain words. It is "" when there is no warning.
func warnLine(st license.State) string {
	line, _, _ := strings.Cut(st.Warning, "\n")
	head, rest, ok := strings.Cut(line, deniedMark)
	if !ok {
		return line
	}
	reason, tail, _ := strings.Cut(rest, ";")
	if tail != "" {
		tail = ";" + tail
	}
	return head + deniedMark + plainReason(strings.TrimSpace(reason)) + tail
}

// clip cuts s to n runes, marking a cut with an ellipsis.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func (e *membershipError) Error() string {
	return e.first() + ".\n" +
		"  Already a member? get your key at " + keysURL(e.st.MID) + "\n" +
		"  then run: rootout license <key>\n" +
		"  Not yet? the same page links to the membership."
}

func (e *membershipError) Is(target error) bool { return target == ErrMembership }

// keysURL is the key service page for this Mac's machine code.
func keysURL(mid string) string {
	if mid == "" {
		return license.ServiceURL
	}
	return license.ServiceURL + "/?m=" + mid
}

// gateDepsFn builds Resolve's inputs; tests swap it for a fake Mac, clock and key service.
var gateDepsFn = gateDeps

// gateDeps is the production wiring: the real Mac, the clock and the key service.
func gateDeps(env fsx.Env, startTrial bool) license.Deps {
	return license.Deps{
		Env:        env,
		Getenv:     os.Getenv,
		Run:        run.Default,
		Now:        time.Now,
		Client:     &license.Client{BaseURL: license.ServiceURL, UserAgent: "rootout/" + version},
		StartTrial: startTrial,
	}
}

// gateCache holds the one licence decision of this process.
var gateCache struct {
	sync.Mutex
	st    license.State
	trial bool // st came from a call that could start the trial
	ok    bool
}

// resetGate forgets the cached decision (tests).
func resetGate() {
	gateCache.Lock()
	defer gateCache.Unlock()
	gateCache.st, gateCache.trial, gateCache.ok = license.State{}, false, false
}

// gate decides whether a gated action may run, resolving at most once per
// process: a keyless result is resolved again whenever a caller may start the
// trial (it was not asked, or the service could not be reached); every other
// result is final. Denials return *membershipError.
func gate(ctx context.Context, env fsx.Env, startTrial bool) (license.State, error) {
	gateCache.Lock()
	defer gateCache.Unlock()
	c := &gateCache
	if !c.ok || (startTrial && !c.trial && c.st.Status == license.StatusNone) {
		st := license.Resolve(ctx, gateDepsFn(env, startTrial))
		// A keyless result is never final for a later trial start: the
		// service may have been unreachable.
		c.st, c.trial, c.ok = st, startTrial && st.Status != license.StatusNone, true
		c.st.Started = false // announce a new trial once
		return verdict(st)
	}
	return verdict(c.st)
}

func verdict(st license.State) (license.State, error) {
	if !st.Allowed {
		return st, &membershipError{st: st}
	}
	return st, nil
}

// denyExit turns a denial into exit 4 with the full message; other errors pass through.
func denyExit(err error) error {
	var me *membershipError
	if errors.As(err, &me) {
		return &exitErr{code: exitLicensing, msg: me.Error()}
	}
	return err
}

// trialNotice announces a trial that was just started.
func trialNotice(st license.State, out io.Writer) {
	if st.Started {
		_, _ = fmt.Fprintf(out, "7-day trial started · ends %s\n", st.Expires.Format(time.DateOnly))
	}
}

// trialPrefix is the home-screen flash prefix for a trial that was just started.
const trialPrefix = "7-day trial started · "

// gateNotice tells a gated user what the gate learnt: a trial that was just
// started and any warning (grace, a coming denial, a key that was not saved).
func gateNotice(st license.State, out io.Writer) {
	trialNotice(st, out)
	if w := warnLine(st); w != "" {
		_, _ = fmt.Fprintln(out, "rootout:", w)
	}
}

// noteFlash is gateNotice for the home screen: the trial prefix and the
// warning's first line on the flash.
func noteFlash(f tui.Flash, st license.State) tui.Flash {
	if st.Started {
		f.Text = trialPrefix + f.Text
	}
	if w := warnLine(st); w != "" {
		f.Text += " · " + w
	}
	return f
}

// offlineDeps is gateDeps without the key service: no renewal, no trial.
func offlineDeps(env fsx.Env) license.Deps {
	d := gateDepsFn(env, false)
	d.Client = nil
	return d
}

// offlineState is this Mac's membership from the installed key alone.
func offlineState(ctx context.Context, env fsx.Env) license.State {
	return license.Resolve(ctx, offlineDeps(env))
}

// gateOffline is gate's verdict from the installed key alone: one Resolve,
// no renewal, no trial and no cache. It suits labels shown before any gated
// work; the work itself goes through gate.
func gateOffline(ctx context.Context, env fsx.Env) (license.State, error) {
	return verdict(offlineState(ctx, env))
}

// licenseStatusLine is the home title suffix. It never reaches the network.
func licenseStatusLine(ctx context.Context, env fsx.Env) string {
	st := offlineState(ctx, env)
	switch st.Status {
	case license.StatusAdmin:
		return "admin"
	case license.StatusMember:
		return "member"
	case license.StatusGrace:
		return "grace: renew soon"
	case license.StatusTrial:
		if st.DaysLeft == 1 {
			return "trial: 1 day left"
		}
		return fmt.Sprintf("trial: %d days left", st.DaysLeft)
	case license.StatusExpired, license.StatusTrialUsed, license.StatusInvalid:
		return membersOnly
	}
	return ""
}

// confirmTrial asks whether to start the trial. It reads one byte at a time
// so the rest of in is left for a later prompt.
func confirmTrial(in io.Reader, out io.Writer) bool {
	_, _ = fmt.Fprint(out, "Start your free 7-day trial? [y/N] ")
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := in.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				break
			}
			line = append(line, b[0])
		}
		if err != nil {
			break
		}
	}
	s := strings.TrimSpace(string(line))
	return s == "y" || s == "Y"
}

// hasKey reports whether a licence key file is installed (valid or not).
func hasKey(env fsx.Env) bool {
	_, err := license.Store{Path: license.Path(env, os.Getenv)}.Load()
	return !errors.Is(err, license.ErrNoKey)
}

// trialOK reports whether a subcommand may start the trial: yes when a key is
// installed (no trial is needed) or --yes was given, otherwise it asks.
func trialOK(env fsx.Env, yes bool) bool {
	return yes || hasKey(env) || confirmTrial(os.Stdin, os.Stderr)
}

// gateAction is the subcommand gate: offer the trial to a keyless user, then
// resolve. A declined offer is exit 3 with nothing; a denial is exit 4.
func gateAction(ctx context.Context, env fsx.Env, yes bool, nothing string) error {
	if !trialOK(env, yes) {
		return &exitErr{code: exitDeclined, msg: nothing}
	}
	st, err := gate(ctx, env, true)
	if err != nil {
		return denyExit(err)
	}
	gateNotice(st, os.Stderr)
	return nil
}

// denyLine is the one-line form of err for a flash or an error row: a
// denial keeps its first line and gains the command that explains it; any
// other error is cut to its first line.
func denyLine(err error) string {
	const hint = " · rootout license"
	var me *membershipError
	if errors.As(err, &me) {
		return me.first() + hint
	}
	line, _, _ := strings.Cut(err.Error(), "\n")
	if isExit(err, exitLicensing) {
		return strings.TrimSuffix(line, ".") + hint
	}
	return line
}
