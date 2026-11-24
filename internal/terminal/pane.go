package terminal

import (
	"fmt"
	"strings"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/font"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
	"github.com/FlameInTheDark/go-gpui/text"

	"github.com/FlameInTheDark/flint/internal/ui"
)

// PaneConfig configures RenderPane.
type PaneConfig struct {
	Term  *Terminal
	Theme ui.Theme
	// Height is the dock's rendered height (the workspace's effective,
	// resize-aware value — never a fixed constant).
	Height float32
	// CharAdvance is the monospace advance; LineHeight the mono row height.
	CharAdvance float32
	LineHeight  float32
	// BodyWidth is the dock's content width (the workspace pane width);
	// the grid is exactly this many cells wide — the shell wraps.
	BodyWidth float32
	// Focus is the terminal's keyboard focus handle.
	Focus gpui.FocusHandle
	// Focused reports whether the window's focus is on the terminal right
	// now (the cursor renders filled when focused, hollow otherwise).
	Focused bool
	// Update redraws after a state change.
	Update func(f func(*Terminal))
	// OnResize fires while the user drags the dock's top edge; the
	// argument is the pointer's window y (the edge follows the pointer).
	OnResize func(y float32)
	// OnClear empties the scrollback (header button).
	OnClear func()
	// OnTerminalToggle hides the dock (header close button, Ctrl+`).
	OnTerminalToggle func()

	// FontSizePx overrides the glyph size (0 = the window default); the
	// workspace passes the settings panel's font size (the editor and the
	// terminal stay in lockstep).
	FontSizePx float32

	// Dock tabs (the upstream editor's terminal pane hosts several terminals; the header
	// shows one tab per terminal plus a "+" button). When Tabs is empty the
	// header falls back to a single "Terminal" label.
	Tabs        []Tab
	OnSelectTab func(i int)
	OnNewTab    func()
	OnCloseTab  func(i int)
}

// Tab is one dock header tab (the terminal's working-directory name).
type Tab struct {
	Title  string
	Active bool
}

// dock geometry (the resize strip and the header ride the panel's top).
const (
	termPadLeft   = 10.0
	termScrollW   = 4.0 // slim overlay scrollbar
	termRowIndent = 0.0
)

