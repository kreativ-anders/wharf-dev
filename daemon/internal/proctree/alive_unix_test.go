//go:build unix

package proctree

import "syscall"

// alive reports whether pid is running. A killed worker is reparented to init,
// which reaps it within moments, so waitGone's polling covers the zombie.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
