package workspace_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"
)

// TestTreeFixtureFrames renders the tree-perf round's fixture frames: a
// real git repository whose `huge/` folder holds 150 files, half of them
// modified in the working tree, expanded in the project tree.
func TestTreeFixtureFrames(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "huge")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 150; i++ {
		body := fmt.Sprintf("package huge\n\nfunc f%03d() int { return %d }\n", i, i*i)
		if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("file%03d.go", i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-qm", "init")
	// Half the files modified in the working tree (amber M), two new (green U).
	for i := 0; i < 150; i += 2 {
		p := filepath.Join(sub, fmt.Sprintf("file%03d.go", i))
		body, _ := os.ReadFile(p)
		os.WriteFile(p, append(body, []byte("// edited in the working tree\n")...), 0o644)
	}
	os.WriteFile(filepath.Join(sub, "brand_new.go"), []byte("package huge\n"), 0o644)
	os.WriteFile(filepath.Join(sub, "another_new.go"), []byte("package huge\n"), 0o644)

	hp, handle, cx := openTreeWindow(t, dir, false)
	_ = hp
	ws := handle.View.Read(cx)
	ws.OpenFile(filepath.Join(dir, "main.go"))
	subNode := ws.Tree.Find(sub)
	if subNode == nil {
		t.Fatal("huge folder missing from the tree")
	}
	subNode.Expanded = true
	subNode.EnsureLoaded()
	handle.Window.Draw()
	handle.Window.Draw() // second frame lands the inline git coloring

	// Frame 1: the expanded folder, top of the list.
	editorShot(t, hp, "/tmp/render", "treeperf_01_expanded.png")

	// Frame 2: wheel deep into the folder - rows stay pixel-aligned and the
	// git colors follow.
	hw := hp.LastWindow()
	for i := 0; i < 12; i++ {
		hw.DispatchInput(platform.ScrollEvent{Position: geometry.PointPixels(120, 300), Delta: platform.ScrollDeltaPixels(0, -120)})
	}
	handle.Window.Draw()
	editorShot(t, hp, "/tmp/render", "treeperf_02_scrolled.png")

	// Frame 3: scroll to the very bottom - the last files render there.
	for i := 0; i < 40; i++ {
		hw.DispatchInput(platform.ScrollEvent{Position: geometry.PointPixels(120, 300), Delta: platform.ScrollDeltaPixels(0, -1200)})
	}
	handle.Window.Draw()
	editorShot(t, hp, "/tmp/render", "treeperf_03_bottom.png")
}
