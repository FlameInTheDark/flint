package workspace

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/debugger"
	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/font"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
	"github.com/FlameInTheDark/go-gpui/text"
)

type debugState struct {
	session                     *debugger.Session
	name                        string
	points                      map[string][]int
	location                    uint64
	tab                         string
	stack, vars, output, breaks *elements.ScrollHandle
	// stackTop..breaksTop keep each segment's last rendered scroll offset
	// (persistScroll re-requests them every render: the element tree
	// rebuilds each frame and resize drags shift element ids, which would
	// otherwise reset the reading position to the top).
	stackTop, varsTop, outputTop, breaksTop float32
	// stackLeft..breaksLeft do the same for the horizontal pan offset: wide
	// values overflow their segment and wheel/shift-wheel pans across them
	// (persistScrollLeft re-anchors the offset every render).
	stackLeft, varsLeft, outputLeft, breaksLeft float32
	follow                                      bool
	openVars                                    map[int]bool
}

func (ws *Workspace) showDebug() {
	ws.ShowDebug = true
	ws.ShowTerminal, ws.ShowGoOutput, ws.ShowProblems = false, false, false
	ws.ShowTodos = false
	if ws.debug.breaks == nil {
		ws.debug.resetPanel("Breakpoints")
	}
}

func (ws *Workspace) toggleDebug() {
	if ws.ShowDebug {
		ws.ShowDebug = false
	} else {
		ws.showDebug()
	}
}

func (d *debugState) resetPanel(tab string) {
	*d = debugState{
		session: d.session, name: d.name, points: d.points, tab: tab, follow: true,
		stack: elements.NewScrollHandle(), vars: elements.NewScrollHandle(),
		output: elements.NewScrollHandle(), breaks: elements.NewScrollHandle(),
	}
}

func (ws *Workspace) startDebug(action goAction, update updateFn, w *gpui.Window) {
	if s := ws.debug.session; s != nil && s.Snapshot().State != debugger.Finished {
		ws.showDebug()
		return
	}
	if err := ws.goPreflight(); err != nil {
		ws.StatusError = err.Error()
		return
	}
	dir, name, args, err := ws.launchTarget(action)
	if err != nil {
		ws.StatusError = err.Error()
		return
	}
	command, err := config.ResolveLanguageServerCommand("dlv")
	if err != nil {
		ws.StatusError = "Delve not found. Install: go install github.com/go-delve/delve/cmd/dlv@latest"
		return
	}
	mode := "debug"
	if b := ws.Buf(); action == goDebugTest || (name == "" && b != nil && strings.HasSuffix(strings.ToLower(b.Path), "_test.go")) {
		mode = "test"
		args = nil
	}
	ws.showDebug()
	ws.StatusError = ""
	s := debugger.Start(command, dir, mode, ws.debug.points, args...)
	ws.debug.session = s
	ws.debug.name = name
	ws.debug.resetPanel("Output")
	if dispatch := ws.dispatch; dispatch != nil {
		go watchDebug(s, dispatch, update, w)
	}
}

// Coalesce log bursts before dispatching; never read or mutate workspace
// fields from a debugger goroutine.
func watchDebug(s *debugger.Session, dispatch func(func()), update updateFn, w *gpui.Window) {
	var queued atomic.Bool
	post := func() {
		if !queued.CompareAndSwap(false, true) {
			return
		}
		defer func() { _ = recover() }()
		dispatch(func() {
			queued.Store(false)
			defer func() { _ = recover() }()
			update(func(ws *Workspace) {
				if ws.debug.session == s {
					ws.applyDebugSnapshot(s.Snapshot())
				}
			})
			w.Refresh()
		})
	}
	for {
		select {
		case <-s.Done():
			post()
			return
		case <-s.Changes():
			post()
			t := time.NewTimer(33 * time.Millisecond)
			select {
			case <-t.C:
			case <-s.Done():
				t.Stop()
				post()
				return
			}
		}
	}
}

func (ws *Workspace) applyDebugSnapshot(snap debugger.Snapshot) {
	d := &ws.debug
	if d.follow {
		d.output.Autoscroll()
	}
	if snap.State != debugger.Paused || snap.Selected < 0 || snap.Selected >= len(snap.Frames) || d.location == snap.Location {
		return
	}
	d.location = snap.Location
	d.tab = "Inspect"
	d.openVars = nil
	// New pause: reset the Inspect reading position (the workspace-owned
	// offsets must follow — persistScroll re-requests them every render
	// and would otherwise restore the previous position over the reset).
	d.vars.ScrollToTop()
	d.varsTop, d.varsLeft = 0, 0
	if snap.Selected == 0 {
		d.stack.ScrollToTop()
		d.stackTop, d.stackLeft = 0, 0
	}
	f := snap.Frames[snap.Selected]
	if filepath.IsAbs(f.Source.Path) && f.Line > 0 {
		previous := ws.Buf()
		ws.openGoDiagnostic(goDiagnostic{path: f.Source.Path, line: f.Line, column: max(1, f.Column)})
		if previous != ws.Buf() && ws.Find != nil {
			ws.RefreshFind()
		}
	}
}

func (ws *Workspace) breakpointPath(path string) string {
	for k := range ws.debug.points {
		if sameFilePath(k, path) {
			return k
		}
	}
	abs, _ := filepath.Abs(path)
	return abs
}

