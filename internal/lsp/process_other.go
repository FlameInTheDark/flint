//go:build !windows

package lsp

import "os/exec"

func configureServerProcess(cmd *exec.Cmd) {}
