package workspace_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// This file covers the Windows HiDPI + minimap regressions: 3-digit line
// numbers wrapping after 99 (gutter rows hug their text, so scale-dependent
// rounding wrapped the third digit onto a phantom row), SVG file/folder
// icons painting in logical coordinates while their bounds and content
// masks were scaled to device pixels (icons vanished or stacked at the
// window origin at 125%/150%), and the minimap drag whose mapping followed
// a sliding window (stuck mid-drag, jumps past the middle).

// findMinimap returns the minimap hitbox (the wide left part of the rail).
func findMinimap(t *testing.T, handle *gpui.WindowHandle[workspace.Workspace]) hitboxRect {
	t.Helper()
	var mm hitboxRect
	for _, hb := range handle.Window.RenderedHitboxes() {
		r := rectOf(hb)
		if r.w >= editor.MinimapWidth-16 && r.w <= editor.MinimapWidth+20 && r.h > 100 {
			mm = r
		}
	}
	if mm.w == 0 {
		t.Fatal("minimap hitbox not found")
	}
	return mm
}

func visibleLinesOf(handle *gpui.WindowHandle[workspace.Workspace], cx *app.App) int {
	ws := handle.View.Read(cx)
	return editor.Metrics{LineHeight: ws.LineHeight}.VisibleLines(ws.DebugPaneHeight())
}

// TestMinimapDragMapsWholeFile drives the minimap like a scrollbar: clicks
// at fractions of the rail and drags end to end, asserting the whole
// document is reachable with a stable, monotonic mapping (the old sliding
// window got stuck mid-drag and jumped past the middle).
func TestMinimapDragMapsWholeFile(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 400; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i+1))
	}
	hp, handle, cx := openEditorWindowWith(t, sb.String())
	hw := hp.LastWindow()
	mm := findMinimap(t, handle)
	visible := visibleLinesOf(handle, cx)
	maxScroll := 400 - visible
	if maxScroll <= 0 {
		t.Fatalf("test needs a scrollable document (visible=%d)", visible)
	}

	// Expected line for a click at frac of the rail: the viewport indicator
	// centers on the pointer (scrollbar jump-to-click semantics). The
	// indicator height is the viewport at the CONSTANT miniature scale
	// (MinimapLineHeight per line — the upstream editor's minimap never compresses).
	indH := float32(visible) * editor.MinimapLineHeight
	if indH < 2 {
		indH = 2
	}
	targetAt := func(frac float32) int {
		travel := mm.h - indH
		ratio := (frac*mm.h - indH/2) / travel
		if ratio < 0 {
			ratio = 0
		}
		if ratio > 1 {
			ratio = 1
		}
		return int(ratio * float32(maxScroll))
	}
	line := func() int { l, _ := handle.View.Read(cx).DebugScrollState(); return l }

	clickFrac := func(frac float32) int {
		pos := geometry.PointPixels(mm.x+mm.w/2, mm.y+frac*mm.h)
		hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: pos})
		l, _ := handle.View.Read(cx).DebugScrollState()
		hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: pos})
		return l
	}
	// The mapping is stable: clicking the same spot twice lands on the same
	// line (the old mapping moved the window under the cursor, so a second
	// click drifted).
	first := clickFrac(0.5)
	second := clickFrac(0.5)
	if absi(first-second) > 2 {
		t.Fatalf("minimap mapping is unstable: click 50%% -> %d then %d", first, second)
	}
	if absi(first-targetAt(0.5)) > 4 {
		t.Fatalf("click at 50%% landed on line %d, want ~%d (indicator-centered mapping)", first, targetAt(0.5))
	}

	// Drag from 25% to 75%: the scroll tracks the cursor monotonically
	// through the middle (no sticking at the window re-center point).
	start := clickFrac(0.25)
	if absi(start-targetAt(0.25)) > 4 {
		t.Fatalf("click at 25%% landed on line %d, want ~%d", start, targetAt(0.25))
	}
	prev := start
	for step := 0; step <= 10; step++ {
		frac := 0.25 + 0.05*float32(step)
		pos := geometry.PointPixels(mm.x+mm.w/2, mm.y+frac*mm.h)
		hw.DispatchInput(platform.MouseMoveEvent{Position: pos, PressedButton: btnPtr(platform.ButtonLeft)})
		cur := line()
		if cur < prev-2 {
			t.Fatalf("minimap drag went backwards at %.2f: %d after %d", frac, cur, prev)
		}
		prev = cur
	}
	if absi(prev-targetAt(0.75)) > 6 {
		t.Fatalf("drag to 75%% landed on line %d, want ~%d", prev, targetAt(0.75))
	}

	// The bottom of the rail reaches the end of the document (2px above the
	// exact boundary: the last pixel row belongs to the status bar).
	if end := clickFrac(0.98); end < maxScroll-4 {
		t.Fatalf("click at the rail bottom landed on line %d, want ~%d", end, maxScroll)
	}
}

