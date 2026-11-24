// Hex view: the read-only presentation of binary buffers — a classic hex
// dump (offset gutter, 16 bytes as hex pairs, ASCII column) with
// virtualized rows, wheel/keyboard scrolling and a scrollbar. Editing,
// find and the minimap are text-pane features and stay off.

package editor

import (
	"fmt"
	"math"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/text"

	"github.com/FlameInTheDark/flint/internal/ui"
)

// HexConfig configures RenderHexPane.
type HexConfig struct {
	Buf   *Buffer
	Theme ui.Theme
	// Metrics: monospace advance and row height (the text pane's).
	CharAdvance float32
	LineHeight  float32
	// Scroll is the first visible row (virtualized like the code pane).
	Scroll int
	// ScrollF is the rendered fractional scroll (the wheel ease; see
	// PaneConfig.ScrollF). 0 with a 0 Scroll renders the plain integer path.
	ScrollF float32
	// ViewportH/W size the visible row window and the scrollbar.
	ViewportH float32
	ViewportW float32
	// FontSizePx overrides the glyph size (0 = window default).
	FontSizePx float32
	// Focus is the editor's focus handle (arrow-key scrolling).
	Focus gpui.FocusHandle
	// OnScroll scrolls by rows (positive = down).
	OnScroll func(delta int)
	// OnWheelScroll, when set, carries raw wheel deltas (fractional rows)
	// through the workspace's smooth-scroll ease.
	OnWheelScroll func(deltaF float32)
}

// hexBytesPerRow is the dump line width; the ASCII column pads to it.
const (
	hexBytesPerRow = 16
	hexAsciiCols   = hexBytesPerRow
)

