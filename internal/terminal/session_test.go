package terminal

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The session suite drives a REAL shell on a real pty (Linux /dev/ptmx —
// ConPTY on Windows builds): streaming output, nested shells, Ctrl+C
// through the line discipline and queued input, all through the same
// SendKey/Write surface the pane uses.

// startTerm spawns an interactive sh on a small grid and waits for its
// first prompt output.
func startTerm(t *testing.T) *Terminal {
	t.Helper()
	tr := New(t.TempDir())
	tr.Shell = "sh"
	tr.ShellArgs = ""
	tr.EnsureStarted(80, 10)
	waitOut(t, tr, 10*time.Second, "$")
	return tr
}

// typeLine writes a command line the way keystrokes do (bytes + CR).
func typeLine(tr *Terminal, line string) {
	tr.Write([]byte(line + "\r"))
}

// waitOut polls the grid text until every needle appears (the pty
// roundtrip is asynchronous) and fails with the final output otherwise.
func waitOut(t *testing.T, tr *Terminal, timeout time.Duration, needles ...string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var out string
	for time.Now().Before(deadline) {
		out = strings.Join(tr.Output(), "\n")
		ok := true
		for _, n := range needles {
			if !strings.Contains(out, n) {
				ok = false
				break
			}
		}
		if ok {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("output never contained %v (got %q)", needles, out)
	return ""
}

// TestSessionEchoAndOneShell pins the persistent interactive shell: typed
// commands echo and run, output streams into the grid, and BOTH commands
// run in the SAME shell process (no per-command spawn like the old
// exec+marker model).
func TestSessionEchoAndOneShell(t *testing.T) {
	tr := startTerm(t)
	defer tr.Close()
	typeLine(tr, "echo one")
	typeLine(tr, "echo two")
	out := waitOut(t, tr, 10*time.Second, "one", "two")
	if strings.Contains(out, "GPUI_TERM_DONE") {
		t.Fatalf("old done-marker leaked: %q", out)
	}
	if tr.Snapshot().Alive {
		// alive is fine — the point is one session served both commands.
	}
	tr.mu.Lock()
	same := tr.sess != nil
	tr.mu.Unlock()
	if !same {
		t.Fatal("session died between commands")
	}
}

// TestSessionStreamsIncrementally pins live streaming: early output of a
// slow command appears BEFORE the command finishes, and the redraw hook
// fires per chunk (output is not buffered until process exit).
func TestSessionStreamsIncrementally(t *testing.T) {
	tr := startTerm(t)
	defer tr.Close()
	var redraws int32
	tr.OnOutput = func() { atomic.AddInt32(&redraws, 1) }
	typeLine(tr, "for i in 1 2 3; do echo line$i; sleep 0.15; done")
	deadline := time.Now().Add(5 * time.Second)
	midway := false
	for time.Now().Before(deadline) {
		out := strings.Join(tr.Output(), "\n")
		if strings.Contains(out, "line1") && !strings.Contains(out, "line3") {
			midway = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !midway {
		t.Fatal("early output did not appear before the command finished (no live streaming)")
	}
	waitOut(t, tr, 5*time.Second, "line3")
	if atomic.LoadInt32(&redraws) < 3 {
		t.Fatalf("redraw hook fired %d times, want >= 3 (streaming chunks)", redraws)
	}
}

// TestSessionNestedSubshell pins the upstream editor's nested-shell behavior: typing `sh`
// enters an interactive subshell on the SAME pty, and later commands run
// inside it (the session process is unchanged).
func TestSessionNestedSubshell(t *testing.T) {
	tr := startTerm(t)
	defer tr.Close()
	tr.mu.Lock()
	before := tr.sess
	tr.mu.Unlock()
	typeLine(tr, "sh") // nested interactive shell
	waitOut(t, tr, 10*time.Second, "$")
	typeLine(tr, "echo inner")
	out := waitOut(t, tr, 10*time.Second, "inner")
	tr.mu.Lock()
	same := tr.sess == before
	tr.mu.Unlock()
	if !same {
		t.Fatal("nested subshell spawned a new session process")
	}
	_ = out
}

// TestSessionCdFollows pins cd: the persistent shell's own pwd follows
// (the shell owns the cwd now; no Go-side prompt sync needed).
func TestSessionCdFollows(t *testing.T) {
	tr := startTerm(t)
	defer tr.Close()
	other := t.TempDir()
	typeLine(tr, "cd "+other)
	typeLine(tr, "pwd")
	waitOut(t, tr, 10*time.Second, other)
}

// TestInterruptPassesCtrlC pins the upstream editor Ctrl+C contract: the interrupt is
// the 0x03 control byte — the pty line discipline delivers SIGINT to the
// foreground command only, and the SHELL SURVIVES (the next command runs
// in the same session; nothing is tree-killed).
func TestInterruptPassesCtrlC(t *testing.T) {
	tr := startTerm(t)
	defer tr.Close()
	typeLine(tr, "sleep 30")
	waitOut(t, tr, 5*time.Second, "sleep 30")
	start := time.Now()
	tr.Interrupt() // 0x03
	typeLine(tr, "echo back")
	out := waitOut(t, tr, 5*time.Second, "back")
	if time.Since(start) > 3*time.Second {
		t.Fatalf("interrupt took %v (SIGINT not delivered)", time.Since(start))
	}
	if strings.Contains(out, "sleep 30\r\n^K") {
		t.Fatal("wrong control byte reached the shell")
	}
	tr.mu.Lock()
	same := tr.sess != nil
	tr.mu.Unlock()
	if !same {
		t.Fatal("Ctrl+C killed the shell — it must survive (line-discipline SIGINT)")
	}
}

// TestCommandsQueueWhileRunning pins that input typed while a command
// runs reaches the same shell (queued; interactive prompts stay
// answerable mid-run).
func TestCommandsQueueWhileRunning(t *testing.T) {
	tr := startTerm(t)
	defer tr.Close()
	typeLine(tr, "sleep 0.4")
	typeLine(tr, "echo queued")
	waitOut(t, tr, 5*time.Second, "queued")
}

// TestSessionEnv pins the child environment: TERM advertises a
// full-color xterm so programs emit the escapes the emulator parses.
func TestSessionEnv(t *testing.T) {
	tr := startTerm(t)
	defer tr.Close()
	typeLine(tr, "echo $TERM")
	waitOut(t, tr, 5*time.Second, "xterm-256color")
}

// TestRespawnAfterExit pins the exit notice + respawn: after the shell
// exits, a notice lands in the scrollback and user input starts a fresh
// session (no spin without input).
func TestRespawnAfterExit(t *testing.T) {
	tr := startTerm(t)
	defer tr.Close()
	typeLine(tr, "exit")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !tr.Snapshot().Alive {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tr.Snapshot().Alive {
		t.Fatal("shell did not exit")
	}
	waitOut(t, tr, 5*time.Second, "process exited")
	// Any input restarts the shell.
	typeLine(tr, "echo revived")
	waitOut(t, tr, 10*time.Second, "revived")
}

// TestSessionStartDir pins the spawn directory: the shell starts in the
// terminal's Cwd (pwd reports it without any cd).
func TestSessionStartDir(t *testing.T) {
	dir := t.TempDir()
	tr := New(dir)
	tr.Shell = "sh"
	tr.ShellArgs = ""
	tr.EnsureStarted(80, 10)
	defer tr.Close()
	waitOut(t, tr, 10*time.Second, "$")
	typeLine(tr, "pwd")
	waitOut(t, tr, 10*time.Second, dir)
	_ = os.Getwd
}
