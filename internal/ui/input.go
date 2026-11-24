package ui

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
	"github.com/FlameInTheDark/go-gpui/text"
)

// InputState stores an editable value and a selection in rune offsets.
type InputState struct {
	Value    []rune
	Cursor   int
	anchor   int
	anchored bool
	dragging bool
}

func (s *InputState) Set(v string) {
	s.Value = []rune(v)
	s.Move(len(s.Value), false)
	s.dragging = false
}

func (s *InputState) String() string { return string(s.Value) }

func (s *InputState) Selection() (lo, hi int) {
	if !s.anchored {
		return s.Cursor, s.Cursor
	}
	return min(s.anchor, s.Cursor), max(s.anchor, s.Cursor)
}

func (s *InputState) Move(pos int, extend bool) {
	if extend && !s.anchored {
		s.anchor = s.Cursor
	}
	s.Cursor = max(0, min(pos, len(s.Value)))
	s.anchored = extend
}

func (s *InputState) Insert(value string) {
	lo, hi := s.Selection()
	s.replace(lo, hi, value)
}

func (s *InputState) replace(lo, hi int, value string) {
	runes := []rune(value)
	s.Value = slices.Replace(s.Value, lo, hi, runes...)
	s.Move(lo+len(runes), false)
}

func wordClass(r rune) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r), r == '_':
		return 1
	default:
		return 2
	}
}

func (s *InputState) wordLeft(i int) int {
	for i > 0 && wordClass(s.Value[i-1]) == 0 {
		i--
	}
	if i > 0 {
		kind := wordClass(s.Value[i-1])
		for i > 0 && wordClass(s.Value[i-1]) == kind {
			i--
		}
	}
	return i
}

func (s *InputState) wordRight(i int) int {
	if i < len(s.Value) && wordClass(s.Value[i]) != 0 {
		kind := wordClass(s.Value[i])
		for i < len(s.Value) && wordClass(s.Value[i]) == kind {
			i++
		}
		if runtime.GOOS == "darwin" {
			return i
		}
	}
	for i < len(s.Value) && wordClass(s.Value[i]) == 0 {
		i++
	}
	if runtime.GOOS == "darwin" && i < len(s.Value) {
		return s.wordRight(i)
	}
	return i
}

func (s *InputState) selectWord(pos int) {
	if len(s.Value) == 0 {
		return
	}
	lo := min(pos, len(s.Value)-1)
	kind := wordClass(s.Value[lo])
	hi := lo + 1
	for lo > 0 && wordClass(s.Value[lo-1]) == kind {
		lo--
	}
	for hi < len(s.Value) && wordClass(s.Value[hi]) == kind {
		hi++
	}
	s.Move(lo, false)
	s.Move(hi, true)
}

// Key edits a single-line field, returning whether the event was consumed.
func (s *InputState) Key(ev platform.KeyDownEvent, w *gpui.Window) bool {
	return s.key(ev, w, false)
}

// MultilineKey preserves clipboard line breaks for a multiline field.
func (s *InputState) MultilineKey(ev platform.KeyDownEvent, w *gpui.Window) bool {
	return s.key(ev, w, true)
}

