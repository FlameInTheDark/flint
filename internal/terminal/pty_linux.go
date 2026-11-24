//go:build linux

package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// Linux pseudo terminal via /dev/ptmx (kernel PTYs): the shell gets a real
// controlling terminal — job control, SIGINT through the line discipline,
// SIGWINCH on resize, isatty detection, the works.

const (
	tiocgptn   = 0x80045430 // query the pts number
	tiocsptlck = 0x40045431 // unlock the pts
	tiocswinsz = 0x5414     // set the window size
)

// winsize mirrors struct winsize.
type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

// openPTY is the Linux ptyConn implementation.
type openPTY struct {
	master *os.File
	cmd    *exec.Cmd
}

func spawnShellForPlatform(shell string, args []string, dir string, env []string, cols, rows uint16) (ptyConn, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("pty: /dev/ptmx: %v", err)
	}
	// Unlock the slave (clear the lock bit) and resolve its path.
	unlock := int32(0)
	syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocsptlck, uintptr(unsafe.Pointer(&unlock)))
	var n uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocgptn, uintptr(unsafe.Pointer(&n))); errno != 0 {
		master.Close()
		return nil, fmt.Errorf("pty: TIOCGPTN: %v", errno)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: open slave: %v", err)
	}
	sz := winsize{rows: rows, cols: cols}
	syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), tiocswinsz, uintptr(unsafe.Pointer(&sz)))

	// The slave becomes the child's stdin/stdout/stderr AND its controlling
	// terminal (Setsid + Setctty): the shell is a session leader on a tty,
	// exactly like a login shell.
	cmd := exec.Command(shell, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		slave.Close()
		master.Close()
		return nil, fmt.Errorf("pty: spawn %s: %v", shell, err)
	}
	slave.Close() // the child holds its own copies
	return &openPTY{master: master, cmd: cmd}, nil
}

func (p *openPTY) Read(bs []byte) (int, error)  { return p.master.Read(bs) }
func (p *openPTY) Write(bs []byte) (int, error) { return p.master.Write(bs) }

func (p *openPTY) Resize(cols, rows uint16) error {
	sz := winsize{rows: rows, cols: cols}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, p.master.Fd(), tiocswinsz, uintptr(unsafe.Pointer(&sz))); errno != 0 {
		return fmt.Errorf("pty: TIOCSWINSZ: %v", errno)
	}
	return nil
}

// Wait blocks until the child exits (the master then reports EIO to readers).
func (p *openPTY) Wait() error {
	err := p.cmd.Wait()
	p.master.Close() // release the master for the reader's EIO/EOF
	return err
}

func (p *openPTY) Close() error {
	// The tab is closing: the controlling-terminal hangup plus the group
	// kill collect the whole tree.
	killProcessTree(p.cmd.Process.Pid)
	_ = p.cmd.Process.Kill()
	_, _ = p.cmd.Process.Wait()
	return p.master.Close()
}

// environ returns the process environment (hook point for tests).
func environ() []string { return os.Environ() }
