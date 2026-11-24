//go:build windows

package config

import (
	"os/exec"
	"testing"
)

func TestGoEnvHasNoConsole(t *testing.T) {
	cmd := exec.Command("go")
	hideHelperWindow(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&0x08000000 == 0 || !cmd.SysProcAttr.HideWindow {
		t.Fatal("go env helper would create a console window")
	}
}
