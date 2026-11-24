package workspace_test

import (
	"fmt"
	"testing"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
)

// keyDown builds a KeyDownEvent with modifiers.
func keyDown(key string, mods keymap.Modifiers) platform.KeyDownEvent {
	ev := platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: key, Modifiers: mods}}
	// Printable keys carry their character (the platform layer fills it for
	// unmodified presses; typing tests rely on it).
	if key != "" && !mods.Modified() {
		ev.Keystroke.KeyChar = key
	}
	return ev
}

// TestZoomKeyboardShortcuts drives the zoom through the real keyboard
// chords (ctrl-=, ctrl+-, ctrl+0) — the regression for "zoom functionality
// does not work at all": the shortcuts were display-only menu hints and no
// key handler ever zoomed.
func TestZoomKeyboardShortcuts(t *testing.T) {
	hp, handle, cx := openEditorWindow(t)
	_ = hp
	w := handle.Window
	hw := hp.LastWindow()

	ctrl := keymap.Modifiers{Control: true}

	if z := w.Zoom(); z != 1 {
		t.Fatalf("initial zoom %v, want 1", z)
	}

	// ctrl-= zooms in one step (1.1). The editor pane holds focus, exactly
	// the situation where the old code ignored the chord.
	hw.DispatchInput(keyDown("=", ctrl))
	handle.Window.Draw()
	if z := w.Zoom(); z < 1.09 || z > 1.11 {
		t.Fatalf("ctrl+= did not zoom in: zoom=%v", z)
	}

	// ctrl+- zooms back out to 1.0 (one 1.1 step down).
	hw.DispatchInput(keyDown("-", ctrl))
	handle.Window.Draw()
	if z := w.Zoom(); z < 0.99 || z > 1.01 {
		t.Fatalf("ctrl+- did not zoom out to 1: zoom=%v", z)
	}

	// A few steps in, ctrl+0 resets.
	for i := 0; i < 3; i++ {
		hw.DispatchInput(keyDown("=", ctrl))
	}
	handle.Window.Draw()
	if z := w.Zoom(); z <= 1.2 {
		t.Fatalf("three ctrl+= did not accumulate (zoom=%v)", z)
	}
	hw.DispatchInput(keyDown("0", ctrl))
	handle.Window.Draw()
	if z := w.Zoom(); z != 1 {
		t.Fatalf("ctrl+0 did not reset zoom: zoom=%v", z)
	}

	// ctrl+shift+= (the + chord) also zooms in: the platform reports the
	// unshifted key name "=" with Shift set.
	hw.DispatchInput(keyDown("=", keymap.Modifiers{Control: true, Shift: true}))
	handle.Window.Draw()
	if z := w.Zoom(); z < 1.09 {
		t.Fatalf("ctrl+shift+= did not zoom in: zoom=%v", z)
	}

	// Ordinary typing still inserts text (the zoom handler must not swallow
	// plain keys): focus the editor, press "x", the buffer changes.
	line0, _ := handle.View.Read(cx).DebugScrollState()
	_ = line0
	before := len(handle.View.Read(cx).Buf().Text())
	hw.DispatchInput(keyDown("x", keymap.Modifiers{}))
	handle.Window.Draw()
	if after := len(handle.View.Read(cx).Buf().Text()); after != before+1 {
		t.Fatalf("plain 'x' keystroke lost after zoom fix: len %d -> %d", before, after)
	}
}

// TestZoomViaViewMenu clicks View → Zoom In through the real popup hitboxes
// and verifies the menu action path zooms too.
func TestZoomViaViewMenu(t *testing.T) {
	hp, handle, _ := openEditorWindow(t)
	w := handle.Window
	hw := hp.LastWindow()

	if z := w.Zoom(); z != 1 {
		t.Fatalf("initial zoom %v, want 1", z)
	}

	var view hitboxRect
	for _, hb := range handle.Window.RenderedHitboxes() {
		if hb.Key == "n:btn-menubar-View" {
			view = rectOf(hb)
			break
		}
	}
	if view.w == 0 {
		t.Fatal("View menu title not found")
	}
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft,
		Position: geometry.PointPixels(view.x+view.w/2, view.y+view.h/2)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft,
		Position: geometry.PointPixels(view.x+view.w/2, view.y+view.h/2)})
	handle.Window.Draw()

	// Find the actual Zoom In item; other View toggles need not stay in
	// any particular order relative to it.
	var id string
	popup := handle.View.Read(w.App()).Popup
	if popup == nil {
		t.Fatal("View menu did not open")
	}
	for i, item := range popup.Items {
		if item.Label == "Zoom In" {
			id = fmt.Sprintf("n:btn-menu-item-%d", i)
			break
		}
	}
	var zoomRow hitboxRect
	for _, hb := range handle.Window.RenderedHitboxes() {
		if hb.Key == id {
			zoomRow = rectOf(hb)
			break
		}
	}
	if zoomRow.w == 0 {
		t.Fatal("Zoom In popup row not found")
	}
	pos := geometry.PointPixels(zoomRow.x+10, zoomRow.y+zoomRow.h/2)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: pos})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: pos})
	handle.Window.Draw()

	if z := w.Zoom(); z < 1.09 {
		t.Fatalf("View → Zoom In did not zoom: zoom=%v", z)
	}
}
