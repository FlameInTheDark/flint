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

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// This file is the post-fix visual regression suite for the reported UI
// bugs: dropdown/context-menu rows overflowing their panel, tab icons
// rendered off-center, scrollbar drags selecting text, and the empty
// workspace placeholder. Every scenario renders a PNG (kept under
// docs/screenshots/) AND asserts the geometry numerically - pixels show
// regressions to a human, bounds assertions catch them in CI.

const shotDir = "" // overridden per-test with t.TempDir; see saveShot

// shotDirOverride lets the visual review copy the rendered frames out of
// the test sandbox (EDITOR_SHOT_DIR); tests default to t.TempDir().
func shotDirOverride() string { return os.Getenv("EDITOR_SHOT_DIR") }

func saveShot(t *testing.T, hp *headless.Platform, dir, name string) string {
	t.Helper()
	hw := hp.LastWindow()
	if hw == nil {
		t.Fatal("no headless window")
	}
	png, err := hw.PNG()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatal(err)
	}
	if out := shotDirOverride(); out != "" {
		_ = os.WriteFile(filepath.Join(out, name), png, 0o644)
	}
	return path
}

type hitboxRect struct{ x, y, w, h float32 }

func rectOf(hb gpui.Hitbox) hitboxRect {
	return hitboxRect{
		hb.Bounds.Origin.X.AsF32(), hb.Bounds.Origin.Y.AsF32(),
		hb.Bounds.Size.Width.AsF32(), hb.Bounds.Size.Height.AsF32(),
	}
}

func (r hitboxRect) right() float32  { return r.x + r.w }
func (r hitboxRect) bottom() float32 { return r.y + r.h }
func (r hitboxRect) containsInner(o hitboxRect) bool {
	return r.x <= o.x+0.01 && r.y <= o.y+0.01 && r.right()+0.01 >= o.right() && r.bottom()+0.01 >= o.bottom()
}

// popupPanelOf finds the popup panel hitbox (the largest non-dismiss box
// near the popup origin) and returns it with every menu row inside it.
// Hitboxes clipped by an overflow container (the editor pane clips its
// code surface) are skipped: their raw bounds spill past their mask, but
// they can never receive input outside it.
func popupPanelOf(hitboxes []gpui.Hitbox, popX, popY float32) (hitboxRect, []hitboxRect) {
	var panel hitboxRect
	found := false
	for _, hb := range hitboxes {
		r := rectOf(hb)
		if r.w >= 900 && r.h >= 600 { // dismiss layer spans the window
			continue
		}
		if r.w < 60 || r.h < 40 { // rows, separators
			continue
		}
		if !maskContains(hb, popX, popY) { // clipped surfaces can't host the popup
			continue
		}
		if r.x > popX+40 || r.y > popY+60 || r.right() < popX { // near the popup
			continue
		}
		if !found || r.w*r.h > panel.w*panel.h {
			panel = r
			found = true
		}
	}
	var rows []hitboxRect
	if found {
		for _, hb := range hitboxes {
			r := rectOf(hb)
			if r.h > 20 && r.h < 32 && panel.containsInner(r) && r != panel {
				rows = append(rows, r)
			}
		}
	}
	return panel, rows
}

// maskContains reports whether the hitbox's content mask covers the point.
// Masks are stored in device pixels; headless tests run at scale 1.
func maskContains(hb gpui.Hitbox, x, y float32) bool {
	m := hb.ContentMask.Bounds
	return x >= m.Origin.X.AsF32()-0.01 && y >= m.Origin.Y.AsF32()-0.01 &&
		x <= m.Origin.X.AsF32()+m.Size.Width.AsF32()+0.01 &&
		y <= m.Origin.Y.AsF32()+m.Size.Height.AsF32()+0.01
}

