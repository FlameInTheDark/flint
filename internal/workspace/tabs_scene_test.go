package workspace_test

import (
	"fmt"
	"testing"

	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestVisualTabsScene renders a crowded tab strip: with the last tab active
// (revealed) and with an early tab active after wheeling back, for visual
// inspection.
func TestVisualTabsScene(t *testing.T) {
	hp, handle, cx := openManyTabsWindow(t, 12, 11)
	handle.Window.Draw()
	editorShot(t, hp, "/tmp/render", "tabs_active_last.png")

	// Switch to an early tab: the strip must reveal it again.
	editorUpdate(handle, cx, func(s *workspace.Workspace) { s.Active = 2 })
	handle.Window.Draw()
	editorShot(t, hp, "/tmp/render", fmt.Sprintf("tabs_active_%02d.png", 2))
}
