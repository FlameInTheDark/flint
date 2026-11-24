package workspace_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/render"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestMinimapViewportLandsOnVisibleRows pins the user-visible contract of
// the minimap viewport preview: the highlight rect covers EXACTLY the
// miniature rows of the code the editor window shows — the first visible
// editor line's row is the first row inside the highlight (upstream
// ScrollbarLayout::for_minimap: thumb top = (scroll - minimap_scroll_top) *
// minimap_line_height).
func TestMinimapViewportLandsOnVisibleRows(t *testing.T) {
	// 40 lines: fits the rail entirely (the miniature does not slide), so
	// the highlight must sit exactly on rows [scroll, scroll+visible).
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&sb, "// line %d of the alignment fixture\n", i+1)
	}
	const scroll = 6
	hp, handle, cx := openEditorWindowWith(t, sb.String())
	hw := hp.LastWindow()
	editorUpdate(handle, cx, func(s *workspace.Workspace) { s.SetScrolls(scroll) })
	handle.Window.Draw()

	ws := handle.View.Read(cx)
	mm := findMinimap(t, handle)
	scale := hw.ScaleFactor()
	if scale <= 0 {
		scale = 1
	}
	miniH := float32(editor.MinimapLineHeight)
	visible := editor.Metrics{LineHeight: ws.LineHeight}.VisibleLines(ws.DebugPaneHeight())

	// The viewport indicator paints behind the token blocks with the text
	// color at alpha 0.08 over the current theme's editor background. Its left
	// pad (first 4 miniature px) carries no blocks, so a column 2 device px
	// inside the minimap shows the pure indicator color.
	img := hw.Image()
	bg, fg := ws.Theme().EditorBg.ToRgb(), ws.Theme().Text.ToRgb()
	wantR, wantG, wantB := (bg.R*.92+fg.R*.08)*255, (bg.G*.92+fg.G*.08)*255, (bg.B*.92+fg.B*.08)*255
	x := int(mm.x*scale) + 2
	if x >= img.Width {
		x = img.Width - 1
	}
	isInd := func(y int) bool {
		i := (y*img.Width + x) * 4
		r, g, b := img.Pixels[i], img.Pixels[i+1], img.Pixels[i+2]
		return absf(float32(r)-wantR) < 4 && absf(float32(g)-wantG) < 4 && absf(float32(b)-wantB) < 4
	}
	top, bottom := -1, -1
	for y := int(mm.y * scale); y < img.Height; y++ {
		if isInd(y) {
			if top < 0 {
				top = y
			}
			bottom = y
		} else if top >= 0 && y > top+2 {
			break
		}
	}
	if top < 0 {
		t.Fatalf("viewport indicator not found in the minimap (scroll %d)", scroll)
	}

	wantTop := float32(mm.y)*scale + float32(scroll)*miniH*scale
	if absf(float32(top)-wantTop) > 3 {
		t.Fatalf("indicator top y=%d, want ~%.1f (scroll %d lands the highlight on visible line %d's miniature row)",
			top, wantTop, scroll, scroll)
	}
	wantH := float32(visible) * miniH * scale
	gotH := float32(bottom - top + 1)
	if absf(gotH-wantH) > 3 {
		t.Fatalf("indicator height %.0f, want ~%.1f (visible %d lines at the constant miniature scale)",
			gotH, wantH, visible)
	}
	// The highlight must also END on the last visible line's row: the
	// editor's viewport shows lines [scroll, scroll+visible).
	wantBottom := float32(mm.y)*scale + float32(scroll+visible)*miniH*scale
	if absf(float32(bottom)-wantBottom) > 3 {
		t.Fatalf("indicator bottom y=%d, want ~%.1f (must cover exactly the visible rows)", bottom, wantBottom)
	}
}

// TestMinimapViewportSweepsRailOnTallDoc checks the other half of the
// contract: a document taller than the rail slides its miniature slowly and
// the highlight sweeps the rail linearly, reaching the bottom exactly when
// the editor reaches the document's end.
func TestMinimapViewportSweepsRailOnTallDoc(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&sb, "// line %d\n", i+1)
	}
	hp, handle, cx := openEditorWindowWith(t, sb.String())
	hw := hp.LastWindow()
	ws := handle.View.Read(cx)
	visible := editor.Metrics{LineHeight: ws.LineHeight}.VisibleLines(ws.DebugPaneHeight())
	total := ws.Buf().LineCount()
	maxScroll := total - visible

	editorScroll := func(line int) {
		editorUpdate(handle, cx, func(s *workspace.Workspace) { s.SetScrolls(line) })
		handle.Window.Draw()
	}
	mm := findMinimap(t, handle)
	scale := hw.ScaleFactor()
	if scale <= 0 {
		scale = 1
	}
	miniH := float32(editor.MinimapLineHeight)
	img := hw.Image()
	bg, fg := ws.Theme().EditorBg.ToRgb(), ws.Theme().Text.ToRgb()
	wantR, wantG, wantB := (bg.R*.92+fg.R*.08)*255, (bg.G*.92+fg.G*.08)*255, (bg.B*.92+fg.B*.08)*255
	x := int(mm.x*scale) + 2
	if x >= img.Width {
		x = img.Width - 1
	}
	isInd := func(img *render.RenderImage, y int) bool {
		i := (y*img.Width + x) * 4
		r, g, b := img.Pixels[i], img.Pixels[i+1], img.Pixels[i+2]
		return absf(float32(r)-wantR) < 4 && absf(float32(g)-wantG) < 4 && absf(float32(b)-wantB) < 4
	}
	band := func() (int, int) {
		img := hw.Image() // each Draw produces a fresh frame image
		top, bottom := -1, -1
		for y := int(mm.y * scale); y < img.Height; y++ {
			if isInd(img, y) {
				if top < 0 {
					top = y
				}
				bottom = y
			} else if top >= 0 && y > top+2 {
				break
			}
		}
		return top, bottom
	}

	// Capacity: the miniature rows the rail shows at the constant scale.
	capacity := int(ws.DebugPaneHeight() / miniH)
	contentTop := func(scroll int) float32 {
		if maxScroll <= 0 {
			return 0
		}
		p := float32(scroll) / float32(maxScroll)
		return p * float32(total-capacity)
	}
	expectTop := func(scroll int) float32 {
		vy := (float32(scroll) - contentTop(scroll)) * miniH * scale
		travel := float32(mm.h)*scale - float32(visible)*miniH*scale
		if vy < 0 {
			vy = 0
		}
		if vy > travel {
			vy = travel
		}
		return float32(mm.y)*scale + vy
	}
	for _, scroll := range []int{0, maxScroll / 3, maxScroll} {
		editorScroll(scroll)
		top, bottom := band()
		if top < 0 {
			t.Fatalf("scroll %d: indicator not found", scroll)
		}
		if absf(float32(top)-expectTop(scroll)) > 3 {
			t.Fatalf("scroll %d: indicator top %d, want ~%.1f", scroll, top, expectTop(scroll))
		}
		if scroll == maxScroll && absf(float32(bottom+1)-float32(mm.y*scale+mm.h*scale)) > 4 {
			t.Fatalf("scroll %d (document end): indicator bottom %d must reach the rail bottom %.1f",
				scroll, bottom+1, float32(mm.y*scale+mm.h*scale))
		}
	}
}
