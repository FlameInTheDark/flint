package editor

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
	"github.com/FlameInTheDark/go-gpui/text"

	"github.com/FlameInTheDark/flint/internal/ui"
)

// Metrics are the pane's fixed text metrics.
type Metrics struct {
	CharAdvance float32
	LineHeight  float32
}

// PaneHeight is the visible line count for a viewport of viewportH px.
func (m Metrics) VisibleLines(viewportH float32) int {
	if m.LineHeight <= 0 {
		return 1
	}
	n := int(viewportH / m.LineHeight)
	if n < 1 {
		n = 1
	}
	return n
}

// FindState is the workspace's in-file search state.
type FindState struct {
	Query   string
	Matches [][2]int // buffer byte ranges
	Current int      // index into Matches (-1 = none)
}

// FindMatches returns all case-insensitive substring matches of query in
// text, as byte ranges.
func FindMatches(text, query string) [][2]int {
	if query == "" {
		return nil
	}
	lower := strings.ToLower(text)
	q := strings.ToLower(query)
	var out [][2]int
	for i := 0; i+len(q) <= len(lower); {
		if at := strings.Index(lower[i:], q); at >= 0 {
			start := i + at
			out = append(out, [2]int{start, start + len(q)})
			i = start + len(q)
		} else {
			break
		}
	}
	return out
}

// MatchesOnLine clips matches to line [lineStart, lineEnd).
func (f *FindState) MatchesOnLine(lineStart, lineEnd int) [][2]int {
	var out [][2]int
	for _, m := range f.Matches {
		if m[1] > lineStart && m[0] < lineEnd {
			out = append(out, m)
		}
	}
	return out
}

// PaneConfig configures RenderPane. All callbacks run inside a workspace
// update (they mutate the buffer through OnEdit's update scope).
type PaneConfig struct {
	Buf     *Buffer
	Theme   ui.Theme
	Metrics Metrics
	Shaper  CodeShaper
	// CaretLineWidth comes from the styled shaping, not the monospace
	// advance estimate (fallback glyphs may be wider).
	CaretLineWidth float32
	// Scroll is the first visible line (virtualized scrolling). The
	// whole-row paths (keys, caret, seeks) read it; it is the scroll
	// target rounded to rows.
	Scroll int
	// ScrollF is the RENDERED scroll position in fractional rows (the
	// wheel ease). When it equals float32(Scroll) the pane renders
	// exactly as it always has; a fractional part shifts the visible
	// window between rows for the smooth glide.
	ScrollF float32
	// ViewportH is the pane height in px (for the visible line count).
	ViewportH float32
	// ScrollX is the code column's horizontal pixel offset (wide lines
	// pan; the gutter and the minimap rail stay fixed).
	ScrollX float32
	// ViewportW is the pane width in px (window minus the project
	// panel). 0 disables the horizontal scrollbar and the minimap rail
	// ( callers that do not know their width yet).
	ViewportW float32
	// ShowGutter toggles line numbers.
	ShowGutter bool
	// Debug markers use the existing gutter inset, even if numbers are hidden.
	// Line numbers in this set and ExecutionLine are one-based.
	Breakpoints   map[int]BreakpointMarker
	ExecutionLine int
	OnBreakpoint  func(line int)
	// ShowMinimap toggles the minimap rail right of the code (the upstream editor's
	// minimap; the settings panel switches it).
	ShowMinimap bool
	// FontSizePx overrides the glyph size (0 = the window default). The
	// workspace passes the settings panel's font size here; the metrics
	// (CharAdvance, LineHeight) are measured at the same size so cursor
	// mapping stays exact.
	FontSizePx float32
	// Find highlights matches when non-nil.
	Find *FindState
	// Focus is the editor's keyboard focus handle.
	Focus gpui.FocusHandle
	// OnEdit applies a buffer mutation (the caller wraps its update).
	OnEdit func(f func(*Buffer))
	// OnScroll scrolls by whole lines (positive = down). Snap paths:
	// minimap/scrollbar seeks and caret scrolling land exactly.
	OnScroll func(delta int)
	// OnWheelScroll applies a raw wheel delta (fractional rows) through
	// the workspace's smooth-scroll ease. When nil the pane falls back
	// to OnScroll with the delta truncated to rows.
	OnWheelScroll func(deltaF float32)
	// OnScrollX applies a horizontal pixel delta (wheel / scrollbar).
	OnScrollX func(deltaPx float32)
	// OnContext opens a context menu at the position (right-click).
	OnContext func(pos geometry.Point[geometry.Pixels])
	// OnSave runs Ctrl+S.
	OnSave func()
	// OnFind runs Ctrl+F.
	OnFind func()
	// OnTerminalToggle runs Ctrl+`.
	OnTerminalToggle func()
	// OnKey gets first refusal for completion/documentation shortcuts.
	OnKey func(platform.KeyDownEvent) bool
	// OnDefinition requests the definition of the Ctrl-clicked symbol.
	OnDefinition func(offset int)
	// CaretVisible drives the blinking caret (the workspace flips it on a
	// timer and forces it on around cursor activity).
	CaretVisible bool
}

type BreakpointMarker struct {
	Rejected bool
}

// Rail metrics: the minimap + vertical scrollbar column on the pane's
// right edge (the upstream editor's minimap lives left of the scrollbar).
const (
	MinimapWidth      = 96.0 // minimap block area
	VScrollWidth      = 10.0 // vertical scrollbar track
	RailWidth         = MinimapWidth + VScrollWidth + 12
	MinimapLineHeight = 3.0 // px per line in the minimap
	MinimapBlockH     = 2.0 // block height (1px gap)
	HScrollHeight     = 9.0 // horizontal scrollbar track
	// maxMinimapRunsPerLine caps the per-line token blocks so a
	// pathological line cannot blow up the minimap's paint cost.
	maxMinimapRunsPerLine = 14
)

// The highlight cache memoizes Highlight per (lang, line, context,
// palette): the code pane and minimap share the runs when computing
// geometry. Keys carry the language and the line's starting context (a
// raw string or block comment carried over from the previous lines) so
// the same text highlights differently per file type and per position.
//
// Entries age in two generations. When the current generation fills, it
// becomes the previous one and a fresh map takes its place — a plain
// clear-all emptied EVERY memoized line at once, and scrolling a long
// file then re-tokenized hundreds of lines inside a single frame (the
// minimap ring and the rows all missed together): periodic stutter that
// grew with the document. The swap keeps the previous generation
// reachable (a hit there promotes the entry into the current one), so
// the working set of a steady scroll never falls off a cliff, while
// memory stays bounded at twice the cap.
type highlightKey struct {
	lang, line string
	palette    Palette
	state      LineState
}

type highlightVal struct {
	runs  []text.TextRun
	state LineState // the context the next line starts in
}

var (
	highlightCachePrev = map[highlightKey]highlightVal{}
	highlightCacheCur  = map[highlightKey]highlightVal{}
)

// highlightCacheCap caps one cache generation (x2 total across both).
const highlightCacheCap = 16384

// HighlightCached returns Highlight's runs, memoized per line text + lang
// (top-of-file context).
func HighlightCached(line, lang string, p Palette) []text.TextRun {
	runs, _ := HighlightCachedIn(line, lang, StateNormal, p)
	return runs
}