func canBreakpoint(b *editor.Buffer) bool {
	return b != nil && b.Kind == editor.KindText && b.Path != "" && strings.EqualFold(filepath.Ext(b.Path), ".go")
}

func (ws *Workspace) toggleBreakpoint(b *editor.Buffer, line int) {
	if !canBreakpoint(b) || line < 1 || line > b.LineCount() {
		return
	}
	if b.Dirty {
		ws.StatusError = "Save this Go file before changing breakpoints"
		return
	}
	ws.StatusError = ""
	d := &ws.debug
	if d.points == nil {
		d.points = make(map[string][]int)
	}
	path := ws.breakpointPath(b.Path)
	lines := d.points[path]
	i, found := slices.BinarySearch(lines, line)
	if found {
		lines = slices.Delete(lines, i, i+1)
	} else {
		lines = slices.Insert(lines, i, line)
	}
	ws.setDebugBreakpoints(path, lines)
}

func (ws *Workspace) setDebugBreakpoints(path string, lines []int) {
	d := &ws.debug
	if len(lines) == 0 {
		delete(d.points, path)
	} else {
		d.points[path] = lines
	}
	if d.session != nil {
		d.session.SetBreakpoints(path, lines)
	}
}

func (ws *Workspace) toggleCaretBreakpoint() {
	if b := ws.Buf(); canBreakpoint(b) {
		line, _ := b.OffsetToLC(b.Cursor)
		ws.toggleBreakpoint(b, line+1)
	}
}

func (ws *Workspace) debugMarkers(b *editor.Buffer, snap debugger.Snapshot) (map[int]editor.BreakpointMarker, int) {
	var marks map[int]editor.BreakpointMarker
	execLine := 0
	if canBreakpoint(b) && len(ws.debug.points) != 0 {
		path := ws.breakpointPath(b.Path)
		if lines := ws.debug.points[path]; len(lines) != 0 {
			marks = make(map[int]editor.BreakpointMarker, len(lines))
			for _, n := range lines {
				bp, resolved := snap.Breakpoints[path][n]
				marks[n] = editor.BreakpointMarker{Rejected: snap.State != debugger.Finished && resolved && !bp.Verified}
			}
		}
	}
	if snap.State == debugger.Paused && len(snap.Frames) > 0 {
		if f := snap.Frames[0]; sameFilePath(b.Path, f.Source.Path) && !b.Dirty {
			execLine = f.Line
		}
	}
	return marks, execLine
}

func (ws *Workspace) debugControl(action debugger.Action) {
	if s := ws.debug.session; s != nil {
		snap := s.Snapshot()
		if (action == debugger.Pause && snap.State == debugger.Running) || (action != debugger.Pause && snap.State == debugger.Paused) {
			ws.showDebug()
			s.Request(action, 0, snap.StopID)
		}
	}
}

// debugIconButton renders a compact square toolbar button with a Lucide
// icon instead of a text label — GoLand's debugger toolbar look. The
// action name moves into the tooltip since the icon replaces the label.
func debugIconButton(t ui.Theme, id, icon string, col gc.Hsla, click func(*gpui.Window), tip string) *elements.Div {
	d := elements.NewDiv().ID(elements.ButtonID(id)).Flex().ItemsCenter().JustifyCenter().W(ui.Px(26)).H(ui.Px(24)).FlexShrink(0).
		Rounded(ui.AbsPx(4)).TooltipBuilder(ui.Tooltip(tip))
	if click != nil {
		d.CursorPointer().Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { click(w); w.Refresh() })
	}
	return d.Child(ui.Icon(icon, col, 15))
}

// debugList builds a scrollable panel segment (the debug views and the
// todos/git lists). h is the persistent scroll handle; stored is the
// workspace-owned offset (advanced in the wheel handler so the per-render
// persistScroll re-anchor never rolls the reading position back).
func debugList(id string, h *elements.ScrollHandle, onScroll func(), stored *float32) *elements.Div {
	return newDebugList(id, h, onScroll, stored, nil, nil).plain()
}

// debugHList is debugList with horizontal panning for the debug segments:
// rows attach to a content column sized to the widest row, so wide values
// overflow the segment and wheel / shift-wheel pans across them (GoLand's
// variables view) instead of being clipped inside their row. storedLeft
// keeps the pan offset across frames like stored does vertically.
func debugHList(id string, h *elements.ScrollHandle, onScroll func(), stored, storedLeft *float32, w *gpui.Window) *debugListEl {
	return newDebugList(id, h, onScroll, stored, storedLeft, w)
}

// debugListEl is one panel segment: the scroll container plus (when it
// pans horizontally) the content column rows attach to. stored/storedLeft
// mirror the wheel handler's workspace-owned offsets (the scrollbar drags
// write them too, so the per-render persistScroll re-anchor cannot roll a
// drag back); onScroll is the user-scrolled hook (cancels the output's
// tail-follow).
type debugListEl struct {
	outer, content, list *elements.Div
	handle               *elements.ScrollHandle
	win                  *gpui.Window
	width                float32
	id                   string
	stored, storedLeft   *float32
	onScroll             func()
}

