package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/demo"
	"github.com/FlameInTheDark/flint/internal/ui"
)

// verifyHeadless drives the editor through its interactive surface without
// a window: tabs, caret movement, mouse selection, editing + undo, the
// menu bar, context menus, find, the terminal dock and the project panel.
func verifyHeadless(a *demo.App[Workspace], cx *app.App) {
	hw := a.Headless.LastWindow()
	out := func(name string) string { return filepath.Join(a.OutDir, name) }
	read := func() *Workspace { return a.Handle.View.Read(cx) }
	update := func(f func(*Workspace)) {
		a.Handle.View.Update(cx, func(state *Workspace, c *app.Context[Workspace]) any {
			f(state)
			c.Notify()
			return nil
		})
	}

	// Reset to the default demo layout: positional args may have opened
	// a project (tree panel) or a file (different active tab), and the
	// fixed coordinate assertions below assume the demo view.
	update(func(ws *Workspace) {
		if ws.ShowTree {
			ws.ToggleTree()
		}
		if len(ws.Buffers) > 0 {
			ws.Active = 0
		}
	})
	a.Handle.Window.Draw()
	if read().ShowTree {
		demo.Fatal("editor", fmt.Errorf("could not reset the tree panel for verification"))
	}

	// Frame 1: the initial workspace.
	frame := out("editor_01_initial.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// Geometry: menu bar 28, tab bar 34; the code pane starts at 62.
	metrics := read()
	lineH := metrics.LineHeight
	adv := metrics.CharAdvance
	paneTop := float32(ui.MenuBarHeight + ui.TabBarHeight)
	// The code column's hitbox origin is the exact text origin (the
	// editor's pointer mapping anchors there); clicks aim at glyph
	// centers so the nearest-boundary mapping is unambiguous.
	codeX := func() float32 {
		for _, hb := range a.Handle.Window.RenderedHitboxes() {
			o := hb.Bounds.Origin
			s := hb.Bounds.Size
			if o.Y.AsF32() >= paneTop-1 && o.Y.AsF32() <= paneTop+2 &&
				s.Height.AsF32() > 300 && s.Width.AsF32() > 200 &&
				o.X.AsF32() > 20 {
				return o.X.AsF32()
			}
		}
		demo.Fatal("editor", fmt.Errorf("code column hitbox not found"))
		return 0
	}

	// --- Tabs ----------------------------------------------------------
	tabs := bandHitboxes(a, ui.MenuBarHeight, ui.MenuBarHeight+ui.TabBarHeight)
	if len(tabs) < 3 {
		demo.Fatal("editor", fmt.Errorf("expected 3 tab hitboxes, got %d", len(tabs)))
	}
	clickAt(hw, tabs[1], 0.3)
	if read().Active != 1 {
		demo.Fatal("editor", fmt.Errorf("click on util.go tab did not activate it (active=%d)", read().Active))
	}
	clickAt(hw, tabs[0], 0.3)
	if read().Active != 0 {
		demo.Fatal("editor", fmt.Errorf("click back on main.go tab failed (active=%d)", read().Active))
	}

	// --- Caret placement + arrows -------------------------------------
	click(hw, geometry.PointPixels(codeX()+adv*2+adv*0.2, paneTop+2*lineH+lineH/2))
	if ws := read(); ws.Buf().Cursor != ws.Buf().LCToOffset(2, 2) {
		demo.Fatal("editor", fmt.Errorf("click on line 3 col 3 failed (cursor=%d)", ws.Buf().Cursor))
	}
	key(hw, "down", false)
	key(hw, "end", false)
	if line, _ := read().Buf().OffsetToLC(read().Buf().Cursor); line != 3 {
		demo.Fatal("editor", fmt.Errorf("arrow-down did not move the caret (line=%d)", line))
	}

	// --- Mouse selection (drag) ----------------------------------------
	drag(hw,
		geometry.PointPixels(codeX()+adv+adv*0.2, paneTop+1*lineH+lineH/2),
		geometry.PointPixels(codeX()+adv*5+adv*0.2, paneTop+5*lineH+lineH/2))
	ws := read()
	if !ws.Buf().HasSelection() {
		demo.Fatal("editor", fmt.Errorf("drag did not select text"))
	}
	sel := ws.Buf().SelectedText()
	if !strings.Contains(sel, "Fib") {
		demo.Fatal("editor", fmt.Errorf("drag selection unexpected content %q", sel))
	}
	frame = out("editor_02_selection.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// --- Select all, collapse, edit, undo ------------------------------
	key(hw, "a", true)
	ws = read()
	if ws.Buf().SelectedText() != ws.Buf().Text() {
		demo.Fatal("editor", fmt.Errorf("ctrl+a did not select the whole buffer"))
	}
	key(hw, "escape", false)
	if read().Buf().HasSelection() {
		demo.Fatal("editor", fmt.Errorf("escape did not collapse the selection"))
	}
	key(hw, "z", true) // undo nothing: no-op
	typeText(hw, "X")
	if !read().Buf().Dirty {
		demo.Fatal("editor", fmt.Errorf("typing did not mark the buffer dirty"))
	}
	key(hw, "z", true)
	if read().Buf().Dirty {
		demo.Fatal("editor", fmt.Errorf("undo did not restore the buffer"))
	}
	if read().Buf().Text() != demoMain {
		demo.Fatal("editor", fmt.Errorf("undo left modified content"))
	}

	// --- Double-click selects a word (line 5 = "// Fib returns …") ------
	clickCount(hw, geometry.PointPixels(codeX()+adv*5+adv*0.2, paneTop+4*lineH+lineH/2), 2)
	ws = read()
	if !ws.Buf().HasSelection() || strings.ContainsAny(ws.Buf().SelectedText(), " \t\n") {
		demo.Fatal("editor", fmt.Errorf("double-click did not select a word (%q)", ws.Buf().SelectedText()))
	}

	// --- Context menu on the code ---------------------------------------
	rightClick(hw, geometry.PointPixels(200, paneTop+8*lineH))
	if read().Popup == nil {
		demo.Fatal("editor", fmt.Errorf("right-click did not open the context menu"))
	}
	frame = out("editor_03_context_menu.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	key(hw, "escape", false)
	if read().Popup != nil {
		demo.Fatal("editor", fmt.Errorf("escape did not close the context menu"))
	}

	// --- Menu bar: File > New File --------------------------------------
	click(hw, geometry.PointPixels(30, ui.MenuBarHeight/2))
	if read().Popup == nil {
		demo.Fatal("editor", fmt.Errorf("clicking File did not open the dropdown"))
	}
	frame = out("editor_04_file_menu.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	// "New File" is the first row of the dropdown panel.
	click(hw, geometry.PointPixels(130, ui.MenuBarHeight+6+13))
	ws = read()
	if ws.Popup != nil || ws.Buf().Name != "untitled" {
		demo.Fatal("editor", fmt.Errorf("File > New File failed (popup=%v name=%s)", ws.Popup != nil, ws.Buf().Name))
	}

	// Back to main.go (also focuses the editor).
	clickAt(hw, tabs[0], 0.3)
	if read().Active != 0 {
		demo.Fatal("editor", fmt.Errorf("returning to main.go failed"))
	}

	// --- Find in file ----------------------------------------------------
	key(hw, "f", true)
	if read().Find == nil {
		demo.Fatal("editor", fmt.Errorf("ctrl+f did not open the find bar"))
	}
	typeText(hw, "Fib")
	ws = read()
	if ws.Find == nil || len(ws.Find.Matches) == 0 {
		demo.Fatal("editor", fmt.Errorf("find 'Fib' produced no matches"))
	}
	key(hw, "enter", false)
	ws = read()
	if !ws.Buf().HasSelection() || ws.Buf().SelectedText() != "Fib" {
		demo.Fatal("editor", fmt.Errorf("find enter did not select the match (%q)", ws.Buf().SelectedText()))
	}
	key(hw, "escape", false)
	if read().Find != nil {
		demo.Fatal("editor", fmt.Errorf("escape did not close the find bar"))
	}
	frame = out("editor_05_find.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// --- Terminal dock ---------------------------------------------------
	key(hw, "`", true) // ctrl+`
	if !read().ShowTerminal {
		demo.Fatal("editor", fmt.Errorf("ctrl+` did not open the terminal"))
	}
	typeText(hw, "echo hello-gpui-terminal")
	key(hw, "enter", false)
	// The pty roundtrip is asynchronous (keys → shell → output → grid):
	// poll frames until the echo lands (or fail with the final output).
	found := false
	for i := 0; i < 400; i++ {
		a.Handle.Window.Draw()
		for _, l := range read().Term.Output() {
			if strings.Contains(l, "hello-gpui-terminal") {
				found = true
			}
		}
		if found {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !found {
		demo.Fatal("editor", fmt.Errorf("terminal did not run the command (output=%v)", read().Term.Output()))
	}
	// The dock's top edge resizes by drag (the edge follows the pointer;
	// winH - status - y is the dock height).
	termH0, _ := read().DebugPanelSizes()
	termTop := float32(700) - ui.StatusHeight - termH0
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(500, termTop+2)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(500, termTop-78), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(500, termTop-78)})
	a.Handle.Window.Draw()
	termH1, _ := read().DebugPanelSizes()
	if termH1 < termH0+60 || termH1 > termH0+100 {
		demo.Fatal("editor", fmt.Errorf("terminal top-edge drag did not grow the dock: %.0f -> %.0f", termH0, termH1))
	}
	// Dragging back down shrinks it again (the edge sticks to the pointer).
	termTop = float32(700) - ui.StatusHeight - termH1
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(500, termTop+2)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(500, termTop+48), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(500, termTop+48)})
	a.Handle.Window.Draw()
	_, treeW0 := read().DebugPanelSizes()
	frame = out("editor_06_terminal.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	key(hw, "`", true) // close the terminal again
	if read().ShowTerminal {
		demo.Fatal("editor", fmt.Errorf("ctrl+` did not close the terminal"))
	}

	// --- Project panel ---------------------------------------------------
	project, err := os.MkdirTemp("", "flint-headless-")
	must(err)
	defer os.RemoveAll(project)
	fixtureDir := filepath.Join(project, "editor")
	must(os.Mkdir(fixtureDir, 0o755))
	fixture := filepath.Join(fixtureDir, "demo.go")
	must(os.WriteFile(fixture, []byte(demoMain), 0o644))
	update(func(ws *Workspace) {
		if err := ws.OpenFolder(project); err != nil {
			demo.Fatal("editor", err)
		}
	})
	if !read().ShowTree || read().Tree == nil {
		demo.Fatal("editor", fmt.Errorf("open folder did not show the project panel"))
	}
	// Click the rendered row: the tree header can vary across displays.
	a.Handle.Window.Draw()
	for _, hitbox := range a.Handle.Window.RenderedHitboxes() {
		if hitbox.Key == "n:btn-tree-"+fixtureDir {
			b := hitbox.Bounds
			click(hw, geometry.PointPixels(b.Origin.X.AsF32()+8, (b.Origin.Y+b.Size.Height/2).AsF32()))
			break
		}
	}
	ws = read()
	expanded := false
	for _, c := range ws.Tree.Children {
		if c.Name == "editor" && c.Expanded {
			expanded = true
		}
	}
	if !expanded {
		demo.Fatal("editor", fmt.Errorf("clicking a tree row did not expand the folder"))
	}
	// The dock's inner edge resizes the panel by drag; dragging far left
	// clamps at the panel minimum.
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(treeW0-ui.TreeResizeGrip/2, 300)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(treeW0+80, 300), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(treeW0+80, 300)})
	a.Handle.Window.Draw()
	_, treeW1 := read().DebugPanelSizes()
	if treeW1 < treeW0+70 || treeW1 > treeW0+90 {
		demo.Fatal("editor", fmt.Errorf("tree grip drag did not resize the panel: %.0f -> %.0f", treeW0, treeW1))
	}
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(treeW1-ui.TreeResizeGrip/2, 300)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(60, 300), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(60, 300)})
	a.Handle.Window.Draw()
	_, treeW2 := read().DebugPanelSizes()
	if treeW2 != ui.TreeMinWidth {
		demo.Fatal("editor", fmt.Errorf("tree grip drag did not clamp at the minimum: %.0f want %.0f", treeW2, ui.TreeMinWidth))
	}
	// Open a file through the workspace (tree click geometry is covered by
	// the expand check above).
	update(func(ws *Workspace) { ws.OpenFile(fixture) })
	if got := read().Buf().Name; got != "demo.go" {
		demo.Fatal("editor", fmt.Errorf("open file from project failed (active=%s)", got))
	}
	frame = out("editor_07_project.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	demo.Printf("editor", "7 frames rendered: tabs, caret, selection, edit/undo, menus, context menu, find, terminal, project panel, dock resizes verified")
}

