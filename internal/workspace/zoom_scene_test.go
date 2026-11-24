package workspace_test

import (
	"testing"
)

// TestVisualZoomScene renders zoom 1.0 vs 1.21 for visual inspection.
func TestVisualZoomScene(t *testing.T) {
	hp, handle, _ := openEditorWindow(t)
	editorShot(t, hp, "/tmp/render", "zoom_100.png")
	handle.Window.ZoomIn()
	handle.Window.ZoomIn()
	handle.Window.Draw()
	editorShot(t, hp, "/tmp/render", "zoom_121.png")
}
