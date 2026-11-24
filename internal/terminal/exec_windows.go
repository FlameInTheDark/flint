//go:build windows

package terminal

import (
	"fmt"
	"os/exec"
	"syscall"
)

// createNoWindow suppresses the console window a console-subsystem child
// would otherwise allocate when spawned by a GUI-subsystem parent (the
// editor is built with -H=windowsgui / FreeConsole): the shell and every
// helper process run headless against our pipes.
const createNoWindow = 0x08000000

// shellSysProcAttr flags the persistent shell to run without a window.
func shellSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNoWindow}
}

// killProcessTree force-terminates the shell and its whole tree. A GUI app
// has no console to GenerateConsoleCtrlEvent into, so taskkill /T is the
// reliable interrupt (the terminal respawn replaces the session).
func killProcessTree(pid int) {
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	_ = cmd.Run()
}
