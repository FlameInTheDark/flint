// Package gotools runs Go commands without a shell and captures their output.
package gotools

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Spec is an explicit executable, argument list and working directory.
type Spec struct {
	Label, Program, Dir string
	Args                []string
	OutputDir           string
	Interactive         bool
}

// Snapshot is safe to read from the UI thread while a task runs.
type Snapshot struct {
	Spec           Spec
	Output, Status string
	Running        bool
}

// Task owns one cancellable child process. Output is bounded to keep a noisy
// build from consuming unbounded memory.
type Task struct {
	mu        sync.Mutex
	spec      Spec
	output    []byte
	status    string
	running   bool
	truncated bool
	cancel    context.CancelFunc
	done      chan struct{}
	changes   chan struct{}
	stdin     io.WriteCloser
}

const maxOutputBytes = 256 << 10

func Start(spec Spec, onChange func(*Task)) *Task {
	spec.Args = append([]string(nil), spec.Args...)
	ctx, cancel := context.WithCancel(context.Background())
	t := &Task{spec: spec, status: "Starting", running: true, cancel: cancel, done: make(chan struct{}), changes: make(chan struct{}, 1)}
	if onChange != nil {
		go func() {
			ticker := time.NewTicker(80 * time.Millisecond)
			defer ticker.Stop()
			dirty := false
			for {
				select {
				case <-t.changes:
					dirty = true
				case <-ticker.C:
					if dirty {
						onChange(t)
						dirty = false
					}
				case <-t.done:
					onChange(t)
					return
				}
			}
		}()
	}
	go t.run(ctx)
	return t
}

func (t *Task) run(ctx context.Context) {
	defer close(t.done)
	defer t.cancel()
	t.appendString(fmt.Sprintf("%s\n$ %s %s\n\n", t.spec.Dir, t.spec.Program, strings.Join(t.spec.Args, " ")))
	cmd := exec.CommandContext(ctx, t.spec.Program, t.spec.Args...)
	cmd.Dir = t.spec.Dir
	configureProcess(cmd)
	if t.spec.Interactive {
		input, err := cmd.StdinPipe()
		if err != nil {
			t.mu.Lock()
			t.status, t.running = "Failed", false
			t.mu.Unlock()
			t.appendString(err.Error() + "\n")
			return
		}
		t.mu.Lock()
		t.stdin = input
		t.mu.Unlock()
	}
	cmd.Stdout = t
	cmd.Stderr = t
	err := cmd.Run()
	t.mu.Lock()
	t.stdin = nil
	t.running = false
	switch {
	case ctx.Err() != nil:
		t.status = "Stopped"
	case err != nil:
		t.status = "Failed"
	default:
		t.status = "Finished"
	}
	status := t.status
	t.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		t.appendString("\n" + err.Error() + "\n")
	}
	if t.spec.OutputDir != "" && status == "Finished" {
		t.appendString("\nBuild output: " + t.spec.OutputDir + "\n")
	}
	t.appendString("\n" + status + "\n")
}

func (t *Task) appendString(s string) { _, _ = io.WriteString(t, s) }
func (t *Task) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.output = append(t.output, p...)
	if len(t.output) > maxOutputBytes {
		start := len(t.output) - maxOutputBytes
		for start < len(t.output) && !utf8.RuneStart(t.output[start]) {
			start++
		}
		t.output = append([]byte(nil), t.output[start:]...)
		t.truncated = true
	}
	t.mu.Unlock()
	select {
	case t.changes <- struct{}{}:
	default:
	}
	return len(p), nil
}
func (t *Task) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	output := string(t.output)
	if t.truncated {
		output = "[Earlier output truncated]\n" + output
	}
	return Snapshot{Spec: t.spec, Output: output, Status: t.status, Running: t.running}
}
func (t *Task) Stop()                 { t.cancel() }
func (t *Task) Done() <-chan struct{} { return t.done }

// SendInput submits one line to a running interactive command. Callers should
// use a background goroutine because a program may not currently read stdin.
func (t *Task) SendInput(line string) error {
	t.mu.Lock()
	input, running := t.stdin, t.running
	t.mu.Unlock()
	if input == nil || !running {
		return fmt.Errorf("the program is not accepting input")
	}
	if _, err := io.WriteString(input, line+"\n"); err != nil {
		return err
	}
	t.appendString("> " + line + "\n")
	return nil
}
