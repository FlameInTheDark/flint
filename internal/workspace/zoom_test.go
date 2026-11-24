package workspace_test

import (
	"testing"
)

// TestZoomScalesWholeUI drives View → Zoom In twice and verifies the zoom
// actually rescales the UI: the layout viewport shrinks in logical points
// (the editor pane narrows by the zoom factor while the tree keeps its
// logical width) while the rendered frame still covers the full window.
func TestZoomScalesWholeUI(t *testing.T) {
	hp, handle, _ := openEditorWindow(t)
	hw := hp.LastWindow()

	paneWidth := func() float32 {
		for _, hb := range handle.Window.RenderedHitboxes() {
			r := rectOf(hb)
			// The editor pane: wide, tall, below the chrome (the dismiss
			// overlay spans the whole window and is excluded by y).
			if r.w > 700 && r.h > 380 && r.y > 30 {
				return r.w
			}
		}
		return -1
	}

	w1 := paneWidth()
	if w1 < 900 || w1 > 1010 {
		t.Fatalf("editor pane width %.1f at zoom 1, want ~1000", w1)
	}

	// Zoom in twice: 1.1 * 1.1 = 1.21 — the logical viewport shrinks by
	// that factor: 1000/1.21 ≈ 826.
	handle.Window.ZoomIn()
	handle.Window.ZoomIn()
	handle.Window.Draw()
	w2 := paneWidth()
	if w2 < 780 || w2 > 880 {
		t.Fatalf("editor pane width %.1f at zoom 1.21, want ~826 (layout viewport shrank)", w2)
	}

	// The rendered frame still fills the window in device pixels.
	img := hw.Image()
	if img.Width != 1000 {
		t.Fatalf("rendered frame width %d, want 1000", img.Width)
	}

	handle.Window.ZoomReset()
	handle.Window.Draw()
	if w3 := paneWidth(); w3 < 900 || w3 > 1010 {
		t.Fatalf("editor pane width %.1f after zoom reset, want ~1000", w3)
	}
}
