package license

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Kind says who a key was issued to and whether it expires.
type Kind string

const (
	KindPatron Kind = "patron"
	KindTrial  Kind = "trial"
	KindAdmin  Kind = "admin"
)

// Claims is the signed payload of a licence key. MID binds the key to one Mac.
type Claims struct {
	V    int    `json:"v"`
	Kind Kind   `json:"kind"`
	Sub  string `json:"sub"`
	MID  string `json:"mid"`
	IAT  int64  `json:"iat"`
	EXP  int64  `json:"exp,omitempty"`
}

// ErrBadKey is wrapped by every failure to decode or validate a key.
var ErrBadKey = errors.New("license: invalid key")

var midRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Expires returns the expiry time; ok is false for admin keys, which never expire.
func (c Claims) Expires() (time.Time, bool) {
	if c.Kind == KindAdmin {
		return time.Time{}, false
	}
	return time.Unix(c.EXP, 0).UTC(), true
}

// Valid checks the claims are well formed. It does not check the clock.
func (c Claims) Valid() error {
	if c.V != 1 {
		return fmt.Errorf("%w: unsupported version %d", ErrBadKey, c.V)
	}
	switch c.Kind {
	case KindPatron, KindTrial, KindAdmin:
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrBadKey, c.Kind)
	}
	if !midRE.MatchString(c.MID) {
		return fmt.Errorf("%w: malformed machine id", ErrBadKey)
	}
	if c.Kind != KindAdmin && (c.EXP == 0 || c.EXP <= c.IAT) {
		return fmt.Errorf("%w: missing or invalid expiry", ErrBadKey)
	}
	return nil
}
