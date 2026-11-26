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

// TestDebugVarsScrollPersistence checks the variables segment's scroll
// offset survives re-renders (a resize drag redraws the whole element tree
// every frame; the reading position must persist like the editor's scroll).
func TestDebugVarsScrollPersistence(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp := headless.New(geometry.SizePixels(1000, 650))
	a := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{Title: "debug-scroll"},
		func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
			ws := New("debug-scroll")
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

	center := func(key string) geometry.Point[geometry.Pixels] {
		t.Helper()
		for _, hb := range handle.Window.RenderedHitboxes() {
			if hb.Key == key {
				return geometry.PointPixels(
					float32(hb.Bounds.Origin.X)+float32(hb.Bounds.Size.Width)/2,
					float32(hb.Bounds.Origin.Y)+float32(hb.Bounds.Size.Height)/2)
			}
		}
		t.Fatalf("hitbox %q not found", key)
		return geometry.PointPixels(0, 0)
	}

	hw := hp.LastWindow()
	hw.DispatchInput(platform.ScrollEvent{Position: center("n:btn-debug-variables"), Delta: platform.ScrollDeltaPixels(0, -60)})
	handle.Window.Draw()
	if top := handle.View.Read(a.App).debug.vars.Top().AsF32(); top <= 0 {
		t.Fatalf("wheel did not scroll the variables segment (top=%.0f)", top)
	}

	// A resize redraws the whole element tree (the reading position used to
	// reset to the top on any layout change above the list).
	update(func(ws *Workspace) { ws.SetTermHeight(300) })
	handle.Window.Draw()
	ws := handle.View.Read(a.App)
	if top := ws.debug.vars.Top().AsF32(); top != 60 {
		t.Fatalf("resize reset the variables scroll offset (top=%.0f want 60)", top)
	}
	if ws.debug.vars.MaxTop().AsF32() <= 0 {
		t.Fatalf("vars segment does not overflow after resize (content=%.0f viewport=%.0f)",
			ws.debug.vars.ContentSize().Height.AsF32(), ws.debug.vars.ViewportSize().Height.AsF32())
	}

	// Scrolling further still works after the resize.
	hw.DispatchInput(platform.ScrollEvent{Position: center("n:btn-debug-variables"), Delta: platform.ScrollDeltaPixels(0, -60)})
	handle.Window.Draw()
	if top := handle.View.Read(a.App).debug.vars.Top().AsF32(); top <= 60 {
		t.Fatalf("wheel after resize did not advance the offset (top=%.0f)", top)
	}
}
