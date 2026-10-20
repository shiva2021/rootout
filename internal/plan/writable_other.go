//go:build !darwin

package plan

func writable(string) bool { return true }
