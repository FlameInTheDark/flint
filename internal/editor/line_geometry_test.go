package editor

import (
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
	"github.com/FlameInTheDark/go-gpui/text"
)

func TestCodeLineMapsTabAndUTF8ByteOffsets(t *testing.T) {
	l := newCodeLine("é\t界x\tz")
	if l.display != "é   界x  z" {
		t.Fatalf("display = %q", l.display)
	}
	for _, source := range []int{0, 2, 3, 6, 7, 8, len(l.source)} {
		if back, _ := l.sourceByte(l.displayByte(source)); back != source {
			t.Errorf("offset %d round-tripped to %d", source, back)
		}
	}
	if source, tab := l.sourceByte(4); source != 2 || tab == nil || tab.end != 5 {
		t.Fatalf("tab interior: offset=%d tab=%+v", source, tab)
	}
}

func TestNearestCaretIncludesLastGlyphAdvance(t *testing.T) {
	l := &text.LineLayout{Len: 3, Width: 33, Runs: []text.ShapedRun{{Glyphs: []text.ShapedGlyph{
		{Index: 0}, {Index: 1, Position: geometry.PointPixels(10, 0)}, {Index: 2, Position: geometry.PointPixels(21, 0)},
	}}}}
	if got := nearestCaretByte(l, 23); got != 2 {
		t.Fatalf("left half of final glyph = byte %d, want 2", got)
	}
	if got := nearestCaretByte(l, 30); got != 3 {
		t.Fatalf("right half of final glyph = byte %d, want 3", got)
	}
}

func TestCodeCaretUsesPaintedRowsAndShapedText(t *testing.T) {
	for _, tc := range []struct {
		name, file, raw, prefix string
		size, scale             float32
		pan                     bool
	}{
		{"styled-unicode", "example.go", "package\tCafé 世界 e\u0301", "package\tCafé ", 23, 1.25, false},
		{"proportional-heading", "example.md", "# Wide iii WWW", "# Wide iii ", 19, 1.5, false},
		{"horizontal-pan", "example.go", strings.Repeat("ordinary ", 30) + "tail", strings.Repeat("ordinary ", 17), 17, 1, true},
		{"rtl", "example.go", "אבגדה עולם", "אב", 20, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := headless.New(geometry.SizePixels(520, 240))
			a := gpui.NewApplication(p)
			b := NewBuffer("", tc.file, "other row\n"+tc.raw+"\nnext row\n"+strings.Repeat("more rows\n", 40))
			b.SetCursor(b.LineStart(1) + len(tc.prefix))
			theme := ui.DefaultTheme()
			focus := gpui.NewFocusHandle()
			const rowHeight = 29.5
			var shaper CodeShaper
			h, err := gpui.OpenView(a, platform.WindowOptions{ScaleFactor: tc.scale}, func(*gpui.Window, *app.Context[struct{}]) struct{} {
				return struct{}{}
			}, func(_ *struct{}, w *gpui.Window, _ *app.Context[struct{}]) gpui.Element {
				shaper = NewCodeShaper(w, tc.size)
				x, width := shaper.CaretMetrics(b, theme)
				pan := float32(0)
				if tc.pan {
					pan = max(0, x-180)
				}
				return RenderPane(PaneConfig{Buf: b, Theme: theme, FontSizePx: tc.size, Shaper: shaper, CaretLineWidth: width,
					Metrics: Metrics{CharAdvance: 8, LineHeight: rowHeight}, ViewportW: 520, ViewportH: 240,
					ScrollF: 0.4, ScrollX: pan, Focus: focus, CaretVisible: true, ShowGutter: true,
					OnEdit: func(f func(*Buffer)) { f(b) }, OnScroll: func(int) {},
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			l := newCodeLine(tc.raw)
			shaped := shaper.shape(l, b.LanguageOf(), paletteOf(theme))
			if shaped == nil || shaped.Width <= 0 {
				t.Skip("no usable system font")
			}
			node, _, ok := h.Window.RecordedAccessibility().FindByLabel(l.display)
			if !ok {
				t.Fatal("painted text row missing from accessibility tree")
			}
			x := node.Bounds.Origin.X + layoutX(shaped, l.displayByte(len(tc.prefix)))
			if want := geometry.RoundToDevicePixel(x.AsF32(), tc.scale) * tc.scale; !hasCaretQuad(h.Window, theme, want, node.Bounds.Origin.Y.AsF32()*tc.scale) {
				t.Fatalf("caret not painted at the shaped boundary x=%.2f (device pixels)", want)
			}
			// A click at the top edge of this painted text row should use
			// the second row, not the first row's nominal click band.
			b.SetCursor(0)
			pos := geometry.PointPixels(x.AsF32(), node.Bounds.Origin.Y.AsF32()+0.1)
			p.LastWindow().DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: pos, ClickCount: 1})
			p.LastWindow().DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: pos, ClickCount: 1})
			if row, col := b.OffsetToLC(b.Cursor); row != 1 || col != len(tc.prefix) {
				t.Fatalf("click moved to row %d byte %d, want row 1 byte %d", row, col, len(tc.prefix))
			}
		})
	}
}

func hasCaretQuad(w *gpui.Window, theme ui.Theme, x, minY float32) bool {
	for _, q := range w.RenderedScene().Quads {
		if q.Background.Solid == theme.Accent && q.Bounds.Origin.Y.AsF32() >= minY &&
			absF(q.Bounds.Origin.X.AsF32()-x) < 0.01 && q.Bounds.Size.Width.AsF32() <= 5 {
			return true
		}
	}
	return false
}
