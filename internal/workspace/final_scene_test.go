package workspace_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestVisualFinalScene renders the whole app with a real folder open (many
// tree entries), several tabs and a long buffer — the final visual check.
func TestVisualFinalScene(t *testing.T) {
	dir, _ := os.MkdirTemp("", "finalproj")
	defer os.RemoveAll(dir)
	var big strings.Builder
	big.WriteString("package main\n\n// App entry\nfunc main() {\n\tprintln(\"hi\")\n}\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&big, "// filler line %d: value = %d\n", i+1, i*i)
	}
	os.WriteFile(filepath.Join(dir, "big.go"), []byte(big.String()), 0o644)
	for i := 0; i < 25; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("module_%02d.go", i)), []byte("package x\n"), 0o644)
	}
	for f := 0; f < 2; f++ {
		sub := filepath.Join(dir, fmt.Sprintf("pkg%d", f))
		os.Mkdir(sub, 0o755)
		for i := 0; i < 10; i++ {
			os.WriteFile(filepath.Join(sub, fmt.Sprintf("file%02d.go", i)), []byte("package y\n"), 0o644)
		}
	}

	hp, handle, cx := openTreeWindow(t, dir, true)
	_ = hp
	// A few more tabs so the strip shows both sparse and crowded states.
	editorUpdate(handle, cx, func(s *workspace.Workspace) {
		for _, name := range []string{"module_00.go", "module_01.go", "module_02.go", "pkg0/file00.go"} {
			s.OpenFile(filepath.Join(dir, name))
		}
	})
	handle.Window.Draw()
	handle.Window.Draw()
	editorShot(t, hp, "/tmp/render", "final_full.png")
}
