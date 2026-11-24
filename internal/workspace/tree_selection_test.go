package workspace

import (
	"os"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/ui"
)

type treeView struct{}

// TestTreeSelectionUnderText verifies the active-file highlight paints
// BELOW the row label (the label must stay readable over the accent tint).
func TestTreeSelectionUnderText(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir+"/sub", 0o755)
	os.WriteFile(dir+"/main.go", []byte("package main\n"), 0o644)
	os.WriteFile(dir+"/sub/nested.go", []byte("package sub\n"), 0o644)

	root := NewTree(dir)
	root.Expanded = true
	root.EnsureLoaded()
	for _, c := range root.Children {
		if c.Name == "sub" {
			c.Expanded = true
			c.EnsureLoaded()
		}
	}

	hp := headless.New(geometry.SizePixels(400, 200))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[treeView](appl, platform.WindowOptions{Title: "tree"}, func(w *gpui.Window, cx *app.Context[treeView]) treeView {
		return treeView{}
	})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		return RenderTree(root, ui.DefaultTheme(), dir+"/sub/nested.go", ui.TreeWidth, TreeCallbacks{
			Toggle:   func(n *TreeNode) {},
			OpenFile: func(p string) {},
		})
	})
	handle.Window.Draw()

	hw := hp.LastWindow()
	if hw == nil {
		t.Fatal("no headless window")
	}
	label, _, ok := handle.Window.RecordedAccessibility().FindByLabel("nested.go")
	if !ok {
		t.Fatal("active file label missing")
	}
	scene := handle.Window.RenderedScene()
	var selectionOrder uint32
	selected := false
	for _, q := range scene.Quads {
		c, ok := q.Background.AsSolid()
		y := q.Bounds.Origin.Y.AsF32()
		if ok && c == ui.DefaultTheme().Accent.Alpha(.18) && y <= label.Bounds.Origin.Y.AsF32() && y+q.Bounds.Size.Height.AsF32() >= label.Bounds.Origin.Y.AsF32() {
			selectionOrder = q.Order
			selected = true
			break
		}
	}
	if !selected {
		t.Fatal("active tree selection is missing")
	}
	for _, glyph := range scene.MonoSprites {
		x, y := glyph.Bounds.Origin.X.AsF32(), glyph.Bounds.Origin.Y.AsF32()
		if glyph.Order > selectionOrder && x >= label.Bounds.Origin.X.AsF32() && x < label.Bounds.Origin.X.AsF32()+label.Bounds.Size.Width.AsF32() && y >= label.Bounds.Origin.Y.AsF32() && y < label.Bounds.Origin.Y.AsF32()+label.Bounds.Size.Height.AsF32() {
			return
		}
	}
	t.Fatal("active label was not painted above its selection")
}
