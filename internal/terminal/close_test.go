package terminal

import (
	"io"
	"testing"
	"time"
)

// blockingPty is a ptyConn whose Close blocks until released — standing in
// for the Windows ConPTY teardown, which waits for the attached clients
// and conhost's final output flush and can hang on a fast open→close→open
// cycle when nobody drains the output pipe.
type blockingPty struct {
	entered chan struct{} // closed once Close started
	release chan struct{} // closed to let Close/Wait/Read finish
}

func (p *blockingPty) Read(b []byte) (int, error) {
	<-p.release
	return 0, io.EOF
}

func (p *blockingPty) Write(b []byte) (int, error) { return len(b), nil }

func (p *blockingPty) Resize(cols, rows uint16) error { return nil }

func (p *blockingPty) Wait() error {
	<-p.release
	return nil
}

func (p *blockingPty) Close() error {
	close(p.entered)
	<-p.release
	return nil
}

// TestTerminalCloseDoesNotBlock pins the tab-close freeze fix: the UI
// thread calls Terminal.Close when a terminal tab (or the whole dock)
// closes, and that call must never wait on the pty teardown — the
// ClosePseudoConsole stall behind "opened and closed terminal twice and
// the editor froze" happened exactly there.
func TestTerminalCloseDoesNotBlock(t *testing.T) {
	p := &blockingPty{entered: make(chan struct{}), release: make(chan struct{})}
	tr := New("")
	tr.mu.Lock()
	tr.sess = p
	tr.started = true
	tr.emu = newEmu(80, 24, 1000)
	tr.mu.Unlock()
	go tr.readLoop(p)
	go tr.waitLoop(p)

	start := time.Now()
	tr.Close()
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("Close blocked for %v on the pty teardown", d)
	}

	// The teardown runs in the background and the session is detached:
	// the session was dropped, so sessionDied must not respawn or notice.
	tr.mu.Lock()
	detached := tr.sess == nil && tr.closed
	tr.mu.Unlock()
	if !detached {
		t.Fatal("Close did not detach the session")
	}

	// Let the background teardown finish; the reader/waiter unwind via
	// sessionDied without touching the detached state.
	close(p.release)
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("the background teardown never ran")
	}

	// A closed terminal never respawns a shell on writes.
	tr.Write([]byte("echo hi"))
	tr.mu.Lock()
	respawned := tr.sess != nil
	tr.mu.Unlock()
	if respawned {
		t.Fatal("a closed terminal respawned its shell")
	}
}
