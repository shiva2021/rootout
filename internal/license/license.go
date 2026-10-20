// Package license is the seam where subscription checks will live (milestone
// M6, decision D-008). Until then every check is allowed.
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
