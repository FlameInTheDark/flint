package workspace_test

import (
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// openFullScene opens the workspace with editor buffers, a project folder
// (so the tree and its resize grip render) and the terminal dock visible.
func openFullScene(t *testing.T, dir string) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl, platform.WindowOptions{Title: "regression-scene"},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws := workspace.New("regression-scene")
			ws.Measure(w)
			ws.TermSync = true
			ws.Buffers = workspace.BenchBuffers()
			if err := ws.OpenFolder(dir); err != nil {
				t.Fatal(err)
			}
			ws.ShowTerminal = true
			ws.Term = newTestTerminal(t)
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
	return hp, &handle, appl.App
}

// Regression: dragging a resize edge (terminal dock header, tree grip)
// with the left button held used to deliver hover moves to the editor
// under the cursor, silently extending its selection. Pointer capture is
// now exclusive, so nothing under the drag path may change.
func TestResizeDragNoEditorSelection(t *testing.T) {
	hp, handle, cx := openFullScene(t, t.TempDir())
	hw := hp.LastWindow()
	handle.Window.Draw()

	ws := handle.View.Read(cx)
	cur0, anc0 := ws.Buffers[ws.Active].Cursor, ws.Buffers[ws.Active].Anchor

	// (a) Drag the terminal dock's header upward across the editor area.
	termH0, _ := ws.DebugPanelSizes()
	topY := 650.0 - ui.StatusHeight - termH0
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(500, topY+14)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(380, 300), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(200, 200), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(200, 200)})
	handle.Window.Draw()

	ws = handle.View.Read(cx)
	cur1, anc1 := ws.Buffers[ws.Active].Cursor, ws.Buffers[ws.Active].Anchor
	if cur1 != cur0 || anc1 != anc0 {
		t.Fatalf("terminal resize drag changed the editor selection: cursor %d->%d anchor %d->%d", cur0, cur1, anc0, anc1)
	}
	termH1, _ := ws.DebugPanelSizes()
	if termH1 < termH0+100 {
		t.Fatalf("header drag did not resize the dock: %.0f -> %.0f", termH0, termH1)
	}

	// (b) Drag the tree's resize grip right, across the editor text.
	_, treeW0 := ws.DebugPanelSizes()
	gripX := float32(treeW0 - ui.TreeResizeGrip/2)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(gripX, 300)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(gripX+120, 280), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(gripX+120, 280)})
	handle.Window.Draw()

	ws = handle.View.Read(cx)
	cur2, anc2 := ws.Buffers[ws.Active].Cursor, ws.Buffers[ws.Active].Anchor
	if cur2 != cur0 || anc2 != anc0 {
		t.Fatalf("tree grip drag changed the editor selection: cursor %d->%d anchor %d->%d", cur0, cur2, anc0, anc2)
	}
	_, treeW1 := ws.DebugPanelSizes()
	if treeW1 < treeW0+100 {
		t.Fatalf("grip drag did not resize the tree: %.0f -> %.0f", treeW0, treeW1)
	}

	// (c) After the drags, ordinary selection must still work: press and
	// drag inside the editor text area (the dock grew in (a), so the
	// editor now ends around y=200 — stay well above it).
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(400, 150)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(430, 150), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(430, 150)})
	handle.Window.Draw()
	ws = handle.View.Read(cx)
	cur3, anc3 := ws.Buffers[ws.Active].Cursor, ws.Buffers[ws.Active].Anchor
	if cur3 == anc3 && cur3 == cur0 {
		t.Fatalf("editor selection broke after resize drags: cursor %d anchor %d", cur3, anc3)
	}
}

// Regression: the win32 backend resolves KeyChar only for single-character
// key names, so the space bar (key name "space") reached text surfaces
// with an empty KeyChar and the terminal dropped it. The window now fills
// the space character at dispatch; simulate the raw backend event shape
// (Key "space", no KeyChar) here.
func TestTerminalSpaceKey(t *testing.T) {
	hp, handle, cx := openTerminalScene(t)
	hw := hp.LastWindow()

	// "l", "s", space (raw win32 shape: no KeyChar), "-", "l", "a".
	typeChar := func(k, ch string) {
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: k, KeyChar: ch}})
	}
	typeChar("l", "l")
	typeChar("s", "s")
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "space"}})
	typeChar("-", "-")
	typeChar("l", "l")
	typeChar("a", "a")
	handle.Window.Draw()

	ws := handle.View.Read(cx)
	var written []byte
	done := make(chan struct{})
	go func() {
		// The keystrokes stream to the shell as raw bytes: collect them
		// through the fake write sink (installed after the keys landed).
		written = nil
		close(done)
	}()
	_ = written
	_ = done
	// The space key must reach the shell like every other character: the
	// keystrokes were dispatched to the focused terminal above — verify
	// through a fresh sink replay of the same keys.
	var seq []byte
	ws.Term.SetTestWriteSink(func(b []byte) { seq = append(seq, b...) })
	for _, k := range []struct{ key, ch string }{{"l", "l"}, {"s", "s"}, {"space", ""}, {"-", "-"}, {"l", "l"}, {"a", "a"}} {
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: k.key, KeyChar: k.ch}})
	}
	handle.Window.Draw()
	ws.Term.SetTestWriteSink(nil)
	if got := string(seq); got != "ls -la" {
		t.Fatalf("terminal input = %q, want %q (space dropped?)", got, "ls -la")
	}
}
