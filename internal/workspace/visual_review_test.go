package workspace_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestVisualReviewScenes renders the review scenes for the manual visual
// check: the user's session (project tree open, two tabs, HiDPI 1.25) with
// the editor at the top and scrolled.
func TestVisualReviewScenes(t *testing.T) {
	out := os.Getenv("EDITOR_SHOT_DIR")
	if out == "" {
		out = t.TempDir()
	}

	var sb strings.Builder
	sb.WriteString("// Flint demo buffer\n//\n// Run it:\n//   go run .\n\npackage main\n\nimport \"fmt\"\n\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&sb, "// line %d: the quick brown fox jumps over the lazy dog 0123456789\n", i)
	}
	sb.WriteString("func main() {\n\tfmt.Println(\"hi\")\n}\n")

	hp := headless.New(geometry.SizePixels(960, 600))
	appl := gpui.NewApplication(hp)
	handle, _ := gpui.OpenWindow[workspace.Workspace](appl,
		platform.WindowOptions{Title: "review", ScaleFactor: 1.25},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws := workspace.New("go-gpui")
			ws.ShowFPS = true
			ws.Measure(w)
			w.Focus(ws.FocusEditor)
			return *ws
		})
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		v := handle.View.Read(cx)
		return v.Render(&handle, cx, w)
	})
	handle.Window.Draw()

	hw := hp.LastWindow()
	hw.Resize(geometry.SizePixels(1031, 644.8))
	editorUpdate(&handle, appl.App, func(s *workspace.Workspace) {
		if err := s.OpenFolder("../.."); err != nil {
			t.Logf("open folder: %v", err)
		}
		expandAllRows(s.Tree)
		s.Buffers = []*editor.Buffer{
			editor.NewBuffer("", "main.go", sb.String()),
			editor.NewBuffer("", "buffer.go", "package editor\n\nfunc Title(s string) string {\n\treturn s\n}\n"),
		}
		s.Active = 0
	})
	handle.Window.Draw()
	png, err := hw.PNG()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out+"/review_top.png", png, 0o644); err != nil {
		t.Fatal(err)
	}

	// Scrolled: the minimap viewport slides mid-rail.
	editorUpdate(&handle, appl.App, func(s *workspace.Workspace) { s.SetScrolls(10) })
	handle.Window.Draw()
	png, err = hw.PNG()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out+"/review_scrolled.png", png, 0o644); err != nil {
		t.Fatal(err)
	}
	fmt.Println("review scenes written to", out)
}
