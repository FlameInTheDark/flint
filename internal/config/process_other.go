//go:build !windows

package config

import "os/exec"

func hideHelperWindow(cmd *exec.Cmd) {}
