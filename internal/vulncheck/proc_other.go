//go:build !windows

package vulncheck

import "os/exec"

// scanCommandPlatform is the no-op console suppression for platforms
// without console windows.
func scanCommandPlatform(*exec.Cmd) {}
