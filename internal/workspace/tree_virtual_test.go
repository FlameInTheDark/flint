package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// bigGitFolderProject builds a temp project whose single folder holds
// `files` entries — the user's "expand a folder with many files and git
// changes" scenario — and reports the tree paths of its first and last
// entries (the tree lists dirs before files, so the last file sorts last).
func bigGitFolderProject(t *testing.T, files int) (dir, firstPath, lastPath string) {
	t.Helper()
	dir = t.TempDir()
	sub := filepath.Join(dir, "huge")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < files; i++ {
		if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("file%04d.go", i)), []byte("package huge\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644)
	firstPath = filepath.Join(sub, "file0000.go")
	lastPath = filepath.Join(sub, fmt.Sprintf("file%04d.go", files-1))
	return dir, firstPath, lastPath
}

// treeRowHitboxes counts the rendered project-tree row hitboxes.
func treeRowHitboxes(handle *gpui.WindowHandle[Workspace]) int {
	n := 0
	for _, hb := range handle.Window.RenderedHitboxes() {
		if len(hb.Key) > 11 && hb.Key[:11] == "n:btn-tree-" {
			n++
		}
	}
	return n
}

// TestTreeVirtualizesLargeFolders pins the project tree's viewport
// virtualization: expanding a folder with hundreds of files must NOT build
// a hitbox per row (that used to cost ~160ms per frame and made the panel
// freeze), yet scrolling to the bottom must still bring up the last rows
// and clicking one must open the file. The tree also carries git coloring
// (the user's report mentioned git changes in the expanded folder).
func TestTreeVirtualizesLargeFolders(t *testing.T) {
	const files = 600
	dir, firstPath, lastPath := bigGitFolderProject(t, files)
	buf := editor.NewBuffer(filepath.Join(dir, "main.go"), "main.go", "package main\n")
	hp, handle, ap := openLeftGapScene(t, true, buf)
	_ = hp
	if err := handle.View.Read(ap).OpenFolder(dir); err != nil {
		t.Fatal(err)
	}
	st := handle.View.Read(ap)
	st.ShowTree = true
	st.tools.left = ""
	// Expand the folder and fake the tree's git coloring (half the files
	// modified): the same shape collectTreeGit produces after a refresh.
	sub := st.Tree.Find(filepath.Join(dir, "huge"))
	if sub == nil {
		t.Fatal("huge folder missing from the tree")
	}
	sub.Expanded = true
	sub.EnsureLoaded()
	st.treeGit = &treeGitState{repo: dir, tried: true,
		files: map[string]gitFileState{}, dirs: map[string]gitFileState{}}
	for i := 0; i < files; i += 2 {
		p := fmt.Sprintf("%s/file%04d.go", sub.Path, i)
		st.treeGit.files[p] = gitModified
		colorDirs(st.treeGit.dirs, dir, filepath.Dir(p), gitModified)
	}
	handle.Window.Draw()

	// 600 expanded rows but only a viewport's worth of hitboxes.
	if total := len(st.Tree.Find(sub.Path).Children); total != files {
		t.Fatalf("folder has %d children, want %d", total, files)
	}
	if boxes := treeRowHitboxes(handle); boxes > 130 {
		t.Fatalf("virtualization leaks: %d row hitboxes rendered for a %d-row tree", boxes, files+2)
	}

	// The first file is inside the initial viewport and clickable.
	if boxes := treeRowHitboxes(handle); boxes == 0 {
		t.Fatal("no tree rows rendered at all")
	}

	// Wheel far down: the window of rendered rows follows the scroll, the
	// first file leaves the element tree and the last one enters it.
	hw := hp.LastWindow()
	// 600 rows * 24px ≈ 14.4k px of content: 15 * 1200px reaches the bottom.
	pos := geometry.PointPixels(120, 300)
	for i := 0; i < 15; i++ {
		hw.DispatchInput(platform.ScrollEvent{Position: pos, Delta: platform.ScrollDeltaPixels(0, -1200)})
	}
	handle.Window.Draw()
	for _, hb := range handle.Window.RenderedHitboxes() {
		if hb.Key == "n:btn-tree-"+firstPath {
			t.Fatal("the first file is still rendered after scrolling to the bottom")
		}
	}
	lastBounds, found := findHitbox(handle, "n:btn-tree-"+lastPath)
	if !found {
		t.Fatal("the last file did not render after scrolling to the bottom")
	}

	// Click the last row: a press-release without travel opens the file.
	center := geometry.PointPixels(
		lastBounds.Origin.X.AsF32()+lastBounds.Size.Width.AsF32()/2,
		lastBounds.Origin.Y.AsF32()+lastBounds.Size.Height.AsF32()/2)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: center})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: center})
	handle.Window.Draw()
	if b := handle.View.Read(ap).Buf(); b == nil || b.Path != lastPath {
		got := ""
		if b != nil {
			got = b.Path
		}
		t.Fatalf("clicking the last row opened %q, want %q", got, lastPath)
	}
}

// findHitbox returns the rendered hitbox bounds with the given key
// (the ok=false case means the key is not rendered this frame).
func findHitbox(handle *gpui.WindowHandle[Workspace], key string) (b geometry.Bounds[geometry.Pixels], ok bool) {
	for _, hb := range handle.Window.RenderedHitboxes() {
		if hb.Key == key {
			return hb.Bounds, true
		}
	}
	return b, false
}
