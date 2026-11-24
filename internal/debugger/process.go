package debugger

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type delveProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// Delve chooses a free port itself, avoiding a close/rebind race. Only accept
// the loopback address reported on the child's stdout before its first client.
func (s *Session) connect(dir, command string) (*delveProcess, net.Conn, error) {
	ready := make(chan string, 1)
	cmd := exec.Command(command, "dap", "--listen=127.0.0.1:0")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	configureProcess(cmd)
	cmd.Stdout = &startupOutput{ready: ready, output: s.addOutput}
	cmd.Stderr = writeFunc(s.addOutput)
	proc := &delveProcess{cmd: cmd, done: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	go func() {
		proc.err = cmd.Wait()
		close(proc.done)
	}()
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return proc, nil, fmt.Errorf("waiting for Delve: %w", ctx.Err())
	case <-proc.done:
		return proc, nil, fmt.Errorf("Delve exited before listening: %v", proc.err)
	case addr := <-ready:
		host, port, splitErr := net.SplitHostPort(addr)
		portNum, portErr := strconv.Atoi(port)
		if splitErr != nil || host != "127.0.0.1" || portErr != nil || portNum < 1 || portNum > 65535 {
			return proc, nil, fmt.Errorf("unexpected Delve listening address: %q", addr)
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
		return proc, conn, err
	}
}

func (p *delveProcess) close(wait bool) {
	if p == nil {
		return
	}
	select {
	case <-p.done:
		return
	default:
	}
	if wait {
		t := time.NewTimer(2 * time.Second)
		defer t.Stop()
		select {
		case <-p.done:
			return
		case <-t.C:
		}
	}
	killProcess(p.cmd)
	<-p.done
}

type writeFunc func(string)

func (f writeFunc) Write(b []byte) (int, error) { f(string(b)); return len(b), nil }

type startupOutput struct {
	ready     chan<- string
	output    writeFunc
	line      string
	readySent bool
}

func (w *startupOutput) Write(b []byte) (int, error) {
	if !w.readySent {
		w.line += string(b)
		for {
			line, rest, ok := strings.Cut(w.line, "\n")
			if !ok {
				if len(w.line) > 4096 {
					w.line = ""
				}
				break
			}
			w.line = rest
			if addr, ok := strings.CutPrefix(line, "DAP server listening at: "); ok {
				w.ready <- strings.TrimSpace(addr)
				w.readySent = true
				w.line = ""
				break
			}
		}
	}
	return w.output.Write(b)
}
