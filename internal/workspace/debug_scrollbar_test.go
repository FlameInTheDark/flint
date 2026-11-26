package workspace

import (
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/debugger"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
	"github.com/FlameInTheDark/go-gpui/render"
)

// TestDebugScrollbarsPaintedAndDraggable checks that the debug segments'
// scroll areas behave like any normal scroll area: the variables segment
// paints a visible vertical scrollbar (rows past the fold) and a visible
// horizontal scrollbar (values wider than the segment), both bars drag —
// click-to-jump plus thumb drag — and the dragged offset persists across a
// resize redraw (the scrollbar writes the workspace-owned offset the same
// way the wheel handler does).
func TestDebugScrollbarsPaintedAndDraggable(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp := headless.New(geometry.SizePixels(1000, 650))
	a := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{Title: "debug-scrollbars"},
		func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
			ws := New("debug-scrollbars")
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
	handle.Window.Draw() // bars mount from the previous frame's mirrored geometry

	hitbox := func(key string) geometry.Bounds[geometry.Pixels] {
		t.Helper()
		for _, hb := range handle.Window.RenderedHitboxes() {
			if hb.Key == key {
				return hb.Bounds
			}
		}
		t.Fatalf("hitbox %q not found", key)
		return geometry.Bounds[geometry.Pixels]{}
	}
	thumbQuad := func(track geometry.Bounds[geometry.Pixels], vertical bool) *render.Quad {
		t.Helper()
		tw, th := track.Size.Width.AsF32(), track.Size.Height.AsF32()
		for i := range handle.Window.PaintedQuads() {
			q := &handle.Window.PaintedQuads()[i]
			w, h := q.Bounds.Size.Width.AsF32(), q.Bounds.Size.Height.AsF32()
			x, y := q.Bounds.Origin.X.AsF32(), q.Bounds.Origin.Y.AsF32()
			if x < track.Origin.X.AsF32() || y < track.Origin.Y.AsF32() ||
				x+w > track.Origin.X.AsF32()+tw || y+h > track.Origin.Y.AsF32()+th {
				continue
			}
			if vertical && w >= 6 && w <= 8 && h < th {
				return q
			}
			if !vertical && h >= 6 && h <= 8 && w < tw {
				return q
			}
		}
		return nil
	}
	btnPtr := func(b platform.MouseButton) *platform.MouseButton { return &b }
	read := func() *Workspace { return handle.View.Read(a.App) }
	hw := hp.LastWindow()

	// Both bars mount when the segment overflows (the snapshot's 200-char
	// value is wider than the segment; the variable rows are taller than it).
	vTrack := hitbox("n:btn-debug-variables-vscroll")
	hTrack := hitbox("n:btn-debug-variables-hscroll")
	if thumbQuad(vTrack, true) == nil {
		t.Fatal("vertical scrollbar thumb not painted in the variables segment")
	}
	if thumbQuad(hTrack, false) == nil {
		t.Fatal("horizontal scrollbar thumb not painted in the variables segment")
	}
	// The horizontal track stops short of the vertical track's column (the
	// corner must not stack two bars).
	if hTrack.Origin.X.AsF32()+hTrack.Size.Width.AsF32() > vTrack.Origin.X.AsF32()+0.5 {
		t.Fatalf("horizontal track overlaps the vertical track (h right %.0f, v left %.0f)",
			hTrack.Origin.X.AsF32()+hTrack.Size.Width.AsF32(), vTrack.Origin.X.AsF32())
	}

	// Drag the vertical thumb downwards: the view follows, and the drag
	// lands in the workspace-owned offset (a redraw must not roll it back).
	if read().debug.vars.Top().AsF32() != 0 {
		t.Fatalf("variables segment should start at the top (top=%.0f)", read().debug.vars.Top().AsF32())
	}
	dragY := vTrack.Origin.Y.AsF32() + 10
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(vTrack.Origin.X.AsF32()+5, dragY)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(vTrack.Origin.X.AsF32()+5, dragY+140), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(vTrack.Origin.X.AsF32()+5, dragY+140)})
	handle.Window.Draw()
	ws := read()
	if top := ws.debug.vars.Top().AsF32(); top <= 0 {
		t.Fatalf("vertical scrollbar drag did not scroll (top=%.0f)", top)
	} else if ws.debug.varsTop != top {
		t.Fatalf("scrollbar drag did not update the stored offset (stored=%.0f top=%.0f)", ws.debug.varsTop, top)
	}

	update(func(ws *Workspace) { ws.SetTermHeight(420) })
	handle.Window.Draw()
	if top := read().debug.vars.Top().AsF32(); top <= 0 {
		t.Fatalf("resize reset the scrollbar-dragged offset (top=%.0f)", top)
	}

	// Clicking near the track's bottom jumps close to the end of the list.
	vTrack = hitbox("n:btn-debug-variables-vscroll")
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(vTrack.Origin.X.AsF32()+5, vTrack.Origin.Y.AsF32()+vTrack.Size.Height.AsF32()-3)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(vTrack.Origin.X.AsF32()+5, vTrack.Origin.Y.AsF32()+vTrack.Size.Height.AsF32()-3)})
	handle.Window.Draw()
	ws = read()
	if maxTop := ws.debug.vars.MaxTop().AsF32(); maxTop > 0 && ws.debug.vars.Top().AsF32() < maxTop*0.6 {
		t.Fatalf("track click near the bottom did not jump (top=%.0f max=%.0f)", ws.debug.vars.Top().AsF32(), maxTop)
	}

	// Drag the horizontal thumb rightwards: the wide values pan into view.
	if left := read().debug.vars.Left().AsF32(); left != 0 {
		t.Fatalf("variables segment should start unpanned (left=%.0f)", left)
	}
	dragX := hTrack.Origin.X.AsF32() + 10
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(dragX, hTrack.Origin.Y.AsF32()+5)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(dragX+200, hTrack.Origin.Y.AsF32()+5), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(dragX+200, hTrack.Origin.Y.AsF32()+5)})
	handle.Window.Draw()
	ws = read()
	if left := ws.debug.vars.Left().AsF32(); left <= 0 {
		t.Fatalf("horizontal scrollbar drag did not pan (left=%.0f)", left)
	} else if ws.debug.varsLeft != left {
		t.Fatalf("horizontal drag did not update the stored offset (stored=%.0f left=%.0f)", ws.debug.varsLeft, left)
	}
}

