package workspace_test

import (
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/terminal"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestTerminalSpawnFailureResilience pins the terminal dock's behavior when
// the pty spawn fails (the ConPTY error report): one notice row naming the
// error, replaced — not appended — on every throttled retry, a live pane
// (typing restarts on demand), and no crash across close/reopen cycles.
func TestTerminalSpawnFailureResilience(t *testing.T) {
	ws := workspace.New("scene")
	ws.TermSync = true
	ws.Buffers = []*editor.Buffer{editor.NewBuffer("", "main.go", "package main\n")}
	ws.ShowTerminal = true
	term := terminal.New(t.TempDir())
	term.Shell = "/definitely/not/a/real/shell"
	ws.Term = term

	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl, platform.WindowOptions{Title: "scene"},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws.Measure(w)
			w.Focus(ws.FocusTerminal)
			return *ws
		})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		v := handle.View.Read(cx)
		return v.Render(&handle, cx, w)
	})
	handle.Window.Draw()

	// The failure is visible as a notice row naming the shell.
	rows := term.Output()
	if len(rows) == 0 || !strings.Contains(rows[0], "not/a/real/shell") {
		t.Fatalf("no spawn-error notice rendered, rows=%q", rows)
	}

	// The retry throttle re-spawns every second: the notice is REPLACED —
	// one status line, not one row per attempt.
	for i := 0; i < 3; i++ {
		time.Sleep(1100 * time.Millisecond)
		handle.Window.Draw()
	}
	notice := 0
	for _, r := range term.Output() {
		if strings.Contains(r, "not/a/real/shell") {
			notice++
		}
	}
	if notice != 1 {
		t.Fatalf("spawn-error notices: %d rows, want exactly 1 (replaced per retry)", notice)
	}

	// Typing into the dead terminal forces a restart attempt (still one
	// notice) and never panics.
	hw := hp.LastWindow()
	hw.DispatchInput(keyDown("a", keymap.Modifiers{}))
	handle.Window.Draw()
	hw.DispatchInput(keyDown("enter", keymap.Modifiers{}))
	handle.Window.Draw()
	notice = 0
	for _, r := range term.Output() {
		if strings.Contains(r, "not/a/real/shell") {
			notice++
		}
	}
	if notice != 1 {
		t.Fatalf("notices after typing: %d rows, want 1", notice)
	}

	// Close and reopen the dock several times: no crash, no state corruption.
	cx := appl.App
	for i := 0; i < 3; i++ {
		handle.View.Update(cx, func(state *workspace.Workspace, c *app.Context[workspace.Workspace]) any {
			state.ShowTerminal = false
			c.Notify()
			return nil
		})
		handle.Window.Draw()
		handle.View.Update(cx, func(state *workspace.Workspace, c *app.Context[workspace.Workspace]) any {
			state.ShowTerminal = true
			c.Notify()
			return nil
		})
		handle.Window.Draw()
	}
	if handle.View.Read(cx).Term == nil {
		t.Fatal("terminal disappeared after reopen")
	}
}