// newDebugList assembles the segment. Without a window and a stored pan
// offset the segment behaves exactly like the plain debugList (rows attach
// to the scroll container itself, which clips per row as before).
func newDebugList(id string, h *elements.ScrollHandle, onScroll func(), stored, storedLeft *float32, w *gpui.Window) *debugListEl {
	horiz := w != nil && storedLeft != nil
	l := &debugListEl{handle: h, win: w, list: nil, id: id, stored: stored, storedLeft: storedLeft, onScroll: onScroll}
	outer := elements.NewDiv().ID(elements.ButtonID(id)).Flex().FlexCol().Flex1().MinH_0().MinW_0().
		OverflowYScroll().TrackScroll(h).Px(ui.DefPx(8)).Py(ui.DefPx(4)).TextSize(ui.AbsPx(12)).
		OnScroll(func(ev platform.ScrollEvent, win *gpui.Window) {
			delta := ev.Delta.PixelDelta(geometry.Px(20))
			dy := delta.Y.AsF32()
			dx := delta.X.AsF32()
			if horiz && ev.Modifiers.Shift && dx == 0 {
				dx, dy = dy, 0 // shift+wheel pans horizontally
			}
			if dx != 0 && horiz && h.MaxLeft() > 0 {
				if storedLeft != nil {
					*storedLeft = max(float32(0), *storedLeft-dx)
				}
				h.ScrollLeftBy(-dx)
				win.StopPropagation()
				win.Refresh()
			}
			if dy == 0 {
				return
			}
			if onScroll != nil {
				onScroll()
			}
			h.ScrollTopBy(-dy)
			if stored != nil {
				*stored = max(0, *stored-dy)
			}
			win.StopPropagation()
			win.Refresh()
		})
	if horiz {
		l.content = elements.NewDiv().ID(elements.ButtonID(id + "-content")).
			Flex().FlexCol().FlexShrink(0).TextSize(ui.AbsPx(12))
		l.list = l.content
		outer = outer.OverflowXScroll().Child(l.content)
	} else {
		l.list = outer
	}
	l.outer = outer
	return l
}

// plain returns the scroll container for the non-panning segments (rows
// attach to it directly, matching the historical layout).
func (l *debugListEl) plain() *elements.Div { return l.outer }

// Child appends an element to the row list.
func (l *debugListEl) Child(el gpui.Element) { l.list.Child(el) }

// addRow appends a decorated row and widens the content column to fit its
// text (indent rides the row's left padding, e.g. the tree depth indent).
func (l *debugListEl) addRow(row *elements.Div, textW, indent float32) {
	l.list.Child(row)
	l.width = max(l.width, indent+textW)
}

// addText appends a plain monospace row.
func (l *debugListEl) addText(label string, col gc.Hsla) {
	l.addRow(debugRow(label, col), measureMonoWidth(l.win, label), 0)
}

// addRuns appends a pre-styled runs row; b carries the value string and
// runs the row's Text element was built from.
func (l *debugListEl) addRuns(row *elements.Div, b *runBuilder, indent float32) {
	l.addRow(row, b.width(l.win), indent)
}

// element finalizes a segment: the content column is sized to the widest
// row (never narrower than the last viewport, so short rows keep a
// full-width hover strip), and the scroll container is wrapped in a scroll
// area with visible scrollbars. The width overflows the container and pans
// instead of clipping; a column that fits gets exactly the viewport's
// width — any extra would be phantom overflow that keeps the horizontal
// bar mounted (covering the last row) with nothing to scroll to.
func (l *debugListEl) element(t ui.Theme) *elements.Div {
	if l.content != nil {
		w := l.width
		if vp := l.handle.ViewportSize().Width.AsF32(); vp > w {
			w = vp
		}
		l.content.W(ui.Px(max(float32(1), w)))
	}
	return l.scrollArea(t)
}

// scrollArea hosts the scroll container with real gutter chrome, like any
// normal scroll area: a bottom strip when the content column is wider than
// the viewport and a right column when the rows overflow vertically. The
// bars live in those reserved strips instead of overlaying the rows, so
// they never cover content — the scroll container clips its children above
// the bottom strip and left of the right column, and the corner cell
// between the two bars stays empty. Geometry mirrors the handle like the
// tree's bars do (one frame of lag, the same timing as the editor's
// scrollbars); the half-pixel slack keeps rounding noise from mounting a
// bar with nothing to scroll.
func (l *debugListEl) scrollArea(t ui.Theme) *elements.Div {
	h := l.handle
	vh, ch := h.ViewportSize().Height.AsF32(), h.ContentSize().Height.AsF32()
	vw, cw := h.ViewportSize().Width.AsF32(), h.ContentSize().Width.AsF32()
	vbar := vh > 0 && ch > vh+0.5
	hbar := vw > 0 && cw > vw+0.5
	main := elements.NewDiv().ID(elements.ButtonID(l.id + "-scrollarea")).
		Flex().FlexCol().Flex1().MinH_0().MinW_0().Child(l.outer)
	if hbar {
		main.Child(l.scrollbar(t, false, vw, cw, h.Left().AsF32(), h.MaxLeft().AsF32()))
	}
	area := elements.NewDiv().ID(elements.ButtonID(l.id + "-area")).
		Flex().Flex1().MinH_0().MinW_0().Child(main)
	if vbar {
		area.Child(l.scrollbar(t, true, vh, ch, h.Top().AsF32(), h.MaxTop().AsF32(), hbar))
	}
	return area
}

