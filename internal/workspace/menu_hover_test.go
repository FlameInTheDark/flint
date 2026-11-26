package workspace

import (
	"testing"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"
)

// menuIndexByTitle maps the top-level menu titles to their index.
func menuIndexByTitle(t *testing.T, title string) int {
	t.Helper()
	for i, m := range MenuDefs(nil, nil, nil, nil) {
		if m.Title == title {
			return i
		}
	}
	t.Fatalf("menu %q not found", title)
	return -1
}

// TestMenuBarHoverSwitchesWhileOpen pins the IDE menu-bar behavior: with a
// dropdown open, moving the pointer onto another top-level title opens that
// menu as if it had been clicked, and clicking the open menu's title closes
// it. The popup's dismiss layer occludes the real bar, so this runs on the
// hover strip layered above the dismiss layer.
func TestMenuBarHoverSwitchesWhileOpen(t *testing.T) {
	hp, handle, a, _ := openDebugTabWindow(t)
	hw := hp.LastWindow()
	read := func() *Workspace { return handle.View.Read(a.App) }

	hitbox := func(key string) geometry.Bounds[geometry.Pixels] {
		t.Helper()
		handle.Window.Draw()
		for _, hb := range handle.Window.RenderedHitboxes() {
			if hb.Key == key {
				return hb.Bounds
			}
		}
		t.Fatalf("hitbox %q not found", key)
		return geometry.Bounds[geometry.Pixels]{}
	}
	center := func(hb geometry.Bounds[geometry.Pixels]) geometry.Point[geometry.Pixels] {
		return geometry.PointPixels(
			hb.Origin.X.AsF32()+hb.Size.Width.AsF32()/2,
			hb.Origin.Y.AsF32()+hb.Size.Height.AsF32()/2)
	}
	clickAt := func(p geometry.Point[geometry.Pixels]) {
		hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
		hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
	}

	fileIdx := menuIndexByTitle(t, "File")
	editIdx := menuIndexByTitle(t, "Edit")

	// Open the File menu with a real click on its title.
	clickAt(center(hitbox("n:btn-menubar-File")))
	if got := read().Menu.Open; got != fileIdx {
		t.Fatalf("clicking File did not open it: Open=%d want %d", got, fileIdx)
	}
	if read().Popup == nil {
		t.Fatal("File menu opened without a popup")
	}
	handle.Window.Draw() // the popup layer and the hover strip mount

	// While File is open, hovering the Edit title must switch to it —
	// the strip item sits above the dismiss layer.
	hw.DispatchInput(platform.MouseMoveEvent{Position: center(hitbox("n:btn-menubar-strip-Edit"))})
	if got := read().Menu.Open; got != editIdx {
		t.Fatalf("hovering Edit while File is open did not switch menus: Open=%d want %d", got, editIdx)
	}
	if read().Popup == nil {
		t.Fatal("hover-switch left no popup open")
	}
	handle.Window.Draw()

	// Hovering back to File switches again (the strip items rebuild with the
	// new open index, the strip-File hitbox is live).
	hw.DispatchInput(platform.MouseMoveEvent{Position: center(hitbox("n:btn-menubar-strip-File"))})
	if got := read().Menu.Open; got != fileIdx {
		t.Fatalf("hovering back to File did not switch: Open=%d want %d", got, fileIdx)
	}
	handle.Window.Draw()

	// Clicking the open menu's own title closes it (a press on the strip
	// item must not leak to the dismiss layer first, which would re-open).
	clickAt(center(hitbox("n:btn-menubar-strip-File")))
	if got := read().Menu.Open; got != -1 {
		t.Fatalf("clicking the open menu title did not close it: Open=%d", got)
	}
	if read().Popup != nil {
		t.Fatal("closing the menu left the popup mounted")
	}
}
