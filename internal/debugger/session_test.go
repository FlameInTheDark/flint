package debugger

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDAPFrames(t *testing.T) {
	one := `{"type":"event","event":"output","body":{"output":"こんにちは"}}`
	two := `{"type":"response","request_seq":25,"success":true}`
	wire := func(body string) string { return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body) }
	r := bufio.NewReader(strings.NewReader(wire(one) + wire(two)))
	if got, err := readMessage(r); err != nil || got.Event != "output" || !strings.Contains(string(got.Body), "こんにちは") {
		t.Fatalf("first frame: %+v %v", got, err)
	}
	if got, err := readMessage(r); err != nil || got.RequestSeq != 25 || !got.Success {
		t.Fatalf("second frame: %+v %v", got, err)
	}
	for _, invalid := range []string{"\r\n", "Content-Length: -1\r\n\r\n", "Content-Length: 16777217\r\n\r\n", "Content-Length: 7\r\n\r\n{}"} {
		if _, err := readMessage(bufio.NewReader(strings.NewReader(invalid))); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}

func TestDelveIntegration(t *testing.T) {
	dlv := os.Getenv("GO_GPUI_TEST_DLV")
	if dlv == "" {
		var err error
		dlv, err = exec.LookPath("dlv")
		if err != nil {
			t.Skip("Delve not on PATH; set GO_GPUI_TEST_DLV to its absolute path")
		}
	}
	write := func(t *testing.T, dir, name, src string) string {
		t.Helper()
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
		return file
	}
	start := func(t *testing.T, dir, mode, file string, line int, args ...string) *Session {
		t.Helper()
		write(t, dir, "go.mod", "module example.test/debugger\n\ngo 1.23\n")
		var points map[string][]int
		if line > 0 {
			points = map[string][]int{file: {line}}
		}
		s := Start(dlv, dir, mode, points, args...)
		t.Cleanup(func() {
			s.Stop()
			select {
			case <-s.Done():
			case <-time.After(8 * time.Second):
				t.Error("Delve did not shut down")
			}
		})
		return s
	}
	t.Run("main", func(t *testing.T) {
		dir := t.TempDir()
		file := write(t, dir, "main.go", `package main

func main() {
	println(value())
	println("finished app")
}

func value() int {
	item := struct{ N int }{41}
	item.N++
	return item.N
}
`)
		s := start(t, dir, "debug", file, 10)
		snap := waitSnapshot(t, s, func(s Snapshot) bool { return s.State == Paused && len(s.Variables) > 0 })
		bp := snap.Breakpoints[file][10]
		if !bp.Verified || len(snap.Frames) < 2 || snap.Frames[0].Line != 10 || !strings.HasSuffix(snap.Frames[0].Name, ".value") {
			t.Fatalf("unexpected stop: %+v; breakpoint %+v", snap, bp)
		}
		ref := 0
		for _, locals := range snap.Variables {
			for _, v := range locals {
				if v.Name == "item" {
					ref = v.VariablesReference
				}
			}
		}
		if ref == 0 {
			t.Fatalf("missing expandable local: %+v", snap.Variables)
		}
		s.Request(Expand, ref, snap.StopID)
		snap = waitSnapshot(t, s, func(s Snapshot) bool { return len(s.Variables[ref]) > 0 })
		if got := snap.Variables[ref][0]; got.Name != "N" || got.Value != "41" {
			t.Fatalf("nested field: %+v", got)
		}
		s.Request(SelectFrame, 1, snap.StopID)
		snap = waitSnapshot(t, s, func(s Snapshot) bool { return s.Selected == 1 })
		if snap.Frames[1].Line != 4 {
			t.Fatalf("caller: %+v", snap.Frames[1])
		}
		s.SetBreakpoints(file, nil)
		s.Request(Next, 0, snap.StopID)
		stepped := waitSnapshot(t, s, func(next Snapshot) bool { return next.StopID > snap.StopID && next.Selected == 0 })
		if stepped.Frames[0].Line != 11 || len(stepped.Breakpoints[file]) != 0 {
			t.Fatalf("step: %+v", stepped)
		}
		s.Request(Continue, 0, stepped.StopID)
		finished := waitSnapshot(t, s, func(s Snapshot) bool { return s.State == Finished })
		if finished.Detail != "Program finished" || !strings.Contains(finished.Output, "finished app") {
			t.Fatalf("finished: %+v", finished)
		}
	})
	t.Run("test and stop", func(t *testing.T) {
		dir := t.TempDir()
		file := write(t, dir, "example_test.go", `package example
import "testing"
func TestExample(t *testing.T) {
	t.Log("running test")
}
`)
		s := start(t, dir, "test", file, 4)
		snap := waitSnapshot(t, s, func(s Snapshot) bool { return s.State == Paused && s.Selected == 0 })
		if snap.Frames[0].Line != 4 || !strings.HasSuffix(snap.Frames[0].Name, ".TestExample") {
			t.Fatalf("not stopped in test: %+v", snap)
		}
		s.Stop()
		if end := waitSnapshot(t, s, func(s Snapshot) bool { return s.State == Finished }); end.Detail != "Session stopped" {
			t.Fatalf("stopping: %+v", end)
		}
	})
	t.Run("pause running program with arguments", func(t *testing.T) {
		dir := t.TempDir()
		file := write(t, dir, "main.go", `package main
import (
 "os"
 "runtime"
)
func main(){
 println(os.Args[1])
 for { runtime.Gosched() }
}
`)
		s := start(t, dir, "debug", file, 0, "literal arg with spaces")
		run := waitSnapshot(t, s, func(s Snapshot) bool {
			return s.State == Running && strings.Contains(s.Output, "literal arg with spaces")
		})
		s.Request(Pause, 0, run.StopID)
		paused := waitSnapshot(t, s, func(s Snapshot) bool { return s.State == Paused })
		s.Request(Continue, 0, paused.StopID)
		waitSnapshot(t, s, func(s Snapshot) bool { return s.State == Running })
	})
}

func waitSnapshot(t *testing.T, s *Session, ok func(Snapshot) bool) Snapshot {
	t.Helper()
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	for {
		snap := s.Snapshot()
		if ok(snap) {
			return snap
		}
		if snap.State == Finished {
			t.Fatalf("session finished unexpectedly: %+v", snap)
		}
		select {
		case <-s.Changes():
		case <-s.Done():
		case <-timer.C:
			t.Fatalf("timed out: %+v", snap)
		}
	}
}