// debugScrollbarMinThumb keeps the thumb grabbable at any scroll ratio (the
// tree's minimum).
const debugScrollbarMinThumb = float32(24)

// scrollbar builds one bar inside its reserved gutter strip (vertical when
// v is set): the strip is a real flex child — the scroll area's bottom
// strip or right column — and the interactive track sits inside it with
// the TreeScrollbarInset breathing room, so the bar paints beside the rows
// instead of on top of them. The track is a 10px band with a
// viewport/content-sized thumb placed at the scroll offset's ratio.
// Pressing the track jumps the view to the click, dragging follows the
// pointer (the tree's scrollbar); both drive the handle AND the
// workspace-owned stored offset, so the per-render persistScroll re-anchor
// cannot roll a drag back.
func (l *debugListEl) scrollbar(t ui.Theme, v bool, viewport, content, offset, maxOffset float32, hbarVisible ...bool) *elements.Div {
	trackLen := viewport - 2*ui.TreeScrollbarInset
	thumbLen := trackLen
	if content > 0 {
		thumbLen = viewport / content * trackLen
	}
	if thumbLen < debugScrollbarMinThumb {
		thumbLen = debugScrollbarMinThumb
	}
	if thumbLen > trackLen {
		thumbLen = trackLen
	}
	travel := trackLen - thumbLen
	thumbPos := float32(0)
	if maxOffset > 0 && travel > 0 {
		thumbPos = offset / maxOffset * travel
	}
	id := l.id + "-vscroll"
	if !v {
		id = l.id + "-hscroll"
	}
	// seek maps a position along the track onto the scroll offset: the thumb
	// centers on the pointer (click-to-jump), then follows the drag.
	seek := func(local float32, w *gpui.Window) {
		if travel <= 0 {
			return
		}
		ratio := (local - thumbLen/2) / travel
		ratio = max(float32(0), min(ratio, 1))
		target := ratio * maxOffset
		if v {
			if l.stored != nil {
				*l.stored = target
			}
			l.handle.SetTop(geometry.Px(target))
			if l.onScroll != nil {
				l.onScroll() // a user drag cancels the output's tail-follow
			}
		} else {
			if l.storedLeft != nil {
				*l.storedLeft = target
			}
			l.handle.SetLeft(geometry.Px(target))
		}
		w.Refresh()
	}
	// Pointer position along the bar's axis, relative to the track's origin
	// (the hitbox bounds give the track's window origin).
	local := func(pos geometry.Point[geometry.Pixels], hb gpui.Hitbox) float32 {
		if v {
			return pos.Y.AsF32() - float32(hb.Bounds.Origin.Y)
		}
		return pos.X.AsF32() - float32(hb.Bounds.Origin.X)
	}
	track := elements.NewDiv().ID(elements.ButtonID(id)).
		Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1.Alpha(0.5)) }).
		// Capture-phase press: the track overlays the rows (deeper,
		// propagation-stopping), so the track claims the drag first.
		OnMouseDownCapture(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				seek(local(ev.Position, hb), w)
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
				seek(local(ev.Position, hb), w)
			}
			w.StopPropagation()
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
			w.Refresh()
		})
	var trackBox, thumb *elements.Div
	if v {
		// The right gutter column spans the scroll area's full height; the
		// interactive track inside it stops above the horizontal strip when
		// that bar is visible too, keeping the corner cell empty.
		bottom := ui.Px(ui.TreeScrollbarInset)
		if len(hbarVisible) > 0 && hbarVisible[0] {
			bottom = ui.Px(ui.TreeScrollbarInset + ui.TreeScrollbarW)
		}
		trackBox = track.Absolute().Left(ui.Px(0)).Right(ui.Px(0)).
			Top(ui.Px(ui.TreeScrollbarInset)).Bottom(bottom)
		thumb = elements.NewDiv().
			Absolute().Left(ui.Px(1.5)).Top(ui.Px(thumbPos)).
			W(ui.Px(ui.TreeScrollbarW - 3)).H(ui.Px(thumbLen)).
			Rounded(ui.AbsPx(3)).
			BgColor(t.Surface2)
		strip := elements.NewDiv().ID(elements.ButtonID(id + "-strip")).
			Relative().FlexShrink(0).W(ui.Px(ui.TreeScrollbarW))
		return strip.Child(trackBox.Child(thumb))
	}
	// The bottom gutter strip spans the scroll container's width (the right
	// column sits outside it, so the track needs no extra corner gap).
	trackBox = track.Absolute().Left(ui.Px(0)).Right(ui.Px(ui.TreeScrollbarInset)).
		Top(ui.Px(0)).Bottom(ui.Px(0))
	thumb = elements.NewDiv().
		Absolute().Top(ui.Px(1.5)).Left(ui.Px(thumbPos)).
		W(ui.Px(thumbLen)).H(ui.Px(ui.TreeScrollbarW - 3)).
		Rounded(ui.AbsPx(3)).
		BgColor(t.Surface2)
	strip := elements.NewDiv().ID(elements.ButtonID(id + "-strip")).
		Relative().FlexShrink(0).H(ui.Px(ui.TreeScrollbarW))
	return strip.Child(trackBox.Child(thumb))
}

// debugTextSize is the debug panel's row text size; measurement must match
// the painted size exactly (the segments set TextSize on their content).
const debugTextSize = 12

