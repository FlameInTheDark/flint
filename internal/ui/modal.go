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
// Text input
// ---------------------------------------------------------------------------

// InputState is a single-line editable field's state.
type InputState struct {
	Value  []rune
	Cursor int // rune index
	// Focused draws the caret.
	Focused bool
}

// Set replaces the value and puts the cursor at the end.
func (s *InputState) Set(v string) {
	s.Value = []rune(v)
	s.Cursor = len(s.Value)
}

// String returns the current text.
func (s *InputState) String() string { return string(s.Value) }

// Insert inserts text at the cursor.
func (s *InputState) Insert(text string) {
	rs := []rune(text)
	s.Value = append(s.Value[:s.Cursor], append(rs, s.Value[s.Cursor:]...)...)
	s.Cursor += len(rs)
}

// Backspace deletes before the cursor.
func (s *InputState) Backspace() {
	if s.Cursor == 0 {
		return
	}
	s.Value = append(s.Value[:s.Cursor-1], s.Value[s.Cursor:]...)
	s.Cursor--
}

// Delete deletes at the cursor.
func (s *InputState) Delete() {
	if s.Cursor >= len(s.Value) {
		return
	}
	s.Value = append(s.Value[:s.Cursor], s.Value[s.Cursor+1:]...)
}

// Home moves the cursor to the start.
func (s *InputState) Home() { s.Cursor = 0 }

// End moves the cursor to the end.
func (s *InputState) End() { s.Cursor = len(s.Value) }

// Left moves the cursor one rune left.
func (s *InputState) Left() {
	if s.Cursor > 0 {
		s.Cursor--
	}
}

// Right moves the cursor one rune right.
func (s *InputState) Right() {
	if s.Cursor < len(s.Value) {
		s.Cursor++
	}
}

// Key handles one key event; it reports whether the key was consumed.
func (s *InputState) Key(ev platform.KeyDownEvent) bool {
	k := ev.Keystroke
	mods := k.Modifiers
	if mods.Control || mods.Platform || mods.Alt {
		// Ctrl/Alt combinations are left to the container (e.g. Enter).
		return false
	}
	switch k.Key {
	case "backspace":
		s.Backspace()
	case "delete":
		s.Delete()
	case "left":
		s.Left()
	case "right":
		s.Right()
	case "home":
		s.Home()
	case "end":
		s.End()
	default:
		ch := k.KeyChar
		if ch == "" {
			return false
		}
		// KeyChar is ONE character by contract; a multi-rune value is a
		// synthetic event (a test helper passing the key NAME), not text
		// the user typed — a filter field must not swallow "down" as text
		// and cancel list navigation.
		if len([]rune(ch)) != 1 {
			return false
		}
		s.Insert(ch)
	}
	return true
}

// InputConfig configures RenderInput.
type InputConfig struct {
	ID    string
	State *InputState
	Theme Theme
	// Placeholder is shown when empty.
	Placeholder string
	// Update applies a state mutation (redraw).
	Update func(f func(*InputState))
	// OnEnter is called on Enter (may be nil).
	OnEnter func(w *gpui.Window)
	// OnEscape is called on Escape (may be nil).
	OnEscape func(w *gpui.Window)
	// Focus is the input's focus handle.
	Focus gpui.FocusHandle
}

// RenderInput renders a single-line input field with a caret. When Focus is
// invalid the field is display-only (no keyboard capture): the container
// routes keys to the state itself.
func RenderInput(cfg InputConfig) gpui.Element {
	t := cfg.Theme
	st := cfg.State

	id := cfg.ID
	if id == "" {
		id = "input"
	}
	field := elements.NewDiv().
		ID(elements.ButtonID(id)).
		Flex().ItemsCenter().FlexGrow(1).MinW_0().WhitespaceNowrap().OverflowHidden().
		H(Px(26)).Px(DefPx(8)).
		CursorText().
		BgColor(t.EditorBg).Border_1().BorderColor(t.Surface2).Rounded(AbsPx(4))
	if cfg.Focus.IsValid() {
		field = field.
			TrackFocus(cfg.Focus).
			OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
				k := ev.Keystroke
				switch k.Key {
				case "enter":
					if cfg.OnEnter != nil {
						cfg.OnEnter(w)
					}
					return
				case "escape":
					if cfg.OnEscape != nil {
						cfg.OnEscape(w)
					}
					return
				}
				// Mutate the state through the update callback so the
				// owner can react (sync query strings, recompute
				// matches) in the same update scope.
				consumed := false
				cfg.Update(func(s *InputState) { consumed = s.Key(ev) })
				if consumed {
					w.Refresh()
				}
			})
	}

	if len(st.Value) == 0 && cfg.Placeholder != "" {
		field = field.Child(elements.NewText(cfg.Placeholder).WithTextColor(t.Overlay))
	} else {
		cursor := len(string(st.Value[:max(0, min(st.Cursor, len(st.Value)))]))
		field = field.Child(elements.NewText(st.String()).WithTextColor(t.Text).WithCaret(cursor, t.Accent))
	}
	return field
}

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
