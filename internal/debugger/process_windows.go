//go:build windows

package debugger

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}

func killProcess(cmd *exec.Cmd) {
	// A cancelled launch can still be compiling. Reap the child tree if
	// Delve could not handle DAP disconnect, rather than just its parent.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	kill := exec.CommandContext(ctx, "taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	configureProcess(kill)
	if err := kill.Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
