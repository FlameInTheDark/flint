package workspace

import (
	"testing"

	"github.com/FlameInTheDark/flint/internal/debugger"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

// TestDebugVarsHorizontalScroll checks the variables segment's horizontal
// pan: a value wider than the segment overflows it instead of being
// clipped per row, the wheel pans across it, the pan offset survives a
// resize redraw (the element tree rebuilds each frame), and it clamps at
// the left edge.
func TestDebugVarsHorizontalScroll(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp := headless.New(geometry.SizePixels(1000, 650))
	a := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{Title: "debug-hscroll"},
		func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
			ws := New("debug-hscroll")
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
	read := func() *Workspace { return handle.View.Read(a.App) }

	vars := read().debug.vars
	// The 200-char test value must overflow the segment: the content column
	// is sized to the widest row and the container pans across it.
	if vars.MaxLeft().AsF32() <= 0 {
		t.Fatalf("wide value did not overflow the variables segment (content=%.0f viewport=%.0f)",
			vars.ContentSize().Width.AsF32(), vars.ViewportSize().Width.AsF32())
	}
	// 200 mono glyphs at 12px far exceed the segment (~570px): the row must
	// keep its natural width rather than shrink to the viewport.
	if vars.ContentSize().Width.AsF32() < 1000 {
		t.Fatalf("content column collapsed to the viewport (content=%.0f)", vars.ContentSize().Width.AsF32())
	}

	// Wheel leftwards (negative X delta pans right, the markdown convention).
	hw := hp.LastWindow()
	hw.DispatchInput(platform.ScrollEvent{Position: center("n:btn-debug-variables"), Delta: platform.ScrollDeltaPixels(-120, 0)})
	handle.Window.Draw()
	if left := read().debug.vars.Left().AsF32(); left <= 0 {
		t.Fatalf("horizontal wheel did not pan the variables segment (left=%.0f)", left)
	}

	// A resize redraws the whole element tree: the pan offset must persist
	// like the vertical reading position does.
	update(func(ws *Workspace) { ws.SetTermHeight(300) })
	handle.Window.Draw()
	ws := read()
	if left := ws.debug.vars.Left().AsF32(); left < 100 {
		t.Fatalf("resize reset the variables pan offset (left=%.0f)", left)
	}

	// Panning back clamps at the left edge instead of going negative.
	hw.DispatchInput(platform.ScrollEvent{Position: center("n:btn-debug-variables"), Delta: platform.ScrollDeltaPixels(4000, 0)})
	handle.Window.Draw()
	if left := read().debug.vars.Left().AsF32(); left != 0 {
		t.Fatalf("pan did not clamp at the left edge (left=%.0f)", left)
	}

	// Shift+wheel converts the vertical delta into a horizontal pan.
	hw.DispatchInput(platform.ScrollEvent{
		Position:  center("n:btn-debug-variables"),
		Delta:     platform.ScrollDeltaPixels(0, -60),
		Modifiers: keymap.Modifiers{Shift: true},
	})
	handle.Window.Draw()
	if left := read().debug.vars.Left().AsF32(); left <= 0 {
		t.Fatalf("shift+wheel did not pan the variables segment (left=%.0f)", left)
	}
}
