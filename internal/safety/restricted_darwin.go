//go:build darwin

package safety

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// sfRestricted is the SF_RESTRICTED chflags bit: the file is protected by
// System Integrity Protection.
const sfRestricted = 0x80000

// isRestricted reports whether path carries the SF_RESTRICTED flag. A missing
// path is not restricted.
func isRestricted(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false, nil
	}
	return st.Flags&sfRestricted != 0, nil
}
