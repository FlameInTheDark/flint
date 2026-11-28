package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// dndProject builds a temp project for the tree tests: two dot entries a
// user must see (.github with a workflow inside, .golangci.yml), the always
// hidden .git, a plain file and a plain directory to move things into.
func dndProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".github/workflows/ci.yml", "on: push\n")
	write(".golangci.yml", "run:\n")
	write(".git/config", "[core]\n")
	write("main.go", "package main\n")
	write("note.txt", "hello\n")
	return dir
}

// TestTreeShowsDotfilesExceptGit pins the visibility rules: every dot
// entry (like .github) is listed and interactable, only .git stays hidden.
func TestTreeShowsDotfilesExceptGit(t *testing.T) {
	root := NewTree(dndProject(t))
	names := map[string]*TreeNode{}
	for _, c := range root.Children {
		names[c.Name] = c
	}
	for _, want := range []string{".github", ".golangci.yml", "main.go", "pkg"} {
		if names[want] == nil {
			t.Fatalf("%s missing from the tree (children: %d)", want, len(root.Children))
		}
	}
	if names[".git"] != nil {
		t.Fatal(".git must stay hidden")
	}
	gh := names[".github"]
	if gh == nil || !gh.IsDir {
		t.Fatal(".github must be a directory node")
	}
	gh.Expanded = true
	gh.EnsureLoaded()
	if len(gh.Children) != 1 || gh.Children[0].Name != "workflows" {
		t.Fatalf(".github children = %v", gh.Children)
	}
}

// TestTreeDropRules pins the drop-target semantics and the move guards.
func TestTreeDropRules(t *testing.T) {
	dir := dndProject(t)
	root := NewTree(dir)
	root.Expanded = true
	root.EnsureLoaded()
	var main, pkg *TreeNode
	for _, c := range root.Children {
		switch c.Name {
		case "main.go":
			main = c
		case "pkg":
			pkg = c
		}
	}
	if main == nil || pkg == nil {
		t.Fatal("fixture nodes missing")
	}
	// Onto a directory = move INTO it; onto a file = into its parent; onto
	// the background = the project root.
	if d := treeDropDir(pkg, root); d != pkg {
		t.Fatal("dropping onto a directory must move into it")
	}
	if d := treeDropDir(main, root); d != root {
		t.Fatal("dropping onto a file must move into the file's parent")
	}
	if d := treeDropDir(nil, root); d != root {
		t.Fatal("dropping on the background must move to the root")
	}
	// Valid move: main.go into pkg/.
	if !treeDropValid(main, pkg) {
		t.Fatal("moving a file into a directory must be valid")
	}
	// Invalid: a directory into itself / its own subtree.
	if treeDropValid(pkg, pkg) {
		t.Fatal("moving a directory into itself must be invalid")
	}
	var nested *TreeNode
	pkg.Expanded = true
	pkg.EnsureLoaded()
	os.WriteFile(filepath.Join(pkg.Path, "inner.go"), []byte("package pkg\n"), 0o644)
	pkg.Reload()
	for _, c := range pkg.Children {
		if c.Name == "inner.go" {
			nested = c
		}
	}
	if nested == nil {
		t.Fatal("fixture nested node missing")
	}
	if treeDropValid(pkg, pkg) || func() bool {
		// pkg into its own subtree: the target dir is nested's parent = pkg —
		// caught by the same-parent guard? No: nested is a FILE. Build the
		// real case: a subdirectory of pkg as destination.
		sub := filepath.Join(pkg.Path, "sub")
		os.Mkdir(sub, 0o755)
		pkg.Reload()
		for _, c := range pkg.Children {
			if c.Name == "sub" {
				return treeDropValid(pkg, c)
			}
		}
		return false
	}() {
		t.Fatal("moving a directory into its own subtree must be invalid")
	}
	// Invalid: same parent (no-op move).
	if treeDropValid(nested, pkg) {
		t.Fatal("moving onto the current parent must be a no-op")
	}
	// Invalid: destination name taken.
	if treeDropValid(main, func() *TreeNode {
		os.WriteFile(filepath.Join(root.Path, "main.go.copy"), []byte("x\n"), 0o644)
		return root
	}()) {
		// root itself is the current parent — guarded before the name check;
		// exercise the name-taken guard with a fresh directory instead.
	}
	taken := filepath.Join(root.Path, "taken")
	os.Mkdir(taken, 0o755)
	root.Reload()
	var takenNode *TreeNode
	os.WriteFile(filepath.Join(taken, "main.go"), []byte("dup\n"), 0o644)
	takenNode = root.Find(taken)
	if takenNode == nil {
		t.Fatal("fixture taken dir missing")
	}
	takenNode.EnsureLoaded()
	if treeDropValid(main, takenNode) {
		t.Fatal("a destination with the same entry name must be invalid")
	}
}

