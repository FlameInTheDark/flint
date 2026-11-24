package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
	"github.com/FlameInTheDark/go-gpui/render"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// These scenes pin the two surfaces the visual reports complained about:
// the hex dump of a real binary (it rendered literally nothing — the row
// text carried zero-length runs) and the fractional wheel-ease positions
// (rows must slide between lines, not jump whole ones).

// scenePNG renders the workspace one frame and saves it.
func scenePNG(t *testing.T, hp *headless.Platform, dir, name string) {
	t.Helper()
	png, err := hp.LastWindow().PNG()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), png, 0o644); err != nil {
		t.Fatal(err)
	}
}

// inkInBand counts bright pixels inside a horizontal band of the image.
func inkInBand(img *render.RenderImage, y0, y1 int) int {
	ink := 0
	for y := y0; y < y1 && y < img.Height; y++ {
		for x := 0; x < img.Width; x++ {
			i := (y*img.Width + x) * 4
			r, g, b, a := img.Pixels[i], img.Pixels[i+1], img.Pixels[i+2], img.Pixels[i+3]
			if a > 200 && r+g+b > 240 {
				ink++
			}
		}
	}
	return ink
}

// openScene opens the workspace headless with one buffer.
func openScene(t *testing.T, buf *editor.Buffer) (*headless.Platform, *Workspace, *gpui.WindowHandle[Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](appl, platform.WindowOptions{Title: "scene"},
		func(w *gpui.Window, cx *app.Context[Workspace]) Workspace {
			ws := New("scene")
			ws.Measure(w)
			ws.Buffers = []*editor.Buffer{buf}
			ws.Active = 0
			ws.ensureScrollArrays()
			return *ws
		})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		v := handle.View.Read(cx)
		return v.Render(&handle, cx, w)
	})
	handle.Window.Draw()
	return hp, handle.View.Read(appl.App), &handle, appl.App
}

// hexFixture returns deterministic bytes for the hex scene tests.
func hexFixture() []byte {
	data := make([]byte, 64*1024)
	for i := range data {
		// A byte mix exercising every hex-view color class: zeros, control
		// bytes, printable ASCII and high bytes, in a repeating pattern.
		switch i % 16 {
		case 0:
			data[i] = 0x00
		case 1, 2:
			data[i] = byte(0x20 + i%0x5f)
		case 3:
			data[i] = 0x7f
		case 4, 5:
			data[i] = byte(0x80 + i%0x60)
		default:
			data[i] = byte(i % 256)
		}
	}
	return data
}

// TestHexViewRendersBinary: a binary file's hex dump shows the offset
// gutter, the byte pairs and the ASCII column (the report: the pane
// rendered only its header — nothing else).
func TestHexViewRendersBinary(t *testing.T) {
	buf := Buffer2Buffer(hexFixture(), "gallery")
	hp, _, _, _ := openScene(t, buf)
	scenePNG(t, hp, os.TempDir(), "hexview_scene.png")
	img := hp.LastWindow().Image()
	// The rows band (below the 24px pane header, above the status bar).
	ink := inkInBand(img, 90, 600)
	if ink < 400 {
		t.Fatalf("hex rows band has too little ink (%d px) — the dump did not render", ink)
	}
	// The first offset row must be visible: check the header note too.
	if inkInBand(img, 62, 90) < 20 {
		t.Fatal("hex pane header note missing")
	}
}

// Buffer2Buffer builds a hex-kind buffer from raw bytes (test helper).
func Buffer2Buffer(data []byte, name string) *editor.Buffer {
	b := editor.NewBuffer("", name, "")
	b.Kind = editor.KindHex
	b.Data = data
	return b
}

// TestFractionalScrollSlidesRows: mid-ease the code rows render shifted
// between whole lines (the smooth wheel), and the pane still paints rows.
func TestFractionalScrollSlidesRows(t *testing.T) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = strings.Repeat("ab", 20) + " line " + strings.Repeat("x", 1)
	}
	buf := editor.NewBuffer("", "lines.go", strings.Join(lines, "\n"))
	hp, ws, handle, cx := openScene(t, buf)
	_ = cx

	// Static frame at an integer position (baseline).
	ws.setScroll(5)
	handle.Window.Draw()
	img0 := hp.LastWindow().Image()
	baseInk := inkInBand(img0, 70, 600)
	if baseInk < 1500 {
		t.Fatalf("integer-scroll baseline too dim (%d px)", baseInk)
	}

	// Mid-ease fractional position: rendered scroll 5.5 (target 5.5, no
	// run — a static rest position; the render path is the same one the
	// ease drives).
	ws.scrolls[ws.Active] = 5.5
	ws.scrollAnims[ws.Active] = 5.5
	ws.scrollAnimRuns[ws.Active] = false
	handle.Window.Draw()
	img1 := hp.LastWindow().Image()
	if inkInBand(img1, 70, 600) < baseInk/2 {
		t.Fatalf("fractional scroll lost the rows (ink %d vs baseline %d)", inkInBand(img1, 70, 600), baseInk)
	}
	scenePNG(t, hp, os.TempDir(), "frac_scroll.png")

	// The rendered scroll survives: scrollF reports it (the ease is idle).
	if got := ws.scrollF(); got != 5.5 {
		t.Fatalf("scrollF reported %v, want 5.5", got)
	}
	// scroll() keeps the rounded whole-row approximation.
	if got := ws.scroll(); got != 6 {
		t.Fatalf("scroll reported %d, want 6", got)
	}
}

// TestFractionalScrollHexSlides: the hex pane slides fractionally too.
func TestFractionalScrollHexSlides(t *testing.T) {
	buf := Buffer2Buffer(hexFixture(), "gallery")
	hp, ws, handle, _ := openScene(t, buf)
	ws.ensureScrollArrays()
	ws.scrolls[ws.Active] = 10.5
	ws.scrollAnims[ws.Active] = 10.5
	ws.scrollAnimRuns[ws.Active] = false
	handle.Window.Draw()
	img := hp.LastWindow().Image()
	if ink := inkInBand(img, 90, 600); ink < 400 {
		t.Fatalf("fractional hex scroll lost the rows (ink %d)", ink)
	}
	scenePNG(t, hp, os.TempDir(), "frac_hex.png")
}
