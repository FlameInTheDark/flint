package ui

import (
	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

// ---------------------------------------------------------------------------
// Modal shell
// ---------------------------------------------------------------------------

// ModalConfig configures RenderModal.
type ModalConfig struct {
	Theme Theme
	// Title is the card header.
	Title string
	// Width, Height override the defaults (0 = default).
	Width, Height float32
	// WindowSize for centering.
	WindowSize geometry.Size[geometry.Pixels]
	// Focus is the modal's keyboard focus handle.
	Focus gpui.FocusHandle
	// Body renders the modal content (inside the card, below the title).
	Body func() gpui.Element
	// OnClose is called on Escape and backdrop clicks (nil = not closable
	// that way).
	OnClose func(w *gpui.Window)
	// Update is the workspace update helper for the body's needs.
	Update func(f func())
}

// RenderModal renders [backdrop, card] for a centered modal dialog.
func RenderModal(cfg ModalConfig) []gpui.Element {
	t := cfg.Theme
	winW := float32(cfg.WindowSize.Width.AsF32())
	winH := float32(cfg.WindowSize.Height.AsF32())
	w := float32(ModalWidth)
	h := float32(ModalHeight)
	if cfg.Width > 0 {
		w = cfg.Width
	}
	if cfg.Height > 0 {
		h = cfg.Height
	}

	var backdrop *elements.Div
	backdrop = elements.NewDiv().
		Absolute().Left(Px(0)).Top(Px(0)).
		W(Px(winW)).H(Px(winH)).
		BgColor(gc.Black().Alpha(0.45)).
		Occlude()
	if cfg.OnClose != nil {
		backdrop = backdrop.OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			cfg.OnClose(w)
			w.Refresh()
		})
	}

	card := elements.NewDiv().
		Absolute().
		Left(Px((winW - w) / 2)).Top(Px((winH - h) / 2)).
		W(Px(w)).H(Px(h)).
		Flex().FlexCol().
		BgColor(t.Crust).Border_1().BorderColor(t.Surface1).Rounded(AbsPx(8)).
		Occlude().
		TrackFocus(cfg.Focus).
		OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
			if ev.Keystroke.Key == "escape" && cfg.OnClose != nil {
				cfg.OnClose(w)
				w.Refresh()
			}
		}).
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			w.StopPropagation()
		}).
		// Header.
		Child(elements.NewDiv().Flex().ItemsCenter().H(Px(38)).Px(DefPx(14)).
			BorderB_1().BorderColor(t.Surface1).
			Child(elements.NewText(cfg.Title).WithTextColor(t.Text)).
			Child(elements.NewDiv().FlexGrow(1)).
			Child(func() gpui.Element {
				btn := elements.NewDiv().
					ID(elements.ButtonID("modal-close")).
					Flex().ItemsCenter().JustifyCenter().
					W(Px(20)).H(Px(20)).Rounded(AbsPx(4)).
					TextSize(AbsPx(13)).
					Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
					Child(Icon("x", t.Dim, 14))
				if cfg.OnClose != nil {
					btn = btn.Tooltip("Close dialog (Esc)").OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
						cfg.OnClose(w)
						w.Refresh()
					})
				}
				return btn
			}()))

	if cfg.Body != nil {
		card = card.Child(cfg.Body())
	}
	return []gpui.Element{backdrop, card}
}

// Button renders a small labeled button (used in modals).
func Button(id, label string, t Theme, accent bool, onClick func(w *gpui.Window)) gpui.Element {
	bg := t.Surface1
	fg := t.Text
	if accent {
		bg = t.Accent
		fg = gc.RGB(0x1e1e2e).Hsla()
	}
	return elements.NewDiv().
		ID(elements.ButtonID(id)).
		Flex().ItemsCenter().JustifyCenter().
		H(Px(26)).Px(DefPx(14)).
		BgColor(bg).Rounded(AbsPx(4)).
		CursorPointer().
		Hover(func(s *style.StyleRefinement) {
			if accent {
				s.BgColor(t.Accent.Alpha(0.8))
			} else {
				s.BgColor(t.Surface2)
			}
		}).
		OnClick(func(ev gpui.ClickEvent, w *gpui.Window) { onClick(w) }).
		Child(elements.NewText(label).WithTextColor(fg))
}

// SecondaryMod reports the "secondary" (shortcut) modifier: ctrl outside
// macOS, cmd on macOS (best-effort for an example).
func SecondaryMod(m keymap.Modifiers) bool {
	return m.Control || m.Platform
}