// HighlightCachedIn is HighlightIn, memoized per (lang, line, context,
// palette). The starting context is part of the key: identical line text
// highlights differently inside a raw string than at the top level.
func HighlightCachedIn(line, lang string, st LineState, p Palette) ([]text.TextRun, LineState) {
	key := highlightKey{lang: lang, line: line, palette: p, state: st}
	if v, ok := highlightCacheCur[key]; ok {
		return v.runs, v.state
	}
	if v, ok := highlightCachePrev[key]; ok {
		highlightCacheCur[key] = v // promote into the current generation
		return v.runs, v.state
	}
	runs, end := HighlightIn(line, lang, st, p)
	if len(highlightCacheCur) >= highlightCacheCap {
		highlightCachePrev = highlightCacheCur
		highlightCacheCur = map[highlightKey]highlightVal{}
	}
	highlightCacheCur[key] = highlightVal{runs: runs, state: end}
	return runs, end
}

// GutterWidth returns the gutter's pixel width for a line count (the
// workspace reuses it to size popups against the code column; folds adds
// the fold-marker column).
func GutterWidth(lines int, adv float32, folds bool) float32 { return gutterWidth(lines, adv, folds) }

// GutterLeftInset is the gutter's left inset (logical px): the leftmost
// digits - or the code text when line numbers are hidden - never touch the
// pane's left edge (the upstream editor keeps the same breathing room).
const GutterLeftInset = 16.0

// GutterCodeGap is the clear separation (logical px) between the line
// numbers' right edge and the first code glyph: without it the code text
// touches the numbers column and the two visually blend together.
const GutterCodeGap = 16.0

// FoldGutterW is the fold-marker column's width (the chevron buttons in
// their own band left of the line numbers, like GoLand's fold gutter and
// VS Code's glyph margin; added to the gutter only when the language can
// fold).
const FoldGutterW = 16.0

// CodeDividerGap is the portion of GutterCodeGap on the code side of the
// divider. Keeping it in the gutter's footprint leaves the text origin,
// clipping and pointer-to-column mapping identical when horizontally panned.
const CodeDividerGap = 6.0

// gutterWidth returns the gutter's pixel width for a line count.
func gutterWidth(lines int, adv float32, folds bool) float32 {
	digits := 1
	for n := lines; n >= 10; n /= 10 {
		digits++
	}
	w := GutterLeftInset + float32(digits)*adv + GutterCodeGap
	if folds {
		w += FoldGutterW
	}
	return w
}

// absF is the float32 absolute value.
func absF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// CodeViewportWidth is shared by the drawing and caret-reveal paths.
func CodeViewportWidth(paneWidth float32, lineCount int, advance float32, gutter, minimap, folds bool) float32 {
	width := paneWidth - 1
	if minimap && paneWidth > 0 {
		width -= RailWidth
	}
	if gutter {
		width -= gutterWidth(lineCount, advance, folds)
	} else {
		width -= GutterLeftInset
	}
	return max(40, width)
}

func CodeContentWidth(b *Buffer, advance, shapedLineWidth float32) float32 {
	return max(float32(b.MaxLineCols())*advance, shapedLineWidth) + 16
}

// maxScroll clamps the first visible line.
func maxScroll(totalLines, visible int) int {
	if totalLines <= visible {
		return 0
	}
	return totalLines - visible
}

// MaxScrollF is the wheel ease's largest scroll TARGET in fractional rows:
// the pixel-exact end of the document (VS Code's scrollBeyondLastLine=false).
// The viewport is rarely a whole number of lines tall, so the whole-row
// ceiling (maxScroll) overshoots it: resting there pins the document's last
// line above the pane's bottom edge and strands the viewport's bottom slack
// as a blank strip. The exact ceiling is total - viewportH/lineH — the last
// line's bottom lands on the pane's bottom edge exactly, with the top row
// partially scrolled out (the pane paints it; see the row budget below).
// It never exceeds the whole-row ceiling by more than a row, and whole-row
// seek paths (keys, caret, scrollbar) keep snapping to maxScroll.
func MaxScrollF(totalLines, visible int, viewportH, lineH float32) float32 {
	maxInt := float32(maxScroll(totalLines, visible))
	if totalLines <= visible {
		return maxInt
	}
	exact := float32(totalLines) - viewportH/lineH
	if exact > maxInt {
		exact = maxInt // a viewportH/lineH above ceil would lie; stay whole-row
	}
	if exact < maxInt-1 {
		exact = maxInt - 1 // never more than one row below the whole-row ceiling
	}
	if exact < 0 {
		exact = 0
	}
	return exact
}