func (s *InputState) key(ev platform.KeyDownEvent, w *gpui.Window, multiline bool) bool {
	k := ev.Keystroke
	m := k.Modifiers
	lo, hi := s.Selection()
	if SecondaryMod(m) && !m.Alt {
		switch k.Key {
		case "a":
			s.Move(0, false)
			s.Move(len(s.Value), true)
			return true
		case "c", "x", "v":
			if w != nil && w.PlatformWindow() != nil {
				pw := w.PlatformWindow()
				if k.Key == "v" {
					if item, ok := pw.ReadClipboard(); ok && item.Text != "" {
						value := strings.ReplaceAll(strings.ReplaceAll(item.Text, "\r\n", "\n"), "\r", "\n")
						if !multiline {
							value = strings.ReplaceAll(value, "\n", " ")
						}
						s.Insert(value)
					}
				} else if lo != hi {
					pw.WriteClipboard(platform.ClipboardItem{Text: string(s.Value[lo:hi])})
					if k.Key == "x" {
						s.replace(lo, hi, "")
					}
				}
			}
			return true
		}
	}
	word := (m.Control && !m.Alt && !m.Platform) || (runtime.GOOS == "darwin" && m.Alt && !SecondaryMod(m))
	line := runtime.GOOS == "darwin" && m.Platform && !m.Control && !m.Alt
	plain := !SecondaryMod(m) && !m.Alt
	switch k.Key {
	case "left", "right":
		if !plain && !word && !line {
			return false
		}
		pos := s.Cursor
		if k.Key == "left" {
			switch {
			case line:
				pos = 0
			case word:
				pos = s.wordLeft(pos)
			case !m.Shift && lo != hi:
				pos = lo
			default:
				pos--
			}
		} else {
			switch {
			case line:
				pos = len(s.Value)
			case word:
				pos = s.wordRight(pos)
			case !m.Shift && lo != hi:
				pos = hi
			default:
				pos++
			}
		}
		s.Move(pos, m.Shift)
	case "home", "end":
		if m.Alt {
			return false
		}
		pos := 0
		if k.Key == "end" {
			pos = len(s.Value)
		}
		s.Move(pos, m.Shift)
	case "backspace", "delete":
		if !plain && !word {
			return false
		}
		if lo == hi {
			if k.Key == "backspace" {
				lo = max(0, lo-1)
				if word {
					lo = s.wordLeft(hi)
				}
			} else {
				hi = min(len(s.Value), hi+1)
				if word {
					hi = s.wordRight(lo)
				}
			}
		}
		s.replace(lo, hi, "")
	default:
		// Ignore named/synthetic keys and control characters, not typed text.
		r, n := utf8.DecodeRuneInString(k.KeyChar)
		if !plain || n == 0 || n != len(k.KeyChar) || unicode.IsControl(r) {
			return false
		}
		s.Insert(k.KeyChar)
	}
	return true
}

type InputConfig struct {
	ID          string
	State       *InputState
	Theme       Theme
	Placeholder string
	Update      func(func(*InputState))
	OnEnter     func(*gpui.Window)
	OnEscape    func(*gpui.Window)
	// With no Focus the parent handles keyboard input; Update still enables mouse selection.
	Focus gpui.FocusHandle
}

func RenderInput(cfg InputConfig) gpui.Element {
	t, st := cfg.Theme, cfg.State
	id := cfg.ID
	if id == "" {
		id = fmt.Sprintf("input-%d", cfg.Focus.FocusID())
	}
	field := elements.NewDiv().ID(elements.ButtonID(id)).
		Flex().ItemsCenter().FlexGrow(1).MinW_0().WhitespaceNowrap().OverflowHidden().
		H(Px(26)).Px(DefPx(8)).CursorText().
		BgColor(t.EditorBg).Border_1().BorderColor(t.Surface2).Rounded(AbsPx(4))
	lo, hi := st.Selection()
	line := NewInputLine(st.String(), t, st.Cursor, lo, hi)
	if len(st.Value) == 0 && cfg.Placeholder != "" {
		field.Child(elements.NewText(cfg.Placeholder).WithTextColor(t.Overlay))
	} else {
		field.Child(line)
	}
	if cfg.Update != nil {
		BindInputPointer(field, cfg.Focus, cfg.Update, func(p geometry.Point[geometry.Pixels], w *gpui.Window) int {
			return line.IndexAt(p.X, w)
		})
	}
	if cfg.Focus.IsValid() && cfg.Update != nil {
		field.TrackFocus(cfg.Focus).OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
			var action func(*gpui.Window)
			switch ev.Keystroke.Key {
			case "enter":
				action = cfg.OnEnter
			case "escape":
				action = cfg.OnEscape
			}
			if action != nil {
				w.StopPropagation()
				action(w)
				return
			}
			consumed := false
			cfg.Update(func(s *InputState) { consumed = s.Key(ev, w) })
			if consumed {
				w.StopPropagation()
				w.Refresh()
			}
		})
	}
	return field
}

