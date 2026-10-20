//go:build darwin

package app

import "syscall"

// sfRestricted is the SF_RESTRICTED BSD flag: the file is protected by
// System Integrity Protection.
const sfRestricted = 0x80000

// restricted reports whether path carries the SF_RESTRICTED flag.
func restricted(path string) bool {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return false
	}
	return st.Flags&sfRestricted != 0
}
