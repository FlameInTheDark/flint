//go:build windows

package vulncheck

import (
	"os/exec"
	"syscall"
)

// scanCommandPlatform keeps the govulncheck process off the taskbar and
// off the screen: govulncheck is a console-subsystem executable, and a
// GUI process spawning it without CREATE_NO_WINDOW allocates a fresh
// console window that steals focus for the whole scan (the "flint opens
// a terminal when scanning" report). The same flags the tools runner
// uses in the workspace package.
func scanCommandPlatform(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}
