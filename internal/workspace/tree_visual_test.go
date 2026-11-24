package workspace_test

import (
	"testing"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"
)

// TestVisualTreeScrollScene renders the big-tree project (40 root files +
// three expanded folders with 15 files each) to /tmp/render for visual
// inspection: the tree must clip inside its panel with a visible scrollbar
// and the rest of the app (menu bar, tabs, editor, status bar) must stay
// exactly where it belongs.
func TestVisualTreeScrollScene(t *testing.T) {
	dir := bigTreeProject(t)
	hp, handle, cx := openTreeWindow(t, dir, true)
	_ = hp
	_ = cx
	editorShot(t, hp, "/tmp/render", "tree_big.png")

	// Wheel the tree down a bit and render again: the scrollbar thumb must
	// move down and rows scroll, while the app frame stays put.
	hw := hp.LastWindow()
	for i := 0; i < 4; i++ {
		hw.DispatchInput(platform.ScrollEvent{
			Position: geometry.PointPixels(120, 400),
			Delta:    platform.ScrollDeltaPixels(0, -60),
		})
	}
	handle.Window.Draw()
	editorShot(t, hp, "/tmp/render", "tree_big_scrolled.png")
}