// RenderPane renders the interactive code pane: gutter, virtualized rows,
// selection, caret and the full editing keymap.
func RenderPane(cfg PaneConfig) gpui.Element {
	t := cfg.Theme
	buf := cfg.Buf
	m := cfg.Metrics
	lineH := max(1, m.LineHeight)

	total := buf.LineCount()
	visible := m.VisibleLines(cfg.ViewportH)
	scroll := min(max(cfg.Scroll, 0), maxScroll(total, visible))
	// The rendered position (fractional rows). At rest ScrollF == Scroll
	// and frac is 0: the render is bit-identical to the integer path.
	// Mid-ease the fractional part slides the window between rows. The
	// clamp stays on the whole-row ceiling: wheel targets stop earlier,
	// at MaxScrollF (the pixel-exact document end), but caret and
	// scrollbar seeks may legitimately render up to the last whole row.
	scrollF := cfg.ScrollF
	if scrollF < 0 {
		scrollF = 0
	}
	if maxF := float32(maxScroll(total, visible)); scrollF > maxF {
		scrollF = maxF
	}
	firstF := float32(math.Floor(float64(scrollF)))
	frac := scrollF - firstF
	first := int(firstF)

	// Folding: ranges for the language (nil when it cannot fold). A
	// scroll that lands inside a collapsed region rests on the first
	// line after it — the view never shows mid-fold.
	lang, palette := buf.LanguageOf(), paletteOf(t)
	folds := buf.FoldRanges() // memoize + validate the folded set for this revision
	foldable := Foldable(lang)
	if foldable {
		for {
			f := buf.foldedRangeContaining(first)
			if f == nil {
				break
			}
			first = min(f.End+1, max(0, total-1))
		}
	}

	gutterW := gutterWidth(total, m.CharAdvance, foldable)
	if !cfg.ShowGutter {
		// Line numbers hidden: the gutter collapses to the left inset alone,
		// so the code text keeps a small gap from the pane's left edge
		// instead of touching it (the old always-rendered full-width strip
		// also kept drawing the numbers it was supposed to hide).
		gutterW = GutterLeftInset
	}
	shifted := frac > 0.0005
	// The painted rows: buffer lines from `first`, advancing through
	// collapsed regions, up to the row budget. Without folds this is
	// exactly the consecutive first..first+budget-1 range. The budget
	// covers the partially visible bottom row whenever the viewport is
	// not a whole number of lines tall: `visible` floors the height
	// ratio, so the slack under the last full row holds the TOP of the
	// next line, and every editor paints it. Skipping it culled a line
	// that was still on screen — the bottom strip went blank as soon
	// as a scroll settled ("the bottom line disappears too early").
	// The fractional wheel slide can reveal one more row on top.
	budget := visible
	if slack := cfg.ViewportH - float32(visible)*lineH; slack > 0.5 {
		budget++ // the partially visible bottom row
	}
	if shifted {
		budget++ // the slide reveals one more row at the bottom edge
	}
	rows := make([]int, 0, budget)
	for line, n := first, 0; line < total && n < budget; line = buf.NextVisible(line) {
		rows = append(rows, line)
		n++
	}
	if len(rows) == 0 && total > 0 {
		rows = append(rows, min(first, total-1))
	}
	// Buffer line -> painted row index (the fold mapping for hit tests).
	rowOf := make(map[int]int, len(rows))
	for i, line := range rows {
		rowOf[line] = i
	}

	// Local helpers operating through the update callback.
	edit := func(f func(*Buffer)) { cfg.OnEdit(f) }

	// ---------------------------------------------------------------------------
	// Horizontal geometry: the code column pans wide lines like any
	// editor (the gutter and the minimap rail stay put).
	// ---------------------------------------------------------------------------
	railW := float32(0)
	if cfg.ViewportW > 0 && cfg.ShowMinimap {
		railW = RailWidth
	}
	codeW := CodeViewportWidth(cfg.ViewportW, total, m.CharAdvance, cfg.ShowGutter, cfg.ShowMinimap, foldable)
	contentW := CodeContentWidth(buf, m.CharAdvance, cfg.CaretLineWidth)
	maxScrollX := contentW - codeW
	if maxScrollX < 0 {
		maxScrollX = 0
	}
	scrollX := cfg.ScrollX
	if scrollX < 0 {
		scrollX = 0
	}
	if scrollX > maxScrollX {
		scrollX = maxScrollX
	}

	// Cross-line highlighting context per line (raw strings, block
	// comments, triple quotes): memoized on the buffer per revision.
	states := buf.LineStates(lang)
	lines := make([]codeLine, len(rows))
	// Use the laid-out rows, not viewport Y divided by a nominal height.
	// Their actual positions include any fractional vertical pan and flex
	// positioning that the frame used. Extrapolation is only for drags or
	// caret clicks outside the laid-out rows (e.g. below the last line).
	lineForY := func(w *gpui.Window, y geometry.Pixels, extrapolate bool) (int, bool) {
		if len(lines) == 0 {
			return 0, false
		}
		slot := sort.Search(len(lines), func(i int) bool {
			return y < w.ElementBounds(lines[i].row).Bottom()
		})
		if slot < len(lines) && y >= w.ElementBounds(lines[slot].row).Origin.Y {
			return rows[slot], true
		}
		if !extrapolate {
			return 0, false
		}
		// Dragging below the painted rows: walk the visible-line chain
		// (fold-aware) from the first painted line.
		startY := w.ElementBounds(lines[0].row).Origin.Y
		idx := int(math.Floor(float64((y - startY).AsF32() / lineH)))
		row := rows[0]
		for k := 0; k < idx; k++ {
			row = buf.NextVisible(row)
		}
		return min(max(row, 0), total-1), true
	}
	lineAt := func(line int, w *gpui.Window, fallbackX float32) (codeLine, *text.LineLayout, float32) {
		if rowIdx, ok := rowOf[line]; ok {
			l := lines[rowIdx]
			if l.element != nil {
				if shaped := l.element.Layout(); shaped != nil {
					return l, shaped, w.ElementBounds(l.element).Origin.X.AsF32()
				}
			}
		}
		// Dragging past the viewport can target a row that wasn't painted.
		l := newCodeLine(buf.Line(line))
		shaped := cfg.Shaper.shape(l, lang, palette)
		return l, shaped, fallbackX + visualOriginShift(shaped).AsF32()
	}
	// positionToOffset maps through the actual laid-out text origin and
	// shaped caret slots, then takes the result back to a buffer byte.
	positionToOffset := func(pos geometry.Point[geometry.Pixels], w *gpui.Window) int {
		bounds := geometry.Bounds[geometry.Pixels]{}
		if hb, ok := w.ListenerHitbox(); ok {
			bounds = hb.Bounds
		}
		line, ok := lineForY(w, pos.Y, true)
		if !ok {
			return 0
		}
		l, shaped, originX := lineAt(line, w, bounds.Origin.X.AsF32()-scrollX)
		return buf.LCToOffset(line, l.caretAt(shaped, pos.X.AsF32()-originX))
	}

	// Navigation must target the glyph actually clicked, rather than the
	// nearest caret boundary (which can resolve blank space to an unrelated
	// identifier on the previous line or at the end of a line).
	positionToSymbol := func(pos geometry.Point[geometry.Pixels], w *gpui.Window) (int, bool) {
		hb, ok := w.ListenerHitbox()
		if !ok {
			return 0, false
		}
		if pos.Y < hb.Bounds.Origin.Y || pos.Y >= hb.Bounds.Bottom() {
			return 0, false
		}
		line, ok := lineForY(w, pos.Y, false)
		if !ok {
			return 0, false
		}
		l, shaped, originX := lineAt(line, w, hb.Bounds.Origin.X.AsF32()-scrollX)
		if shaped == nil || l.display == "" {
			return 0, false
		}
		penX := geometry.Px(pos.X.AsF32()-originX) + shaped.XForIndex(0)
		index, inside := shaped.IndexForX(penX)
		if penX < 0 || !inside || index < 0 || index >= len(l.display) {
			return 0, false
		}
		byteCol, _ := l.sourceByte(index)
		if byteCol >= len(l.source) {
			return 0, false
		}
		r, _ := utf8.DecodeRuneInString(l.source[byteCol:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && r != '_' && r != '$' {
			return 0, false
		}
		return buf.LCToOffset(line, byteCol), true
	}

	// scrollIntoView keeps the cursor's line visible. Fold-aware: the
	// caret's own painted row (if any) means it is already on screen —
	// hidden lines above it must not trigger a scroll.
	scrollIntoView := func() {
		line, _ := buf.OffsetToLC(buf.Cursor)
		if rowIdx, ok := rowOf[line]; ok && rowIdx < visible {
			return
		}
		s := scroll
		if line < s {
			s = line
		}
		if line > s+visible-1 {
			s = line - visible + 1
		}
		if s != scroll {
			final := s
			cfg.OnScroll(final - cfg.Scroll)
		}
	}

	selStart, selEnd := buf.Selection()
	// One cursor-line lookup per frame: the previous version called
	// isCursorLine per gutter row and per code row, each converting the
	// cursor offset to a line (an O(document) scan in the old buffer).
	cursorLine, _ := buf.OffsetToLC(buf.Cursor)

	// ---------------------------------------------------------------------------
	// Gutter column
	// ---------------------------------------------------------------------------
	// WhitespaceNowrap keeps the fit-content rows from ever wrapping their
	// number: the row hugs the text (align-items: flex-end), so the text's
	// available width equals its own shaped width and scale-dependent
	// rounding could wrap 3-digit numbers onto a phantom second row (the
	// "broken after line 99" Windows bug). The numbers use the same
	// monospace face as the code, so gutterWidth's advance estimate
	// matches the rendered width and right-aligned numbers keep a slack
	// margin instead of touching the border.
	gutter := elements.NewDiv().Flex().FlexCol().ItemsEnd().FlexShrink(0).
		WhitespaceNowrap().
		W(ui.Px(gutterW)).BgColor(t.Surface).BorderR_1().BorderColor(t.EditorBg).Relative()
	if !cfg.ShowGutter {
		// Hidden: an invisible slim spacer (editor background, no
		// border, NO number rows) holding the code column off the
		// pane's left edge. The old branch only recolored the strip
		// and kept drawing the numbers it was supposed to hide.
		gutter = elements.NewDiv().FlexShrink(0).
			W(ui.Px(gutterW)).BgColor(t.EditorBg)
	}
	if cfg.FontSizePx > 0 {
		gutter = gutter.TextSize(ui.AbsPx(cfg.FontSizePx))
	}
	// The wheel ease slides the window between rows: the rows move into
	// an absolutely positioned wrapper shifted by the fractional part
	// (the same Absolute().Left pan the wide-line rows use). At rest the
	// wrapper is skipped and the structure is exactly the integer path's.
	var gutterRows *elements.Div
	if shifted && cfg.ShowGutter {
		gutterRows = elements.NewDiv().
			Absolute().Right(ui.Px(0)).Top(ui.Px(-frac * lineH)).
			Flex().FlexCol().ItemsEnd()
	}
	if cfg.ShowGutter {
		for _, i := range rows {
			numColor := t.Dim
			cursorLine := cursorLine == i
			if cursorLine {
				numColor = t.Accent
			}
			num := fmt.Sprintf("%d", i+1)
			numRow := elements.NewDiv().
				H(ui.Px(lineH)).FlexShrink(0).Flex().ItemsCenter().Pr(ui.DefPx(GutterCodeGap)).
				Child(elements.NewText(num).WithRuns([]text.TextRun{
					{Len: len(num), Font: mono(false), Color: numColor},
				}))
			if gutterRows != nil {
				gutterRows = gutterRows.Child(numRow)
			} else {
				gutter = gutter.Child(numRow)
			}
		}
	}
	if gutterRows != nil {
		gutter = gutter.Child(gutterRows)
	}
	if cfg.ShowGutter {
		gutter.Child(elements.NewDiv().Absolute().Right(ui.Px(0)).Top(ui.Px(0)).Bottom(ui.Px(0)).
			W(ui.Px(CodeDividerGap)).BgColor(t.EditorBg).BorderL_1().BorderColor(t.Surface1))
	}
	if cfg.OnBreakpoint != nil || cfg.ExecutionLine > 0 {
		gutter.Relative()
		markers := elements.NewDiv().Absolute().Left(ui.Px(0)).Top(ui.Px(-frac * lineH)).W(ui.Px(GutterLeftInset)).Flex().FlexCol()
		for _, i := range rows {
			marker := elements.NewDiv().H(ui.Px(lineH)).FlexShrink(0).Flex().ItemsCenter().JustifyCenter()
			if cfg.OnBreakpoint != nil {
				marker.CursorPointer().Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) })
			}
			if i+1 == cfg.ExecutionLine {
				marker.Child(elements.NewText("›").WithTextColor(t.Yellow))
			} else if bp, ok := cfg.Breakpoints[i+1]; ok {
				dot := elements.NewDiv().W(ui.Px(8)).H(ui.Px(8)).Rounded(ui.AbsPx(4)).Border_1().BorderColor(t.Red)
				if !bp.Rejected {
					dot.BgColor(t.Red)
				}
				marker.Child(dot)
			}
			markers.Child(marker)
		}
		gutter.Child(markers)
	}
	// Fold markers: chevron buttons in their own band LEFT of the line
	// numbers (GoLand's fold gutter, VS Code's glyph margin). Headers show
	// down/right chevrons; clicking toggles the region. The band sits
	// between the breakpoint strip and the numbers, so a chevron never
	// overlaps a digit and the hover highlight is a small rounded button
	// around the glyph only — not a full-height strip.
	if foldable && cfg.ShowGutter {
		gutter.Relative()
		foldLeft := float32(0)
		if cfg.OnBreakpoint != nil || cfg.ExecutionLine > 0 {
			foldLeft = GutterLeftInset // keep the breakpoint strip clear
		}
		foldCol := elements.NewDiv().
			Absolute().Left(ui.Px(foldLeft)).Top(ui.Px(-frac * lineH)).
			W(ui.Px(FoldGutterW)).Flex().FlexCol()
		for _, i := range rows {
			cell := elements.NewDiv().H(ui.Px(lineH)).FlexShrink(0).Flex().ItemsCenter().JustifyCenter()
			if f, ok := buf.FoldAt(i); ok {
				folded := buf.IsFolded(i)
				icon, tip := "chevron-down", "Fold"
				if folded {
					icon, tip = "chevron-right", "Unfold"
				}
				start := f.Start
				cell = cell.Child(elements.NewDiv().
					W(ui.Px(FoldGutterW)).H(ui.Px(FoldGutterW)).
					Rounded(ui.AbsPx(4)).
					Flex().ItemsCenter().JustifyCenter().
					ID(elements.ButtonID(fmt.Sprintf("fold-chev-%d", start))).
					CursorPointer().
					Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
					TooltipBuilder(ui.Tooltip(tip)).
					OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
						if ev.Button != platform.ButtonLeft {
							return
						}
						edit(func(b *Buffer) { b.ToggleFold(start) })
						w.StopPropagation()
						w.Refresh()
					}).
					Child(ui.Icon(icon, t.Dim, 12)))
			}
			foldCol = foldCol.Child(cell)
		}
		gutter.Child(foldCol)
	}
	// Gutter clicks place the cursor at the clicked line's start (the code
	// column's own handlers cover only the code area to their right);
	// right-click still opens the editor context menu.
	gutter.OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
		if ev.Button != platform.ButtonLeft {
			return
		}
		var left float32
		if hb, ok := w.ListenerHitbox(); ok {
			left = hb.Bounds.Origin.X.AsF32()
		}
		if cfg.OnBreakpoint != nil && ev.Position.X.AsF32()-left < GutterLeftInset {
			if line, ok := lineForY(w, ev.Position.Y, false); ok {
				cfg.OnBreakpoint(line + 1)
			}
			w.StopPropagation()
			w.Refresh()
			return
		}
		if line, ok := lineForY(w, ev.Position.Y, true); ok {
			edit(func(b *Buffer) { b.SetCursor(b.LineStart(line)) })
		}
		w.StopPropagation()
		w.Refresh()
	})
	if cfg.OnContext != nil {
		gutter.OnRightDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if line, ok := lineForY(w, ev.Position.Y, true); ok {
				edit(func(b *Buffer) { b.SetCursor(b.LineStart(line)) })
			}
			cfg.OnContext(ev.Position)
			w.StopPropagation()
			w.Refresh()
		})
	}

	// ---------------------------------------------------------------------------
	// Code column
	// ---------------------------------------------------------------------------
	// The pointer handlers live HERE (not on the pane): the code column's
	// hitbox origin is the exact text origin, so positionToOffset never
	// estimates the gutter width. The mouse-down captures the pointer for
	// the whole gesture, so drag-selection keeps extending while the
	// cursor moves over the gutter, the rail or past the window edges.
	code := elements.NewDiv().
		ID(elements.ButtonID("editor-code")).
		Flex().FlexCol().Flex1().Relative().
		CursorText(). // the code text surface gets the I-beam caret cursor
		OverflowHidden()
	if cfg.FontSizePx > 0 {
		code = code.TextSize(ui.AbsPx(cfg.FontSizePx))
	}
	// The wheel ease slides the window between rows: rows move into an
	// absolutely positioned wrapper shifted by the fractional part (see
	// the gutter wrapper). At rest the structure is the integer path's.
	var codeRows *elements.Div
	if shifted {
		codeRows = elements.NewDiv().
			Absolute().Left(ui.Px(0)).Top(ui.Px(-frac * lineH)).
			Flex().FlexCol()
	}
	for rowIdx, i := range rows {
		lineText := buf.Line(i)
		lineStart := buf.LineStart(i)
		cursorLine := cursorLine == i

		row := elements.NewDiv().
			H(ui.Px(lineH)).FlexShrink(0).Flex().ItemsCenter().Relative().
			WhitespaceNowrap()
		if cursorLine {
			row = row.BgColor(t.Accent.Alpha(0.10))
		}
		if i+1 == cfg.ExecutionLine {
			row = row.BgColor(t.Yellow.Alpha(0.18))
		}

		// Shaping and styling take byte indices in the tab-expanded text.
		l := newCodeLine(lineText)
		startState := StateNormal
		if i < len(states) {
			startState = states[i]
		}
		runs, _ := HighlightCachedIn(l.display, lang, startState, palette)
		if len(runs) == 0 && l.display == "" {
			runs = []text.TextRun{{Len: 0, Font: mono(false), Color: t.Text}}
		}
		var spans []Span
		if cfg.Find != nil {
			for _, mt := range cfg.Find.MatchesOnLine(lineStart, lineStart+len(lineText)) {
				s0 := l.displayByte(mt[0] - lineStart)
				s1 := l.displayByte(mt[1] - lineStart)
				if s1 > s0 {
					spans = append(spans, Span{Start: s0, End: s1})
				}
			}
		}
		if selEnd > selStart {
			s0 := l.displayByte(selStart - lineStart)
			s1 := l.displayByte(selEnd - lineStart)
			if s1 > s0 {
				spans = append(spans, Span{Start: s0, End: s1, Sel: true})
			}
		}
		runs = OverlaySpans(runs, spans, t.SelectionBackground(), t.MatchBackground())
		textEl := elements.NewText(l.display).WithRuns(runs)
		l.element = textEl

		textLeft := -scrollX
		if text.IsRTL(l.display) {
			textLeft += visualOriginShift(cfg.Shaper.shape(l, lang, palette)).AsF32()
		}
		if textLeft != 0 {
			row = row.Child(elements.NewDiv().
				Absolute().Left(ui.Px(textLeft)).Child(textEl))
		} else {
			row = row.Child(textEl)
		}

		// Collapsed fold placeholder: GoLand's `{…}` inline summary
		// (paren blocks show a bare `…`). Clicking it expands the
		// region again, like every editor's ghost marker.
		if f, ok := buf.FoldAt(i); ok && buf.IsFolded(i) {
			ph := " {…}"
			if f.Paren {
				ph = " …"
			}
			start := f.Start
			phEl := elements.NewDiv().
				ID(elements.ButtonID(fmt.Sprintf("fold-ph-%d", start))).
				Flex().ItemsCenter().CursorPointer().
				TooltipBuilder(ui.Tooltip("Unfold")).
				OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
					if ev.Button != platform.ButtonLeft {
						return
					}
					edit(func(b *Buffer) { b.Unfold(start) })
					w.StopPropagation()
					w.Refresh()
				}).
				Child(elements.NewText(ph).WithRuns([]text.TextRun{
					{Len: len(ph), Font: mono(false), Color: t.Dim},
				}))
			row = row.Child(phEl)
		}

		// Caret. Two device-independent px wide so it survives rounding
		// (a 1.5px bar rounds to a single faint pixel merged with the
		// glyph's left bearing - effectively invisible). It blinks like
		// any other editor: the workspace toggles CaretVisible on a
		// timer and forces it on around cursor activity.
		if cursorLine && cfg.CaretVisible {
			row = row.Child(l.caretOverlay(cursorCol(buf, lineStart), lineH, t.Accent))
		}
		l.row = row
		lines[rowIdx] = l
		if codeRows != nil {
			codeRows = codeRows.Child(row)
		} else {
			code = code.Child(row)
		}
	}
	if codeRows != nil {
		code = code.Child(codeRows)
	}

	// ---------------------------------------------------------------------------
	// Scrollbars + minimap rail
	// ---------------------------------------------------------------------------
	codeArea := elements.NewDiv().Flex().FlexCol().Flex1().
		Child(code)
	if maxScrollX > 0 {
		codeArea = codeArea.Child(renderHScrollBar(hScrollBarConfig{
			Theme:     t,
			TrackW:    codeW,
			ContentW:  contentW,
			ViewportW: codeW,
			ScrollX:   scrollX,
			MaxScroll: maxScrollX,
			OnSeek: func(target float32) {
				if cfg.OnScrollX != nil {
					cfg.OnScrollX(target - scrollX)
				}
			},
		}))
	}

	var rail gpui.Element
	if railW > 0 {
		// Collapsed ranges paint a dim band in the minimap where the
		// hidden lines live.
		var foldedRanges []FoldRange
		if foldable {
			for _, f := range folds {
				if buf.IsFolded(f.Start) {
					foldedRanges = append(foldedRanges, f)
				}
			}
		}
		rail = renderRail(railConfig{
			Theme:         t,
			Buf:           buf,
			Advance:       m.CharAdvance,
			Height:        cfg.ViewportH,
			Scroll:        scroll,
			ScrollF:       scrollF,
			Visible:       visible,
			ScrollX:       scrollX,
			Folds:         foldedRanges,
			OnScroll:      cfg.OnScroll,
			OnWheelScroll: cfg.OnWheelScroll,
			OnScrollX:     cfg.OnScrollX,
			CursorLine:    cursorLine,
		})
	}

	// ---------------------------------------------------------------------------
	// The pane
	// ---------------------------------------------------------------------------
	pane := elements.NewDiv().
		ID(elements.ButtonID("editor-pane")).
		Flex().Flex1().Relative().
		// The pane clips its own rows and takes the available height (basis 0):
		// its content must never inflate the flex chain above it.
		OverflowHidden().
		BgColor(t.EditorBg).
		TrackFocus(cfg.Focus).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			if dx := ev.Delta.PixelDelta(geometry.Px(1)).X.AsF32(); dx != 0 {
				if cfg.OnScrollX != nil {
					cfg.OnScrollX(dx)
				}
			}
			// Wheel down arrives as a negative delta (raw platform sign):
			// negate into the buffer's top-line position so the view moves
			// toward later lines. The RAW fractional delta goes through
			// the workspace's smooth-scroll ease (OnWheelScroll); whole-row
			// seek paths keep using OnScroll.
			delta := -ev.Delta.PixelDelta(geometry.Px(lineH)).Y.AsF32() / lineH
			if delta != 0 {
				if cfg.OnWheelScroll != nil {
					cfg.OnWheelScroll(delta)
				} else if cfg.OnScroll != nil {
					cfg.OnScroll(int(delta))
				}
			}
			w.Refresh()
		}).
		OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
			if cfg.OnKey != nil && cfg.OnKey(ev) {
				w.Refresh()
				return
			}
			handlePaneKey(cfg, buf, edit, scrollIntoView, visible, ev, w)
		})

	// Pointer handlers on the code column (see the comment above it).
	code.OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
		if ev.Button != platform.ButtonLeft {
			return
		}
		if (ev.Modifiers.Control || ev.Modifiers.Platform) && cfg.OnDefinition != nil {
			if off, ok := positionToSymbol(ev.Position, w); ok {
				cfg.OnDefinition(off)
			}
			w.Refresh()
			return
		}
		if hb, ok := w.ListenerHitbox(); ok {
			// Capture the whole gesture: drag-selection keeps working
			// when the pointer leaves the code column.
			w.CapturePointer(hb.ID)
		}
		off := positionToOffset(ev.Position, w)
		switch ev.ClickCount {
		case 2:
			edit(func(b *Buffer) { b.SelectWordAt(off) })
		case 3:
			edit(func(b *Buffer) { b.SelectLineAt(off) })
		default:
			edit(func(b *Buffer) {
				if ev.Modifiers.Shift {
					b.ExtendCursor(off)
				} else {
					b.SetCursor(off)
				}
			})
		}
		w.Refresh()
	}).
		OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
			if !ev.Dragging() {
				return
			}
			off := positionToOffset(ev.Position, w)
			edit(func(b *Buffer) { b.ExtendCursor(off) })
			w.Refresh()
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
		}).
		OnRightDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if cfg.OnContext != nil {
				// Select under the cursor first (editor convention), then
				// open the menu at the mouse position.
				off := positionToOffset(ev.Position, w)
				if !offsetInSelection(buf, off) {
					edit(func(b *Buffer) { b.SetCursor(off) })
				}
				cfg.OnContext(ev.Position)
				w.Refresh()
			}
		})

	pane = pane.Child(gutter).Child(codeArea)
	if rail != nil {
		pane = pane.Child(rail)
	}
	return pane
}