// TestGutterThreeDigitNumbersAtScale renders a >100-line file at Windows
// HiDPI scales and asserts every gutter row shows exactly one number ink
// cluster (the pre-fix render wrapped "100" into "10"+"0", splitting the
// ink into two clusters and overlapping the next row's number).
func TestGutterThreeDigitNumbersAtScale(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 150; i++ {
		sb.WriteString(fmt.Sprintf("line %d := value(%d)\n", i+1, i))
	}
	for _, scale := range []float32{1.0, 1.25, 1.5} {
		scale := scale
		t.Run(fmt.Sprintf("scale%.2f", scale), func(t *testing.T) {
			hp, handle, cx := openEditorWindowScale(t, sb.String(), scale)
			editorUpdate(handle, cx, func(s *workspace.Workspace) { s.SetScrolls(90) })
			handle.Window.Draw()
			shot := saveShot(t, hp, t.TempDir(), fmt.Sprintf("gutter_s%.2f.png", scale))

			img := hp.LastWindow().Image()
			s := float32(1)
			if scale > 0 {
				s = scale
			}
			ws := handle.View.Read(cx)
			lineH := ws.LineHeight * s
			// Gutter geometry: pane starts below menu+tab bars; the gutter is
			// the fixed-width column at the pane's left edge (tree hidden).
			top := (ui.MenuBarHeight + ui.TabBarHeight) * s
			gutterW := editor.GutterWidth(150, ws.CharAdvance, false) * s
			for row := 1; row <= 10; row++ { // scroll=90: rows are lines 91..100
				bandTop := int(top + float32(row)*lineH + lineH*0.15)
				bandBot := int(top + float32(row)*lineH + lineH*0.85)
				// Ink column clusters within the gutter band. A healthy row
				// holds one adjacent cluster (the right-aligned number); a
				// wrapped row adds a separated cluster at the row's left.
				runs := 0
				inInk, gap := false, 99
				gapLimit := int(5.5 * s) // inter-digit ink gap of the mono face
				for x := 2; x < int(gutterW)-2; x++ {
					ink := false
					for y := bandTop; y < bandBot; y++ {
						i := (y*img.Width + x) * 4
						r, g, b := img.Pixels[i], img.Pixels[i+1], img.Pixels[i+2]
						if r > 110 && g > 110 && b > 110 {
							ink = true
							break
						}
					}
					if ink && !inInk && gap > gapLimit {
						runs++
					}
					if ink {
						inInk, gap = true, 0
					} else {
						gap++
						if gap > gapLimit {
							inInk = false
						}
					}
				}
				if runs == 0 {
					t.Fatalf("scale %.2f: gutter row %d has no number ink (shot %s)", scale, row, shot)
				}
				if runs > 1 {
					t.Fatalf("scale %.2f: gutter row %d number ink split into %d clusters (wrapped digits; shot %s)", scale, row, runs, shot)
				}
			}
		})
	}
}