// RenderPane renders the terminal dock (the upstream editor's bottom dock): a resize strip
// plus header the user grabs to resize, and the shell's own grid — prompt,
// output, full-screen programs — virtualized, selectable and scrollable.
// The pane's grid size IS the pty size: the shell lays itself out.
func RenderPane(cfg PaneConfig) gpui.Element {
	t := cfg.Theme

	lineH := cfg.LineHeight
	if lineH <= 0 {
		lineH = 18
	}
	adv := cfg.CharAdvance
	if adv <= 0 {
		adv = 8
	}

	// Body geometry: everything below the header chrome.
	bodyH := cfg.Height - ui.TermResizeStrip - ui.TermHeaderH
	if bodyH < lineH {
		bodyH = lineH
	}
	bodyW := cfg.BodyWidth - termPadLeft - termScrollW - 4
	if bodyW < 8*adv {
		bodyW = 8 * adv
	}
	cols := int(bodyW / adv)
	if cols < 8 {
		cols = 8
	}
	rows := int((bodyH - 4) / lineH)
	if rows < 1 {
		rows = 1
	}

	// The pane's grid size is the pty's size: spawn the session on the
	// first frame and push resizes through (SIGWINCH / ResizePseudoConsole).
	// Direct model calls — no entity update, no notify (the reader
	// goroutine's OnOutput drives redraws; render itself is already a frame).
	cfg.Term.EnsureStarted(cols, rows)

	total := cfg.Term.RowTotal()
	first := cfg.Term.EffectiveScroll(total, rows)
	visibleRows := cfg.Term.VisibleRows(first, rows)
	snap := cfg.Term.Snapshot()
	hasSel := snap.SelOK

	// ---- visible rows (virtualized: exactly the window that fits) --------

	rowsEl := elements.NewDiv().
		Flex().FlexCol().JustifyEnd(). // the grid pins to the bottom like a real terminal
		Flex1().OverflowHidden().
		Pt(ui.DefPx(4))
	if cfg.FontSizePx > 0 {
		rowsEl = rowsEl.TextSize(ui.AbsPx(cfg.FontSizePx))
	}

	nRows := 0
	for i, cells := range visibleRows {
		absRow := first + i
		selLo, selHi := selSpan(snap, absRow)
		rowsEl = rowsEl.Child(gridRow(cfg, t, cells, lineH, selLo, selHi, hasSel))
		nRows++
	}
	if nRows == 0 {
		rowsEl = rowsEl.Child(gridRow(cfg, t, textRow("(terminal)", cols), lineH, -1, -1, false))
		nRows++
	}

	// mapSelPos converts a window position into a grid position (absolute
	// row + cell column) for the selection gestures.
	mapSelPos := func(pos geometry.Point[geometry.Pixels], w *gpui.Window) (int, int, bool) {
		hb, ok := w.ListenerHitbox()
		if !ok || nRows == 0 {
			return 0, 0, false
		}
		origin := hb.Bounds.Origin
		h := hb.Bounds.Size.Height.AsF32()
		// Rows pin to the bottom of the body (JustifyEnd).
		rowsTop := h - float32(nRows)*lineH
		row := int((pos.Y.AsF32() - origin.Y.AsF32() - rowsTop) / lineH)
		if row < 0 {
			row = 0
		}
		if row > nRows-1 {
			row = nRows - 1
		}
		col := int((pos.X.AsF32() - origin.X.AsF32() - termPadLeft) / adv)
		if col < 0 {
			col = 0
		}
		return first + row, col, true
	}

	// ---- body -------------------------------------------------------------
	body := elements.NewDiv().
		ID(elements.ButtonID("terminal-body")).
		Flex().Flex1().Relative().OverflowHidden().
		CursorText().
		TrackFocus(cfg.Focus).
		OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
			termKey(cfg, ev, w)
		}).
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			// Clicking the dock focuses the terminal (the upstream editor focuses the pane
			// on click) and starts a selection gesture; a plain click
			// collapses the selection (StartSelection sets anchor = head).
			w.Focus(cfg.Focus)
			if hb, ok := w.ListenerHitbox(); ok {
				w.CapturePointer(hb.ID)
			}
			if row, col, ok := mapSelPos(ev.Position, w); ok {
				switch ev.ClickCount {
				case 2:
					cfg.Update(func(term *Terminal) { term.SelectWordAt(row, col) })
				case 3:
					cfg.Update(func(term *Terminal) { term.SelectLineAt(row) })
				default:
					cfg.Update(func(term *Terminal) { term.StartSelection(row, col) })
				}
			}
			w.Refresh()
		}).
		OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
			if !ev.Dragging() {
				return
			}
			if row, col, ok := mapSelPos(ev.Position, w); ok {
				cfg.Update(func(term *Terminal) { term.ExtendSelection(row, col) })
				w.Refresh()
			}
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
		}).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			// Wheel down is a negative platform delta; negate into the
			// terminal's first-visible-row scroll position.
			delta := -ev.Delta.PixelDelta(geometry.Px(lineH)).Y.AsF32() / lineH
			if delta != 0 {
				cfg.Update(func(term *Terminal) { term.ScrollLines(int(delta), total, rows) })
				w.Refresh()
			}
		})
	body = body.Child(rowsEl)

	// The cursor: a block over the cursor cell while the pane is focused,
	// a hollow box when it is not (the upstream editor's terminal cursor). The shell's
	// cursor-addressing decides the cell; hide-and-show comes from DECSET 25.
	if snap.CursorVisible && snap.CursorRow >= first && snap.CursorRow < first+rows {
		relRow := snap.CursorRow - first
		cursorX := float32(snap.CursorCol)*adv + termPadLeft
		// Rows pin to the body's bottom (JustifyEnd): the cursor cell sits
		// at rowsTop + relRow*lineH, rowsTop = bodyH - rendered rows.
		cursorY := bodyH - float32(nRows)*lineH + float32(relRow)*lineH + 2
		if relRow < nRows {
			cursorEl := elements.NewDiv().
				Absolute().Left(ui.Px(cursorX)).Top(ui.Px(cursorY)).
				W(ui.Px(adv)).H(ui.Px(lineH - 3)).
				Occlude().
				OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) { w.StopPropagation() })
			if cfg.Focused {
				cursorEl = cursorEl.BgColor(t.Accent.Alpha(0.45))
			} else {
				cursorEl = cursorEl.Border_1().BorderColor(t.Accent.Alpha(0.8))
			}
			body = body.Child(cursorEl)
		}
	}

	// Slim overlay scrollbar over the body's right edge.
	maxScroll := total - rows
	if maxScroll > 0 && bodyH > 0 {
		thumbH := float32(rows) / float32(total) * bodyH
		if thumbH < 16 {
			thumbH = 16
		}
		thumbTop := float32(first) / float32(maxScroll) * (bodyH - thumbH)
		seek := func(y float32, w *gpui.Window) {
			frac := clampF((y-8)/(bodyH-16), 0, 1)
			cfg.Update(func(term *Terminal) {
				term.SetScroll(int(frac*float32(maxScroll) + 0.5))
			})
			w.Refresh()
		}
		scrollbar := elements.NewDiv().
			Absolute().Right(ui.Px(1)).Top(ui.Px(0)).Bottom(ui.Px(0)).
			W(ui.Px(termScrollW)).
			OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
				if ev.Button != platform.ButtonLeft {
					return
				}
				if hb, ok := w.ListenerHitbox(); ok {
					seek(ev.Position.Y.AsF32()-float32(hb.Bounds.Origin.Y.AsF32()), w)
					w.CapturePointer(hb.ID)
				}
				w.StopPropagation()
			}).
			OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
				if !ev.Dragging() {
					return
				}
				if hb, ok := w.ListenerHitbox(); ok {
					seek(ev.Position.Y.AsF32()-float32(hb.Bounds.Origin.Y.AsF32()), w)
				}
				w.StopPropagation()
			}).
			OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
				w.ReleasePointer()
			}).
			Child(elements.NewDiv().
				Absolute().Left(ui.Px(0.5)).Top(ui.Px(thumbTop)).
				W(ui.Px(termScrollW - 1)).H(ui.Px(thumbH)).
				Rounded(ui.AbsPx(2)).
				BgColor(t.Surface2))
		body = body.Child(scrollbar)
	}

	// ---- dock assembly ----------------------------------------------------
	header := renderHeader(cfg, t, snap)
	strip := renderResizeStrip(cfg, t)

	panel := elements.NewDiv().Flex().FlexCol().H(ui.Px(cfg.Height)).
		FlexShrink(0).
		BgColor(t.Surface).BorderT_1().BorderColor(t.Surface1)
	panel = panel.Child(strip).Child(header).Child(body)
	return panel
}

