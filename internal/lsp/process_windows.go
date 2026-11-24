//go:build windows

package lsp

import (
	"os/exec"
	"syscall"
)

// Language servers speak over pipes and must never create a console window.
func configureServerProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}
