// Package license holds the membership gate: Ed25519-signed licence keys bound to a
// Mac (milestone M6). `Check` is a stub removed in a later task.
package license

import "context"

// Status is the outcome of a license check.
type Status struct {
	Allowed bool
	Reason  string
}

// Check reports whether the current user may use the given feature.
// Feature names are stable strings such as "remove", "scan", "llm".
func Check(_ context.Context, _ string) Status {
	return Status{Allowed: true}
}
