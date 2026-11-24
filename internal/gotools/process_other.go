//go:build !windows && !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package gotools

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}
