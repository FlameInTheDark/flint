//go:build !windows

package workspace

import "os/exec"

func toolCommandPlatform(*exec.Cmd) {}
