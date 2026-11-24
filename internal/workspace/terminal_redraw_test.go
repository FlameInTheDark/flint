package workspace

import (
	"testing"

	"github.com/FlameInTheDark/flint/internal/terminal"
)

func TestTerminalRedrawRequiresUIDispatcher(t *testing.T) {
	ws := &Workspace{Term: terminal.New(t.TempDir()), ShowTerminal: true}
	frames := 0
	draw := func() { frames++ }
	ws.wireTerminalRedraw(draw)
	if ws.Term.OnOutput != nil {
		t.Fatal("terminal installed a redraw hook without a UI dispatcher")
	}

	queue := make(chan func(), 1)
	ws.SetEnv(nil, func(f func()) { queue <- f }, nil)
	ws.wireTerminalRedraw(draw)
	hook := ws.Term.OnOutput
	if hook == nil {
		t.Fatal("terminal did not install a redraw hook with a UI dispatcher")
	}
	// Simulate the PTY worker without requiring an actual shell.
	done := make(chan struct{})
	go func() {
		hook()
		close(done)
	}()
	<-done
	if frames != 0 {
		t.Fatal("terminal drew inline on the worker goroutine")
	}
	select {
	case f := <-queue:
		f()
	default:
		t.Fatal("terminal did not queue a draw on the UI dispatcher")
	}
	if frames != 1 {
		t.Fatalf("queued draw produced %d frames, want one", frames)
	}

	ws.TermSync = true
	ws.wireTerminalRedraw(draw)
	if ws.Term.OnOutput != nil {
		t.Fatal("synchronous terminal retained its async redraw hook")
	}
}