// measureMonoWidth shapes a plain monospace row label at the panel's text
// size and returns the painted width (0 when no text system is available).
func measureMonoWidth(w *gpui.Window, s string) float32 {
	if w == nil || s == "" {
		return 0
	}
	sys := w.TextSystem()
	if sys == nil {
		return 0
	}
	fid, ok := sys.FontID(debugMonoFont)
	if !ok {
		return 0
	}
	return sys.LayoutLine(s, geometry.Px(debugTextSize), []text.FontRun{{Len: len(s), FontID: fid}}).Width.AsF32()
}

func debugRow(label string, col gc.Hsla) *elements.Div {
	return elements.NewDiv().H(ui.Px(20)).FlexShrink(0).Flex().ItemsCenter().WhitespaceNowrap().OverflowHidden().
		Child(ui.MonoText(label, col))
}

// debugRunsRow is a 20px monospaced row with pre-styled runs (the
// GoLand-style variable/frame rendering: name, value and type each carry
// their own code color). The builder owns both the value string and the
// runs (a Text element needs them paired).
func debugRunsRow(b *runBuilder) *elements.Div {
	return elements.NewDiv().H(ui.Px(20)).FlexShrink(0).Flex().ItemsCenter().WhitespaceNowrap().OverflowHidden().
		Child(b.text())
}

// runBuilder accumulates styled runs and their shared value string. Text
// elements need both (the runs slice the value by byte length).
type runBuilder struct {
	value strings.Builder
	runs  []text.TextRun
}

var debugMonoFont = font.Font{Family: "Monospace", Weight: font.WeightNormal}

func (b *runBuilder) add(s string, c gc.Hsla) {
	if s == "" {
		return
	}
	b.value.WriteString(s)
	b.runs = append(b.runs, text.TextRun{Len: len(s), Font: debugMonoFont, Color: c})
}

func (b *runBuilder) addItalic(s string, c gc.Hsla) {
	if s == "" {
		return
	}
	b.value.WriteString(s)
	b.runs = append(b.runs, text.TextRun{Len: len(s), Font: debugMonoFont.Italic(), Color: c})
}

func (b *runBuilder) text() *elements.Text {
	return elements.NewText(b.value.String()).WithRuns(b.runs)
}

// width shapes the builder's value as one line with its per-run fonts at
// the panel's text size and returns the painted width (the runs pair with
// the value by byte length, so shaping sees exactly what painting sees).
func (b *runBuilder) width(w *gpui.Window) float32 {
	if w == nil {
		return 0
	}
	sys := w.TextSystem()
	if sys == nil || b.value.Len() == 0 {
		return 0
	}
	runs := make([]text.FontRun, 0, len(b.runs))
	for _, r := range b.runs {
		fid, ok := sys.FontID(r.Font)
		if !ok {
			fid, _ = sys.FontID(debugMonoFont) // fallback face keeps the pairing
		}
		runs = append(runs, text.FontRun{Len: r.Len, FontID: fid})
	}
	if len(runs) == 0 {
		return 0
	}
	return sys.LayoutLine(b.value.String(), geometry.Px(debugTextSize), runs).Width.AsF32()
}

// debugValueMax caps a variable's rendered value: delve prints large
// strings and deep composites in full, which would make rows overflow the
// segment for screens of text (GoLand truncates with an ellipsis too).
const debugValueMax = 240

// formatDebugValue sanitizes and truncates a Delve-printed value for one
// line: control characters and raw newlines become spaces, values past the
// cap end with an ellipsis.
func formatDebugValue(value string) string {
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	runes := []rune(clean)
	if len(runes) > debugValueMax {
		return string(runes[:debugValueMax]) + "…"
	}
	return clean
}

