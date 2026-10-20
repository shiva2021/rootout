//go:build !darwin

package safety

// isRestricted is a stub: SF_RESTRICTED only exists on darwin.
func isRestricted(string) (bool, error) {
	return false, nil
}
