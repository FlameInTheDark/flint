package workspace

import (
	"testing"
	"time"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"
)

// hoverCenter moves the pointer to the center of a hitbox.
func hoverCenter(hw interface {
	DispatchInput(ev platform.Input) platform.DispatchEventResult
}, hb geometry.Bounds[geometry.Pixels]) {
	p := geometry.PointPixels(
		hb.Origin.X.AsF32()+hb.Size.Width.AsF32()/2,
		hb.Origin.Y.AsF32()+hb.Size.Height.AsF32()/2)
	hw.DispatchInput(platform.MouseMoveEvent{Position: p})
}

// TestDebugIconTooltipShows pins that hovering a debugger control shows its
// name tooltip after the standard delay — the icons replaced the text
// labels, so the tooltip is the only place the action name lives.
func TestDebugIconTooltipShows(t *testing.T) {
	hp, handle, a, update := openDebugTabWindow(t)
	hw := hp.LastWindow()
	_ = a
	_ = update

	find := func(key string) geometry.Bounds[geometry.Pixels] {
		t.Helper()
		for _, hb := range handle.Window.RenderedHitboxes() {
			if hb.Key == key {
				return hb.Bounds
			}
		}
		t.Fatalf("hitbox %q not found", key)
		return geometry.Bounds[geometry.Pixels]{}
	}

	cont := find("n:btn-debug-continue")
	hoverCenter(hw, cont)
	handle.Window.Draw() // paint arms the tooltip waiting state (now+500ms)
	time.Sleep(600 * time.Millisecond)
	handle.Window.Draw() // past the delay: this paint must surface the tooltip
	if !handle.Window.TooltipVisible() {
		t.Fatal("hovering the Continue button for >500ms did not show a tooltip")
	}

	// The app tooltip is a rectangle with slightly rounded corners (6px,
	// matching the popup panels), not the gpui default 14px pill: find the
	// tooltip's surface quad near the pointer and check its corner radii.
	// Quad bounds are in scaled pixels; hitbox bounds are logical.
	scale := handle.Window.ScaleFactor()
	pos := geometry.PointPixels(
		(cont.Origin.X.AsF32()+cont.Size.Width.AsF32()/2)*scale,
		(cont.Origin.Y.AsF32()+cont.Size.Height.AsF32()/2)*scale)
	found := false
	for i := range handle.Window.PaintedQuads() {
		q := &handle.Window.PaintedQuads()[i]
		x := q.Bounds.Origin.X.AsF32()
		y := q.Bounds.Origin.Y.AsF32()
		w := q.Bounds.Size.Width.AsF32()
		h := q.Bounds.Size.Height.AsF32()
		r := q.CornerRadii.TopLeft.AsF32() / scale
		// The tooltip floats just right/below of the pointer (it flips
		// above/left near the window edges), so match by origin proximity.
		dx := x - pos.X.AsF32()
		dy := y - pos.Y.AsF32()
		near := dx > -4 && dx < 4 && dy > -44 && dy < 4
		if !near || w < 30 || h < 16 || h > 60 || r == 0 {
			continue
		}
		if r != 6 {
			t.Fatalf("tooltip surface corner radius is %.0fpx, want 6px (a rounded rectangle, not a pill)", r)
		}
		found = true
	}
	if !found {
		t.Fatal("tooltip surface quad with rounded corners not painted near the pointer")
	}
}

// TestDebugIconTooltipSurvivesTreeChange pins that the tooltip still shows
// when the element tree is rebuilt while the pointer rests on the button
// (caret blink, output lines arriving, diagnostics updating): the hover and
// tooltip state live under the button's element identity, so a renumbered
// tree above the panel must not reset them — otherwise a stationary hover
// during an active session never reaches the 500ms delay.
func TestDebugIconTooltipSurvivesTreeChange(t *testing.T) {
	hp, handle, a, update := openDebugTabWindow(t)
	hw := hp.LastWindow()
	_ = a

	find := func(key string) geometry.Bounds[geometry.Pixels] {
		t.Helper()
		for _, hb := range handle.Window.RenderedHitboxes() {
			if hb.Key == key {
				return hb.Bounds
			}
		}
		t.Fatalf("hitbox %q not found", key)
		return geometry.Bounds[geometry.Pixels]{}
	}

	cont := find("n:btn-debug-continue")
	hoverCenter(hw, cont)
	handle.Window.Draw() // arm the waiting state
	// The tree changes above the panel while the pointer stays still.
	update(func(ws *Workspace) { ws.ShowFPS = true })
	handle.Window.Draw()
	time.Sleep(600 * time.Millisecond)
	handle.Window.Draw()
	if !handle.Window.TooltipVisible() {
		t.Fatal("tooltip state was lost by a tree rebuild during the hover delay")
	}
}
