package workspace

import (
	"sort"
	"strings"

	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
)

// tabRect is a minimal hitbox rectangle for the tab strip's reveal logic.
type tabRect struct {
	x, y, w, h float32
}

// revealActiveTab pans the scrollable tab strip so the active tab is fully
// visible whenever the active buffer changes (edge-detected through
// *revealed). Tab geometry comes from the last rendered frame — the same
// one-frame-of-lag pattern the scrollbars' mirrored handles use; tabs hug
// their content, so their bounds only move when the strip pans or the tab
// set changes.
func revealActiveTab(w *gpui.Window, strip *elements.ScrollHandle, active int, revealed *int) {
	if active == *revealed {
		return
	}

	var stripR tabRect
	for _, hb := range w.RenderedHitboxes() {
		if hb.Key != "n:btn-tabs-strip" {
			continue
		}
		o, s := hb.Bounds.Origin, hb.Bounds.Size
		stripR = tabRect{x: o.X.AsF32(), y: o.Y.AsF32(), w: s.Width.AsF32(), h: s.Height.AsF32()}
		break
	}
	if stripR.w == 0 {
		return // no strip rendered yet: retry on a later frame
	}

	var tabs []tabRect
	for _, hb := range w.RenderedHitboxes() {
		if !strings.HasPrefix(hb.Key, "n:btn-tab-") {
			continue
		}
		o, s := hb.Bounds.Origin, hb.Bounds.Size
		r := tabRect{x: o.X.AsF32(), y: o.Y.AsF32(), w: s.Width.AsF32(), h: s.Height.AsF32()}
		tabs = append(tabs, r)
	}
	if active < 0 || active >= len(tabs) {
		return // geometry not ready or the tab set changed underneath: retry
	}
	*revealed = active
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].x < tabs[j].x })

	// Tab position in content coordinates (the frame's bounds already
	// include the strip's current pan).
	left := strip.Left().AsF32()
	contentLeft := tabs[active].x - stripR.x + left
	contentRight := contentLeft + tabs[active].w
	viewW := stripR.w
	if contentRight > left+viewW {
		left = contentRight - viewW
	}
	if contentLeft < left {
		left = contentLeft
	}
	strip.SetLeft(geometry.Px(left))
}
