package gotools

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTaskHelperProcess(t *testing.T) {
	if os.Getenv("GO_GPUI_TASK_HELPER") != "1" {
		return
	}
	fmt.Print("task-output\n")
	if strings.Contains(strings.Join(os.Args, " "), "wait-mode") {
		time.Sleep(30 * time.Second)
	}
	if strings.Contains(strings.Join(os.Args, " "), "fail-mode") {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestTaskCapturesOutputAndFailure(t *testing.T) {
	t.Setenv("GO_GPUI_TASK_HELPER", "1")
	for _, tc := range []struct{ name, arg, status string }{{"success", "ok-mode", "Finished"}, {"failure", "fail-mode", "Failed"}} {
		t.Run(tc.name, func(t *testing.T) {
			task := Start(Spec{Label: "test", Program: os.Args[0], Dir: t.TempDir(), Args: []string{"-test.run=^TestTaskHelperProcess$", "--", tc.arg}}, nil)
			select {
			case <-task.Done():
			case <-time.After(10 * time.Second):
				task.Stop()
				t.Fatal("task timed out")
			}
			s := task.Snapshot()
			if s.Running || s.Status != tc.status || !strings.Contains(s.Output, "task-output") {
				t.Fatalf("snapshot = %+v", s)
			}
		})
	}
}

func TestTaskCanBeStopped(t *testing.T) {
	t.Setenv("GO_GPUI_TASK_HELPER", "1")
	updates := make(chan *Task, 16)
	task := Start(Spec{Label: "test", Program: os.Args[0], Dir: t.TempDir(), Args: []string{"-test.run=^TestTaskHelperProcess$", "--", "wait-mode"}}, func(task *Task) {
		select {
		case updates <- task:
		default:
		}
	})
	deadline := time.After(10 * time.Second)
	for !strings.Contains(task.Snapshot().Output, "task-output") {
		select {
		case <-updates:
		case <-deadline:
			task.Stop()
			t.Fatal("process did not start")
		}
	}
	task.Stop()
	select {
	case <-task.Done():
	case <-deadline:
		t.Fatal("process did not stop")
	}
	if got := task.Snapshot().Status; got != "Stopped" {
		t.Fatalf("status=%q", got)
	}
}