// selSpan returns the selected cell column window of one absolute row.
func selSpan(snap Snapshot, absRow int) (int, int) {
	if !snap.SelOK || absRow < snap.SelStart.Row || absRow > snap.SelEnd.Row {
		return -1, -1
	}
	lo, hi := 0, 1<<30
	if absRow == snap.SelStart.Row {
		lo = snap.SelStart.Col
	}
	if absRow == snap.SelEnd.Row {
		hi = snap.SelEnd.Col
	}
	if hi <= lo {
		return -1, -1
	}
	return lo, hi
}

// clampF clamps a float.
func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// textRow builds a plain-text row (the empty-pane placeholder).
func textRow(s string, cols int) []Cell {
	cells := make([]Cell, cols)
	for i := range cells {
		if i < len([]rune(s)) {
			cells[i] = Cell{R: rune([]rune(s)[i])}
		}
	}
	return cells
}

// gridRow converts one grid row into merged styled text runs (selection
// and reverse-video fold into the run backgrounds) and renders it.
func gridRow(cfg PaneConfig, t ui.Theme, cells []Cell, lineH float32, selLo, selHi int, hasSel bool) gpui.Element {
	var sb strings.Builder
	var runs []text.TextRun

	type key struct {
		fg  vtColor
		bg  vtColor
		at  cellAttrs
		sel bool
	}
	var curKey key
	runStart := 0
	flush := func(end int) {
		if end <= runStart {
			return
		}
		st := curKey
		fgc := termColor(st.fg, t, true)
		bgc := termColor(st.bg, t, false)
		if st.at&attrReverse != 0 {
			fgc, bgc = termColor(st.bg, t, true), termColor(st.fg, t, false)
			if st.bg == defaultColor {
				bgc = t.Text
			}
			if st.fg == defaultColor {
				fgc = t.Crust
			}
		}
		if st.sel {
			selBg := t.SelectionBackground()
			bgc = *selBg
		}
		run := text.TextRun{
			Len:   sb.Len() - runStart,
			Font:  font.Font{Family: "Monospace", Weight: boldWeight(st.at&attrBold != 0), Style: italicStyle(st.at&attrItalic != 0)},
			Color: fgc,
		}
		if st.sel || st.bg != defaultColor || st.at&attrReverse != 0 {
			run.BackgroundColor = &bgc
		}
		if st.at&attrUnderline != 0 {
			run.Underline = &style.UnderlineStyle{Thickness: 1}
		}
		runs = append(runs, run)
		runStart = end
	}
	// Trailing default-styled blanks collapse to nothing (a real terminal
	// does not paint past the last glyph); everything up to the last
	// content cell — inter-word spaces included — renders as runs.
	end := len(cells)
	for end > 0 {
		c := cells[end-1]
		if (c.R != ' ' && c.R != 0) || c.Bg != defaultColor || c.At != 0 || (hasSel && selLo <= end-1 && end-1 < selHi) {
			break
		}
		end--
	}
	for i, c := range cells {
		if i >= end {
			break
		}
		selected := hasSel && i >= selLo && i < selHi
		r := c.R
		if r == 0 || c.Tail {
			r = ' '
		}
		k := key{fg: c.Fg, bg: c.Bg, at: c.At, sel: selected}
		if k != curKey {
			flush(sb.Len())
			curKey = k
		}
		sb.WriteRune(r)
	}
	flush(sb.Len())

	row := elements.NewDiv().
		H(ui.Px(lineH)).FlexShrink(0).Flex().
		Pl(ui.DefPx(termPadLeft)).
		WhitespaceNowrap()
	if sb.Len() == 0 {
		row = row.Child(elements.NewText("").WithRuns([]text.TextRun{{Len: 0, Font: font.Font{Family: "Monospace"}, Color: t.Text}}))
	} else {
		row = row.Child(elements.NewText(sb.String()).WithRuns(runs))
	}
	return row
}

