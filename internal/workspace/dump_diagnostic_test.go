package workspace_test

import (
	"os"
	"testing"

	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestDumpIconsScene renders the editor window and dumps PNGs to /tmp for
// visual inspection of the tree icons, menus and minimap (temporary
// diagnostic; kept excluded from CI by the GO_TEST_ICON_DUMP env guard).
func TestDumpIconsScene(t *testing.T) {
	if os.Getenv("GO_TEST_ICON_DUMP") == "" {
		t.Skip("diagnostic dump; set GO_TEST_ICON_DUMP=1")
	}
	hp, handle, cx := openEditorWindow(t)
	dir := "/tmp/icondump"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Open a real project so the tree shows folders + files.
	editorUpdate(handle, cx, func(s *workspace.Workspace) {
		_ = s.OpenFolder("../..")
	})
	editorShot(t, hp, dir, "editor-full.png")

	// Open a View-menu-like popup with the same items as the user's
	// screenshot (long label + "Ctrl+`" shortcut + checks).
	editorUpdate(handle, cx, func(s *workspace.Workspace) {
		s.Menu.Open = 2
		s.Menu.X = 92
		s.Popup = &ui.PopupState{
			Items: []ui.MenuItem{
				{Label: "Toggle Project Panel", Shortcut: "Ctrl+B", HasCheck: true},
				{Label: "Toggle Terminal", Shortcut: "Ctrl+`", HasCheck: true},
				{Label: "Toggle Line Numbers", HasCheck: true, Checked: true},
				{Label: "Toggle FPS Overlay", HasCheck: true},
				{Label: "Open File with a very long label that does not fit", Shortcut: "Ctrl+Shift+O"},
			},
			X: 92, Y: ui.MenuBarHeight, Hover: -1,
		}
	})
	editorShot(t, hp, dir, "editor-viewmenu.png")
}