// --- input helpers -------------------------------------------------------

func key(hw *headless.Window, k string, ctrl bool) {
	mods := keymap.Modifiers{}
	if ctrl {
		mods.Control = true
	}
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: k, Modifiers: mods}})
}

func typeText(hw *headless.Window, s string) {
	for _, r := range s {
		ch := string(r)
		k := ch
		switch ch {
		case " ":
			k = "space"
		case "`":
			k = "`"
		}
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: k, KeyChar: ch}})
	}
}

func click(hw *headless.Window, pos geometry.Point[geometry.Pixels]) {
	clickCount(hw, pos, 1)
}

func clickCount(hw *headless.Window, pos geometry.Point[geometry.Pixels], count int) {
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: pos, ClickCount: count})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: pos, ClickCount: count})
}

func rightClick(hw *headless.Window, pos geometry.Point[geometry.Pixels]) {
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonRight, Position: pos, ClickCount: 1})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonRight, Position: pos, ClickCount: 1})
}

func drag(hw *headless.Window, from, to geometry.Point[geometry.Pixels]) {
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: from, ClickCount: 1})
	hw.DispatchInput(platform.MouseMoveEvent{Position: to, PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: to, ClickCount: 1})
}

func btnPtr(b platform.MouseButton) *platform.MouseButton { return &b }