// BindInputPointer shares click, Shift+click, double-click and drag selection
// between single-line fields and the multiline commit message.
func BindInputPointer(field *elements.Div, focus gpui.FocusHandle, update func(func(*InputState)), index func(geometry.Point[geometry.Pixels], *gpui.Window) int) {
	field.OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
		if ev.Button != platform.ButtonLeft {
			return
		}
		if focus.IsValid() {
			w.Focus(focus)
		}
		pos := index(ev.Position, w)
		update(func(s *InputState) {
			s.dragging = true
			if ev.ClickCount == 2 && !ev.Modifiers.Shift {
				s.selectWord(pos)
			} else {
				s.Move(pos, ev.Modifiers.Shift)
			}
		})
		if hb, ok := w.ListenerHitbox(); ok {
			w.CapturePointer(hb.ID)
		}
		w.StopPropagation()
		w.Refresh()
	}).OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
		if !ev.Dragging() {
			return
		}
		pos := index(ev.Position, w)
		moved := false
		update(func(s *InputState) {
			if s.dragging {
				s.Move(pos, true)
				moved = true
			}
		})
		if moved {
			w.StopPropagation()
			w.Refresh()
		}
	}).OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
		if ev.Button == platform.ButtonLeft {
			update(func(s *InputState) { s.dragging = false })
		}
	})
}

// InputLine uses the same shaped text for rendering and pointer hit testing.
type InputLine struct {
	*elements.Text
	value          string
	theme          Theme
	cursor, lo, hi int // byte offsets; cursor is -1 on inactive rows
	node           gpui.LayoutID
	align          style.TextAlign
}

func NewInputLine(value string, t Theme, cursor, lo, hi int) *InputLine {
	runes := []rune(value)
	byteAt := func(i int) int { return len(string(runes[:max(0, min(i, len(runes)))])) }
	line := &InputLine{Text: elements.NewText(value).WithTextColor(t.Text), value: value, theme: t, cursor: -1, lo: byteAt(lo), hi: byteAt(hi)}
	if cursor >= 0 {
		line.cursor = byteAt(cursor)
		line.WithCaret(line.cursor, t.Accent)
	}
	return line
}

func (l *InputLine) RequestLayout(w *gpui.Window) {
	ts := w.TextStyle()
	l.align = ts.TextAlign
	if l.lo < l.hi {
		font, bg := ts.ToFont(), l.theme.SelectionBackground()
		runs := make([]text.TextRun, 0, 3)
		if l.lo > 0 {
			runs = append(runs, text.TextRun{Len: l.lo, Font: font, Color: l.theme.Text})
		}
		runs = append(runs, text.TextRun{Len: l.hi - l.lo, Font: font, Color: l.theme.Text, BackgroundColor: bg})
		if l.hi < len(l.value) {
			runs = append(runs, text.TextRun{Len: len(l.value) - l.hi, Font: font, Color: l.theme.Text})
		}
		l.WithRuns(runs)
	}
	l.node = w.RequestLayoutElement(l.Text)
}

func (l *InputLine) Prepaint(w *gpui.Window, _ geometry.Bounds[geometry.Pixels]) {
	w.PrepaintElement(l.Text, l.node)
}

func (l *InputLine) Paint(w *gpui.Window) { w.PaintElement(l.Text) }

func (l *InputLine) IndexAt(x geometry.Pixels, w *gpui.Window) int {
	layout := l.Layout()
	if layout == nil {
		return 0
	}
	bounds := w.ElementBounds(l.Text)
	origin := bounds.Origin.X
	switch l.align {
	case style.TextAlignCenter:
		origin += (bounds.Size.Width - layout.Width) / 2
	case style.TextAlignRight:
		origin += bounds.Size.Width - layout.Width
	}
	// Match Text.WithCaret's horizontal pan when the line is clipped.
	const caretWidth = geometry.Pixels(1.5)
	if l.cursor >= 0 && layout.Width+caretWidth > bounds.Size.Width {
		caret := origin + layout.XForIndex(l.cursor) - layout.XForIndex(0)
		origin -= max(0, caret-max(bounds.Origin.X, bounds.Right()-caretWidth))
	}
	index := layout.ClosestIndexForX(x - origin + layout.XForIndex(0))
	return utf8.RuneCountInString(l.value[:max(0, min(index, len(l.value)))])
}