func boldWeight(bold bool) font.FontWeight {
	if bold {
		return font.WeightBold
	}
	return font.WeightNormal
}

func italicStyle(italic bool) font.FontStyle {
	if italic {
		return font.StyleItalic
	}
	return font.StyleNormal
}

// rgbHsla builds an opaque theme color from RGB components.
func rgbHsla(r, g, b uint8) gc.Hsla {
	return gc.RGB(uint32(r)<<16 | uint32(g)<<8 | uint32(b)).Hsla()
}

// termColor maps a cell color to the theme: palette indexes 0-15 ride the
// Catppuccin terminal palette, 16-255 and RGB pass through directly.
func termColor(c vtColor, t ui.Theme, foreground bool) gc.Hsla {
	if c.rgb {
		return rgbHsla(c.r, c.g, c.b)
	}
	switch {
	case c.idx < 16:
		return termPalette(int(c.idx), t, foreground)
	case c.idx < 232:
		// xterm 6x6x6 color cube.
		n := int(c.idx) - 16
		lv := [6]uint8{0, 95, 135, 175, 215, 255}
		return rgbHsla(lv[n/36], lv[(n/6)%6], lv[n%6])
	default:
		v := uint8(8 + int(c.idx-232)*10)
		return rgbHsla(v, v, v)
	}
}

// termPalette maps the 16 ANSI colors to the theme (Catppuccin Mocha's
// terminal palette, the pane's existing mapping).
func termPalette(i int, t ui.Theme, foreground bool) gc.Hsla {
	if !foreground {
		// Background palette: dark but visible variants.
		switch i {
		case 0:
			return t.Surface1
		case 7, 15:
			return t.Overlay
		case 11:
			return t.Yellow.Alpha(0.35)
		default:
			h := termPalette(i, t, true)
			return h.Alpha(0.35)
		}
	}
	switch i {
	case 0:
		return t.Dim
	case 1:
		return t.Red
	case 2:
		return t.Green
	case 3:
		return t.Yellow
	case 4:
		return t.Accent
	case 5:
		return t.Mauve
	case 6:
		return t.Teal
	case 7:
		return t.Text
	case 8:
		return t.Overlay
	case 9:
		return t.Red
	case 10:
		return t.Green
	case 11:
		return t.Yellow
	case 12:
		return t.Sky
	case 13:
		return t.Mauve
	case 14:
		return t.Teal
	case 15:
		return t.Text
	}
	return t.Text
}

