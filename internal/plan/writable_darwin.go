//go:build darwin

package plan

import (
	"path/filepath"
	"syscall"
)

// writable reports whether the current user can remove path (needs write
// access to its parent directory).
func writable(path string) bool {
	return syscall.Access(filepath.Dir(path), 0x2 /* W_OK */) == nil
}
