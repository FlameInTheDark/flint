//go:build !windows

package terminal

import (
	"syscall"
)

// shellSysProcAttr puts the persistent shell into its own process group so
// an interrupt can signal the whole tree.
func shellSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree signals the shell's process group (the shell and every
// child it spawned).
func killProcessTree(pid int) {
	// Escalate: SIGINT lets well-behaved programs clean up, SIGKILL after a
	// grace period collects the rest.
	_ = syscall.Kill(-pid, syscall.SIGINT)
}