// RenderHexPane renders the binary dump pane.
func RenderHexPane(cfg HexConfig) gpui.Element {
	t := cfg.Theme
	b := cfg.Buf
	lineH := cfg.LineHeight
	if lineH <= 0 {
		lineH = 18
	}
	adv := cfg.CharAdvance
	if adv <= 0 {
		adv = 8
	}

	total := b.HexRowCount()
	visible := int(cfg.ViewportH / lineH)
	if visible < 1 {
		visible = 1
	}
	maxScroll := total - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	// Rendered position (the wheel ease): a fractional part slides the
	// window between rows exactly like the text pane's.
	scrollF := cfg.ScrollF
	if scrollF < 0 {
		scrollF = 0
	}
	if maxF := float32(maxScroll); scrollF > maxF {
		scrollF = maxF
	}
	firstF := float32(math.Floor(float64(scrollF)))
	frac := scrollF - firstF
	scroll := int(firstF)
	if scrollF <= 0 && cfg.Scroll > 0 && cfg.ScrollF == 0 {
		// Caller passed only Scroll (ScrollF's zero value): render the
		// integer position.
		scroll = min(max(cfg.Scroll, 0), maxScroll)
	}
	shifted := frac > 0.0005
	first := scroll
	last := min(total-1, first+visible-1) // inclusive
	if shifted && last+1 <= total-1 {
		last++ // the slide can reveal one more row at the bottom edge
	}

	monoRuns := func(color gc.Hsla, s string) []text.TextRun {
		return []text.TextRun{{Len: len(s), Font: mono(false), Color: color}}
	}

	// ---- rows -------------------------------------------------------------
	// The rows live in one column; while the wheel ease slides the window
	// between rows they move into an absolutely positioned wrapper shifted
	// by the fractional part (see PaneConfig's codeRows wrapper).
	rows := elements.NewDiv().Flex().FlexCol().OverflowHidden().Flex1()
	if cfg.FontSizePx > 0 {
		rows = rows.TextSize(ui.AbsPx(cfg.FontSizePx))
	}
	var hexRows *elements.Div
	if shifted {
		hexRows = elements.NewDiv().
			Absolute().Left(ui.Px(0)).Top(ui.Px(-frac * lineH)).
			Flex().FlexCol()
	}
	addRow := func(row *elements.Div) {
		if hexRows != nil {
			hexRows = hexRows.Child(row)
		} else {
			rows = rows.Child(row)
		}
	}
	for i := first; i <= last; i++ {
		row := elements.NewDiv().H(ui.Px(lineH)).Flex().ItemsCenter().
			WhitespaceNowrap().FlexShrink(0)
		off := i * hexBytesPerRow
		chunk := b.Data
		if off+hexBytesPerRow <= len(chunk) {
			chunk = chunk[off : off+hexBytesPerRow]
		} else if off < len(chunk) {
			chunk = chunk[off:]
		} else {
			chunk = nil
		}

		// Offset gutter (dim).
		row = row.Child(elements.NewText(fmt.Sprintf("%08x", off)).
			WithRuns(monoRuns(t.Dim, fmt.Sprintf("%08x", off)))).
			Child(elements.NewDiv().W(ui.Px(14)))

		// Hex pairs: printable-position bytes tinted by class.
		for j := 0; j < hexBytesPerRow; j++ {
			if j > 0 {
				row = row.Child(elements.NewDiv().W(ui.Px(adv)))
			}
			if j >= len(chunk) {
				row = row.Child(elements.NewText("  ").WithRuns(monoRuns(t.Surface2, "  ")))
				continue
			}
			c := chunk[j]
			color := t.Text
			switch {
			case c == 0x00:
				color = t.Surface2
			case c < 0x20 || c == 0x7F:
				color = t.Mauve
			case c >= 0x80:
				color = t.Yellow
			}
			row = row.Child(elements.NewText(fmt.Sprintf("%02x", c)).
				WithRuns(monoRuns(color, fmt.Sprintf("%02x", c))))
		}
		row = row.Child(elements.NewDiv().W(ui.Px(14)))

		// ASCII column.
		var sb []byte
		for _, c := range chunk {
			if c >= 0x20 && c < 0x7F {
				sb = append(sb, c)
			} else {
				sb = append(sb, '.')
			}
		}
		ascii := string(sb)
		for len(ascii) < hexAsciiCols {
			ascii += " "
		}
		row = row.Child(elements.NewText(ascii).WithRuns(monoRuns(t.Overlay, ascii)))
		addRow(row)
	}
	if hexRows != nil {
		rows = rows.Child(hexRows)
	}

	// ---- header note + pane ------------------------------------------------
	note := fmt.Sprintf("%s — %d bytes%s", b.Name, len(b.Data), "")
	if b.DataTruncated {
		note = fmt.Sprintf("%s — showing %d of more bytes (16 MB view cap)", b.Name, len(b.Data))
	}

	pane := elements.NewDiv().
		ID(elements.ButtonID("hex-pane")).
		Flex().FlexCol().Flex1().Relative().OverflowHidden().
		BgColor(t.EditorBg).
		TrackFocus(cfg.Focus).
		Child(elements.NewDiv().
			H(ui.Px(24)).Flex().ItemsCenter().Px(ui.DefPx(10)).
			FlexShrink(0).
			BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).
			TextSize(ui.AbsPx(11)).
			Child(elements.NewText(note).WithTextColor(t.Dim))).
		Child(elements.NewDiv().Flex().Flex1().OverflowHidden().Pl(ui.DefPx(12)).
			Child(rows)).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
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
			k := ev.Keystroke
			mods := k.Modifiers
			if mods.Control || mods.Alt || mods.Platform {
				return
			}
			var step int
			switch k.Key {
			case "down":
				step = 1
			case "up":
				step = -1
			case "pagedown":
				step = visible
			case "pageup":
				step = -visible
			case "home":
				step = -scroll
			case "end":
				step = maxScroll - scroll
			default:
				return
			}
			if cfg.OnScroll != nil && step != 0 {
				cfg.OnScroll(step)
			}
			w.Refresh()
		})

	// ---- scrollbar ---------------------------------------------------------
	if maxScroll > 0 && cfg.ViewportH > 0 {
		trackH := cfg.ViewportH - 24
		if trackH < 40 {
			trackH = 40
		}
		thumbH := trackH * float32(visible) / float32(total)
		if thumbH < 24 {
			thumbH = 24
		}
		if thumbH > trackH {
			thumbH = trackH
		}
		travel := trackH - thumbH
		thumbTop := float32(0)
		if maxScroll > 0 && travel > 0 {
			thumbTop = float32(scroll) / float32(maxScroll) * travel
		}
		seek := func(y float32, w *gpui.Window) {
			if travel <= 0 {
				return
			}
			ratio := (y - thumbH/2) / travel
			if ratio < 0 {
				ratio = 0
			}
			if ratio > 1 {
				ratio = 1
			}
			if cfg.OnScroll != nil {
				cfg.OnScroll(int(ratio*float32(maxScroll)+0.5) - scroll)
			}
			w.Refresh()
		}
		scrollbar := elements.NewDiv().
			ID(elements.ButtonID("hex-vscroll")).
			Absolute().Right(ui.Px(2)).Top(ui.Px(24)).Bottom(ui.Px(0)).
			W(ui.Px(6)).
			OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
				if ev.Button != platform.ButtonLeft {
					return
				}
				if hb, ok := w.ListenerHitbox(); ok {
					seek(ev.Position.Y.AsF32()-float32(hb.Bounds.Origin.Y), w)
					w.CapturePointer(hb.ID)
				}
				w.StopPropagation()
			}).
			OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
				if !ev.Dragging() {
					return
				}
				if hb, ok := w.ListenerHitbox(); ok {
					seek(ev.Position.Y.AsF32()-float32(hb.Bounds.Origin.Y), w)
				}
				w.StopPropagation()
			}).
			OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
				w.ReleasePointer()
			}).
			Child(elements.NewDiv().
				Absolute().Left(ui.Px(0.5)).Top(ui.Px(thumbTop)).
				W(ui.Px(5)).H(ui.Px(thumbH)).
				Rounded(ui.AbsPx(2.5)).
				BgColor(t.Surface2))
		pane = pane.Child(scrollbar)
	}

	return pane
}
