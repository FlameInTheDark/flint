package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/debugger"
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

	// --- Menu bar: hover-switch while a dropdown is open -----------------
	// The dismiss layer occludes the real bar, so switching runs on the
	// hover strip layered above it: opening File and moving onto Edit must
	// open Edit as if it had been clicked (the IDE menu-bar behavior).
	click(hw, geometry.PointPixels(30, ui.MenuBarHeight/2))
	if read().Popup == nil {
		demo.Fatal("editor", fmt.Errorf("opening the File menu for the hover check failed"))
	}
	a.Handle.Window.Draw()
	editStrip, ok := hitboxByKey(a, "n:btn-menubar-strip-Edit")
	if !ok {
		demo.Fatal("editor", fmt.Errorf("menu hover strip not mounted while a dropdown is open"))
	}
	hw.DispatchInput(platform.MouseMoveEvent{Position: centerOf(editStrip)})
	a.Handle.Window.Draw()
	if got := read().Menu.Open; got != 1 { // Edit is the second menu
		demo.Fatal("editor", fmt.Errorf("hovering Edit while File is open did not switch menus (open=%d)", got))
	}
	frame = out("editor_04b_menu_hover_switch.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	key(hw, "escape", false)
	if read().Popup != nil {
		demo.Fatal("editor", fmt.Errorf("escape did not close the hover-switched menu"))
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

	// --- Debug panel, dock resizes, left tool resize ---------------------
	verifyDebugDocks(a, update, hw, out)

	// --- Git tool panel (throwaway repo) ---------------------------------
	verifyGitPanel(a, update, hw, out)

	// --- Module files, code folding, TODO tags, file-type icons ----------
	verifyEditorExtras(a, update, hw, out)

	// --- The caller's real project (FLINT_HEADLESS_PROJECT), if given ----
	verifyRealProject(a, update, hw, out)

	demo.Printf("editor", "26 frames rendered: tabs, caret, selection, edit/undo, menus with hover-switching while open, context menu, find, terminal, project panel, debug panel with icon controls, name tooltips and underline tabs, visible draggable scrollbars (vertical + horizontal), dock resizes, problems, structure tool, git panel with plus/minus stage actions, file-type icons in the project tree, go.mod/go.sum highlighting, code folding with gutter chevrons and inline placeholders, TODO tag highlighting, python indent folds; dock resizes verified")
}

// verifyRealProject renders the project passed via the FLINT_HEADLESS_PROJECT
// environment variable: its file tree with the per-type icons, a Go entry
// file with TODO tag colors and fold chevrons, a folded view of the same
// file, and go.mod / go.sum highlighting. It mirrors the screen a developer
// sees when they open this repository in flint, so the delivered features
// can be verified against the real project rather than the fixture.
func verifyRealProject(a *demo.App[Workspace], update func(f func(*Workspace)), hw *headless.Window, out func(name string) string) {
	dir := os.Getenv("FLINT_HEADLESS_PROJECT")
	if dir == "" {
		return
	}
	abs, err := filepath.Abs(dir)
	must(err)
	if st, statErr := os.Stat(abs); statErr != nil || !st.IsDir() {
		demo.Fatal("editor", fmt.Errorf("FLINT_HEADLESS_PROJECT %q is not a directory", dir))
	}
	read := func() *Workspace { return a.Handle.View.Read(a.App.App) }

	// Frame R1: the project tree with file-type icons. OpenFolder
	// rebuilds the tree from the directory (Root alone would leave the
	// previous project's nodes on screen).
	var openErr error
	update(func(ws *Workspace) { openErr = ws.OpenFolder(abs) })
	must(openErr)
	a.Handle.Window.Draw()
	frame := out("repo_01_tree_icons.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// Frame R2: the repository's entry file — TODO tags in orange, fold
	// chevrons in the gutter.
	goFile := filepath.Join(abs, "main.go")
	if _, statErr := os.Stat(goFile); statErr != nil {
		matches, _ := filepath.Glob(filepath.Join(abs, "*.go"))
		if len(matches) == 0 {
			return // no Go file at the root; the tree frame still rendered
		}
		goFile = matches[0]
	}
	update(func(ws *Workspace) { ws.OpenFile(goFile) })
	a.Handle.Window.Draw()
	if lang := read().Buf().LanguageOf(); lang != "go" {
		demo.Fatal("editor", fmt.Errorf("%s detected as %q", goFile, lang))
	}
	if len(read().Buf().FoldRanges()) == 0 {
		demo.Fatal("editor", fmt.Errorf("%s produced no fold ranges", goFile))
	}
	frame = out("repo_02_entry_todo.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// The import block's line (0-based) drives the hover and fold frames.
	importLine := -1
	for i, ln := range read().Buf().Lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "import (") {
			importLine = i
			break
		}
	}

	// Frame R2b: the chevron's hover state. The pointer really moves
	// over the import chevron's button, so the frame shows the small
	// rounded hover highlight around the glyph only — never the line
	// number next to it.
	if importLine >= 0 {
		if hb, ok := hitboxByKey(a, fmt.Sprintf("n:btn-fold-chev-%d", importLine)); ok {
			hw.DispatchInput(platform.MouseMoveEvent{Position: centerOf(hb)})
			a.Handle.Window.Draw()
			frame = out("repo_02b_chevron_hover.png")
			must(savePNG(hw, frame))
			verifyInk(hw, frame)
			// Park the pointer over the code area so the later
			// frames stay neutral.
			hw.DispatchInput(platform.MouseMoveEvent{
				Position: geometry.PointPixels(760, 300),
			})
			a.Handle.Window.Draw()
		}
	}

	// Frame R3: the same file with its import block folded via the API.
	if importLine >= 0 {
		update(func(ws *Workspace) { ws.Buf().ToggleFold(importLine) })
		a.Handle.Window.Draw()
		if !read().Buf().IsFolded(importLine) {
			demo.Fatal("editor", fmt.Errorf("import block at line %d did not fold", importLine))
		}
		frame = out("repo_03_entry_folded.png")
		must(savePNG(hw, frame))
		verifyInk(hw, frame)
		update(func(ws *Workspace) { ws.Buf().UnfoldAll() })
		a.Handle.Window.Draw()
	}

	// Frames R4/R5: the module files when they exist.
	for _, m := range []struct{ file, name, lang string }{
		{"go.mod", "repo_04_gomod.png", "gomod"},
		{"go.sum", "repo_05_gosum.png", "gosum"},
	} {
		path := filepath.Join(abs, m.file)
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		update(func(ws *Workspace) { ws.OpenFile(path) })
		a.Handle.Window.Draw()
		if lang := read().Buf().LanguageOf(); lang != m.lang {
			demo.Fatal("editor", fmt.Errorf("%s detected as %q", m.file, lang))
		}
		frame = out(m.name)
		must(savePNG(hw, frame))
		verifyInk(hw, frame)
	}

	// Frame R6: scrolled to the end of the repository's largest Go file.
	// The wheel ease rests on the pixel-exact document end (the last
	// line's bottom on the pane's bottom edge) and the row budget paints
	// the partially visible bottom row — the two fixes for the reported
	// "bottom line disappears too early while still on screen" bug.
	longFile := filepath.Join(abs, "internal", "editor", "editor.go")
	if _, statErr := os.Stat(longFile); statErr == nil {
		update(func(ws *Workspace) { ws.OpenFile(longFile) })
		a.Handle.Window.Draw()
		if lines := read().Buf().LineCount(); lines < 400 {
			demo.Fatal("editor", fmt.Errorf("scroll-end fixture %s too short (%d lines)", longFile, lines))
		}
		update(func(ws *Workspace) { ws.addScrollDelta(1e6) })
		for i := 1; i < 400 && read().scrollAnimating(); i++ {
			at := time.Now().Add(time.Duration(i) * 250 * time.Millisecond)
			update(func(ws *Workspace) { ws.advanceScrollAnimAt(at) })
		}
		if read().scrollAnimating() {
			demo.Fatal("editor", fmt.Errorf("the scroll ease did not settle"))
		}
		ws := read()
		whole := float32(ws.maxScrollRows())
		if ws.scrollF() > whole || ws.scrollF() < whole-1 {
			demo.Fatal("editor", fmt.Errorf("scroll end rest %v outside the pixel-exact range (%v,%v]", ws.scrollF(), whole-1, whole))
		}
		a.Handle.Window.Draw()
		frame = out("repo_06_scroll_end.png")
		must(savePNG(hw, frame))
		verifyInk(hw, frame)
	}

	// Leave the demo view as verifyEditorExtras restored it.
	update(func(ws *Workspace) { ws.ShowTree = false })
	a.Handle.Window.Draw()
	demo.Printf("editor", "real project frames rendered from "+abs)
}

// verifyEditorExtras renders the module-file support (go.mod / go.sum),
// code folding (gutter chevrons, collapsed placeholders, click-to-toggle)
// and the TODO tag colors over a fixture project whose tree shows a file
// icon for every common type.
func verifyEditorExtras(a *demo.App[Workspace], update func(f func(*Workspace)), hw *headless.Window, out func(name string) string) {
	read := func() *Workspace { return a.Handle.View.Read(a.App.App) }
	proj, err := os.MkdirTemp("", "flint-features-demo-")
	if err != nil {
		demo.Fatal("editor", err)
	}
	defer os.RemoveAll(proj)
	write := func(name, content string) {
		must(os.WriteFile(filepath.Join(proj, name), []byte(content), 0o644))
	}
	const modSrc = `module flint.demo/features

go 1.23

// Dependency pins for the demo workspace.
require (
        github.com/foo/bar v1.4.2
        github.com/baz/qux v2.1.0 // TODO: bump to the next minor
)

exclude github.com/old/pkg v0.9.0

replace github.com/foo/bar => github.com/foo/bar-fork v1.4.3
`
	const sumSrc = `github.com/baz/qux v2.1.0 h1:QUxeX1pR3WKhZ0vAbCdEfGhIjKlMnOpQrStUvWxYz01=
github.com/baz/qux v2.1.0/go.mod h1:AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdefghij=
github.com/foo/bar v1.4.2 h1:ZxYwVuTsRqPoNmLkJiHgFeDcBa9876543210zyxwvuts=
github.com/foo/bar v1.4.2/go.mod h1:qwErTyUiOpAsDfGhJkLzXcVbNm1234567890QwErTy=
`
	const mainSrc = `package main

import (
        "fmt"
        "strings"
)

// banner builds the demo banner.
//
// TODO: localize the punctuation.
// FIXME: the width math breaks below 20 columns.
func banner(title string, width int) string {
        if width < 20 {
                width = 20
        }
        line := strings.Repeat("=", width)
        return fmt.Sprintf("%s\n  %s\n%s", line, title, line)
}

// TODO: make the greeting configurable via flags.
func main() {
        fmt.Println(banner("flint feature demo", 40))
}
`
	const pySrc = `def banner(title, width):
    if width < 20:
        width = 20
    line = "=" * width
    # TODO: localize the punctuation.
    return line + "\n  " + title + "\n" + line


def main():
    print(banner("flint", 40))
`
	write("go.mod", modSrc)
	write("go.sum", sumSrc)
	write("main.go", mainSrc)
	write("calc.py", pySrc)
	write("README.md", "# Feature demo\n\nProject tree with file-type icons.\n")
	write("config.yaml", "services:\n  web:\n    image: nginx:latest\n  db:\n    image: postgres:16\n")
	write("config.toml", "[server]\nhost = \"localhost\"\nport = 8080\n")
	write("notes.txt", "scratch notes for the demo\n")
	write("data.json", "{\n  \"name\": \"flint\",\n  \"tags\": [\"demo\", \"icons\"]\n}\n")
	write("style.css", "body {\n  color: #eee;\n  background: #222;\n}\n")
	write("index.html", "<!doctype html>\n<html>\n  <body><h1>flint</h1></body>\n</html>\n")
	write("app.js", "const greet = (n) => `hello ${n}`;\nconsole.log(greet('flint'));\n")
	write("lib.rs", "fn banner(title: &str, width: usize) -> String {\n    let line = \"=\".repeat(width);\n    format!(\"{line}\\n  {title}\\n{line}\")\n}\n")
	// One file per logo'd extension: the tree frame then shows every
	// official language mark the picker can reach, not just the generic
	// glyphs.
	write("core.c", "#include <stdio.h>\n\nint main(void) { puts(\"flint\"); return 0; }\n")
	write("app.cpp", "#include <iostream>\n\nint main() { std::cout << \"flint\"; }\n")
	write("Program.cs", "class Program {\n    static void Main() => System.Console.WriteLine(\"flint\");\n}\n")
	write("Main.java", "class Main {\n    public static void main(String[] a) { System.out.println(\"flint\"); }\n}\n")
	write("app.ts", "const greet = (n: string): string => `hello ${n}`;\nconsole.log(greet('flint'));\n")
	write("index.php", "<?php\necho 'hello from flint';\n")
	write("app.rb", "def banner(n) = '=' * n\nputs banner(40)\n")
	write("lib.kt", "fun main() {\n    println(\"flint\")\n}\n")
	write("app.swift", "import Foundation\nprint(\"flint\")\n")
	write("init.lua", "print(\"flint\")\n")
	write("main.dart", "void main() {\n  print('flint');\n}\n")
	write("Grid.scala", "object Grid {\n  def main(args: Array[String]): Unit = println(\"flint\")\n}\n")
	write("tools.pl", "print \"flint\\n\";\n")
	write("App.vue", "<template>\n  <div>flint</div>\n</template>\n")
	write("page.svelte", "<div>flint</div>\n")
	write("config.xml", "<?xml version=\"1.0\"?>\n<app name=\"flint\"/>\n")
	write("style.scss", "$c: #eee;\nbody { color: $c; }\n")
	write("build.bat", "@echo off\necho hello from the batch script\n")
	write("query.sql", "SELECT name, version FROM modules WHERE indirect = false;\n")
	write("script.sh", "#!/bin/sh\necho hello from the script\n")
	write("table.csv", "name,version\nbar,1.4.2\nqux,2.1.0\n")
	write("Makefile", "all: build\n\nbuild:\n\tgo build ./...\n")
	write("LICENSE", "MIT License\n\nCopyright (c) 2026 flint demo\n")
	write("bundle.zip", "PK\x03\x04 placeholder archive bytes\x00\x01\x02")
	write("app.exe", "MZ\x90\x00 placeholder binary bytes\x00\x01\x02")
	write("font.ttf", "\x00\x01\x00\x00\x00 placeholder font bytes")
	write("clip.mp4", "\x00\x00\x00\x18ftypmp42 placeholder video bytes")
	write("song.mp3", "ID3 placeholder audio bytes")
	write("creds.pem", "-----BEGIN CERTIFICATE-----\nplaceholder\n-----END CERTIFICATE-----\n")
	write("secret.lock", "lockfile placeholder")
	write("logo.png", "\x89PNG\r\n\x1a\n placeholder image bytes")

	oldRoot, oldActive := read().Root, read().Active
	oldBuffers := read().Buffers
	oldShowTree := read().ShowTree
	update(func(ws *Workspace) {
		if err := ws.OpenFolder(proj); err != nil {
			demo.Fatal("editor", err)
		}
	})
	a.Handle.Window.Draw()
	// flattenVisible always paints the root's children, so every fixture
	// file (one icon per file type) is on screen right away.
	frame := out("editor_17_file_icons.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	// The list is longer than the viewport (one icon per file type);
	// park it on the last page so the bottom half of the icon set is
	// verified too.
	update(func(ws *Workspace) { ws.treeScrollHandle.ScrollToBottom() })
	a.Handle.Window.Draw()
	frame = out("editor_17b_file_icons_end.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// go.mod: directive keywords, module paths, versions, comments; the
	// require block shows its fold chevron in the gutter.
	update(func(ws *Workspace) { ws.OpenFile(filepath.Join(proj, "go.mod")) })
	a.Handle.Window.Draw()
	if lang := read().Buf().LanguageOf(); lang != "gomod" {
		demo.Fatal("editor", fmt.Errorf("go.mod detected as %q", lang))
	}
	frame = out("editor_18_gomod.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// Click the require block's fold chevron: the block collapses to the
	// "require (" header with the … placeholder.
	hb, ok := hitboxByKey(a, "n:btn-fold-chev-5") // line 6 (1-based): `require (`
	if !ok {
		demo.Fatal("editor", fmt.Errorf("require block fold chevron not found"))
	}
	clickAt(hw, hb, 0.5)
	a.Handle.Window.Draw()
	if buf := read().Buf(); buf == nil || !buf.IsFolded(5) {
		demo.Fatal("editor", fmt.Errorf("require block did not fold on chevron click"))
	}
	frame = out("editor_19_gomod_folded.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	// The placeholder expands it again.
	hb, ok = hitboxByKey(a, "n:btn-fold-ph-5")
	if !ok {
		demo.Fatal("editor", fmt.Errorf("fold placeholder not found"))
	}
	clickAt(hw, hb, 0.5)
	a.Handle.Window.Draw()
	if buf := read().Buf(); buf == nil || buf.IsFolded(5) {
		demo.Fatal("editor", fmt.Errorf("fold placeholder did not expand"))
	}

	// main.go: TODO/FIXME tags painted inside the comments, then the
	// banner function folded (chevron click again).
	update(func(ws *Workspace) { ws.OpenFile(filepath.Join(proj, "main.go")) })
	a.Handle.Window.Draw()
	frame = out("editor_20_todo.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	hb, ok = hitboxByKey(a, "n:btn-fold-chev-11") // banner's `func` line (12, 1-based)
	if !ok {
		demo.Fatal("editor", fmt.Errorf("banner fold chevron not found"))
	}
	clickAt(hw, hb, 0.5)
	a.Handle.Window.Draw()
	if buf := read().Buf(); buf == nil || !buf.IsFolded(11) {
		demo.Fatal("editor", fmt.Errorf("banner function did not fold on chevron click"))
	}
	frame = out("editor_21_gofold.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	update(func(ws *Workspace) { ws.Buf().UnfoldAll() })
	a.Handle.Window.Draw()

	// go.sum: module paths, versions and dimmed hashes.
	update(func(ws *Workspace) { ws.OpenFile(filepath.Join(proj, "go.sum")) })
	a.Handle.Window.Draw()
	if lang := read().Buf().LanguageOf(); lang != "gosum" {
		demo.Fatal("editor", fmt.Errorf("go.sum detected as %q", lang))
	}
	frame = out("editor_22_gosum.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// calc.py: indentation folds (every nesting level gets a chevron).
	update(func(ws *Workspace) { ws.OpenFile(filepath.Join(proj, "calc.py")) })
	a.Handle.Window.Draw()
	if len(read().Buf().FoldRanges()) == 0 {
		demo.Fatal("editor", fmt.Errorf("python produced no indent folds"))
	}
	frame = out("editor_23_python.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// Language picker: the status-bar selector open over the picker's
	// catalog — every row shows the file-type icon its files carry in
	// the tree, and the grabbable scrollbar rides the list's right
	// edge. The second frame parks the view mid-catalog to prove the
	// thumb tracks the offset.
	update(func(ws *Workspace) { ws.OpenLangMenu() })
	a.Handle.Window.Draw()
	if read().LangMenu == nil {
		demo.Fatal("editor", fmt.Errorf("language menu did not open"))
	}
	frame = out("editor_24_lang_menu.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	update(func(ws *Workspace) {
		if m := ws.LangMenu; m != nil {
			m.Scroll = (len(filteredLanguages("")) - int(ui.LangMenuVisible)) / 2
			m.Selected = m.Scroll
		}
	})
	a.Handle.Window.Draw()
	frame = out("editor_24b_lang_menu_scrolled.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	// Park the view on the last page too: across the three frames every
	// catalog row (and its icon) is on screen at least once.
	update(func(ws *Workspace) {
		if m := ws.LangMenu; m != nil {
			m.Scroll = len(filteredLanguages("")) - int(ui.LangMenuVisible)
			m.Selected = m.Scroll
		}
	})
	a.Handle.Window.Draw()
	frame = out("editor_24c_lang_menu_end.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	update(func(ws *Workspace) { ws.CloseLangMenu() })
	a.Handle.Window.Draw()

	// Restore the demo view (buffers and root as verifyHeadless left them).
	update(func(ws *Workspace) {
		ws.Buffers = oldBuffers
		ws.Active = oldActive
		ws.Root = oldRoot
		ws.ShowTree = oldShowTree
	})
	a.Handle.Window.Draw()
}

// verifyGitPanel opens the Git tool over a throwaway repository with all
// three change groups present (staged, tracked, untracked) and saves a
// frame: the row stage/unstage actions render as Lucide plus/minus icons.
func verifyGitPanel(a *demo.App[Workspace], update func(f func(*Workspace)), hw *headless.Window, out func(name string) string) {
	read := func() *Workspace { return a.Handle.View.Read(a.App.App) }
	repo, err := os.MkdirTemp("", "flint-git-demo-")
	if err != nil {
		demo.Fatal("editor", err)
	}
	defer os.RemoveAll(repo)
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if msg, err := cmd.CombinedOutput(); err != nil {
			demo.Fatal("editor", fmt.Errorf("git %v: %v: %s", args, err, msg))
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "demo@flint.local")
	git("config", "user.name", "Flint Demo")
	must(os.WriteFile(filepath.Join(repo, "readme.md"), []byte("# Demo\n\nTracked file, edited for the staged group.\n"), 0o644))
	must(os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n\nfunc main() { println(\"demo\") }\n"), 0o644))
	git("add", ".")
	git("commit", "-q", "-m", "initial demo")
	// One file per group: readme.md edited and staged, main.go edited and
	// unstaged, notes.txt never added.
	must(os.WriteFile(filepath.Join(repo, "readme.md"), []byte("# Demo\n\nTracked file, edited for the staged group. More text.\n"), 0o644))
	git("add", "readme.md")
	must(os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n\n// edited for the tracked group\nfunc main() { println(\"demo\") }\n"), 0o644))
	must(os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("untracked scratch notes\n"), 0o644))

	oldRoot := read().Root
	// The status scan normally mutates workspace state from its background
	// goroutine (headless dispatch runs inline on the caller), racing this
	// loop's Draw. Swap the dispatch hook out so the tool load runs on this
	// goroutine; restore the platform hooks afterwards.
	update(func(ws *Workspace) { ws.SetEnv(nil, nil, nil) })
	update(func(ws *Workspace) { ws.Root = repo; ws.selectLeftTool("git", update, a.Handle.Window) })
	a.Handle.Window.Draw()
	if g := read().tools.git; g == nil || g.result.repo == "" {
		demo.Fatal("editor", fmt.Errorf("git tool did not load the demo repository"))
	}
	frame := out("editor_14_git.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	// Leave the demo view as it was found (structure tool open, old root,
	// platform hooks back in place).
	update(func(ws *Workspace) {
		ws.Root = oldRoot
		ws.tools.left = "structure"
		ws.SetEnv(
			func(item platform.ClipboardItem) {
				if pw := a.Handle.Window.PlatformWindow(); pw != nil {
					pw.WriteClipboard(item)
				}
			},
			func(f func()) { a.Platform.DispatchOnMainThread(f) },
			func() { a.Platform.Quit() },
		)
	})
	a.Handle.Window.Draw()
}

// verifyDebugDocks drives the Delve debug panel (synthetic paused session),
// the bottom dock resize strips (debug / problems) and the left tool
// panel's resize strip, saving a frame of each state.
func verifyDebugDocks(a *demo.App[Workspace], update func(f func(*Workspace)), hw *headless.Window, out func(name string) string) {
	read := func() *Workspace { return a.Handle.View.Read(a.App.App) }
	// Synthetic sources live in a temp dir: the frames output stays clean.
	debugDir, err := os.MkdirTemp("", "flint-debug-demo-")
	if err != nil {
		demo.Fatal("editor", err)
	}
	defer os.RemoveAll(debugDir)
	mainPath := filepath.Join(debugDir, "main.go")
	helperPath := filepath.Join(debugDir, "helper.go")
	const helperSrc = `package main

func helper(cfg Config) int {
        n := 42
        return n + cfg.Retries
}

type Config struct {
        Verbose bool
        Retries int
        Tag     string
}
`
	must(os.WriteFile(mainPath, []byte("package main\n\nfunc main() { println(helper(Config{})) }\n"), 0o644))
	must(os.WriteFile(helperPath, []byte(helperSrc), 0o644))

	// Synthetic paused snapshot: mixed data types so the value coloring and
	// expandable tree render like a real Delve stop (GoLand-style inspect).
	snap := debugger.Snapshot{
		State:    debugger.Paused,
		Detail:   "breakpoint hit",
		Thread:   1,
		StopID:   7,
		Selected: 0,
		Frames: []debugger.Frame{
			{ID: 1, Line: 3, Column: 12, Name: "main.helper", Source: debugger.Source{Path: helperPath}},
			{ID: 2, Line: 3, Column: 24, Name: "main.main", Source: debugger.Source{Path: mainPath}},
			{ID: 3, Line: 225, Column: 0, Name: "runtime.main", Source: debugger.Source{Path: "/usr/local/go/src/runtime/proc.go"}},
		},
		Scopes: []debugger.Scope{
			{Name: "Locals", VariablesReference: 1000},
			{Name: "Globals", VariablesReference: 2000, Expensive: true},
		},
		Variables: map[int][]debugger.Variable{
			1000: {
				{Name: "n", Value: "42", Type: "int"},
				{Name: "ratio", Value: "3.14159", Type: "float64"},
				{Name: "greeting", Value: "\"hello, 世界\"", Type: "string"},
				{Name: "enabled", Value: "true", Type: "bool"},
				{Name: "missing", Value: "nil", Type: "*main.Config"},
				{Name: "steps", Value: "[]int len: 3, cap: 3, [1, 2, 3]", Type: "[]int"},
				{Name: "ages", Value: "map[string]int [\"alice\": 30, \"bob\": 41]", Type: "map[string]int"},
				{Name: "conf", Value: "main.Config{Verbose: true, Retries: 3, Tag: \"prod\"}", Type: "main.Config", VariablesReference: 1001},
				{Name: "ptr", Value: "0xc0000b4000", Type: "*main.Config", VariablesReference: 1002},
				{Name: "fn", Value: "func(int) int", Type: "func(int) int"},
				{Name: "ch", Value: "chan int 0/0", Type: "chan int"},
				{Name: "long", Value: "\"" + strings.Repeat("payload/", 60) + "\"", Type: "string"},
			},
			1001: {
				{Name: "Verbose", Value: "true", Type: "bool"},
				{Name: "Retries", Value: "3", Type: "int"},
				{Name: "Tag", Value: "\"prod\"", Type: "string"},
			},
			1002: {
				{Name: "Verbose", Value: "false", Type: "bool"},
				{Name: "Tag", Value: "\"dev\"", Type: "string"},
			},
		},
		Breakpoints: map[string]map[int]debugger.Breakpoint{
			helperPath: {3: {ID: 1, Line: 3, Verified: true}},
		},
	}
	session := debugger.OfflineSession(snap)
	// Synthetic run log: the Output tab renders the session's output
	// buffer (Delve's stdout/stderr stream); a long line among the
	// short ones gives the tab a horizontal overflow too.
	session.AppendOutput(strings.Repeat("2026/11/25 09:59:01 helper: config loaded (pid 4242)\n", 40) +
		"2026/11/25 09:59:02 helper: retry schedule " + strings.Repeat("[backoff 250ms] ", 18) + "applied\n" +
		strings.Repeat("2026/11/25 09:59:03 helper: waiting for the debugger to attach\n", 30))
	update(func(ws *Workspace) {
		ws.debug.points = map[string][]int{helperPath: {3}, mainPath: {7, 11}}
		ws.debug.session = session
		ws.debug.name = "demo"
		ws.showDebug()
		ws.debug.tab = "Inspect"
		ws.debug.openVars = map[int]bool{1001: true, 1002: true}
	})
	a.Handle.Window.Draw()

	// The variables segment scrolls independently: wheel over it, the top
	// offset moves and the header stays (segment-local scroll).
	varsBox, ok := hitboxByKey(a, "n:btn-debug-variables")
	if !ok {
		demo.Fatal("editor", fmt.Errorf("debug variables segment not found"))
	}
	maxTop := read().debug.vars.MaxTop().AsF32()
	if maxTop <= 0 {
		demo.Fatal("editor", fmt.Errorf("variables segment does not overflow (MaxTop=%.0f): scroll broken", maxTop))
	}
	hw.DispatchInput(platform.ScrollEvent{Position: centerOf(varsBox), Delta: platform.ScrollDeltaPixels(0, -60)})
	a.Handle.Window.Draw()
	if got := read().debug.vars.Top().AsF32(); got <= 0 {
		demo.Fatal("editor", fmt.Errorf("wheel did not scroll the variables segment (top=%.0f)", got))
	}
	frame := out("editor_08_debug_inspect.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// Hovering an icon control surfaces its name tooltip after the standard
	// delay (the icons replaced the text labels): a rounded rectangle that
	// must also survive the tree rebuilds a live session constantly makes.
	cont, ok := hitboxByKey(a, "n:btn-debug-continue")
	if !ok {
		demo.Fatal("editor", fmt.Errorf("debug continue button not found"))
	}
	hw.DispatchInput(platform.MouseMoveEvent{Position: centerOf(cont)})
	a.Handle.Window.Draw()
	time.Sleep(600 * time.Millisecond)
	a.Handle.Window.Draw()
	if !a.Handle.Window.TooltipVisible() {
		demo.Fatal("editor", fmt.Errorf("debug control tooltip did not surface"))
	}
	frame = out("editor_08b_debug_tooltip.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(500, 200)})
	a.Handle.Window.Draw()

	// Wide values overflow the variables segment horizontally: wheel over
	// it pans across the long string instead of clipping the row (the
	// panned frame shows the value's tail past the segment edge).
	maxLeft := read().debug.vars.MaxLeft().AsF32()
	if maxLeft <= 0 {
		demo.Fatal("editor", fmt.Errorf("wide value did not overflow the variables segment (MaxLeft=%.0f): horizontal scroll broken", maxLeft))
	}
	hw.DispatchInput(platform.ScrollEvent{Position: centerOf(varsBox), Delta: platform.ScrollDeltaPixels(-400, 0)})
	a.Handle.Window.Draw()
	if got := read().debug.vars.Left().AsF32(); got <= 0 {
		demo.Fatal("editor", fmt.Errorf("horizontal wheel did not pan the variables segment (left=%.0f)", got))
	}
	frame = out("editor_14_debug_vars_hscroll.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	// Pan back to the left edge before the scrollbar checks below.
	hw.DispatchInput(platform.ScrollEvent{Position: centerOf(varsBox), Delta: platform.ScrollDeltaPixels(4000, 0)})
	a.Handle.Window.Draw()
	if got := read().debug.vars.Left().AsF32(); got != 0 {
		demo.Fatal("editor", fmt.Errorf("variables pan did not clamp at the left edge (left=%.0f)", got))
	}

	// The segments paint visible scrollbars when they overflow (the
	// project tree's bars): the variables segment carries both — rows
	// past the fold and values wider than the segment — and dragging a
	// bar scrolls the segment like any normal scroll area.
	vBar, ok := hitboxByKey(a, "n:btn-debug-variables-vscroll")
	if !ok {
		demo.Fatal("editor", fmt.Errorf("variables vertical scrollbar not mounted"))
	}
	hBar, ok := hitboxByKey(a, "n:btn-debug-variables-hscroll")
	if !ok {
		demo.Fatal("editor", fmt.Errorf("variables horizontal scrollbar not mounted"))
	}
	vX := float32(vBar.Bounds.Origin.X) + 5
	vTopEdge := float32(vBar.Bounds.Origin.Y)
	vTrackH := float32(vBar.Bounds.Size.Height)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(vX, vTopEdge+10)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(vX, vTopEdge+vTrackH/2), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(vX, vTopEdge+vTrackH/2)})
	a.Handle.Window.Draw()
	draggedTop := read().debug.vars.Top().AsF32()
	if draggedTop <= maxTop*0.15 || draggedTop >= maxTop*0.85 {
		demo.Fatal("editor", fmt.Errorf("vertical scrollbar drag landed outside the middle of the list (top=%.0f max=%.0f)", draggedTop, maxTop))
	}
	frame = out("editor_15_debug_vars_scrollbars.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	// Drag the horizontal thumb rightwards: the wide values pan into
	// view. The long row sits below the fold, so wheel it into view
	// first — only rows wider than the pan offset reach into the panned
	// viewport (the short rows scroll out left, GoLand-style).
	hw.DispatchInput(platform.ScrollEvent{Position: centerOf(varsBox), Delta: platform.ScrollDeltaPixels(0, -4000)})
	a.Handle.Window.Draw()
	hY := float32(hBar.Bounds.Origin.Y) + 5
	hLeftEdge := float32(hBar.Bounds.Origin.X)
	hTrackW := float32(hBar.Bounds.Size.Width)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(hLeftEdge+10, hY)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(hLeftEdge+hTrackW*0.3, hY), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(hLeftEdge+hTrackW*0.3, hY)})
	a.Handle.Window.Draw()
	if got := read().debug.vars.Left().AsF32(); got <= 0 {
		demo.Fatal("editor", fmt.Errorf("horizontal scrollbar drag did not pan the variables segment (left=%.0f)", got))
	}
	frame = out("editor_16_debug_vars_hbar_drag.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	// Pan back to the left edge for the resize checks below.
	hw.DispatchInput(platform.ScrollEvent{Position: centerOf(varsBox), Delta: platform.ScrollDeltaPixels(4000, 0)})
	a.Handle.Window.Draw()
	hw.DispatchInput(platform.ScrollEvent{Position: centerOf(varsBox), Delta: platform.ScrollDeltaPixels(0, 4000)})
	a.Handle.Window.Draw()

	// The debug panel's top edge resizes the shared dock height (strip drag).
	termH0, _ := read().DebugPanelSizes()
	dragStrip(a, "n:btn-debug-resize", -70)
	a.Handle.Window.Draw()
	termH1, _ := read().DebugPanelSizes()
	if termH1 < termH0+50 || termH1 > termH0+90 {
		demo.Fatal("editor", fmt.Errorf("debug panel strip drag did not resize the dock: %.0f -> %.0f", termH0, termH1))
	}
	// Drag back down: the edge sticks to the pointer.
	dragStrip(a, "n:btn-debug-resize", 30)
	a.Handle.Window.Draw()
	termH2, _ := read().DebugPanelSizes()
	if termH2 > termH1-20 || termH2 < termH1-40 {
		demo.Fatal("editor", fmt.Errorf("debug panel strip drag down did not shrink the dock: %.0f -> %.0f", termH1, termH2))
	}
	frame = out("editor_09_debug_resized.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// Output and Breakpoints tabs. Dragging the output segment's vertical
	// scrollbar reads back through the log and cancels the tail-follow.
	clickHitbox(hw, a, "n:btn-debug-tab-Output")
	a.Handle.Window.Draw()
	outBar, ok := hitboxByKey(a, "n:btn-debug-output-vscroll")
	if !ok {
		demo.Fatal("editor", fmt.Errorf("output vertical scrollbar not mounted"))
	}
	oX := float32(outBar.Bounds.Origin.X) + 5
	oTop := float32(outBar.Bounds.Origin.Y)
	oTrackH := float32(outBar.Bounds.Size.Height)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(oX, oTop+8)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(oX, oTop+oTrackH*0.4), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(oX, oTop+oTrackH*0.4)})
	a.Handle.Window.Draw()
	if got := read().debug.output.Top().AsF32(); got <= 0 {
		demo.Fatal("editor", fmt.Errorf("output scrollbar drag did not scroll (top=%.0f)", got))
	}
	if read().debug.follow {
		demo.Fatal("editor", fmt.Errorf("output scrollbar drag did not cancel the tail-follow"))
	}
	frame = out("editor_10_debug_output.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	clickHitbox(hw, a, "n:btn-debug-tab-Breakpoints")
	a.Handle.Window.Draw()
	frame = out("editor_11_debug_breakpoints.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// Problems panel: its strip resizes the dock the same way.
	update(func(ws *Workspace) { ws.ShowDebug = false; ws.ShowProblems = true })
	a.Handle.Window.Draw()
	termH3, _ := read().DebugPanelSizes()
	dragStrip(a, "n:btn-problems-resize", -50)
	a.Handle.Window.Draw()
	termH4, _ := read().DebugPanelSizes()
	if termH4 < termH3+40 || termH4 > termH3+60 {
		demo.Fatal("editor", fmt.Errorf("problems panel strip drag did not resize the dock: %.0f -> %.0f", termH3, termH4))
	}
	frame = out("editor_12_problems.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)
	update(func(ws *Workspace) { ws.ShowProblems = false })

	// Left tool panel (structure): its inner-edge strip resizes the panel
	// the same way the project tree's grip does.
	update(func(ws *Workspace) { ws.selectLeftTool("structure", update, a.Handle.Window) })
	a.Handle.Window.Draw()
	_, treeW0 := read().DebugPanelSizes()
	dragStripX(a, "n:btn-tool-left-resize", 50)
	a.Handle.Window.Draw()
	_, treeW1 := read().DebugPanelSizes()
	if treeW1 < treeW0+40 || treeW1 > treeW0+60 {
		demo.Fatal("editor", fmt.Errorf("structure panel strip drag did not resize the panel: %.0f -> %.0f", treeW0, treeW1))
	}
	frame = out("editor_13_structure.png")
	must(savePNG(hw, frame))
	verifyInk(hw, frame)

	// Shutdown race: the offline session must close like a real one.
	update(func(ws *Workspace) {
		ws.selectLeftTool("structure", update, a.Handle.Window)
		if s := ws.debug.session; s != nil {
			s.Stop()
		}
	})
	select {
	case <-time.After(2 * time.Second):
		demo.Fatal("editor", fmt.Errorf("offline debug session did not close on Stop"))
	default:
	}
}

// hitboxByKey finds a rendered hitbox by its key.
func hitboxByKey(a *demo.App[Workspace], key string) (gpui.Hitbox, bool) {
	for _, hb := range a.Handle.Window.RenderedHitboxes() {
		if hb.Key == key {
			return hb, true
		}
	}
	return gpui.Hitbox{}, false
}

func centerOf(hb gpui.Hitbox) geometry.Point[geometry.Pixels] {
	return geometry.PointPixels(
		float32(hb.Bounds.Origin.X)+float32(hb.Bounds.Size.Width)/2,
		float32(hb.Bounds.Origin.Y)+float32(hb.Bounds.Size.Height)/2)
}

// dragStrip drags a bottom dock's resize strip vertically by dy (negative
// grows the dock): press, move, release. The strip rides the panel's top
// edge, so the press lands on the hitbox center and slides from there.
func dragStrip(a *demo.App[Workspace], key string, dy float32) {
	hb, ok := hitboxByKey(a, key)
	if !ok {
		demo.Fatal("editor", fmt.Errorf("resize strip %q not found", key))
	}
	c := centerOf(hb)
	hw := a.Headless.LastWindow()
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: c})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(c.X.AsF32(), c.Y.AsF32()+dy), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(c.X.AsF32(), c.Y.AsF32()+dy)})
	a.Handle.Window.Draw()
}

// dragStripX drags a left panel's resize strip horizontally by dx.
func dragStripX(a *demo.App[Workspace], key string, dx float32) {
	hb, ok := hitboxByKey(a, key)
	if !ok {
		demo.Fatal("editor", fmt.Errorf("resize strip %q not found", key))
	}
	c := centerOf(hb)
	hw := a.Headless.LastWindow()
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: c})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(c.X.AsF32()+dx, c.Y.AsF32()), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(c.X.AsF32()+dx, c.Y.AsF32())})
	a.Handle.Window.Draw()
}

func clickHitbox(hw *headless.Window, a *demo.App[Workspace], key string) {
	hb, ok := hitboxByKey(a, key)
	if !ok {
		demo.Fatal("editor", fmt.Errorf("hitbox %q not found", key))
	}
	click(hw, centerOf(hb))
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