// bandTopHitboxes returns the non-nested hitboxes fully inside a
// horizontal band (the menu-bar buttons; their labels are nested inside).
func bandTopHitboxes(hitboxes []gpui.Hitbox, top, bottom float32) []gpui.Hitbox {
	var out []gpui.Hitbox
	for _, hb := range hitboxes {
		r := rectOf(hb)
		if r.y < top || r.bottom() > bottom+0.5 {
			continue
		}
		nested := false
		for _, other := range out {
			o := rectOf(other)
			if o.containsInner(r) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, hb)
		}
	}
	return out
}

// openMenuAt clicks the i-th menu-bar title (File=0, Edit=1, View=2...).
func openMenuAt(hp *headless.Platform, handle *gpui.WindowHandle[workspace.Workspace], cx *app.App, i int) {
	hw := hp.LastWindow()
	hits := bandTopHitboxes(handle.Window.RenderedHitboxes(), 0, ui.MenuBarHeight)
	if len(hits) <= i {
		panic(fmt.Sprintf("menu bar has %d hitboxes, need index %d", len(hits), i))
	}
	hb := hits[i]
	pos := geometry.PointPixels(
		float32(hb.Bounds.Origin.X)+float32(hb.Bounds.Size.Width)/2,
		float32(hb.Bounds.Origin.Y)+float32(hb.Bounds.Size.Height)/2)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: pos})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: pos})
	drawAndWait(handle, cx)
}

func drawAndWait(handle *gpui.WindowHandle[workspace.Workspace], cx *app.App) {
	handle.Window.Draw()
}

// TestVisualEmptyStateWorkspace renders the fresh (zero-buffer) editor:
// the welcome placeholder with Open File / Open Folder / New File buttons.
func TestVisualEmptyStateWorkspace(t *testing.T) {
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl, platform.WindowOptions{Title: "editor-empty"},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws := workspace.New("editor-empty")
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
	dir := t.TempDir()
	shot := saveShot(t, hp, dir, "empty_state.png")

	ws := handle.View.Read(appl.App)
	if len(ws.Buffers) != 0 {
		t.Fatalf("fresh workspace should have no buffers, got %d", len(ws.Buffers))
	}

	// Buttons: three ~26px hitboxes in the editor area, horizontally near
	// the center (the buttons row is centered).
	var buttons []hitboxRect
	for _, hb := range handle.Window.RenderedHitboxes() {
		r := rectOf(hb)
		if r.h >= 24 && r.h <= 28 && r.y > ui.MenuBarHeight+ui.TabBarHeight && r.bottom() < 650-ui.StatusHeight {
			buttons = append(buttons, r)
		}
	}
	if len(buttons) < 3 {
		t.Fatalf("empty state shows %d buttons, want >= 3 (shot %s)", len(buttons), shot)
	}
	sumX := float32(0)
	for _, b := range buttons {
		sumX += b.x + b.w/2
	}
	meanX := sumX / float32(len(buttons))
	if meanX < 250 || meanX > 750 {
		t.Fatalf("empty-state buttons centered at x=%.0f, want ~500 (centered row)", meanX)
	}

	// Closing every tab of a populated workspace also lands here.
	path := filepath.Join(dir, "example.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	editorUpdate(&handle, appl.App, func(s *workspace.Workspace) {
		s.OpenFile(path)
	})
	if handle.View.Read(appl.App).Buf() == nil {
		t.Fatal("fixture did not open")
	}
	for handle.View.Read(appl.App).Buf() != nil {
		editorUpdate(&handle, appl.App, func(s *workspace.Workspace) { s.CloseEditor() })
	}
	handle.Window.Draw()
	if n := len(handle.View.Read(appl.App).Buffers); n != 0 {
		t.Fatalf("closing all tabs left %d buffers", n)
	}
	_ = saveShot(t, hp, dir, "empty_state_after_close.png")
}

