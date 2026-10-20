//go:build !darwin

package leftover

// lstatUID is a stub: ownership checks only matter on darwin.
func lstatUID(string) (int, bool) { return 0, false }

// accessWrite is a stub that reports every directory as writable.
func accessWrite(string) bool { return true }