// TestDebugOutputScrollbarCancelsFollow checks the output segment's vertical
// bar: dragging it stops the tail-follow (the wheel's user-scroll hook), so
// new output no longer yanks the view to the bottom.
func TestDebugOutputScrollbarCancelsFollow(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp := headless.New(geometry.SizePixels(1000, 650))
	a := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{Title: "debug-output-scroll"},
		func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
			ws := New("debug-output-scroll")
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
	session := debugger.OfflineSession(pausedSnapshotForTest())
	session.AppendOutput(strings.Repeat("goroutine line of log output\n", 120))
	update(func(ws *Workspace) {
		ws.debug.session = session
		ws.showDebug()
		ws.debug.tab = "Output"
		ws.debug.follow = true
		ws.debug.output.Autoscroll()
	})
	handle.Window.Draw()
	handle.Window.Draw() // bars mount from the previous frame's mirrored geometry

	var track geometry.Bounds[geometry.Pixels]
	for _, hb := range handle.Window.RenderedHitboxes() {
		if hb.Key == "n:btn-debug-output-vscroll" {
			track = hb.Bounds
		}
	}
	if track.Size.Width.AsF32() == 0 {
		t.Fatal("output segment vertical scrollbar not mounted")
	}
	read := func() *Workspace { return handle.View.Read(a.App) }
	if !read().debug.output.IsAutoscrolling() {
		t.Fatal("output segment should tail-follow until the user scrolls")
	}

	hw := hp.LastWindow()
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(track.Origin.X.AsF32()+5, track.Origin.Y.AsF32()+track.Size.Height.AsF32()-4)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(track.Origin.X.AsF32()+5, track.Origin.Y.AsF32()+track.Size.Height.AsF32()-4)})
	handle.Window.Draw()
	ws := read()
	if ws.debug.output.IsAutoscrolling() {
		t.Fatal("scrollbar drag left the output tail-following")
	}
	if ws.debug.follow {
		t.Fatal("scrollbar drag did not clear the follow flag")
	}
	if top := ws.debug.output.Top().AsF32(); top <= 0 {
		t.Fatalf("dragging the output scrollbar did not scroll (top=%.0f)", top)
	}
}
