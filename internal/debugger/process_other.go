//go:build !windows && !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package debugger

import "os/exec"

func configureProcess(*exec.Cmd) {}

func killProcess(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
