package workspace_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/terminal"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// openTerminalScene opens the workspace with the terminal dock visible.
func openTerminalScene(t *testing.T) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl, platform.WindowOptions{Title: "terminal-scene"},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws := workspace.New("terminal-scene")
			ws.Measure(w)
			ws.TermSync = true
			ws.Buffers = workspace.BenchBuffers()
			ws.ShowTerminal = true
			ws.Term = newTestTerminal(t)
			w.Focus(ws.FocusTerminal)
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

func newTestTerminal(t *testing.T) *terminal.Terminal {
	t.Helper()
	term := terminal.New(t.TempDir())
	// Render scenes feed a deterministic grid: no shell spawns. The grid
	// is 24 rows tall and the pane shows its bottom window, so enough
	// filler lines precede the colored block for it to land in view.
	term.AutoStart = false
	var sb strings.Builder
	for i := 0; i < 24; i++ {
		sb.WriteString(fmt.Sprintf("filler row %02d of the synthetic scrollback\r\n", i))
	}
	sb.WriteString("builder $ make\r\n" +
		"\x1b[32m> compile\x1b[0m internal/editor/editor.go\r\n" +
		"\x1b[33mwarn\x1b[0m: unused import \"fmt\" (12 lines)\r\n" +
		"\x1b[31merror\x1b[0m: undefined: FooBar at editor.go:1042:15\r\n" +
		"\x1b[1mbold tail\x1b[0m and a very long plain line that keeps going well past the typical terminal width so the soft wrap has something to chew on, ideally at least two whole rows of wrapped continuation text\r\n" +
		"\x1b[36m> link\x1b[0m go-gpui\r\n" +
		"exit code 0\r\n")
	term.Inject(sb.String())
	return term
}

func shot(t *testing.T, hp *headless.Platform, dir, name string) {
	t.Helper()
	hw := hp.LastWindow()
	png, err := hw.PNG()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), png, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVisualTerminalScene renders the terminal dock: colored scrollback,
// soft wrap, header; then drags the top edge to resize and renders again.
func TestVisualTerminalScene(t *testing.T) {
	hp, handle, cx := openTerminalScene(t)
	handle.Window.Draw()
	dir := "/tmp/render"
	shot(t, hp, dir, "terminal_scene.png")

	ws := handle.View.Read(cx)
	termH, _ := ws.DebugPanelSizes()
	t.Logf("term height %.0f", termH)

	// Drag the dock's top edge (the resize strip/header band) 80px UP: the
	// edge follows the pointer, so the dock grows. Dock top =
	// winH - status - termHeight.
	hw := hp.LastWindow()
	topY := 650.0 - ui.StatusHeight - termH
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(500, topY+2)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(500, topY-78), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(500, topY-78)})
	handle.Window.Draw()
	ws = handle.View.Read(cx)
	newH, _ := ws.DebugPanelSizes()
	if newH < termH+60 || newH > termH+100 {
		t.Fatalf("drag up did not grow the dock: %.0f -> %.0f", termH, newH)
	}
	t.Logf("after drag: term height %.0f", newH)
	shot(t, hp, dir, "terminal_resized.png")
}