func isDebugIdentRune(r rune, first bool) bool {
	if r == '_' {
		return true
	}
	if first {
		return unicode.IsLetter(r)
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// debugValueRuns colors a Delve-printed value like Go code (the GoLand
// debugger's syntax-aware values): string and rune literals green, numbers
// peach, true/false/nil keyword mauve, field and map keys teal, composite
// type names peach, pointer addresses dim, punctuation subdued.
func debugValueRuns(b *runBuilder, value string, t ui.Theme) {
	runes := []rune(value)
	i := 0
	for i < len(runes) {
		r := runes[i]
		switch {
		case r == '"' || r == '`':
			// String literal: consume through the closing quote. Interpreted
			// strings honor backslash escapes; raw strings run to the quote.
			j := i + 1
			if r == '`' {
				for j < len(runes) && runes[j] != '`' {
					j++
				}
				if j < len(runes) {
					j++
				}
			} else {
				for j < len(runes) {
					if runes[j] == '\\' {
						j += 2
						continue
					}
					if runes[j] == '"' {
						j++
						break
					}
					j++
				}
			}
			b.add(string(runes[i:min(j, len(runes))]), t.Green)
			i = j
		case unicode.IsDigit(r):
			// Number or pointer address: 0x-prefixed hex values are pointers,
			// dimmed; everything numeric renders in the number color.
			j := i
			if strings.HasPrefix(value[i:], "0x") || strings.HasPrefix(value[i:], "0X") {
				j += 2
				for j < len(runes) && isHexDigit(runes[j]) {
					j++
				}
				b.add(string(runes[i:j]), t.Overlay)
			} else {
				for j < len(runes) && (unicode.IsDigit(runes[j]) || runes[j] == '.' || runes[j] == 'e' || runes[j] == 'E' ||
					((runes[j] == '+' || runes[j] == '-') && j > i && (runes[j-1] == 'e' || runes[j-1] == 'E'))) {
					j++
				}
				b.add(string(runes[i:j]), t.Peach)
			}
			i = j
		case isDebugIdentRune(r, true):
			j := i
			for j < len(runes) && isDebugIdentRune(runes[j], false) {
				j++
			}
			word := string(runes[i:j])
			rest := string(runes[j:])
			switch {
			case word == "true" || word == "false" || word == "nil" || word == "func" || word == "interface" || word == "chan":
				b.add(word, t.Mauve)
			case strings.HasPrefix(rest, ":") && !strings.HasPrefix(rest, "::"):
				b.add(word, t.Teal) // struct field or map key
			case strings.HasPrefix(rest, "{"):
				b.add(word, t.Peach) // composite type name
			case word == "len" || word == "cap":
				b.add(word, t.Teal) // slice/map annotations len: cap:
			default:
				b.add(word, t.Text)
			}
			i = j
		default:
			// Brackets, commas, colons, spaces: structural noise, subdued so
			// the data stands out (GoLand renders punctuation dimmer too).
			b.add(string(r), t.Dim)
			i++
		}
	}
}

func isHexDigit(r rune) bool {
	return unicode.IsDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// debugVarRuns renders one variable row the GoLand way:
//
//	name = <value colored by data type>   (type in italic)
//
// The expand arrow rides in front when the value has nested children.
func debugVarRuns(v debugger.Variable, t ui.Theme, arrow string) *runBuilder {
	b := &runBuilder{}
	b.add(arrow, t.Dim)
	b.add(v.Name, t.Text)
	b.add(" = ", t.Dim)
	debugValueRuns(b, formatDebugValue(v.Value), t)
	if v.Type != "" {
		b.add("   ", t.Dim)
		b.addItalic("("+v.Type+")", t.Overlay)
	}
	return b
}

// debugFrameRuns renders a call-stack row: the file:line reference in the
// link color, the function name in the code function color (GoLand's
// frames read like source references).
func debugFrameRuns(path, fn string, line int, t ui.Theme, selected bool) *runBuilder {
	b := &runBuilder{}
	if path != "" {
		name := filepath.Base(path)
		b.add(fmt.Sprintf("%s:%d", name, line), t.Accent)
		b.add(" · ", t.Overlay)
	}
	col := t.Sky
	if selected {
		col = t.Accent
	}
	b.add(fn, col)
	return b
}

// debugBreakpointRuns renders one breakpoint row: the file:line link, a
// moved-target marker in yellow, an unresolved message in red.
func debugBreakpointRuns(path string, line int, moved int, message string, t ui.Theme) *runBuilder {
	b := &runBuilder{}
	base := path
	if base == "" {
		base = "?"
	}
	b.add(fmt.Sprintf("%s:%d", base, line), t.Accent)
	if moved > 0 {
		b.add(fmt.Sprintf(" → %d", moved), t.Yellow)
	}
	if message != "" {
		b.add(" · "+message, t.Red)
	}
	return b
}

func renderDebug(ws *Workspace, snap debugger.Snapshot, t ui.Theme, update updateFn, w *gpui.Window) gpui.Element {
	d := &ws.debug
	s := d.session
	persistScroll(d.stack, &d.stackTop)
	persistScroll(d.vars, &d.varsTop)
	persistScroll(d.output, &d.outputTop)
	persistScroll(d.breaks, &d.breaksTop)
	persistScrollLeft(d.stack, &d.stackLeft)
	persistScrollLeft(d.vars, &d.varsLeft)
	persistScrollLeft(d.output, &d.outputLeft)
	persistScrollLeft(d.breaks, &d.breaksLeft)
	label := "Go Debug · " + string(snap.State)
	if d.name != "" {
		label += " · " + d.name
	}
	if s == nil {
		label = "Go Debug"
	}
	controls := elements.NewDiv().ID(ui.StructID("debug-controls")).Flex().ItemsCenter().H(ui.Px(32)).FlexShrink(0).Px(ui.DefPx(6)).Gap(ui.DefPx(2)).BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).
		Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().Child(elements.NewText(label).WithTextColor(t.Text))).Child(elements.NewDiv().FlexGrow(1))
	addControl := func(id, icon, tip string, col gc.Hsla, fn func(*gpui.Window)) {
		controls.Child(debugIconButton(t, "debug-"+id, icon, col, fn, tip))
	}
	action := func(a debugger.Action) func(*gpui.Window) {
		return func(*gpui.Window) { s.Request(a, 0, snap.StopID) }
	}
	switch snap.State {
	case debugger.Paused:
		addControl("continue", "play", "Continue", t.Green, action(debugger.Continue))
		addControl("next", "redo", "Step Over", t.Text, action(debugger.Next))
		addControl("in", "arrow-down-to-line", "Step Into", t.Text, action(debugger.StepIn))
		addControl("out", "arrow-up-from-line", "Step Out", t.Text, action(debugger.StepOut))
	case debugger.Running:
		addControl("pause", "pause", "Pause", t.Text, action(debugger.Pause))
	}
	if s == nil || snap.State == debugger.Finished {
		addControl("start", "play", "Start debug session", t.Green, func(w *gpui.Window) { ws.startGoAction(goDebug, update, w) })
	} else if snap.State != debugger.Stopping {
		addControl("stop", "square", "Stop", t.Red, func(*gpui.Window) { s.Stop() })
	}
	controls.Child(debugIconButton(t, "debug-hide", "x", t.Dim, func(*gpui.Window) { update(func(ws *Workspace) { ws.ShowDebug = false }) }, "Hide debugger panel"))
	// GoLand-style underline tabs: the active tab keeps the panel
	// background with an accent underline; inactive tabs dim and light up
	// on hover. Selection acts on mouse-down (see the editor tab strip:
	// a tree rebuild mid-click can never drop the press).
	tabs := elements.NewDiv().ID(ui.StructID("debug-tabs")).Flex().ItemsCenter().FlexShrink(0).Px(ui.DefPx(6)).Gap(ui.DefPx(2)).BorderB_1().BorderColor(t.Surface1)
	for _, tab := range []string{"Inspect", "Output", "Breakpoints"} {
		active := tab == d.tab
		el := elements.NewDiv().ID(elements.ButtonID("debug-tab-" + tab)).Flex().ItemsCenter().H(ui.Px(28)).FlexShrink(0).
			Px(ui.DefPx(10)).TextSize(ui.AbsPx(12)).WhitespaceNowrap().CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
			OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
				if ev.Button != platform.ButtonLeft {
					return
				}
				update(func(ws *Workspace) { ws.debug.tab = tab })
				w.Refresh()
			})
		if active {
			el.BgColor(t.EditorBg).BorderB_2().BorderColor(t.Accent)
		}
		col := t.Dim
		if active {
			col = t.Text
		}
		el.Child(elements.NewText(tab).WithTextColor(col))
		tabs.Child(el)
	}
	if snap.Detail != "" {
		tabs.Child(elements.NewDiv().FlexGrow(1))
		tabs.Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().Child(elements.NewText(snap.Detail).WithTextColor(t.Dim)))
	}
	var body gpui.Element
	switch d.tab {
	case "Inspect":
		body = renderDebugInspect(ws, snap, t, update, s, w)
	case "Breakpoints":
		list := debugHList("debug-breakpoints", d.breaks, nil, &d.breaksTop, &d.breaksLeft, w)
		paths := make([]string, 0, len(d.points))
		for path := range d.points {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		if len(paths) == 0 {
			list.addText("Click the Go editor gutter or press F9 to add a breakpoint.", t.Dim)
		}
		for _, path := range paths {
			for _, line := range d.points[path] {
				b := debugBreakpointRuns(path, line, 0, "", t)
				if bp, ok := snap.Breakpoints[path][line]; ok && snap.State != debugger.Finished {
					if !bp.Verified {
						b = debugBreakpointRuns(path, line, 0, bp.Message, t)
					} else if bp.Line > 0 && bp.Line != line {
						b = debugBreakpointRuns(path, line, bp.Line, "", t)
					}
				}
				row := elements.NewDiv().Flex().ItemsCenter().FlexShrink(0).Gap(ui.DefPx(4))
				row.Child(debugIconButton(t, fmt.Sprintf("debug-remove-%s:%d", path, line), "x", t.Dim, func(*gpui.Window) {
					update(func(ws *Workspace) {
						ws.setDebugBreakpoints(path, slices.DeleteFunc(ws.debug.points[path], func(n int) bool { return n == line }))
					})
				}, "Remove breakpoint"))
				row.Child(elements.NewDiv().MinW_0().FlexShrink(0).Child(b.text()))
				row.CursorPointer().Hover(func(st *style.StyleRefinement) { st.BgColor(t.Surface1) }).OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
					update(func(ws *Workspace) { ws.openGoDiagnostic(goDiagnostic{path: path, line: line, column: 1}) })
					w.Focus(ws.FocusEditor)
					w.Refresh()
				})
				list.addRuns(row, b, 26) // indent covers the × button + gap
			}
		}
		body = list.element(t)
	default:
		list := debugHList("debug-output", d.output, func() {
			update(func(ws *Workspace) { ws.debug.follow = false })
			d.output.StopAutoscroll()
		}, &d.outputTop, &d.outputLeft, w)
		lines := strings.Split(strings.ReplaceAll(snap.Output, "\r\n", "\n"), "\n")
		for _, line := range lines[max(0, len(lines)-500):] {
			list.addText(line, t.Text)
		}
		body = list.element(t)
	}
	return elements.NewDiv().ID(elements.ButtonID("debug-panel")).Flex().FlexCol().H(ui.Px(ws.effectiveTermHeight())).FlexShrink(0).
		BgColor(t.EditorBg).BorderT_1().BorderColor(t.Surface1).
		// The dock resize strip rides the panel's top edge: dragging it
		// re-anchors the shared dock height (same as the terminal's edge).
		Child(renderDockResize("debug-resize", t, func(y float32, w *gpui.Window) {
			update(func(s *Workspace) { s.SetTermHeight(y) })
			w.Refresh()
		})).
		Child(controls).Child(tabs).Child(body)
}

