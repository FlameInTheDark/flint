package workspace

import (
	"time"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
)

// The framework rebuilds a frame synchronously for every dispatched mouse
// move. Coalesce hover and rail-drag moves to one per frame so high-rate
// mice cannot build scenes faster than the display can paint them.
const pointerFrameInterval = time.Second / 60

type railDragInput struct {
	next      func(platform.Input) platform.DispatchEventResult
	hit       func(geometry.Point[geometry.Pixels]) bool
	schedule  func(time.Duration, func())
	active    bool
	pending   *platform.MouseMoveEvent
	queued    bool
	sequence  uint64
	lastFrame time.Time
	lastPos   geometry.Point[geometry.Pixels]
	buttons   map[platform.MouseButton]bool
}

func newRailDragInput(next func(platform.Input) platform.DispatchEventResult, hit func(geometry.Point[geometry.Pixels]) bool, schedule func(time.Duration, func())) *railDragInput {
	return &railDragInput{next: next, hit: hit, schedule: schedule}
}

func (d *railDragInput) handle(input platform.Input) platform.DispatchEventResult {
	switch ev := input.(type) {
	case platform.MouseDownEvent:
		d.flushPending()
		if d.buttons == nil {
			d.buttons = make(map[platform.MouseButton]bool)
		}
		d.buttons[ev.Button] = true
		if ev.Button == platform.ButtonLeft {
			d.cancel()
			d.active = d.hit(ev.Position)
			if d.active {
				d.lastFrame = time.Now()
				d.lastPos = ev.Position
			}
		}
	case platform.MouseMoveEvent:
		if d.active || len(d.buttons) == 0 && ev.PressedButton == nil {
			if !d.active && !d.queued && time.Since(d.lastFrame) >= pointerFrameInterval {
				d.lastFrame, d.lastPos = time.Now(), ev.Position
				return d.next(input)
			}
			move := ev
			d.pending = &move
			if !d.queued {
				d.queued = true
				seq := d.sequence
				delay := max(time.Duration(0), pointerFrameInterval-time.Since(d.lastFrame))
				d.schedule(delay, func() {
					if d.sequence == seq {
						d.flush()
					}
				})
			}
			return platform.DispatchEventResult{Propagate: false}
		}
		d.flushPending()
		d.lastFrame, d.lastPos = time.Now(), ev.Position
	case platform.MouseUpEvent:
		if ev.Button == platform.ButtonLeft && d.active {
			// The release may arrive before the scheduled move, or at a
			// new position with no final move event. Apply it before the
			// framework releases pointer capture.
			if ev.Position != d.lastPos {
				button := platform.ButtonLeft
				d.pending = &platform.MouseMoveEvent{Position: ev.Position, Modifiers: ev.Modifiers, PressedButton: &button}
				d.flush()
			}
			d.cancel()
		} else {
			d.flushPending()
		}
		delete(d.buttons, ev.Button)
	default:
		// Preserve event order: a pending hover must reach the framework
		// before a wheel, key, exit, or other non-move event.
		d.flushPending()
	}
	return d.next(input)
}

func (d *railDragInput) flush() {
	d.queued = false
	d.sequence++
	if d.pending != nil {
		move := *d.pending
		d.pending = nil
		d.lastFrame = time.Now()
		d.lastPos = move.Position
		d.next(move)
	}
}

func (d *railDragInput) flushPending() {
	if d.pending != nil {
		d.flush()
	}
}

func (d *railDragInput) cancel() {
	d.active = false
	d.pending = nil
	d.queued = false
	d.sequence++ // callbacks from an earlier gesture must not flush a new one
}

// editorRailHit follows the front-to-back hitbox ordering, so a modal or
// other blocking overlay above the editor never starts a rail drag.
func editorRailHit(w *gpui.Window, pos geometry.Point[geometry.Pixels]) bool {
	if w == nil {
		return false
	}
	return railHitboxes(w.RenderedHitboxes(), w.ScaleFactor(), pos)
}

func railHitboxes(boxes []gpui.Hitbox, scale float32, pos geometry.Point[geometry.Pixels]) bool {
	for i := len(boxes) - 1; i >= 0; i-- {
		box := boxes[i]
		if !geometry.BoundsContains(box.Bounds, pos) {
			continue
		}
		mask := box.ContentMask.Bounds
		if mask.Size.Width > 0 && mask.Size.Height > 0 {
			if scale <= 0 {
				scale = 1
			}
			logicalMask := geometry.BoundsPixels(mask.Origin.X.AsF32()/scale, mask.Origin.Y.AsF32()/scale, mask.Size.Width.AsF32()/scale, mask.Size.Height.AsF32()/scale)
			if !geometry.BoundsContains(logicalMask, pos) {
				continue
			}
		}
		if box.Key == "n:btn-minimap" || box.Key == "n:btn-vscroll" {
			return true
		}
		if box.Behavior == gpui.HitboxBlockMouse || box.Behavior == gpui.HitboxBlockMouseExceptScroll {
			break
		}
	}
	return false
}
