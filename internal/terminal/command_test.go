package terminal

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/config"
)

func TestCommandHelper(t *testing.T) {
	if os.Getenv("GO_GPUI_PTY_HELPER") != "1" {
		return
	}
	fmt.Print("direct-command-ready\r\n")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(0)
}

func TestDelveInteractiveDebugSession(t *testing.T) {
	dlv, err := config.ResolveLanguageServerCommand("dlv")
	if err != nil {
		t.Skip("Delve is not installed:", err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/debug\n\ngo 1.23\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nimport \"fmt\"\nfunc main(){ fmt.Println(\"hello\") }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "debug-app")
	term := NewCommand(dir, dlv, "debug", ".", "--output", output, "--build-flags=-mod=readonly")
	defer term.Close()
	updates := make(chan struct{}, 32)
	term.SetOutputHook(func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	})
	term.EnsureStarted(100, 20)
	deadline := time.After(45 * time.Second)
	wait := func(needle string) {
		t.Helper()
		for !strings.Contains(strings.Join(term.Output(), "\n"), needle) {
			select {
			case <-updates:
			case <-deadline:
				t.Fatalf("Delve output missing %q: %s", needle, strings.Join(term.Output(), "\n"))
			}
		}
	}
	wait("(dlv)")
	term.Write([]byte("break main.main\r"))
	wait("Breakpoint 1")
	term.Write([]byte("continue\r"))
	wait("[Breakpoint 1]")
	term.Write([]byte("exit\ry\r"))
}

func TestNewCommandUsesInteractivePTYWithoutRestart(t *testing.T) {
	t.Setenv("GO_GPUI_PTY_HELPER", "1")
	term := NewCommand(t.TempDir(), os.Args[0], "-test.run=^TestCommandHelper$")
	defer term.Close()
	updates := make(chan struct{}, 16)
	term.SetOutputHook(func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	})
	term.EnsureStarted(80, 10)
	deadline := time.After(10 * time.Second)
	for !strings.Contains(strings.Join(term.Output(), "\n"), "direct-command-ready") {
		select {
		case <-updates:
		case <-deadline:
			t.Fatalf("direct program did not start in PTY: output=%q snapshot=%+v", strings.Join(term.Output(), "\n"), term.Snapshot())
		}
	}
	term.Write([]byte("\r"))
	for term.Snapshot().Alive {
		select {
		case <-updates:
		case <-deadline:
			t.Fatal("direct program did not exit")
		}
	}
	term.EnsureStarted(80, 10)
	if term.Snapshot().Alive {
		t.Fatal("one-shot debugger restarted after exit")
	}
}