// debugSegment assembles one Inspect column: a fixed header (title plus an
// optional annotation) over an independently scrolling list — each segment
// scrolls on its own like GoLand's debugger views, and the headers stay
// visible while the content moves.
func debugSegment(id, title, note string, list *elements.Div, t ui.Theme) *elements.Div {
	header := elements.NewDiv().ID(ui.StructID("debug-seg-head-" + id)).Flex().ItemsCenter().H(ui.Px(24)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(8)).BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).TextSize(ui.AbsPx(11))
	header.Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().Child(
		elements.NewText(title).WithTextColor(t.Dim)))
	if note != "" {
		header.Child(elements.NewDiv().FlexGrow(1))
		header.Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().Child(
			elements.NewText(note).WithTextColor(t.Overlay)))
	}
	return elements.NewDiv().ID(ui.StructID("debug-seg-" + id)).Flex().FlexCol().Flex1().MinW_0().MinH_0().Child(header).Child(list)
}

func renderDebugInspect(ws *Workspace, snap debugger.Snapshot, t ui.Theme, update updateFn, s *debugger.Session, w *gpui.Window) gpui.Element {
	d := &ws.debug
	// Call stack segment.
	stack := debugHList("debug-stack", d.stack, nil, &d.stackTop, &d.stackLeft, w)
	if snap.State != debugger.Paused {
		stack.addText("Pause to inspect the program", t.Dim)
	} else {
		for i, frame := range snap.Frames {
			b := debugFrameRuns(frame.Source.Path, frame.Name, frame.Line, t, i == snap.Selected)
			row := debugRunsRow(b).ID(elements.ButtonID(fmt.Sprintf("debug-frame-%d", i))).CursorPointer().
				Hover(func(st *style.StyleRefinement) { st.BgColor(t.Surface1) }).
				OnClick(func(_ gpui.ClickEvent, _ *gpui.Window) { s.Request(debugger.SelectFrame, i, snap.StopID) })
			stack.addRuns(row, b, 0)
		}
	}
	stackSeg := debugSegment("stack", "Call Stack", goroutineNote(snap), stack.element(t), t)
	stackSeg.BorderR_1().BorderColor(t.Surface1).FlexBasis(ui.Px(0)).FlexGrow(2)

	// Variables segment.
	vars := debugHList("debug-variables", d.vars, nil, &d.varsTop, &d.varsLeft, w)
	if snap.State == debugger.Paused {
		count := 500 // cap render work for large nested values
		for _, scope := range snap.Scopes {
			ref := scope.VariablesReference
			_, loaded := snap.Variables[ref]
			if !scope.Expensive && loaded {
				vars.addText("▾ "+scope.Name, t.Mauve)
				appendDebugVars(vars, ws, snap, t, update, ref, 1, &count)
			} else {
				debugExpandable(vars, ws, snap, t, update, scope.Name, ref, 0, &count)
			}
		}
	}
	varsSeg := debugSegment("vars", "Variables", "", vars.element(t), t)
	varsSeg.FlexBasis(ui.Px(0)).FlexGrow(3)

	return elements.NewDiv().ID(ui.StructID("debug-inspect")).Flex().Flex1().MinH_0().Child(stackSeg).Child(varsSeg)
}

