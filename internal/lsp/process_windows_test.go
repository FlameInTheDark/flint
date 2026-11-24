//go:build windows

package lsp

import (
	"os/exec"
	"testing"
)

func TestServerProcessHasNoConsole(t *testing.T) {
	cmd := exec.Command("gopls")
	configureServerProcess(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&0x08000000 == 0 || !cmd.SysProcAttr.HideWindow {
		t.Fatal("language server would create a console window")
	}
}
