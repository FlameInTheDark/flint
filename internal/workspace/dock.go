package workspace

import (
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

// The bottom dock (terminal / debug / problems / todos / Go task output)
// holds one panel at a time and shares one height (TermHeight, rendered
// through effectiveTermHeight). The terminal renders its own resize strip;
// every other dock panel mounts the shared strip below so the whole dock
// resizes from whichever panel is attached — the upstream editor's dock
// resize handle.
const dockResizeStripH = ui.TermResizeStrip

// renderDockResize builds the strip riding a bottom dock panel's top edge:
// 5px tall, row-resize cursor, highlight on hover. Dragging it re-anchors
// the dock's height through onDrag (y = pointer window y). The strip mounts
// as the panel's FIRST flex child, so it sits exactly on the panel's top
// border and is hit-tested before the header rows below it.
func renderDockResize(id string, t ui.Theme, onDrag func(y float32, w *gpui.Window)) gpui.Element {
	return elements.NewDiv().
		ID(elements.ButtonID(id)).
		H(ui.Px(dockResizeStripH)).FlexShrink(0).
		CursorRowResize().
		Hover(func(s *style.StyleRefinement) { s.BgColor(t.Accent.Alpha(0.45)) }).
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				w.CapturePointer(hb.ID)
			}
			w.StopPropagation()
		}).
		OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
			if !ev.Dragging() {
				return
			}
			if onDrag != nil {
				onDrag(ev.Position.Y.AsF32(), w)
			}
			w.StopPropagation()
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
			w.Refresh()
		})
}

// persistScroll re-anchors a panel list's scroll handle at its last
// rendered offset. The element tree rebuilds every frame and the lists'
// element ids shift when the layout above them changes (a dock resize, a
// find bar, a tab change), which silently resets element-state scroll
// offsets to the top mid-reading. The workspace owns the offset instead:
// the wheel handlers advance the stored value, every render re-requests it
// (the handle's mirror lags one frame, so it never overrides the fresher
// stored value), and the div clamps it at prepaint. A bottom-following
// handle (autoscroll) moves on its own: the stored value tracks its live
// offset so the first user scroll-up is relative to the real position.
func persistScroll(h *elements.ScrollHandle, stored *float32) {
	if h == nil || stored == nil {
		return
	}
	if h.IsAutoscrolling() {
		*stored = h.Top().AsF32()
		return
	}
	h.SetTop(geometry.Px(*stored))
}

// persistScrollLeft re-anchors a panel list's horizontal pan offset the
// same way persistScroll keeps the vertical reading position (wide values
// overflow their segment; the pan must survive resize redraws).
func persistScrollLeft(h *elements.ScrollHandle, stored *float32) {
	if h == nil || stored == nil {
		return
	}
	h.SetLeft(geometry.Px(*stored))
}