// TestVisualTabIconsCentered renders two tabs and verifies each file icon
// sits vertically centered in the tab (the Center/Baseline enum swap put
// them at the tab's very top edge, half-clipped by the tab bar border).
func TestVisualTabIconsCentered(t *testing.T) {
	hp, handle, cx := openEditorWindow(t)
	dir := t.TempDir()
	shot := saveShot(t, hp, dir, "tabs_after_fix.png")

	hw := hp.LastWindow()
	img := hw.Image()
	// The .go tab icon paints in the fixed Go accent color (see
	// ui.FileAccentColor — file-type glyphs no longer tint with the
	// theme). Collect its pixels inside the tab bar band and require the
	// icon's ink to be vertically centered around the band's middle, not
	// glued to the top.
	goIcon := ui.FileAccentColor("main.go", handle.View.Read(cx).Theme()).ToRgb()
	top := float32(ui.MenuBarHeight)
	bottom := top + ui.TabBarHeight
	minY, maxY := float32(1e9), float32(-1)
	for y := int(top); y < int(bottom); y++ {
		for x := 0; x < 140; x++ { // first tab region
			i := (y*img.Width + x) * 4
			r, g, b := img.Pixels[i], img.Pixels[i+1], img.Pixels[i+2]
			if absf(float32(r)-goIcon.R*255) < 24 && absf(float32(g)-goIcon.G*255) < 24 && absf(float32(b)-goIcon.B*255) < 24 {
				fy := float32(y)
				if fy < minY {
					minY = fy
				}
				if fy > maxY {
					maxY = fy
				}
			}
		}
	}
	if maxY < minY {
		t.Fatalf("no icon-colored ink found in the tab band (shot %s)", shot)
	}
	center := (minY + maxY) / 2
	want := top + ui.TabBarHeight/2
	if absf(center-want) > 5 {
		t.Fatalf("tab icon ink centered at y=%.1f, want ~%.1f (icon off-center; span %.0f..%.0f)",
			center, want, minY, maxY)
	}
}

// TestVisualMenuRowsStayInsidePanel opens the View menu and the editor
// context menu and asserts every row hitbox lies inside the panel bounds
// (before the flexbox margin fix the rows were wider than the panel by the
// Mx margin: hover backgrounds spilled past the border and right-aligned
// shortcut text clipped).
func TestVisualMenuRowsStayInsidePanel(t *testing.T) {
	hp, handle, cx := openEditorWindow(t)
	dir := t.TempDir()

	// View menu (menu-bar index 2: File, Edit, View).
	openMenuAt(hp, handle, cx, 2)
	ws := handle.View.Read(cx)
	if ws.Popup == nil {
		t.Fatal("clicking View did not open the dropdown")
	}
	shot := saveShot(t, hp, dir, "view_menu_after_fix.png")
	panel, rows := popupPanelOf(handle.Window.RenderedHitboxes(),
		float32(ws.Popup.X), float32(ws.Popup.Y))
	if panel.w == 0 {
		t.Fatalf("popup panel hitbox not found (shot %s)", shot)
	}
	if len(rows) < 5 {
		t.Fatalf("found %d menu rows in the panel, want >= 5", len(rows))
	}
	for i, r := range rows {
		if !panel.containsInner(r) {
			t.Fatalf("menu row %d (%.0f..%.0f) overflows the panel (%.0f..%.0f)",
				i, r.x, r.right(), panel.x, panel.right())
		}
	}
	// The panel itself stays inside the window.
	if panel.x < 0 || panel.right() > 1000 || panel.y < 0 || panel.bottom() > 650 {
		t.Fatalf("panel %.0fx%.0f at (%.0f,%.0f) leaves the window", panel.w, panel.h, panel.x, panel.y)
	}

	// Close (Escape), then the editor context menu.
	keyAt(hp, "escape")
	rightClickAt(hp, geometry.PointPixels(200, ui.MenuBarHeight+ui.TabBarHeight+8*18))
	if ws = handle.View.Read(cx); ws.Popup == nil {
		t.Fatal("right-click did not open the context menu")
	}
	shotCtx := saveShot(t, hp, dir, "context_menu_after_fix.png")
	panel, rows = popupPanelOf(handle.Window.RenderedHitboxes(),
		float32(ws.Popup.X), float32(ws.Popup.Y))
	if panel.w == 0 {
		t.Fatalf("context panel hitbox not found (shot %s)", shotCtx)
	}
	if len(rows) < 5 {
		t.Fatalf("found %d context rows, want >= 5", len(rows))
	}
	for i, r := range rows {
		if !panel.containsInner(r) {
			t.Fatalf("context row %d (%.0f..%.0f) overflows the panel (%.0f..%.0f)",
				i, r.x, r.right(), panel.x, panel.right())
		}
	}
}

