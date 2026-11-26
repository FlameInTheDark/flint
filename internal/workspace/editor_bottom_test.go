package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
	"github.com/FlameInTheDark/go-gpui/render"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
)

// inkInBandX counts bright pixels inside a horizontal band restricted to
// x in [x0, x1) — the code column only, excluding the minimap rail and
// the gutter.
func inkInBandX(img *render.RenderImage, x0, x1, y0, y1 int) int {
	ink := 0
	for y := y0; y < y1 && y < img.Height; y++ {
		for x := x0; x < x1 && x < img.Width; x++ {
			i := (y*img.Width + x) * 4
			r, g, b, a := img.Pixels[i], img.Pixels[i+1], img.Pixels[i+2], img.Pixels[i+3]
			if a > 200 && r+g+b > 240 {
				ink++
			}
		}
	}
	return ink
}

// bottomScene builds a tall buffer of bright, uniform lines so the
// bottom-edge bands have plenty of glyph ink to count.
func bottomScene(t *testing.T) (*headless.Platform, *Workspace, *gpui.WindowHandle[Workspace], *app.App) {
	t.Helper()
	// Join without a trailing newline: the document's LAST line must hold
	// text, so the bottom-edge bands have glyph ink to count.
	line := strings.Repeat("ab", 40)
	lines := make([]string, 400)
	for i := range lines {
		lines[i] = line
	}
	buf := editor.NewBuffer("", "bottom.go", strings.Join(lines, "\n"))
	return openScene(t, buf)
}

// TestBottomSlackRowPaintsAtRest pins the reported bug: at an integer
// scroll rest the pane used to paint exactly VisibleLines(viewportH)
// whole rows, and the viewport is rarely a whole number of lines tall —
// the top of the next line kept a blank strip under the last full row
// ("the bottom line disappears too early, while still staying on the
// screen"). The row budget must cover the partially visible bottom row.
func TestBottomSlackRowPaintsAtRest(t *testing.T) {
	hp, ws, handle, _ := bottomScene(t)

	lineH := ws.LineHeight
	paneH := ws.paneHeight()
	visible := editor.Metrics{LineHeight: lineH}.VisibleLines(paneH)
	slack := paneH - float32(visible)*lineH
	if slack < 8 {
		t.Skipf("headless metrics make the slack band too thin (%.1fpx)", slack)
	}

	// An integer rest with room below: rows 20..20+visible-1 fill the pane,
	// and the TOP of row 20+visible must paint into the slack band.
	ws.setScroll(20)
	handle.Window.Draw()
	img := hp.LastWindow().Image()
	y0 := int(ui.MenuBarHeight + ui.TabBarHeight + float32(visible)*lineH)
	y1 := int(ui.MenuBarHeight + ui.TabBarHeight + paneH)
	if ink := inkInBandX(img, 100, 600, y0, y1); ink < 50 {
		t.Fatalf("the slack band under the last full row is blank (%d px of ink) — the partially visible bottom row was culled", ink)
	}
}

// TestWheelRestsAtTheBottomEdge pins the overscroll: wheel scrolling to
// the end used to clamp at the last WHOLE-row page, leaving the pane's
// bottom slack permanently blank even though the last line could reach
// the bottom edge. The wheel ease must rest on the fractional ceiling
// (whole-row max + slack), and the document's last line must paint there.
func TestWheelRestsAtTheBottomEdge(t *testing.T) {
	hp, ws, handle, _ := bottomScene(t)

	lineH := ws.LineHeight
	paneH := ws.paneHeight()

	ws.addScrollDelta(1e6)
	// Integrate the ease to rest: each step advances at most the 250ms
	// stall cap, so pump fixed-size steps until it settles.
	for i := 1; i < 400 && ws.scrollAnimating(); i++ {
		ws.advanceScrollAnimAt(time.Now().Add(time.Duration(i) * 250 * time.Millisecond))
	}
	if ws.scrollAnimating() {
		t.Fatalf("the ease did not settle (rendered %v, target %v)", ws.scrollF(), ws.scrolls[ws.Active])
	}
	whole := float32(ws.maxScrollRows())
	if ws.scrollF() > whole || ws.scrollF() < whole-1 {
		t.Fatalf("the wheel rest %v outside the pixel-exact end range (%v,%v]", ws.scrollF(), whole-1, whole)
	}

	// The last line's glyphs must be visible at the pane's bottom edge.
	handle.Window.Draw()
	img := hp.LastWindow().Image()
	if ws.Buf().LineCount() <= (editor.Metrics{LineHeight: lineH}).VisibleLines(paneH) {
		t.Fatal("the fixture is not taller than the viewport")
	}
	y1 := int(ui.MenuBarHeight + ui.TabBarHeight + paneH)
	y0 := y1 - int(lineH)
	if ink := inkInBandX(img, 100, 600, y0, y1); ink < 50 {
		t.Fatalf("the bottom edge band is blank (%d px) — the last line does not reach the pane's bottom", ink)
	}
}