// renderResizeStrip builds the 5px strip riding the dock's top edge: part
// of the grab area (the whole top part resizes the dock).
func renderResizeStrip(cfg PaneConfig, t ui.Theme) gpui.Element {
	return elements.NewDiv().
		ID(elements.ButtonID("terminal-resize-strip")).
		H(ui.Px(ui.TermResizeStrip)).FlexShrink(0).
		CursorRowResize().
		Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1.Alpha(0.6)) }).
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				w.CapturePointer(hb.ID)
			}
			w.StopPropagation()
		}).
		OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
			if !ev.Dragging() {
				return
			}
			if cfg.OnResize != nil {
				cfg.OnResize(ev.Position.Y.AsF32())
			}
			w.StopPropagation()
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
		})
}

// Terminal dock chrome type scale: every glyph in the header lives inside
// a small box (tab, close square, action button), so the header pins an
// explicit text size and line height instead of the default 1rem/phi
// 26px line — which overflowed the 20px tab and made the close glyph hang
// below the tab's rounded rect.
const (
	termTabH       = 22.0
	termTabTextPx  = 12.0
	termTextLinePx = 16.0
	termClosePx    = 16.0
)

// renderHeader builds the dock header: one tab per terminal (the working
// directory's name, like the upstream editor's dock tabs) with a "+" button, and the
// clear / close actions at the right. The header is part of the resize
// grab area.
func renderHeader(cfg PaneConfig, t ui.Theme, snap Snapshot) gpui.Element {
	header := elements.NewDiv().
		ID(elements.ButtonID("terminal-header")).
		Flex().ItemsCenter().H(ui.Px(ui.TermHeaderH)).FlexShrink(0).
		Px(ui.DefPx(10)).Gap(ui.DefPx(6)).
		TextSize(ui.AbsPx(termTabTextPx)).LineHeight(ui.DefPx(termTextLinePx)).
		BgColor(t.Surface).
		CursorRowResize().
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				w.CapturePointer(hb.ID)
			}
			w.StopPropagation()
		}).
		OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
			if !ev.Dragging() {
				return
			}
			if cfg.OnResize != nil {
				cfg.OnResize(ev.Position.Y.AsF32())
			}
			w.StopPropagation()
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
		})

	if len(cfg.Tabs) == 0 {
		// Single-terminal fallback: the dock's identity label.
		tab := elements.NewDiv().Flex().ItemsCenter().
			Child(elements.NewText("Terminal").WithTextColor(t.Text))
		header = header.Child(tab)
	}
	for i, tab := range cfg.Tabs {
		header = header.Child(renderTermTab(cfg, t, i, tab))
	}
	if len(cfg.Tabs) > 0 && cfg.OnNewTab != nil {
		header = header.Child(headerButton(t, "+", "plus", "New terminal", t.Dim, func(w *gpui.Window) {
			cfg.OnNewTab()
			w.Refresh()
		}))
	}
	header = header.Child(elements.NewDiv().FlexGrow(1))

	if !snap.Alive {
		header = header.Child(elements.NewText("restart").WithTextColor(t.Overlay))
	}
	header = header.Child(headerButton(t, "clear", "eraser", "Clear terminal", t.Dim, func(w *gpui.Window) {
		if cfg.OnClear != nil {
			cfg.OnClear()
		}
		w.Refresh()
	}))
	header = header.Child(headerButton(t, "x", "x", "Hide terminal (Ctrl+`)", t.Dim, func(w *gpui.Window) {
		if cfg.OnTerminalToggle != nil {
			cfg.OnTerminalToggle()
		}
		w.Refresh()
	}))
	return header
}

