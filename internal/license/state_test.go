package license

import (
	"testing"
	"time"
)

func TestDaysLeft(t *testing.T) {
	for d, want := range map[time.Duration]int{-time.Hour: 0, 0: 0, time.Minute: 1, 24 * time.Hour: 1, 25 * time.Hour: 2} {
		if got := daysLeft(testNow.Add(d), testNow); got != want {
			t.Errorf("daysLeft(%v) = %d, want %d", d, got, want)
		}
	}
}

func TestSummary(t *testing.T) {
	exp := time.Date(2026, 12, 4, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		s    State
		want string
	}{
		{State{Status: StatusAdmin}, "admin"},
		{State{Status: StatusMember, Expires: exp}, "member · renews 2026-12-04"},
		{State{Status: StatusTrial, DaysLeft: 5}, "trial · 5 days left"},
		{State{Status: StatusTrial, DaysLeft: 1}, "trial · 1 day left"},
		{State{Status: StatusGrace, Expires: exp}, "grace · expired 2026-12-04"},
		{State{Status: StatusExpired, Expires: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)}, "expired 2026-11-06"},
		{State{Status: StatusTrialUsed}, "trial used on this Mac"},
		{State{Status: StatusNone}, "no key"},
		{State{Status: StatusInvalid, Reason: "signature mismatch"}, "invalid key: signature mismatch"},
		{State{Status: StatusNoMachine}, "could not read this Mac's id"},
	}
	for _, tt := range tests {
		if got := tt.s.Summary(); got != tt.want {
			t.Errorf("Summary(%s) = %q, want %q", tt.s.Status, got, tt.want)
		}
	}
}
