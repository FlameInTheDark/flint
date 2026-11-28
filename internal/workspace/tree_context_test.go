package workspace

import (
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/ui"
)

// openContextTreeWindow opens a real workspace window with the project
// tree rendered: the empty-space context menu is wired through the render
// pass's update plumbing, so calling RenderTree bare would not exercise
// the popup path.
func openContextTreeWindow(t *testing.T, dir string) (*headless.Platform, *gpui.WindowHandle[Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](appl, platform.WindowOptions{Title: "tree-context"},
		func(w *gpui.Window, cx *app.Context[Workspace]) Workspace {
			ws := New("tree-context")
			ws.Measure(w)
			if err := ws.OpenFolder(dir); err != nil {
				t.Fatal(err)
			}
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
	return hp, &handle, appl.App
}

// treeRowY computes a row's vertical center: the tree list starts under
// the menu/tab bars and the panel header.
func treeRowY(index int) float32 {
	return ui.MenuBarHeight + ui.TabBarHeight + ui.TreeHeaderH + float32(index)*treeRowHeight + treeRowHeight/2
}

// dndProject lists dirs first (alphabetical), then files: with the fixture
// the rows are .github(0), .golangci.yml(1), pkg(2), main.go(3), note.txt(4).
func treeFileRowIndex(t *testing.T, dir string, name string) int {
	t.Helper()
	root := NewTree(dir)
	idx := 0
	for _, c := range root.Children {
		if c.Name == name {
			return idx
		}
		idx++
	}
	t.Fatalf("%s not a root child", name)
	return 0
}

// TestTreeEmptySpaceRightClickOpensRootMenu pins the empty-background
// behavior: right-clicking below the rows opens the project ROOT's
// context menu (create file / folder in the workspace root), and the
// root-scoped actions (rename/delete) are disabled there.
func TestTreeEmptySpaceRightClickOpensRootMenu(t *testing.T) {
	dir := dndProject(t)
	hp, handle, cx := openContextTreeWindow(t, dir)
	hw := hp.LastWindow()

	// 5 visible rows; click well below the last one, inside the panel.
	y := treeRowY(5) + 60
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonRight, Position: geometry.PointPixels(120, y)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonRight, Position: geometry.PointPixels(120, y)})
	handle.Window.Draw()

	ws := handle.View.Read(cx)
	if ws.Popup == nil {
		t.Fatal("right-click on the tree background opened no context menu")
	}
	if len(ws.Popup.Items) < 6 {
		t.Fatalf("root context menu too short: %d items", len(ws.Popup.Items))
	}
	if ws.Popup.Items[0].Label != "New File…" || ws.Popup.Items[1].Label != "New Folder…" {
		t.Fatalf("root menu lacks create actions: %q, %q", ws.Popup.Items[0].Label, ws.Popup.Items[1].Label)
	}
	// The root node has no parent: rename/delete are correctly disabled.
	if !ws.Popup.Items[3].Disabled || !ws.Popup.Items[4].Disabled {
		t.Fatal("root menu enabled rename/delete on the project root")
	}
}

// TestTreeRowRightClickStaysNodeScoped guards the counterpart: a right
// click ON a row still opens the NODE's menu (not the root one) — the
// background handler must not double-fire over rows.
func TestTreeRowRightClickStaysNodeScoped(t *testing.T) {
	dir := dndProject(t)
	hp, handle, cx := openContextTreeWindow(t, dir)
	hw := hp.LastWindow()

	y := treeRowY(treeFileRowIndex(t, dir, "main.go"))
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonRight, Position: geometry.PointPixels(120, y)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonRight, Position: geometry.PointPixels(120, y)})
	handle.Window.Draw()

	ws := handle.View.Read(cx)
	if ws.Popup == nil {
		t.Fatal("right-click on a row opened no context menu")
	}
	// A file node has a parent: rename/delete are enabled on its menu.
	if ws.Popup.Items[3].Disabled || ws.Popup.Items[4].Disabled {
		t.Fatal("row menu disabled rename/delete (the root menu leaked over a row)")
	}
}