// renderTermTab builds one terminal tab: the working-directory name and a
// 16×16 close square, matching the upstream editor's dock tabs (title only — no prompt
// glyph; the active tab gets the highlighted background). Long titles
// truncate with an ellipsis so a wide working directory can't push the
// close square or the "+" button out of the header. The tab swallows
// mouse-down so the header's resize-drag never steals the click.
func renderTermTab(cfg PaneConfig, t ui.Theme, i int, tab Tab) gpui.Element {
	el := elements.NewDiv().
		ID(elements.ButtonID("terminal-tab-" + tab.Title)).
		Flex().ItemsCenter().Gap(ui.DefPx(5)).
		H(ui.Px(termTabH)).Px(ui.DefPx(7)).Rounded(ui.AbsPx(4)).
		MaxW(ui.Px(200)).
		WhitespaceNowrap().TextEllipsis().OverflowHidden().
		CursorPointer().
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			w.StopPropagation() // not a resize grab
		}).
		OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
			if cfg.OnSelectTab != nil {
				cfg.OnSelectTab(i)
			}
			w.Refresh()
		})
	if tab.Active {
		el = el.BgColor(t.Surface2)
		el = el.Child(elements.NewText(tab.Title).WithTextColor(t.Text))
	} else {
		el = el.Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) })
		el = el.Child(elements.NewText(tab.Title).WithTextColor(t.Dim))
	}
	if cfg.OnCloseTab != nil {
		el = el.Child(elements.NewDiv().
			ID(elements.ButtonID(fmt.Sprintf("terminal-close-tab-%d", i))).Tooltip("Close terminal tab").
			FlexShrink(0).
			Flex().ItemsCenter().JustifyCenter().
			W(ui.Px(termClosePx)).H(ui.Px(termClosePx)).Rounded(ui.AbsPx(3)).
			CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
			OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
				if ev.Button == platform.ButtonLeft {
					w.StopPropagation()
				}
			}).
			OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
				cfg.OnCloseTab(i)
				w.Refresh()
			}).
			Child(ui.Icon("x", t.Dim, 12)))
	}
	return el
}

// headerButton is a small flat action button (hover highlight only). The
// 16px line height inherited from the header keeps its glyph inside the
// 20px box.
func headerButton(t ui.Theme, id, icon, tip string, color gc.Hsla, onClick func(w *gpui.Window)) gpui.Element {
	return elements.NewDiv().
		ID(elements.ButtonID("terminal-" + id)).
		Tooltip(tip).
		Flex().ItemsCenter().JustifyCenter().
		H(ui.Px(20)).Px(ui.DefPx(6)).Rounded(ui.AbsPx(4)).
		CursorPointer().
		Child(ui.Icon(icon, color, 14)).
		Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
		OnClick(func(ev gpui.ClickEvent, w *gpui.Window) { onClick(w) })
}

// termKey routes a keystroke to the shell (raw bytes — the shell owns
// line editing) with the upstream editor's clipboard bindings on top: ctrl-shift-c copies
// the selection, ctrl-shift-v pastes (bracketed), ctrl+` toggles the dock.
func termKey(cfg PaneConfig, ev platform.KeyDownEvent, w *gpui.Window) {
	k := ev.Keystroke
	mods := k.Modifiers
	secondary := mods.Control || mods.Platform

	if secondary && mods.Shift {
		switch k.Key {
		case "c":
			if cfg.Term.HasSelection() {
				if pw := w.PlatformWindow(); pw != nil {
					pw.WriteClipboard(platform.ClipboardItem{Text: cfg.Term.SelectedText()})
				}
			}
			w.Refresh()
			return
		case "v":
			if pw := w.PlatformWindow(); pw != nil {
				if item, ok := pw.ReadClipboard(); ok && item.Text != "" {
					cfg.Term.Paste(item.Text)
					w.Refresh()
				}
			}
			return
		}
	}

	if secondary && k.Key == "`" {
		if cfg.OnTerminalToggle != nil {
			cfg.OnTerminalToggle()
		}
		w.Refresh()
		return
	}

	// Everything else — letters, digits, Enter, arrows, Ctrl+C (0x03),
	// Tab, Escape — goes to the shell exactly as typed (the upstream editor's terminal
	// passes Escape through; Ctrl+` is the only local binding).
	cfg.Term.SendKey(k.Key, k.KeyChar, mods.Control, mods.Alt, mods.Shift)
	w.Refresh()
}