func goroutineNote(snap debugger.Snapshot) string {
	if snap.State != debugger.Paused {
		return ""
	}
	return fmt.Sprintf("goroutine %d · %d frames", snap.Thread, len(snap.Frames))
}

func appendDebugVars(list *debugListEl, ws *Workspace, snap debugger.Snapshot, t ui.Theme, update updateFn, ref, depth int, count *int) {
	if depth > 10 {
		return
	}
	for _, v := range snap.Variables[ref] {
		if *count <= 0 {
			return
		}
		*count--
		debugExpandableVar(list, ws, snap, t, update, v, depth, count)
	}
}

func debugExpandable(list *debugListEl, ws *Workspace, snap debugger.Snapshot, t ui.Theme, update updateFn, label string, ref, depth int, count *int) {
	v := debugger.Variable{Name: label, VariablesReference: ref}
	debugExpandableVar(list, ws, snap, t, update, v, depth-1, count)
}

// debugExpandableVar renders one variable row: the GoLand-style colored
// runs, tree indentation per depth, hover highlight, and click-to-expand
// when Delve reports nested children (VariablesReference > 0).
func debugExpandableVar(list *debugListEl, ws *Workspace, snap debugger.Snapshot, t ui.Theme, update updateFn, v debugger.Variable, depth int, count *int) {
	open := ws.debug.openVars[v.VariablesReference] && v.VariablesReference > 0
	arrow := "  "
	if v.VariablesReference > 0 {
		arrow = "▸ "
		if open {
			arrow = "▾ "
		}
	}
	b := debugVarRuns(v, t, arrow)
	row := debugRunsRow(b).Pl(ui.DefPx(float32(depth * 12)))
	row.Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) })
	if v.VariablesReference > 0 {
		row.CursorPointer()
		row.TooltipBuilder(ui.Tooltip(v.Value)) // full value on hover (rows pan instead of wrapping)
		row.OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
			update(func(ws *Workspace) {
				if ws.debug.openVars == nil {
					ws.debug.openVars = make(map[int]bool)
				}
				ws.debug.openVars[v.VariablesReference] = !open
			})
			if !open {
				ws.debug.session.Request(debugger.Expand, v.VariablesReference, snap.StopID)
			}
			w.Refresh()
		})
	}
	list.addRuns(row, b, float32(depth*12))
	if open {
		appendDebugVars(list, ws, snap, t, update, v.VariablesReference, depth+1, count)
	}
}
