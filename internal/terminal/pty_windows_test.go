//go:build windows

package terminal

import (
	"os"
	"testing"
	"time"
)

// TestConPTYSmoke spawns the real Windows shell on a pseudo console and
// verifies the full session lifecycle: spawn succeeds (the inverted
// InitializeProcThreadAttributeList BOOL check used to report success —
// r1=TRUE — as "0x00000001"), output streams (the banner/prompt arrives),
// input reaches the shell ("exit\r" ends it), and Wait reaps the process.
func TestConPTYSmoke(t *testing.T) {
	if err := procCreatePseudoConsole.Find(); err != nil {
		t.Skipf("CreatePseudoConsole unavailable: %v (need Windows 10 1809+)", err)
	}
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}

	s, err := spawnShell(shell, nil, "", childEnv(), 80, 24)
	if err != nil {
		t.Fatalf("spawn %q on ConPTY: %v", shell, err)
	}

	// Output streams: the banner/prompt must arrive within the deadline.
	out := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 4096)
		n, err := s.Read(buf)
		if n > 0 {
			out <- buf[:n]
		} else {
			out <- []byte(err.Error())
		}
	}()
	select {
	case b := <-out:
		if len(b) == 0 {
			t.Fatal("no pty output after spawn")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the shell's banner")
	}

	// Input reaches the shell: "exit\r" ends the session.
	if _, err := s.Write([]byte("exit\r")); err != nil {
		t.Fatalf("write exit: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- s.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("wait: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("shell did not exit after 'exit'")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestWindowsCommandLineQuotes pins the CreateProcessW command-line
// quoting: bare argv[0], quoted arguments with spaces, embedded quotes
// escaped.
func TestWindowsCommandLineQuotes(t *testing.T) {
	if got := windowsCommandLine(`cmd.exe`); got != "cmd.exe" {
		t.Errorf("argv0 without spaces = %q, want cmd.exe", got)
	}
	if got := windowsCommandLine(`C:\Program Files\tool.exe`, `-c`, `echo hi`); got != `C:\Program Files\tool.exe -c "echo hi"` {
		t.Errorf("spaced path = %q", got)
	}
	if got := windowsCommandLine(`tool`, `he said "hi"`); got != `tool "he said \"hi\""` {
		t.Errorf("embedded quotes = %q", got)
	}
	if got := windowsCommandLine(`tool`, ``); got != `tool ""` {
		t.Errorf("empty argument = %q", got)
	}
}
