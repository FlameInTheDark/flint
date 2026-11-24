package workspace

import (
	"bytes"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// These scenes pin the editor pane's LEFT-EDGE spacing: the leftmost
// symbols (line numbers when shown, code text when hidden) keep
// editor.GutterLeftInset of breathing room from the pane's left edge, and
// hiding line numbers actually removes the numbers (the old render kept
// drawing them inside a recolored full-width strip).

// openLeftGapScene opens the workspace headlessly with the line-number
// setting applied in the constructor (a single frame: reading the window
// image right after one draw is stable; the Update+Notify+Draw flow leaves
// the in-memory frame mid-swap).
func openLeftGapScene(tb testing.TB, showNumbers bool, buf *editor.Buffer) (*headless.Platform, *gpui.WindowHandle[Workspace], *app.App) {
	tb.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](appl, platform.WindowOptions{Title: "leftgap"},
		func(w *gpui.Window, cx *app.Context[Workspace]) Workspace {
			ws := New("leftgap")
			ws.Measure(w)
			ws.Buffers = []*editor.Buffer{buf}
			ws.Active = 0
			ws.ShowLineNumbers = showNumbers
			ws.ensureScrollArrays()
			return *ws
		})
	if err != nil {
		tb.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		v := handle.View.Read(cx)
		return v.Render(&handle, cx, w)
	})
	handle.Window.Draw()
	return hp, &handle, appl.App
}

// framePixels decodes the rendered frame (the PNG bytes are the presented
// output; the raw window image can still be mid-swap right after Draw).
func framePixels(t *testing.T, hp *headless.Platform) (w, h int, at func(x, y int) (r, g, b, a int)) {
	t.Helper()
	pngBytes, err := hp.LastWindow().PNG()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	bounds := decoded.Bounds()
	return bounds.Dx(), bounds.Dy(), func(x, y int) (int, int, int, int) {
		r, g, b, a := decoded.At(x, y).RGBA()
		return int(r / 257), int(g / 257), int(b / 257), int(a / 257)
	}
}

// TestLeftGapScene renders the code pane with and without the line-number
// gutter and asserts the left-edge spacing and the hidden-numbers removal.
func TestLeftGapScene(t *testing.T) {
	for _, tc := range []struct {
		name   string
		gutter bool
	}{
		{"gutter_on", true},
		{"gutter_off", false},
	} {
		buf := editor.NewBuffer("", "code.go", strings.Repeat("symbol text line here\n", 40))
		hp, handle, cx := openLeftGapScene(t, tc.gutter, buf)
		if pngBytes, err := hp.LastWindow().PNG(); err == nil {
			_ = os.WriteFile("/tmp/leftgap_"+tc.name+".png", pngBytes, 0o644)
		}
		w, h, at := framePixels(t, hp)

		const y0, y1 = 90, 560
		inkCol := make([]bool, w)
		for x := 0; x < w; x++ {
			for y := y0; y < y1 && y < h; y++ {
				r, g, b, a := at(x, y)
				if a > 200 && r+g+b > 240 {
					inkCol[x] = true
					break
				}
			}
		}
		firstX := -1
		for x := 0; x < w; x++ {
			if inkCol[x] {
				firstX = x
				break
			}
		}
		if firstX < 0 {
			t.Fatalf("%s: no ink in the rows band", tc.name)
		}
		// The left inset holds in both modes: nothing renders left of it.
		if float32(firstX) < editor.GutterLeftInset-1.5 {
			t.Fatalf("%s: leftmost ink at x=%d, want >= %v (left inset)", tc.name, firstX, editor.GutterLeftInset)
		}
		// The code text: first x whose ink columns SPAN at least 30px
		// (gaps <= 4px) - "symbol" spans ~57px, the 1-2 glyph number
		// clusters ~19px.
		const minSpan = 30
		codeX := -1
		for x := 0; x < w-minSpan; x++ {
			if !inkCol[x] {
				continue
			}
			last := x
			gaps := 0
			for xx := x + 1; xx < w; xx++ {
				if inkCol[xx] {
					last = xx
					gaps = 0
				} else {
					gaps++
					if gaps > 4 {
						break
					}
				}
			}
			if last-x >= minSpan {
				codeX = x
				break
			}
		}
		if codeX < 0 {
			t.Fatalf("%s: no code-text run found", tc.name)
		}
		if tc.gutter {
			// Numbers shown: the code column starts after the full
			// gutter (left inset + digits + number/code gap), and
			// number ink exists left of it, separated by the gap
			// (the complaint this pins: code letters used to touch
			// the numbers column and the two blended together).
			digits := 1
			for n := buf.LineCount(); n >= 10; n /= 10 {
				digits++
			}
			ws := handle.View.Read(cx)
			minCodeX := editor.GutterLeftInset + float32(digits)*ws.CharAdvance + editor.GutterCodeGap - 2
			if float32(codeX) < minCodeX-2 {
				t.Fatalf("gutter_on: code text at x=%d, want >= %.1f (gutter not applied)", codeX, minCodeX)
			}
			if codeX-firstX < 4 {
				t.Fatal("gutter_on: no separate number ink left of the code column")
			}
			numRight := firstX
			for x := codeX - 6; x > firstX; x-- {
				if inkCol[x] {
					numRight = x
					break
				}
			}
			if gap := codeX - numRight; gap < editor.GutterCodeGap-4 {
				t.Fatalf("gutter_on: code starts %dpx after the last number ink, want >= %v (numbers and code blend together)", gap, editor.GutterCodeGap-4)
			}
			// A gap also has to exist on the code side of the painted divider.
			codeOrigin := 0
			for _, hb := range handle.Window.RenderedHitboxes() {
				if hb.Key == "n:btn-editor-code" {
					codeOrigin = int(hb.Bounds.Origin.X.AsF32())
					break
				}
			}
			if codeOrigin < 12 {
				t.Fatal("missing code-column origin")
			}
			border := ws.Theme().Surface1.ToRgb()
			closeColor := func(a, b int) bool { return a >= b-2 && a <= b+2 }
			divider := -1
			for x := codeOrigin - 11; x < codeOrigin; x++ {
				r, g, b, _ := at(x, 120)
				if closeColor(r, int(border.R*255)) && closeColor(g, int(border.G*255)) && closeColor(b, int(border.B*255)) {
					divider = x
				}
			}
			if divider == -1 || codeOrigin-divider < int(editor.CodeDividerGap)-1 {
				t.Fatalf("divider at x=%d, code origin x=%d: want a visible divider with space after it", divider, codeOrigin)
			}
		} else {
			// Numbers hidden: the code text IS the leftmost ink, starting
			// at the left inset (the collapsed gutter's width).
			if float32(codeX) > editor.GutterLeftInset+2.5 {
				t.Fatalf("gutter_off: code text at x=%d, want ~%v (collapsed gutter)", codeX, editor.GutterLeftInset)
			}
			if codeX != firstX {
				t.Fatalf("gutter_off: number ink still left of the code text (first=%d code=%d)", firstX, codeX)
			}
		}
	}
}