func clickAt(hw *headless.Window, hb gpui.Hitbox, frac float32) {
	click(hw, geometry.PointPixels(
		float32(hb.Bounds.Origin.X)+frac*float32(hb.Bounds.Size.Width),
		float32(hb.Bounds.Origin.Y)+float32(hb.Bounds.Size.Height)/2))
}

// bandHitboxes returns the hitboxes inside a horizontal band (tabs contain
// their close buttons; only outer boxes count). Band-filling containers —
// the scrollable tab strip spans the whole band and would otherwise swallow
// every tab as its "nested" child — are skipped by width.
func bandHitboxes(a *demo.App[Workspace], top, bottom float32) []gpui.Hitbox {
	var out []gpui.Hitbox
	winW := float32(a.Handle.Window.LogicalContentSize().Width.AsF32())
	for _, hb := range a.Handle.Window.RenderedHitboxes() {
		y := hb.Bounds.Origin.Y.AsF32()
		h := hb.Bounds.Size.Height.AsF32()
		w := hb.Bounds.Size.Width.AsF32()
		if y < top || y+h > bottom+0.5 {
			continue
		}
		// The scrollable strip fills the band; the tabs are what callers click.
		if winW > 0 && w > winW*0.9 {
			continue
		}
		nested := false
		for _, other := range out {
			if contains(other, hb) {
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

func contains(outer, inner gpui.Hitbox) bool {
	o := outer.Bounds
	i := inner.Bounds
	return o.Origin.X.AsF32() <= i.Origin.X.AsF32() &&
		o.Origin.Y.AsF32() <= i.Origin.Y.AsF32() &&
		o.Origin.X.AsF32()+o.Size.Width.AsF32() >= i.Origin.X.AsF32()+i.Size.Width.AsF32() &&
		o.Origin.Y.AsF32()+o.Size.Height.AsF32() >= i.Origin.Y.AsF32()+i.Size.Height.AsF32()
}

func must(err error) {
	if err != nil {
		demo.Fatal("editor", err)
	}
}

func savePNG(hw *headless.Window, path string) error {
	pngBytes, err := hw.PNG()
	if err != nil {
		return err
	}
	return os.WriteFile(path, pngBytes, 0o644)
}

// verifyInk asserts the frame has glyph ink and several distinct colors
// (light text on the dark theme, syntax palette, selection).
func verifyInk(hw *headless.Window, name string) {
	img := hw.Image()
	ink, colors := 0, map[uint32]bool{}
	for i := 0; i < img.Width*img.Height; i++ {
		r, g, b, a := img.Pixels[i*4], img.Pixels[i*4+1], img.Pixels[i*4+2], img.Pixels[i*4+3]
		colors[uint32(r)<<16|uint32(g)<<8|uint32(b)] = true
		if a > 200 && r > 140 && g > 140 && b > 140 {
			ink++
		}
	}
	if ink < 20 {
		demo.Fatal("editor", fmt.Errorf("%s: too little text ink (%d px)", name, ink))
	}
	if len(colors) < 30 {
		demo.Fatal("editor", fmt.Errorf("%s: too few colors (%d): highlighting likely broken", name, len(colors)))
	}
}
