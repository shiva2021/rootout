//go:build contract

package license

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestContractTrialThenRenewDenied runs the Client against the real key
// service (wrangler dev) and checks the keys it issues verify with testPub.
// It makes 4 requests, under the service's 5 per minute per IP limit.
func TestContractTrialThenRenewDenied(t *testing.T) {
	base := os.Getenv("ROOTOUT_SERVICE_URL")
	if base == "" {
		t.Skip("ROOTOUT_SERVICE_URL not set")
	}
	c := Client{BaseURL: base, UserAgent: "rootout-contract-test"}
	ctx := context.Background()

	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	mid := hex.EncodeToString(b[:])

	key, err := c.Trial(ctx, mid)
	if err != nil {
		t.Fatalf("Trial: %v", err)
	}
	claims, err := Decode(key, testPub)
	if err != nil {
		t.Fatalf("Decode trial key: %v", err)
	}
	if claims.Kind != KindTrial {
		t.Errorf("kind = %q, want trial", claims.Kind)
	}
	if claims.MID != mid {
		t.Errorf("mid = %q, want %q", claims.MID, mid)
	}
	if got := claims.EXP - claims.IAT; got != int64((7 * 24 * time.Hour).Seconds()) {
		t.Errorf("exp-iat = %ds, want 7 days", got)
	}

	if _, err := c.Trial(ctx, mid); !errors.Is(err, ErrTrialUsed) {
		t.Errorf("second Trial err = %v, want ErrTrialUsed", err)
	}

	_, err = c.Renew(ctx, key)
	var denied *DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("Renew(trial key) err = %v, want *DeniedError", err)
	}
	if denied.Reason != "not_renewable" {
		t.Errorf("reason = %q, want not_renewable", denied.Reason)
	}

	// The service answers 400 bad_key; Client maps every status other than
	// 200, 403 and 409 to ErrServiceUnavailable.
	_, err = c.Renew(ctx, "ro1.garbage.garbage")
	if !errors.Is(err, ErrServiceUnavailable) || !strings.Contains(err.Error(), "400") {
		t.Errorf("Renew(garbage) err = %v, want ErrServiceUnavailable answering 400", err)
	}
}
