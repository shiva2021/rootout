package license

import (
	"fmt"
	"time"
)

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

// Summary is a one-line status for doctor, the home title and `license status`.
func (s State) Summary() string {
	switch s.Status {
	case StatusAdmin:
		return "admin"
	case StatusMember:
		return "member · renews " + s.Expires.Format(time.DateOnly)
	case StatusTrial:
		return "trial · " + days(s.DaysLeft) + " left"
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

// daysLeft is ceil((exp-now)/24h), never negative.
func daysLeft(exp, now time.Time) int {
	d := exp.Sub(now)
	if d <= 0 {
		return 0
	}
	return int((d + 24*time.Hour - 1) / (24 * time.Hour))
}

// days formats n as "1 day" or "n days".
func days(n int) string {
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}