// TestSvgIconsAtHiDPIScale opens a folder at 125% and asserts every tree
// row paints its icon ink at the row's inline icon box (the pre-fix paint
// put the curves in logical pixels against device-space masks: icons
// vanished or stacked at the window origin).
func TestSvgIconsAtHiDPIScale(t *testing.T) {
	const scale = 1.25
	hp, handle, cx := openEditorWindowScale(t, "package main\n\nfunc main() {}\n", scale)
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package p\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	editorUpdate(handle, cx, func(s *workspace.Workspace) {
		if err := s.OpenFolder(dir); err != nil {
			t.Fatal(err)
		}
	})
	handle.Window.Draw()
	shot := saveShot(t, hp, t.TempDir(), "icons_s1.25.png")

	img := hp.LastWindow().Image()
	s := float32(scale)
	treePanelTop := float32(ui.MenuBarHeight) * s
	treeW := ui.TreeWidth * s
	rowH := float32(24) * s
	// Tree rows start below the panel header (30 logical px + 4 padding).
	treeTop := treePanelTop + 31*s

	// The panel header carries the root folder's mauve icon; the two child
	// rows are .go files painting their Go wordmark glyph in the fixed Go
	// accent color (ui.FileAccentColor resolves what FileTypeIcon paints).
	rowsWithIcon := 0
	const wantRows = 3
	for row := -1; row < wantRows-1; row++ {
		theme := handle.View.Read(cx).Theme()
		want := theme.Sky.ToRgb()
		if row >= 0 { // file rows: fixed file-type color, not the theme
			want = ui.FileAccentColor("a.go", theme).ToRgb()
		}
		var rowY float32
		if row < 0 { // header
			rowY = treePanelTop + 15*s
		} else {
			rowY = treeTop + float32(row)*rowH + rowH/2
		}
		bandTop := int(rowY - rowH*0.3)
		bandBot := int(rowY + rowH*0.3)
		found := false
		for y := bandTop; y < bandBot && !found; y++ {
			for x := int(2 * s); x < int(treeW-2); x++ {
				i := (y*img.Width + x) * 4
				r, g, b := img.Pixels[i], img.Pixels[i+1], img.Pixels[i+2]
				if absf(float32(r)-want.R*255) < 28 && absf(float32(g)-want.G*255) < 28 && absf(float32(b)-want.B*255) < 28 {
					found = true
					break
				}
			}
		}
		if found {
			rowsWithIcon++
		}
	}
	if rowsWithIcon < wantRows {
		t.Fatalf("scale %.2f: only %d of %d tree rows show icon ink (icons misplaced/clipped; shot %s)",
			scale, rowsWithIcon, wantRows, shot)
	}

	// Tab icons: the main.go tab's Go wordmark glyph must sit inside the
	// tab's left half (x < 140 device px), not at the window origin. The
	// glyph paints in the fixed Go accent color (see ui.FileAccentColor).
	goIcon := ui.FileAccentColor("main.go", handle.View.Read(cx).Theme()).ToRgb()
	tabTop := ui.MenuBarHeight * s
	tabBot := tabTop + ui.TabBarHeight*s
	iconInk := 0
	for y := int(tabTop) + 2; y < int(tabBot)-2; y++ {
		for x := int(treeW) + 4; x < int(treeW)+140 && x < img.Width; x++ {
			i := (y*img.Width + x) * 4
			r, g, b := img.Pixels[i], img.Pixels[i+1], img.Pixels[i+2]
			if absf(float32(r)-goIcon.R*255) < 28 && absf(float32(g)-goIcon.G*255) < 28 && absf(float32(b)-goIcon.B*255) < 28 {
				iconInk++
			}
		}
	}
	if iconInk < 20 {
		t.Fatalf("scale %.2f: tab icon ink missing near the tab label (%d px; shot %s)", scale, iconInk, shot)
	}
}

func absi(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

var (
	_ = gc.TransparentBlack
	_ = geometry.PointPixels(0, 0)
)
