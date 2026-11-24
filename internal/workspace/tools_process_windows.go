//go:build windows

package workspace

import (
	"os/exec"
	"syscall"
)

func toolCommandPlatform(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}
