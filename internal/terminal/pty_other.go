//go:build !windows && !linux

package terminal

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Fallback for unix platforms without the Linux kernel-PTY ioctls: the
// shell runs with piped stdio (no controlling terminal). Everything else
// — streaming output, the VT emulator, interactive write-side input —
// works the same; only job control and isatty-dependent behavior differ.

// openPTY is the pipe fallback ptyConn.
type openPTY struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
}

func spawnShellForPlatform(shell string, args []string, dir string, env []string, cols, rows uint16) (ptyConn, error) {
	cmd := exec.Command(shell, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = shellSysProcAttr()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout // one stream keeps VT ordering intact
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %v", shell, err)
	}
	return &openPTY{stdin: stdin, stdout: stdout, cmd: cmd}, nil
}

func (p *openPTY) Read(bs []byte) (int, error)  { return p.stdout.Read(bs) }
func (p *openPTY) Write(bs []byte) (int, error) { return p.stdin.Write(bs) }
func (p *openPTY) Resize(cols, rows uint16) error {
	return nil // no tty to resize
}
func (p *openPTY) Wait() error {
	_ = p.stdout.Close()
	return p.cmd.Wait()
}
func (p *openPTY) Close() error {
	killProcessTree(p.cmd.Process.Pid)
	return p.cmd.Process.Kill()
}

// environ returns the process environment (hook point for tests).
func environ() []string { return os.Environ() }
