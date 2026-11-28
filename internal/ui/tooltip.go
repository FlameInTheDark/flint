package ui

import (
	"github.com/FlameInTheDark/go-gpui/color"
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

// tooltip colors mirror elements.NewTooltip's surface so both tooltip
// styles read as one chrome family.
var (
	tooltipBg     = color.RGB(0x262b35).Hsla()
	tooltipBorder = color.RGB(0x535a67).Hsla()
	tooltipTitle  = color.RGB(0xf3f5f8).Hsla()
	tooltipBody   = color.RGB(0xaab3c2).Hsla()
	tooltipHint   = color.RGB(0x7d8796).Hsla()
)

// TooltipRich builds a multi-line tooltip for Div.TooltipBuilder: a plain
// title row, optional mono body rows and an optional trailing hint row.
//
// The text system shapes a Text element as ONE line — a "\n" inside the
// string paints as a missing-glyph bar (the profiler's tooltips rendered
// as a window-wide strip), so every line is its own element and the
// builder composes them vertically. Lines never wrap: they clip, and the
// surface caps at 460px so a long source path can't cover the panel.
//
//	elements.NewDiv()...TooltipBuilder(ui.TooltipRich(name, []string{"main.go:12"}, "Click: zoom"))
func TooltipRich(title string, body []string, hint string) func(*gpui.Window) gpui.Element {
	return func(*gpui.Window) gpui.Element {
		tt := elements.NewDiv().
			MinW_0().MaxW(Px(460)).
			Flex().FlexCol().Gap(DefPx(3)).
			BgColor(tooltipBg).Border_1().BorderColor(tooltipBorder).
			Rounded(AbsPx(6)).ShadowMd().
			Px(DefPx(10)).Py(DefPx(6))
		tt.Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().
			Child(elements.NewText(title).WithTextColor(tooltipTitle)))
		for _, line := range body {
			if line == "" {
				continue
			}
			tt.Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().
				Child(MonoText(line, tooltipBody)))
		}
		if hint != "" {
			tt.Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().TextSize(AbsPx(11)).
				Child(elements.NewText(hint).WithTextColor(tooltipHint)))
		}
		return tt
	}
}