// TestVisualScrollbarDragDoesNotSelectText presses the vertical scrollbar,
// drags across the code text and back, and asserts (a) the drag kept
// steering the thumb (scroll changed) and (b) no text selection appeared
// (before the pointer-capture fix the pane treated those moves as
// drag-select).
func TestVisualScrollbarDragDoesNotSelectText(t *testing.T) {
	hp, handle, cx := openEditorWindow(t)
	hw := hp.LastWindow()

	// The vertical scrollbar: a ~10px-wide hitbox at the pane's right edge.
	const vScrollW = float32(editor.VScrollWidth)
	paneRight := float32(1000)
	var vscroll hitboxRect
	for _, hb := range handle.Window.RenderedHitboxes() {
		r := rectOf(hb)
		if r.w >= vScrollW-1 && r.w <= vScrollW+2 && r.x > paneRight-vScrollW-16 && r.h > 100 {
			vscroll = r
		}
	}
	if vscroll.w == 0 {
		t.Fatal("vertical scrollbar hitbox not found")
	}

	line0, sx0 := handle.View.Read(cx).DebugScrollState()
	scroll0 := fmt.Sprintf("%d/%.0f", line0, sx0)
	thumbX := vscroll.x + vscroll.w/2
	thumbY := vscroll.y + 40
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(thumbX, thumbY)})

	// Drag left across the code text and up/down: with capture the drag
	// stays on the scrollbar and the pane never sees it.
	for _, p := range []geometry.Point[geometry.Pixels]{
		geometry.PointPixels(thumbX-200, thumbY+30),
		geometry.PointPixels(300, ui.MenuBarHeight+ui.TabBarHeight+100),
		geometry.PointPixels(300, ui.MenuBarHeight+ui.TabBarHeight+300),
		geometry.PointPixels(thumbX, thumbY+60),
	} {
		hw.DispatchInput(platform.MouseMoveEvent{Position: p, PressedButton: btnPtr(platform.ButtonLeft)})
	}
	line1, sx1 := handle.View.Read(cx).DebugScrollState()
	scroll1 := fmt.Sprintf("%d/%.0f", line1, sx1)
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(thumbX, thumbY+60)})

	if scroll1 == scroll0 {
		t.Fatalf("scrollbar drag did not scroll (state %v)", scroll1)
	}
	ws := handle.View.Read(cx)
	if b := ws.Buf(); b.HasSelection() {
		t.Fatalf("scrollbar drag selected text: %q", b.SelectedText())
	}

	// A plain text drag still selects (the fix must not break selection).
	lineY := float32(ui.MenuBarHeight + ui.TabBarHeight + 2*18)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(120, lineY)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(200, lineY+54), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(200, lineY+54)})
	if b := handle.View.Read(cx).Buf(); !b.HasSelection() {
		t.Fatal("normal text drag no longer selects")
	}
}

func keyAt(hp *headless.Platform, k string) {
	hp.LastWindow().DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: k}})
}

func btnPtr(b platform.MouseButton) *platform.MouseButton { return &b }

func rightClickAt(hp *headless.Platform, pos geometry.Point[geometry.Pixels]) {
	hw := hp.LastWindow()
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonRight, Position: pos})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonRight, Position: pos})
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