// TestMoveTreeNodeMovesFiles exercises the real move: the file lands in the
// destination on disk, both directories reload and an open buffer under the
// moved path is re-pointed.
func TestMoveTreeNodeMovesFiles(t *testing.T) {
	dir := dndProject(t)
	ws := New("dnd")
	ws.Root = dir
	ws.Tree = NewTree(dir)
	ws.Tree.Expanded = true
	ws.Tree.EnsureLoaded()
	var main, pkg *TreeNode
	for _, c := range ws.Tree.Children {
		switch c.Name {
		case "main.go":
			main = c
		case "pkg":
			pkg = c
		}
	}
	if main == nil || pkg == nil {
		t.Fatal("fixture nodes missing")
	}
	b := editor.NewBuffer("", "main.go", "package main\n")
	b.Path = main.Path
	ws.Buffers = append(ws.Buffers, b)

	ws.moveTreeNode(main, pkg)

	newPath := filepath.Join(pkg.Path, "main.go")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("file not moved: %v", err)
	}
	if _, err := os.Stat(main.Path); !os.IsNotExist(err) {
		t.Fatal("the source file still exists at the old path")
	}
	if b.Path != newPath {
		t.Fatalf("open buffer path = %q, want %q", b.Path, newPath)
	}
	if !pkg.Expanded {
		t.Fatal("the destination directory must expand after the drop")
	}
	var moved *TreeNode
	for _, c := range pkg.Children {
		if c.Name == "main.go" {
			moved = c
		}
	}
	if moved == nil {
		t.Fatal("the reloaded destination directory does not list the moved file")
	}
	// Move back to the project root (dropping onto the background).
	ws.moveTreeNode(moved, ws.Tree)
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		t.Fatalf("move back to root failed: %v", err)
	}
	if b.Path != filepath.Join(dir, "main.go") {
		t.Fatalf("buffer path after the root move = %q", b.Path)
	}
}

// openDndWindow opens a headless window with the project tree visible.
func openDndWindow(t *testing.T, dir string) (*headless.Platform, *gpui.WindowHandle[Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](appl, platform.WindowOptions{Title: "tree-dnd"},
		func(w *gpui.Window, cx *app.Context[Workspace]) Workspace {
			ws := New("tree-dnd")
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

// rowCenter finds a tree row's window position by its hitbox key.
func rowCenter(t *testing.T, handle *gpui.WindowHandle[Workspace], path string) geometry.Point[geometry.Pixels] {
	t.Helper()
	for _, hb := range handle.Window.RenderedHitboxes() {
		if hb.Key == "n:btn-tree-"+path {
			b := hb.Bounds
			return geometry.PointPixels(b.Origin.X.AsF32()+b.Size.Width.AsF32()/2, b.Origin.Y.AsF32()+b.Size.Height.AsF32()/2)
		}
	}
	t.Fatalf("tree row %q not rendered", path)
	return geometry.Point[geometry.Pixels]{}
}

// TestTreeDragDropsFilesIntoDirectories drives the real gesture: press on a
// file row, drag onto a directory row, release — the file moves. A
// press-release without travel stays a click (the file opens).
func TestTreeDragDropsFilesIntoDirectories(t *testing.T) {
	dir := dndProject(t)
	hp, handle, appCtx := openDndWindow(t, dir)
	hw := hp.LastWindow()
	_ = appCtx

	notePath := filepath.Join(dir, "note.txt")
	pkgPath := filepath.Join(dir, "pkg")
	src := rowCenter(t, handle, notePath)
	dst := rowCenter(t, handle, pkgPath)

	// The drag: press on note.txt, sweep onto pkg/, release.
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: src})
	steps := 8
	for i := 1; i <= steps; i++ {
		x := src.X.AsF32() + (dst.X.AsF32()-src.X.AsF32())*float32(i)/float32(steps)
		y := src.Y.AsF32() + (dst.Y.AsF32()-src.Y.AsF32())*float32(i)/float32(steps)
		btn := platform.ButtonLeft
		hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(x, y), PressedButton: &btn})
	}
	if read := handle.View.Read(appCtx); read.TreeDrag == nil || !read.TreeDrag.armed {
		t.Fatal("the drag did not arm")
	}
	if read := handle.View.Read(appCtx); read.TreeDrag != nil && read.TreeDrag.target != nil && read.TreeDrag.target.Path != pkgPath {
		t.Fatalf("drag target = %q, want pkg", read.TreeDrag.target.Path)
	}
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: dst})
	if _, err := os.Stat(filepath.Join(pkgPath, "note.txt")); err != nil {
		t.Fatalf("note.txt not moved into pkg: %v", err)
	}
	if _, err := os.Stat(notePath); !os.IsNotExist(err) {
		t.Fatal("note.txt still at the source path")
	}
	if read := handle.View.Read(appCtx); read.TreeDrag != nil {
		t.Fatal("drag state not cleared after the drop")
	}

	// The click play-back: press and release on main.go without travel
	// opens the file (no accidental moves from sloppy clicks).
	mainPath := filepath.Join(dir, "main.go")
	rc := rowCenter(t, handle, mainPath)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: rc})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: rc})
	read := handle.View.Read(appCtx)
	if read.Buf() == nil || read.Buf().Path != mainPath {
		t.Fatalf("click play-back opened %v, want main.go", read.Buf())
	}
}