// Span is a line-relative display-column range painted with a background
// (selection or find match) over the tab-expanded line.
type Span struct {
	Start, End int
	Sel        bool // selection wins over find matches
}

// hScrollBarConfig configures the horizontal scrollbar under the code.
type hScrollBarConfig struct {
	Theme     ui.Theme
	TrackW    float32
	ViewportW float32
	ContentW  float32
	ScrollX   float32
	MaxScroll float32
	OnSeek    func(targetX float32)
}

// renderHScrollBar builds the wide-line scrollbar: a full-width track with
// a proportional thumb; clicking or dragging seeks (thumb centers on the
// pointer, like editors' jump-to-click).
func renderHScrollBar(cfg hScrollBarConfig) gpui.Element {
	t := cfg.Theme
	thumbW := cfg.TrackW * cfg.ViewportW / max(cfg.ContentW, 1)
	if thumbW < 24 {
		thumbW = 24
	}
	if thumbW > cfg.TrackW {
		thumbW = cfg.TrackW
	}
	travel := cfg.TrackW - thumbW
	thumbX := float32(0)
	if cfg.MaxScroll > 0 && travel > 0 {
		thumbX = (cfg.ScrollX / cfg.MaxScroll) * travel
	}
	seek := func(localX float32) {
		if travel <= 0 || cfg.OnSeek == nil {
			return
		}
		ratio := (localX - thumbW/2) / travel
		if ratio < 0 {
			ratio = 0
		}
		if ratio > 1 {
			ratio = 1
		}
		cfg.OnSeek(ratio * cfg.MaxScroll)
	}
	track := elements.NewDiv().
		ID(elements.ButtonID("hscroll")).
		H(ui.Px(HScrollHeight)).Relative().
		BgColor(t.Surface).
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				seek(ev.Position.X.AsF32() - float32(hb.Bounds.Origin.X))
				// Capture for the whole drag (see the vertical
				// scrollbar): dragging off the track keeps seeking
				// and cannot start a text selection in the pane.
				w.CapturePointer(hb.ID)
			}
			w.StopPropagation()
			w.Refresh()
		}).
		OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
			if !ev.Dragging() {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				seek(ev.Position.X.AsF32() - float32(hb.Bounds.Origin.X))
			}
			w.StopPropagation()
			w.Refresh()
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
			w.Refresh()
		}).
		Child(elements.NewDiv().
			Absolute().Left(ui.Px(thumbX)).Top(ui.Px(1.5)).
			W(ui.Px(thumbW)).H(ui.Px(HScrollHeight - 3)).
			Rounded(ui.AbsPx(3)).
			BgColor(t.Surface2))
	return track
}

