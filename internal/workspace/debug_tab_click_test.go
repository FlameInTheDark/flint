package workspace

import (
	"testing"

	"github.com/FlameInTheDark/flint/internal/debugger"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

// openDebugTabWindow opens a workspace with a paused offline debug session
// and the debugger panel visible, drawn and ready for input.
func openDebugTabWindow(t *testing.T) (*headless.Platform, *gpui.WindowHandle[Workspace], *gpui.Application, func(f func(*Workspace))) {
	t.Helper()
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp := headless.New(geometry.SizePixels(1000, 650))
	a := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{Title: "debug-tab-click"},
		func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
			ws := New("debug-tab-click")
			ws.Measure(w)
			return *ws
		})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element {
		return handle.View.Read(c).Render(&handle, c, w)
	})
	handle.Window.Draw()
	update := func(f func(*Workspace)) {
		handle.View.Update(a.App, func(s *Workspace, c *app.Context[Workspace]) any {
			f(s)
			c.Notify()
			return nil
		})
	}
	update(func(ws *Workspace) {
		ws.debug.session = debugger.OfflineSession(pausedSnapshotForTest())
		ws.showDebug()
		ws.debug.tab = "Inspect"
	})
	handle.Window.Draw()
	handle.Window.Draw()
	return hp, &handle, a, update
}

// clickTabHitbox dispatches a full click (down at p, then up at p) with an
// optional mid-click mutation+draw between the two halves.
func clickTabHitbox(hw *headless.Window, hb geometry.Bounds[geometry.Pixels], between func()) {
	p := geometry.PointPixels(
		hb.Origin.X.AsF32()+hb.Size.Width.AsF32()/2,
		hb.Origin.Y.AsF32()+hb.Size.Height.AsF32()/2)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
	if between != nil {
		between()
	}
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
}

// TestDebugTabSingleClickSwitches pins the IDE contract that ONE click
// switches a debug panel tab — selection acts on mouse-down, in the frame
// the press landed on, so a redraw that restructures the tree mid-click
// (caret blink, output text arriving, the FPS readout toggling) can never
// drop it, and a plain click needs no second try.
func TestDebugTabSingleClickSwitches(t *testing.T) {
	hp, handle, a, update := openDebugTabWindow(t)
	hw := hp.LastWindow()
	read := func() *Workspace { return handle.View.Read(a.App) }

	tabBounds := func(tab string) geometry.Bounds[geometry.Pixels] {
		t.Helper()
		handle.Window.Draw()
		handle.Window.Draw()
		for _, hb := range handle.Window.RenderedHitboxes() {
			if hb.Key == "n:btn-debug-tab-"+tab {
				return hb.Bounds
			}
		}
		t.Fatalf("%s tab hitbox not found", tab)
		return geometry.Bounds[geometry.Pixels]{}
	}

	// Control: a plain single click switches Inspect -> Output.
	clickTabHitbox(hw, tabBounds("Output"), nil)
	if read().debug.tab != "Output" {
		t.Fatalf("plain single click did not switch the tab, got %q", read().debug.tab)
	}

	// Click back to Inspect with a tree-changing redraw between down and up
	// (the FPS readout appears above the debug panel and renumbers every
	// layout node after the menu bar): the press already acted on down.
	clickTabHitbox(hw, tabBounds("Inspect"), func() {
		update(func(ws *Workspace) { ws.ShowFPS = true })
		handle.Window.Draw()
	})
	if read().debug.tab != "Inspect" {
		t.Fatalf("click crossing a tree-changing redraw did not switch the tab, got %q", read().debug.tab)
	}
}
