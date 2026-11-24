package workspace_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// longTreeProject builds a temp project whose rows are wider than the
// default panel: long file names deep in nested folders.
func longTreeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	deep := filepath.Join(dir, "a-rather-long-directory-name", "another-verbose-folder-inside-it")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(deep, "an-extremely-long-file-name-that-certainly-overflows-the-default-panel-width.go")
	if err := os.WriteFile(name, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%02d.go", i)), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func openTreeResizeScene(t *testing.T, dir string) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl, platform.WindowOptions{Title: "tree-resize"},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws := workspace.New("tree-resize")
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

// treeRowHitboxes returns the tree panel's row-sized hitboxes (x < 400,
// height ~24) sorted by y.
func treeRowHitboxes(handle *gpui.WindowHandle[workspace.Workspace]) []hitboxRect {
	var out []hitboxRect
	for _, hb := range handle.Window.RenderedHitboxes() {
		r := rectOf(hb)
		if r.h >= 20 && r.h <= 28 && r.x < 400 && r.w > 60 {
			out = append(out, r)
		}
	}
	return out
}

// TestTreeResizeAndHorizontalScroll covers the left dock's new interactions:
// dragging the inner edge resizes the panel, shift+wheel pans wide rows
// horizontally, and the horizontal scrollbar drag does the same.
func TestTreeResizeAndHorizontalScroll(t *testing.T) {
	dir := longTreeProject(t)
	hp, handle, cx := openTreeResizeScene(t, dir)
	hw := hp.LastWindow()
	editorShot(t, hp, "/tmp/render", "tree_resize_default.png")

	// The deep folder starts collapsed; expand it so a WIDE row exists.
	// Row band starts below the header (30 + 4 pad), rows 24 tall.
	treeClickY := float32(ui.MenuBarHeight + ui.TreeHeaderH + 4 + 12)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(30, treeClickY)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(30, treeClickY)})
	handle.Window.Draw()
	ws := handle.View.Read(cx)
	expanded := false
	var walk func(n *workspace.TreeNode)
	walk = func(n *workspace.TreeNode) {
		if n.Name == "a-rather-long-directory-name" && n.Expanded {
			expanded = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ws.Tree)
	if !expanded {
		t.Fatal("clicking the folder row did not expand it")
	}
	editorShot(t, hp, "/tmp/render", "tree_resize_expanded.png")

	// (a) Drag the resize grip right by 80px: the panel grows.
	for _, hb := range handle.Window.RenderedHitboxes() {
		r := rectOf(hb)
		if r.x > 200 && r.x < 260 && r.h > 300 {
			t.Logf("hitbox x=%.1f w=%.1f h=%.1f y=%.1f", r.x, r.w, r.h, r.y)
		}
	}
	_, treeW0 := handle.View.Read(cx).DebugPanelSizes()
	gripX := float32(treeW0 - ui.TreeResizeGrip/2)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(gripX, 300)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(gripX+80, 300), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(gripX+80, 300)})
	handle.Window.Draw()
	_, treeW1 := handle.View.Read(cx).DebugPanelSizes()
	if treeW1 < treeW0+70 || treeW1 > treeW0+90 {
		t.Fatalf("grip drag right did not resize the panel: %.0f -> %.0f", treeW0, treeW1)
	}
	editorShot(t, hp, "/tmp/render", "tree_resize_wide.png")

	// (b) Drag it far left: clamped at the panel minimum.
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(treeW1-ui.TreeResizeGrip/2, 300)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(60, 300), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(60, 300)})
	handle.Window.Draw()
	_, treeW2 := handle.View.Read(cx).DebugPanelSizes()
	if treeW2 != ui.TreeMinWidth {
		t.Fatalf("grip drag left did not clamp at the minimum: %.0f want %.0f", treeW2, ui.TreeMinWidth)
	}

	// The panel is now 160 wide: the deep rows overflow horizontally.
	// (c) Shift+wheel over the tree pans the rows left.
	rows0 := treeRowHitboxes(handle)
	if len(rows0) == 0 {
		t.Fatal("no tree rows found")
	}
	minX0 := rows0[0].x
	for _, r := range rows0 {
		if r.x < minX0 {
			minX0 = r.x
		}
	}
	for i := 0; i < 3; i++ {
		hw.DispatchInput(platform.ScrollEvent{
			Position:  geometry.PointPixels(80, 300),
			Delta:     platform.ScrollDeltaPixels(0, -60),
			Modifiers: keymap.Modifiers{Shift: true},
		})
	}
	handle.Window.Draw()
	rows1 := treeRowHitboxes(handle)
	minX1 := rows1[0].x
	for _, r := range rows1 {
		if r.x < minX1 {
			minX1 = r.x
		}
	}
	if minX0-minX1 < 100 {
		t.Fatalf("shift+wheel did not pan the rows horizontally: minX %.1f -> %.1f", minX0, minX1)
	}
	editorShot(t, hp, "/tmp/render", "tree_resize_hscrolled.png")

	// (d) Dragging the horizontal scrollbar back pans right again.
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(20, 650-ui.StatusHeight-ui.TreeScrollbarInset-5)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(2, 650-ui.StatusHeight-ui.TreeScrollbarInset-5), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(2, 650-ui.StatusHeight-ui.TreeScrollbarInset-5)})
	handle.Window.Draw()
	rows2 := treeRowHitboxes(handle)
	minX2 := rows2[0].x
	for _, r := range rows2 {
		if r.x < minX2 {
			minX2 = r.x
		}
	}
	if minX2 <= minX1 {
		t.Fatalf("h-scrollbar drag did not pan back: minX %.1f still <= %.1f", minX2, minX1)
	}

	_ = cx
}
