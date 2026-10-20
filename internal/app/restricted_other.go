//go:build !darwin

package app

// restricted always reports false off macOS; SF_RESTRICTED is a Darwin flag.
func restricted(string) bool { return false }
