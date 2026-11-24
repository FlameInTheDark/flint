//go:build windows

package config

import (
	"os/exec"
	"syscall"
)

func hideHelperWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}
