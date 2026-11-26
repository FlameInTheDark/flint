package workspace

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/render"
)

func TestRailDragCoalescesAndFlushes(t *testing.T) {
	var received []platform.Input
	var callbacks []func()
	d := newRailDragInput(func(ev platform.Input) platform.DispatchEventResult {
		received = append(received, ev)
		return platform.DispatchEventResult{Propagate: true}
	}, func(pos geometry.Point[geometry.Pixels]) bool { return pos.X == 10 }, func(_ time.Duration, f func()) { callbacks = append(callbacks, f) })
	point := func(x, y float32) geometry.Point[geometry.Pixels] { return geometry.PointPixels(x, y) }
	d.handle(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: point(10, 0)})
	for i := 1; i <= 100; i++ {
		d.handle(platform.MouseMoveEvent{Position: point(10, float32(i))})
	}
	if len(received) != 1 || len(callbacks) != 1 {
		t.Fatalf("100 moves produced %d events and %d callbacks", len(received), len(callbacks))
	}
	callbacks[0]()
	if got := received[1].(platform.MouseMoveEvent).Position.Y.AsF32(); got != 100 {
		t.Fatalf("coalesced move = %v, want 100", got)
	}
	// Release at a new position before the next frame: its final position
	// must be applied before the button-up, even outside the rail.
	d.handle(platform.MouseMoveEvent{Position: point(20, 120)})
	d.handle(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: point(20, 130)})
	if len(received) != 4 || received[2].(platform.MouseMoveEvent).Position.Y.AsF32() != 130 {
		t.Fatalf("release did not flush final position: %+v", received)
	}
	// A stale callback cannot flush a later gesture.
	d.handle(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: point(10, 0)})
	d.handle(platform.MouseMoveEvent{Position: point(10, 50)})
	callbacks[1]()
	if len(received) != 5 {
		t.Fatal("old callback flushed a new gesture")
	}
	callbacks[2]()
	if len(received) != 6 || received[5].(platform.MouseMoveEvent).Position.Y.AsF32() != 50 {
		t.Fatal("new gesture's pending move was lost")
	}
	d.handle(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: point(10, 50)})
	beforeClick := len(received)
	d.handle(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: point(10, 50)})
	d.handle(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: point(10, 50)})
	if len(received) != beforeClick+2 {
		t.Fatal("stationary click dispatched an unnecessary move")
	}
	// Ordinary text selection is dispatched synchronously, not coalesced.
	d.handle(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: point(1, 0)})
	before := len(received)
	d.handle(platform.MouseMoveEvent{Position: point(1, 30)})
	if len(received) != before+1 {
		t.Fatal("non-rail move was delayed")
	}
}

func TestRailHitboxesRespectOcclusionAndScale(t *testing.T) {
	bounds := geometry.BoundsPixels(100, 50, 10, 100)
	rail := gpui.Hitbox{
		Key: "n:btn-vscroll", Bounds: bounds,
		ContentMask: render.ContentMask{Bounds: geometry.ScaleBounds(bounds, 1.5)},
	}
	pos := geometry.PointPixels(105, 80)
	if !railHitboxes([]gpui.Hitbox{rail}, 1.5, pos) {
		t.Fatal("scaled rail was not detected")
	}
	blocker := gpui.Hitbox{Bounds: bounds, Behavior: gpui.HitboxBlockMouseExceptScroll}
	if railHitboxes([]gpui.Hitbox{rail, blocker}, 1.5, pos) {
		t.Fatal("rail underneath an overlay was detected")
	}
	rail.ContentMask.Bounds = geometry.ScaleBounds(geometry.BoundsPixels(100, 50, 10, 10), 1.5)
	if railHitboxes([]gpui.Hitbox{rail}, 1.5, pos) {
		t.Fatal("rail outside its clipping mask was detected")
	}
}

