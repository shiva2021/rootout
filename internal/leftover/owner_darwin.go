//go:build darwin

package leftover

import (
	"os"
	"syscall"
)

// wOK is the access(2) W_OK mode bit.
const wOK = 0x2

// lstatUID returns the owner uid of path without following symlinks.
func lstatUID(path string) (int, bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

// accessWrite reports whether the current user may write to dir.
func accessWrite(dir string) bool {
	return syscall.Access(dir, wOK) == nil
}
