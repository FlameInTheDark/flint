package editor

import (
	"strings"
	"unicode/utf8"

	"github.com/FlameInTheDark/flint/internal/ui"
	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/style"
	"github.com/FlameInTheDark/go-gpui/text"
)

// CodeShaper measures lines that aren't on screen yet (for horizontal
// caret reveal and drag selection). On-screen input uses Text.Layout(),
// the very same layout that painted the frame.
type CodeShaper struct {
	system *text.System
	size   geometry.Pixels
}

func NewCodeShaper(w *gpui.Window, fontSize float32) CodeShaper {
	size := w.TextStyle().FontSize.ToPixels(w.RemSize())
	if fontSize > 0 {
		size = geometry.Px(fontSize)
	}
	return CodeShaper{system: w.TextSystem(), size: size}
}

type expandedTab struct {
	source, start, end int
}

// codeLine maps UTF-8 byte offsets in the buffer to UTF-8 byte offsets in
// the displayed line. Only tabs change those offsets: keep a sparse map so
// ordinary lines need no conversion table.
type codeLine struct {
	source, display string
	tabs            []expandedTab
	element         *elements.Text
	row             *elements.Div
}

func newCodeLine(source string) codeLine {
	l := codeLine{source: source, display: source}
	if !strings.ContainsRune(source, '\t') {
		return l
	}
	var out strings.Builder
	out.Grow(len(source))
	col, start := 0, 0
	for i, r := range source {
		if r == '\t' {
			out.WriteString(source[start:i])
			n := tabAdvance(col)
			pos := out.Len()
			for j := 0; j < n; j++ {
				out.WriteByte(' ')
			}
			l.tabs = append(l.tabs, expandedTab{source: i, start: pos, end: pos + n})
			col += n
			start = i + 1
		} else {
			col++
		}
	}
	out.WriteString(source[start:])
	l.display = out.String()
	return l
}

func (l codeLine) displayByte(sourceByte int) int {
	sourceByte = min(max(sourceByte, 0), len(l.source))
	extra := 0
	for _, tab := range l.tabs {
		if tab.source >= sourceByte {
			break
		}
		extra += tab.end - tab.start - 1
	}
	return sourceByte + extra
}

// sourceByte maps a displayed byte back to the buffer. Within a tab's
// expanded spaces, return the tab itself; a caret can then snap to either
// edge based on the measured distance to each edge.
func (l codeLine) sourceByte(displayByte int) (int, *expandedTab) {
	displayByte = min(max(displayByte, 0), len(l.display))
	extra := 0
	for i := range l.tabs {
		tab := &l.tabs[i]
		if displayByte < tab.start {
			break
		}
		if displayByte < tab.end {
			return tab.source, tab
		}
		extra += tab.end - tab.start - 1
	}
	index := displayByte - extra
	if index < len(l.source) && !utf8.RuneStart(l.source[index]) && utf8.ValidString(l.source) {
		for index > 0 && !utf8.RuneStart(l.source[index]) {
			index--
		}
	}
	return index, nil
}

func (s CodeShaper) shape(l codeLine, lang string, palette Palette) *text.LineLayout {
	if s.system == nil {
		return nil
	}
	// Match elements.Text's font-run construction. Selection and find
	// only change backgrounds; Text merges those splits back for shaping.
	var small [8]text.FontRun
	fontRuns := small[:0]
	for _, run := range HighlightCached(l.display, lang, palette) {
		id, _ := s.system.FontID(run.Font)
		if i := len(fontRuns) - 1; i >= 0 && fontRuns[i].FontID == id {
			fontRuns[i].Len += run.Len
		} else {
			fontRuns = append(fontRuns, text.FontRun{Len: run.Len, FontID: id})
		}
	}
	return s.system.LayoutLine(l.display, s.size, fontRuns)
}

// layoutX uses the same row origin as the unwrapped Text painter. In
// particular, the logical start of a reordered line need not be at x=0.
func layoutX(layout *text.LineLayout, index int) geometry.Pixels {
	return layout.XForIndex(index) - layout.XForIndex(0)
}

// The ordinary Text painter anchors the logical start at its element's
// origin. For RTL-base code, compensate so the visual line fits to the
// right of the gutter instead of being painted to its left.
func visualOriginShift(layout *text.LineLayout) geometry.Pixels {
	if layout != nil && layout.BidiBaseRTL {
		return layout.XForIndex(0)
	}
	return 0
}

func (s CodeShaper) CaretMetrics(b *Buffer, theme ui.Theme) (x, width float32) {
	row, col := b.OffsetToLC(b.Cursor)
	l := newCodeLine(b.Line(row))
	if shaped := s.shape(l, b.LanguageOf(), paletteOf(theme)); shaped != nil {
		return (visualOriginShift(shaped) + layoutX(shaped, l.displayByte(col))).AsF32(), shaped.Width.AsF32()
	}
	return 0, 0
}

func (l codeLine) caretAt(shaped *text.LineLayout, x float32) int {
	if shaped == nil {
		return 0
	}
	penX := geometry.Px(x) + shaped.XForIndex(0)
	index, tab := l.sourceByte(nearestCaretByte(shaped, penX))
	if tab != nil && absF(x-layoutX(shaped, tab.end).AsF32()) < absF(x-layoutX(shaped, tab.start).AsF32()) {
		return index + 1
	}
	return index
}

func nearestCaretByte(l *text.LineLayout, x geometry.Pixels) int {
	if l.BidiSegments != nil {
		return l.ClosestIndexForX(x)
	}
	// The generic LTR helper jumps to the end as soon as x passes the
	// last glyph origin on a multi-byte line. Treat line end as a real
	// boundary, so the first half of the last glyph still clicks before it.
	index, prevX := 0, l.XForIndex(0)
	for _, run := range l.Runs {
		for _, glyph := range run.Glyphs {
			if glyph.Index <= index {
				continue // an additional glyph in the same shaping cluster
			}
			gx := glyph.Position.X
			if x <= gx {
				if x-prevX < gx-x {
					return index
				}
				return glyph.Index
			}
			index, prevX = glyph.Index, gx
		}
	}
	if x-prevX < l.Width-x {
		return index
	}
	return l.Len
}

// caretOverlay paints over the text, using its prepaint bounds and layout
// rather than placing a second flex element at an estimated x coordinate.
func (l codeLine) caretOverlay(sourceByte int, lineHeight float32, color gc.Hsla) gpui.Element {
	index := l.displayByte(sourceByte)
	return elements.NewCanvas(nil, func(_ geometry.Bounds[geometry.Pixels], w *gpui.Window) {
		shaped := l.element.Layout()
		if shaped == nil {
			return
		}
		origin := w.ElementBounds(l.element).Origin
		origin.X += layoutX(shaped, index)
		origin.Y += geometry.Px(2)
		bg := gc.SolidBackground(color)
		w.PaintQuad(style.Style{Background: &bg}, geometry.Bounds[geometry.Pixels]{
			Origin: origin,
			Size:   geometry.SizePixels(2, max(1, lineHeight-4)),
		})
	}).Absolute().Left(ui.Px(0)).Top(ui.Px(0)).W(ui.Px(1)).H(ui.Px(lineHeight))
}