func TestHoverMovesCoalesceAndPreserveEventOrder(t *testing.T) {
	var received []platform.Input
	var callbacks []func()
	d := newRailDragInput(func(ev platform.Input) platform.DispatchEventResult {
		received = append(received, ev)
		return platform.DispatchEventResult{Propagate: true}
	}, func(geometry.Point[geometry.Pixels]) bool { return false }, func(_ time.Duration, f func()) { callbacks = append(callbacks, f) })
	point := func(y float32) geometry.Point[geometry.Pixels] { return geometry.PointPixels(50, y) }
	d.handle(platform.MouseMoveEvent{Position: point(1)})
	for i := 2; i <= 100; i++ {
		d.handle(platform.MouseMoveEvent{Position: point(float32(i))})
	}
	if len(received) != 1 || len(callbacks) != 1 {
		t.Fatalf("hover burst produced %d events and %d callbacks", len(received), len(callbacks))
	}
	// A click must see the latest hover before its press, even when the
	// scheduled frame has not fired yet.
	d.handle(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: point(100)})
	if len(received) != 3 || received[1].(platform.MouseMoveEvent).Position != point(100) {
		t.Fatalf("press did not flush the latest hover: %+v", received)
	}
	callbacks[0]()
	if len(received) != 3 {
		t.Fatal("stale hover callback dispatched twice")
	}
	d.handle(platform.MouseMoveEvent{Position: point(101)})
	if len(received) != 4 {
		t.Fatal("selection drag was coalesced")
	}
	d.handle(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: point(101)})
	// A queued hover is also flushed before exit, so it cannot re-hover an
	// element after the pointer has left the window.
	d.handle(platform.MouseMoveEvent{Position: point(102)})
	d.handle(platform.MouseExitEvent{Position: point(103)})
	if _, ok := received[len(received)-1].(platform.MouseExitEvent); !ok {
		t.Fatalf("exit was not the last event: %+v", received)
	}
	for _, callback := range callbacks {
		callback()
	}
	if _, ok := received[len(received)-1].(platform.MouseExitEvent); !ok {
		t.Fatal("stale callback dispatched hover after exit")
	}
}

func TestRailDragCoalescingInWindow(t *testing.T) {
	for _, key := range []string{"n:btn-minimap", "n:btn-vscroll"} {
		t.Run(key, func(t *testing.T) {
			var src strings.Builder
			for i := 0; i < 2000; i++ {
				fmt.Fprintf(&src, "line %d\n", i)
			}
			buf := editor.NewBuffer("", "long.go", src.String())
			_, handle, ap := openLeftGapScene(t, true, buf)
			var bounds geometry.Bounds[geometry.Pixels]
			for _, hb := range handle.Window.RenderedHitboxes() {
				if hb.Key == key {
					bounds = hb.Bounds
				}
			}
			if bounds.Size.Height <= 0 {
				t.Fatalf("missing %s", key)
			}
			point := func(f float32) geometry.Point[geometry.Pixels] {
				return geometry.PointPixels(bounds.Origin.X.AsF32()+bounds.Size.Width.AsF32()/2, bounds.Origin.Y.AsF32()+bounds.Size.Height.AsF32()*f)
			}
			if !editorRailHit(handle.Window, point(0.25)) {
				t.Fatal("rail hit test did not detect the rail")
			}
			var callbacks []func()
			d := newRailDragInput(handle.Window.HandleInput, func(pos geometry.Point[geometry.Pixels]) bool {
				return editorRailHit(handle.Window, pos)
			}, func(_ time.Duration, f func()) { callbacks = append(callbacks, f) })
			frames := 0
			handle.Window.SetAfterDraw(func(*render.Scene) { frames++ })
			d.handle(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: point(0.2)})
			frames = 0
			for i := 0; i < 100; i++ {
				d.handle(platform.MouseMoveEvent{Position: point(0.2 + float32(i)*0.005)})
			}
			if frames != 0 || len(callbacks) != 1 {
				t.Fatalf("100 queued moves produced %d frames and %d callbacks", frames, len(callbacks))
			}
			callbacks[0]()
			if frames != 1 {
				t.Fatalf("coalesced move produced %d frames, want 1", frames)
			}
			first := handle.View.Read(ap).scroll()
			d.handle(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: point(0.9)})
			last := handle.View.Read(ap).scroll()
			if first < 500 || last <= first {
				t.Fatalf("scroll positions after move and release: %d, %d", first, last)
			}
		})
	}
}
