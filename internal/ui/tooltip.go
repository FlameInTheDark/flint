package ui

import (
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/gpui"
)

// StructID builds a stable structural element id for chrome wrappers
// (rows, columns, bars) that never appear twice in the tree.
//
// Every element's interactive state — hover styles, a pending click
// between mouse-down and mouse-up, the tooltip show timer, scroll
// offsets — is keyed by the element's id path, and an unnamed div
// contributes its per-frame layout node number, which renumbers whenever
// any earlier subtree changes structure (a caret blink, an output line
// arriving, a scrollbar mounting). Naming the structural wrappers keeps
// the identity of every interactive element below them stable, so clicks
// land on the first press and tooltips surface on a stationary hover
// regardless of what the rest of the window is doing.
func StructID(name string) gpui.ElementId { return gpui.NameElementId("st-" + name) }

// Tooltip builds the application tooltip view for Div.TooltipBuilder: a
// compact rectangle with slightly rounded corners (6px, matching the
// popup panels) instead of the gpui default 14px pill, so tooltips read
// as part of the same chrome family as menus and modals.
//
//	elements.NewDiv()...TooltipBuilder(ui.Tooltip("Save (Ctrl+S)"))
func Tooltip(text string) func(*gpui.Window) gpui.Element {
	return func(*gpui.Window) gpui.Element {
		tt := elements.NewTooltip(text)
		tt.Rounded(AbsPx(6)) // mutates the embedded div; the wrapper keeps Prepaint/Paint
		return tt
	}
}
