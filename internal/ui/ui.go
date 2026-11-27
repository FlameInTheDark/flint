// Package ui holds the editor example's shared widgets: the Catppuccin
// Mocha theme, dropdown/context menus, modal dialogs and a text input.
//
// These components mirror the upstream editor's ui crate layer (above gpui, below the app):
// gpui itself stays a primitive element library, while reusable composite
// widgets live in the application.
package ui

import (
	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/font"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/text"
)

// Theme is the Catppuccin Mocha palette (the upstream editor's default dark theme).
type Theme struct {
	EditorBg gc.Hsla // base
	Surface  gc.Hsla // mantle
	Surface1 gc.Hsla // surface0
	Surface2 gc.Hsla // surface1
	Crust    gc.Hsla // crust (menus, modals)
	Overlay  gc.Hsla // overlay0 (disabled text)
	Text     gc.Hsla
	Dim      gc.Hsla
	Accent   gc.Hsla // blue
	Green    gc.Hsla
	Peach    gc.Hsla
	Yellow   gc.Hsla
	Mauve    gc.Hsla
	Teal     gc.Hsla
	Red      gc.Hsla
	Orange   gc.Hsla
	Sky      gc.Hsla

	// Selection is the editor selection background.
	Selection gc.Hsla
	// Match is the find-match background.
	Match gc.Hsla
}

// DefaultTheme returns the standard palette.
func DefaultTheme() Theme {
	return Theme{
		EditorBg:  gc.RGB(0x1e1e2e).Hsla(),
		Surface:   gc.RGB(0x181825).Hsla(),
		Surface1:  gc.RGB(0x313244).Hsla(),
		Surface2:  gc.RGB(0x45475a).Hsla(),
		Crust:     gc.RGB(0x11111b).Hsla(),
		Overlay:   gc.RGB(0x6c7086).Hsla(),
		Text:      gc.RGB(0xcdd6f4).Hsla(),
		Dim:       gc.RGB(0x7f849c).Hsla(),
		Accent:    gc.RGB(0x89b4fa).Hsla(),
		Green:     gc.RGB(0xa6e3a1).Hsla(),
		Peach:     gc.RGB(0xfab387).Hsla(),
		Yellow:    gc.RGB(0xf9e2af).Hsla(),
		Mauve:     gc.RGB(0xcba6f7).Hsla(),
		Teal:      gc.RGB(0x94e2d5).Hsla(),
		Red:       gc.RGB(0xf38ba8).Hsla(),
		Orange:    gc.RGB(0xfab387).Hsla(),
		Sky:       gc.RGB(0x89dceb).Hsla(),
		Selection: gc.RGB(0x585b70).Hsla().Alpha(0.55),
		Match:     gc.RGB(0xf9e2af).Hsla().Alpha(0.30),
	}
}

// Px is a helper for pixel lengths (widths, margins, offsets).
func Px(v float32) geometry.Length { return geometry.LengthFromPixels(geometry.Px(v)) }

// DefPx is a helper for definite pixel lengths (padding, gaps).
func DefPx(v float32) geometry.DefiniteLength { return geometry.PixelsDefinite(geometry.Px(v)) }

// AbsPx is a helper for absolute pixel lengths (borders, radii, font size).
func AbsPx(v float32) geometry.AbsoluteLength { return geometry.PixelsLength(geometry.Px(v)) }

// SelectionBackground is a text-run background for selected code.
func (t Theme) SelectionBackground() *gc.Hsla { c := t.Selection; return &c }

// MatchBackground is a text-run background for find matches.
func (t Theme) MatchBackground() *gc.Hsla { c := t.Match; return &c }

// MenuMetrics are the fixed sizes of menu chrome. Untyped constants: use
// them in float32 expressions directly, or wrap with ui.F(px).
const (
	MenuBarHeight  = 28.0
	MenuItemHeight = 26.0
	MenuPadY       = 6.0
	MenuWidth      = 260.0
	MenuItemPadX   = 10.0
	ContextPad     = 8.0
	ModalWidth     = 560.0
	ModalHeight    = 380.0
	TabBarHeight   = 34.0
	StatusHeight   = 26.0
	TreeWidth      = 240.0
	TerminalHeight = 220.0
	FindBarHeight  = 34.0

	// Dock chrome (the upstream editor's bottom dock): a slim resize strip rides the top
	// edge of the terminal panel; the header row below it is part of the
	// grab area too (the whole top part resizes the dock).
	TermResizeStrip = 5.0
	TermHeaderH     = 26.0
	TermInputH      = 26.0

	// Custom window decorations: the in-app title bar's height and
	// the window button boxes (close/minimize/maximize).
	TitleBarHeight  = 34.0
	TitleBarButtonW = 46.0
	LangMenuWidth   = 320.0
	LangMenuRowH    = 24.0
	LangMenuVisible = 10.0 // visible rows before scrolling
	// LangMenuScrollbarW is the scrollbar's hit area inside the picker's
	// list: the thumb paints 5px inside it, the rest is grab margin —
	// a 4px thumb alone is nearly impossible to catch with the pointer.
	LangMenuScrollbarW = 10.0

	// Panel resize limits: the user drags the dock edges, but the docks
	// never starve the editor (or the window chrome) below a working size.
	TermMinHeight      = 120.0
	TreeMinWidth       = 160.0
	MinEditorHeight    = 140.0
	MinEditorWidth     = 320.0
	TreeResizeGrip     = 6.0 // width of the left dock's edge grab strip
	TreeHeaderH        = 30.0
	TreeScrollbarW     = 10.0
	TreeScrollbarInset = 4.0
)

// MonoText renders text in the monospace family with explicit runs.
func MonoText(s string, c gc.Hsla) *elements.Text {
	return elements.NewText(s).WithRuns([]text.TextRun{
		{Len: len(s), Font: font.Font{Family: "Monospace", Weight: font.WeightNormal}, Color: c},
	})
}