// railConfig configures the minimap + vertical scrollbar rail.
type railConfig struct {
	Theme   ui.Theme
	Buf     *Buffer
	Advance float32
	Height  float32
	Scroll  int
	// ScrollF is the rendered fractional scroll (the wheel ease) — the
	// miniature's top offset and the thumb track it smoothly.
	ScrollF  float32
	Visible  int
	ScrollX  float32
	OnScroll func(delta int)
	// OnWheelScroll, when set, carries the rail's wheel deltas through the
	// smooth-scroll ease (drag seeks stay on the snapped OnScroll).
	OnWheelScroll func(deltaF float32)
	OnScrollX     func(deltaPx float32)
	CursorLine    int
	// Folds lists the collapsed ranges: the miniature draws a dim band
	// over the hidden lines (the upstream editor's folded-region strip).
	Folds []FoldRange
}

// renderRail builds the right rail: a code map with a viewport indicator,
// next to a proportional vertical scrollbar.
//
// the upstream editor's minimap behavior (crates/editor/src/element.rs, MinimapLayout):
// the miniature scale is CONSTANT (upstream renders the buffer at a fixed
// 2px font, never shrinking it) - the rail always shows the same number of
// miniature lines. A document taller than the rail scrolls its miniature
// SLOWLY underneath it: the content top offset is the editor's scroll
// progress times the lines beyond the rail's capacity
// (calculate_minimap_top_offset). The viewport thumb is positioned on an
// oversized track and shifted by the miniature's own offset
// (ScrollbarLayout::for_minimap), so it covers exactly the miniature rows
// of the visible code — for a fitting document it sits right on those
// rows; for a taller one it sweeps the rail while the content slides
// underneath. The click/drag mapping stays anchored to the rail, so
// seeking remains stable across the whole document.
func renderRail(cfg railConfig) gpui.Element {
	t := cfg.Theme
	buf := cfg.Buf
	total := buf.LineCount()
	if total == 0 {
		total = 1
	}

	// Constant miniature scale (never compressed into the rail).
	miniH := float32(MinimapLineHeight)
	minimapLines := int(cfg.Height / miniH) // rail capacity in miniature lines
	if minimapLines < 1 {
		minimapLines = 1
	}
	maxScrollLines := max(0, total-cfg.Visible)

	// The miniature's top line (fractional for smooth sub-line sliding):
	// the editor's RENDERED scroll progress maps onto the lines the rail
	// cannot show at once (upstream calculate_minimap_top_offset).
	contentTopF := float32(0)
	if maxScrollLines > 0 {
		progress := float32(min(max(cfg.ScrollF, 0), float32(maxScrollLines))) / float32(maxScrollLines)
		contentTopF = progress * float32(max(0, total-minimapLines))
	}

	// seekMinimap inverts the thumb mapping (thumb top = (scroll -
	// contentTop) * miniH): a click/drag under the rail lands the clicked
	// code region under the viewport thumb.
	seekMinimap := func(localY float32) {
		if cfg.OnScroll == nil || maxScrollLines <= 0 {
			return
		}
		indH := float32(cfg.Visible) * miniH
		if indH < 2 {
			indH = 2
		}
		travel := cfg.Height - indH
		if travel <= 0 {
			return
		}
		want := localY - indH/2
		if want < 0 {
			want = 0
		}
		if want > travel {
			want = travel
		}
		var target float32
		if total > minimapLines {
			// The miniature slides underneath; undo both motions.
			denom := float32(minimapLines - cfg.Visible)
			if denom < 1 {
				denom = 1
			}
			target = (want / miniH) * float32(maxScrollLines) / denom
		} else {
			// The whole document fits the rail: thumb top = scroll * miniH.
			target = want / miniH
		}
		if target < 0 {
			target = 0
		}
		if target > float32(maxScrollLines) {
			target = float32(maxScrollLines)
		}
		cfg.OnScroll(int(target) - cfg.Scroll)
	}

	cacheKey := minimapKey{revision: buf.Revision, language: buf.LanguageOf(), palette: paletteOf(t), dim: t.Dim,
		scale: (MinimapWidth - 8) / max(float32(buf.MaxLineCols()), 40)}

	minimap := elements.NewDiv().
		ID(elements.ButtonID("minimap")).
		Flex().FlexCol().Flex1().Relative().
		OverflowHidden().
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				seekMinimap(ev.Position.Y.AsF32() - float32(hb.Bounds.Origin.Y))
				// Capture the pointer for the whole drag: moves keep
				// matching this hitbox even off the minimap, and the
				// editor pane's drag-select listener is suppressed by
				// the StopPropagation below while the button is held.
				w.CapturePointer(hb.ID)
			}
			w.StopPropagation()
			w.Refresh()
		}).
		OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
			if !ev.Dragging() {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				seekMinimap(ev.Position.Y.AsF32() - float32(hb.Bounds.Origin.Y))
			}
			w.StopPropagation()
			w.Refresh()
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
			w.Refresh()
		}).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			// Wheel down is negative (raw platform sign): negate into the
			// buffer's top-line position. The raw delta rides the
			// smooth-scroll ease when available.
			delta := -ev.Delta.PixelDelta(geometry.Px(MinimapLineHeight)).Y.AsF32() / MinimapLineHeight
			if delta != 0 && cfg.OnWheelScroll != nil {
				cfg.OnWheelScroll(delta)
				w.Refresh()
			} else if delta != 0 && cfg.OnScroll != nil {
				cfg.OnScroll(int(delta))
				w.Refresh()
			}
		})

	// Blocks + viewport indicator paint through one canvas element: a div
	// per line made the minimap the most expensive subtree in flexbox
	// (hundreds of layout nodes per frame).
	minimap = minimap.Child(elements.NewCanvas(nil, func(bounds geometry.Bounds[geometry.Pixels], w *gpui.Window) {
		// Viewport indicator behind the blocks (the minimap's thumb).
		// Upstream ScrollbarLayout::for_minimap positions the thumb on an
		// oversized track (the whole document at the miniature scale) and
		// shifts it by the miniature's own top offset, which resolves to
		// exactly this: the highlight covers the miniature rows of the code
		// the editor currently shows (thumb top = (scroll - contentTop) *
		// mini line height). For a document that fits the rail it sits right
		// on those rows; for a taller one it sweeps the rail linearly while
		// the miniature slides underneath at the slower rate.
		vh := float32(cfg.Visible) * miniH
		if vh < 2 {
			vh = 2
		}
		if vh > cfg.Height {
			vh = cfg.Height
		}
		travel := cfg.Height - vh
		vy := (min(max(cfg.ScrollF, 0), float32(maxScrollLines)) - contentTopF) * miniH
		if vy < 0 {
			vy = 0
		}
		if vy > travel {
			vy = travel
		}
		if vh > 0 {
			ind := style.Style{Background: &gc.Background{
				Tag:   gc.BackgroundTagSolid,
				Solid: t.Text.Alpha(0.08),
			}}
			indBounds := bounds
			indBounds.Origin.Y = indBounds.Origin.Y.Add(geometry.Px(vy))
			indBounds.Size.Height = geometry.Px(vh)
			w.PaintQuad(ind, indBounds)
		}

		// Whole-line mode: one token-colored row per line, relative to
		// the miniature's top line (only the rows inside the rail
		// paint, so the per-frame work is bounded by the rail height,
		// not the document length).
		foldStarts := map[int]FoldRange{}
		for _, f := range cfg.Folds {
			foldStarts[f.Start] = f
		}
		inFold := func(line int) bool {
			for _, f := range cfg.Folds {
				if line > f.Start && line <= f.End {
					return true
				}
			}
			return false
		}
		firstRow := int(contentTopF)
		lastRow := min(total, firstRow+minimapLines+1)
		buf.minimap.prepare(cacheKey, min(total, 2*(minimapLines+1)))
		for i := firstRow; i < lastRow; i++ {
			rowY := (float32(i) - contentTopF) * miniH
			if rowY > cfg.Height {
				break
			}
			if _, ok := foldStarts[i]; ok {
				// Collapsed region: a dim band stands in for the
				// hidden lines (nothing else paints on them).
				st := style.Style{Background: &gc.Background{
					Tag:   gc.BackgroundTagSolid,
					Solid: t.Dim.Alpha(0.35),
				}}
				b := bounds
				b.Origin.Y = b.Origin.Y.Add(geometry.Px(rowY))
				b.Size = geometry.Size[geometry.Pixels]{Width: geometry.Px(MinimapWidth - 4), Height: geometry.Px(min(MinimapBlockH, miniH))}
				w.PaintQuad(st, b)
				continue
			}
			if inFold(i) {
				continue
			}
			if i == cfg.CursorLine {
				// Cursor-line strip behind the token blocks.
				st := style.Style{Background: &gc.Background{
					Tag:   gc.BackgroundTagSolid,
					Solid: t.Accent.Alpha(0.10),
				}}
				b := bounds
				b.Origin.Y = b.Origin.Y.Add(geometry.Px(rowY))
				b.Size = geometry.Size[geometry.Pixels]{Width: geometry.Px(MinimapWidth - 4), Height: geometry.Px(miniH)}
				w.PaintQuad(st, b)
			}
			for _, block := range buf.minimap.blocks(buf, i) {
				st := style.Style{Background: &gc.Background{
					Tag:   gc.BackgroundTagSolid,
					Solid: block.color,
				}}
				b := bounds
				b.Origin.X = b.Origin.X.Add(geometry.Px(block.x))
				b.Origin.Y = b.Origin.Y.Add(geometry.Px(rowY))
				b.Size = geometry.Size[geometry.Pixels]{Width: geometry.Px(block.width), Height: geometry.Px(min(MinimapBlockH, miniH))}
				w.PaintQuad(st, b)
			}
		}
	}))

	// Vertical scrollbar: proportional thumb; click/drag seeks.
	trackH := cfg.Height
	thumbH := trackH * float32(cfg.Visible) / float32(total)
	if thumbH < 24 {
		thumbH = 24
	}
	if thumbH > trackH {
		thumbH = trackH
	}
	travel := trackH - thumbH
	thumbTop := float32(0)
	if maxScrollLines > 0 && travel > 0 {
		thumbTop = (min(max(cfg.ScrollF, 0), float32(maxScrollLines)) / float32(maxScrollLines)) * travel
	}
	seekV := func(localY float32) {
		if travel <= 0 || cfg.OnScroll == nil {
			return
		}
		ratio := (localY - thumbH/2) / travel
		if ratio < 0 {
			ratio = 0
		}
		if ratio > 1 {
			ratio = 1
		}
		cfg.OnScroll(int(ratio*float32(maxScrollLines)) - cfg.Scroll)
	}
	vscroll := elements.NewDiv().
		ID(elements.ButtonID("vscroll")).
		Flex().W(ui.Px(VScrollWidth)).FlexShrink(0).Relative().
		BgColor(t.Surface).
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				seekV(ev.Position.Y.AsF32() - float32(hb.Bounds.Origin.Y))
				// Pointer capture keeps the drag on the scrollbar:
				// moves off the track still steer the thumb (the
				// captured hitbox stays hovered) while the pane's
				// text-selection listener is suppressed by the
				// StopPropagation below (the scrollbar is painted
				// after the pane, so its bubble handlers run first).
				w.CapturePointer(hb.ID)
			}
			w.StopPropagation()
			w.Refresh()
		}).
		OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
			if !ev.Dragging() {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				seekV(ev.Position.Y.AsF32() - float32(hb.Bounds.Origin.Y))
			}
			w.StopPropagation()
			w.Refresh()
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
			w.Refresh()
		}).
		Child(elements.NewDiv().
			Absolute().Left(ui.Px(1.5)).Top(ui.Px(thumbTop)).
			W(ui.Px(VScrollWidth - 3)).H(ui.Px(thumbH)).
			Rounded(ui.AbsPx(3)).
			BgColor(t.Surface2))

	rail := elements.NewDiv().Flex().W(ui.Px(RailWidth)).FlexShrink(0).
		BgColor(t.EditorBg).BorderL_1().BorderColor(t.Surface1).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			if dx := ev.Delta.PixelDelta(geometry.Px(1)).X.AsF32(); dx != 0 && cfg.OnScrollX != nil {
				cfg.OnScrollX(dx)
			}
			// Wheel down is negative (raw platform sign): negate into the
			// buffer's top-line position.
			delta := -ev.Delta.PixelDelta(geometry.Px(MinimapLineHeight)).Y.AsF32() / MinimapLineHeight
			if delta != 0 && cfg.OnScroll != nil {
				cfg.OnScroll(int(delta))
			}
			w.Refresh()
		}).
		Child(minimap).
		Child(vscroll)
	return rail
}

