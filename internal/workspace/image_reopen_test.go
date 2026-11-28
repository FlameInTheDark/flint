package workspace_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/workspace"
)

// imageScene opens a headless window on a workspace and returns the
// pieces the image-viewer regression tests drive (mirrors openFileScene).
func imageScene(t *testing.T, ws *workspace.Workspace) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App, func(func(*workspace.Workspace))) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl, platform.WindowOptions{Title: "image-scene"},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws.Measure(w)
			w.Focus(ws.FocusEditor)
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
	update := func(f func(*workspace.Workspace)) {
		handle.View.Update(appl.App, func(state *workspace.Workspace, c *app.Context[workspace.Workspace]) any {
			f(state)
			c.Notify()
			return nil
		})
	}
	return hp, &handle, appl.App, update
}

// writeTestPNG encodes img to dir/name and returns the path.
func writeTestPNG(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// countImagePixels counts pixels within 30 RGB distance of col.
func countImagePixels(hw *headless.Window, col color.RGBA) int {
	img := hw.Image()
	if img == nil {
		return 0
	}
	n := 0
	for y := 0; y < img.Height; y++ {
		for x := 0; x < img.Width; x++ {
			i := (y*img.Width + x) * 4
			d := int(img.Pixels[i]-col.R)*int(img.Pixels[i]-col.R) +
				int(img.Pixels[i+1]-col.G)*int(img.Pixels[i+1]-col.G) +
				int(img.Pixels[i+2]-col.B)*int(img.Pixels[i+2]-col.B)
			if d < 2700 {
				n++
			}
		}
	}
	return n
}

// TestImageOversizedPaints pins the empty-image bug: a source bigger than
// the sprite atlas page (1024px, minus the 1px padding ring, caps a tile
// at 1022px) used to park as an empty 1x1 atlas tile and the viewer
// rendered "just an empty nothing". The viewer now paints the image as a
// grid of page-sized chunks, so any size renders.
func TestImageOversizedPaints(t *testing.T) {
	dir := t.TempDir()
	const W, H = 1600, 1200 // beyond the atlas page on both axes
	col := color.RGBA{R: 0x30, G: 0x80, B: 0xC0, A: 0xFF}
	img := image.NewNRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: col.R, G: col.G, B: col.B, A: 0xFF})
		}
	}
	path := writeTestPNG(t, dir, "big.png", img)

	ws := workspace.New("image-scene")
	ws.TermSync = true
	ws.OpenFile(path)
	hp, handle, _, update := imageScene(t, ws)
	hw := hp.LastWindow()

	// Fit view: the mip path uploads one small tile; it must still paint.
	if n := countImagePixels(hw, col); n < 200000 {
		t.Fatalf("oversized image at fit painted %d matching pixels (want the pane filled, >200k)", n)
	}

	// 1:1 and beyond: the full-resolution grid path (multiple chunks,
	// culled to the viewport) must paint too.
	update(func(state *workspace.Workspace) {
		state.DebugToggleImgFit()
		state.DebugSetImgZoom(2)
	})
	handle.Window.Draw()
	saveShot(t, hp, "/tmp/render", "image_tiles_2x.png")
	if n := countImagePixels(hw, col); n < 200000 {
		t.Fatalf("oversized image at 2x zoom painted %d matching pixels (want the pane filled, >200k)", n)
	}
}

// TestImageReopenRenders drives the reported repro: open an image, close
// the tab, open it again. Every cycle must repaint the image (a fresh
// decode gets fresh atlas keys and the pane must never go blank).
func TestImageReopenRenders(t *testing.T) {
	dir := t.TempDir()
	col := color.RGBA{R: 0x30, G: 0x80, B: 0xC0, A: 0xFF}
	img := image.NewNRGBA(image.Rect(0, 0, 1400, 1050))
	for y := 0; y < 1050; y++ {
		for x := 0; x < 1400; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: col.R, G: col.G, B: col.B, A: 0xFF})
		}
	}
	path := writeTestPNG(t, dir, "reopen.png", img)

	ws := workspace.New("image-scene")
	ws.TermSync = true
	hp, handle, _, update := imageScene(t, ws)
	hw := hp.LastWindow()

	for cycle := 0; cycle < 3; cycle++ {
		update(func(s *workspace.Workspace) { s.OpenFile(path) })
		handle.Window.Draw()
		if n := countImagePixels(hw, col); n < 200000 {
			t.Fatalf("cycle %d open: image painted %d pixels (want >200k)", cycle, n)
		}
		update(func(s *workspace.Workspace) { s.CloseEditor() })
		handle.Window.Draw()
		update(func(s *workspace.Workspace) { s.OpenFile(path) })
		handle.Window.Draw()
		if n := countImagePixels(hw, col); n < 200000 {
			t.Fatalf("cycle %d reopen: image painted %d pixels (want >200k) — the pane went blank", cycle, n)
		}
	}
}