// handlePaneKey implements the editor keymap.
func handlePaneKey(cfg PaneConfig, buf *Buffer, edit func(func(*Buffer)),
	scrollIntoView func(), visible int, ev platform.KeyDownEvent, w *gpui.Window) {

	k := ev.Keystroke
	key := k.Key
	mods := k.Modifiers
	secondary := mods.Control || mods.Platform

	if secondary {
		switch key {
		case "a":
			edit(func(b *Buffer) { b.SelectAll() })
			w.Refresh()
			return
		case "c":
			if buf.HasSelection() {
				w.PlatformWindow().WriteClipboard(platform.ClipboardItem{Text: buf.SelectedText()})
			}
			return
		case "x":
			if buf.HasSelection() {
				w.PlatformWindow().WriteClipboard(platform.ClipboardItem{Text: buf.SelectedText()})
				edit(func(b *Buffer) { b.Insert("") })
				w.Refresh()
			}
			return
		case "v":
			if item, ok := w.PlatformWindow().ReadClipboard(); ok && item.Text != "" {
				edit(func(b *Buffer) { b.Insert(item.Text); b.UnfoldAtCursor() })
				scrollIntoView()
				w.Refresh()
			}
			return
		case "z":
			if mods.Shift {
				edit(func(b *Buffer) { b.Redo(); b.UnfoldAtCursor() })
			} else {
				edit(func(b *Buffer) { b.Undo(); b.UnfoldAtCursor() })
			}
			scrollIntoView()
			w.Refresh()
			return
		case "y":
			edit(func(b *Buffer) { b.Redo(); b.UnfoldAtCursor() })
			scrollIntoView()
			w.Refresh()
			return
		case "s":
			if cfg.OnSave != nil {
				cfg.OnSave()
				w.Refresh()
			}
			return
		case "f":
			if cfg.OnFind != nil {
				cfg.OnFind()
				w.Refresh()
			}
			return
		case "`":
			if cfg.OnTerminalToggle != nil {
				cfg.OnTerminalToggle()
				w.Refresh()
			}
			return
		}
	}

	switch key {
	case "left", "right", "up", "down", "home", "end", "pageup", "pagedown":
		word := secondary && (key == "left" || key == "right")
		edit(func(b *Buffer) {
			b.Move(key, mods.Shift, word, visible-1)
			b.UnfoldAtCursor() // entering a folded region expands it
		})
		scrollIntoView()
		w.Refresh()
	case "backspace":
		edit(func(b *Buffer) { b.DeleteBackward(); b.UnfoldAtCursor() })
		scrollIntoView()
		w.Refresh()
	case "delete":
		edit(func(b *Buffer) { b.DeleteForward(); b.UnfoldAtCursor() })
		scrollIntoView()
		w.Refresh()
	case "enter":
		edit(func(b *Buffer) { b.Insert("\n"); b.UnfoldAtCursor() })
		scrollIntoView()
		w.Refresh()
	case "tab":
		edit(func(b *Buffer) { b.Insert("    "); b.UnfoldAtCursor() })
		scrollIntoView()
		w.Refresh()
	case "escape":
		if buf.HasSelection() {
			edit(func(b *Buffer) { b.SetCursor(b.Cursor) })
			w.Refresh()
		}
	default:
		if ch := k.KeyChar; ch != "" && !mods.Control && !mods.Alt && !mods.Platform {
			edit(func(b *Buffer) { b.Insert(ch); b.UnfoldAtCursor() })
			scrollIntoView()
			w.Refresh()
		}
	}
}

func cursorCol(b *Buffer, lineStart int) int {
	return b.Cursor - lineStart
}

func offsetInSelection(b *Buffer, off int) bool {
	if !b.HasSelection() {
		return false
	}
	s, e := b.Selection()
	return off >= s && off < e
}

// paletteOf maps the UI theme onto the highlight palette.
// PaletteOfTheme exposes the highlight palette for a UI theme (tests and
// callers outside this package).
func PaletteOfTheme(t ui.Theme) Palette { return paletteOf(t) }

func paletteOf(t ui.Theme) Palette {
	return Palette{
		Keyword:  t.Mauve,
		Type:     t.Peach,
		String:   t.Green,
		Comment:  t.Overlay,
		Number:   t.Peach,
		Function: t.Sky,
		Field:    t.Teal,
		Text:     t.Text,
		Heading:  t.Accent,
		Link:     t.Teal,
		Todo:     t.Orange,
	}
}
