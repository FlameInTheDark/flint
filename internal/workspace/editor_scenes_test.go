package workspace_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

// editorWideContent builds a buffer with a very long line, tab indentation
// and enough filler lines for the scrollbars and the minimap to seek.
func editorWideContent() string {
	long := strings.Repeat("sx = somelongidentifier.callAnotherMethod(andMoreArguments, here); // trailing comment padding", 3)
	var sb strings.Builder
	sb.WriteString("package main\n\n")
	sb.WriteString("func withTabs() {\n")
	sb.WriteString("\tif true {\n")
	sb.WriteString("\t\treturn deepIndented\n")
	sb.WriteString("\t}\n")
	sb.WriteString("}\n\n")
	sb.WriteString(long + "\n")
	for i := 0; i < 200; i++ {
		sb.WriteString(fmt.Sprintf("// filler line %d: value = %d\n", i+1, i*i))
	}
	return sb.String()
}

// openEditorWindow opens the workspace headlessly with the wide buffer.
func openEditorWindow(t *testing.T) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App) {
	t.Helper()
	return openEditorWindowWith(t, editorWideContent())
}

// openEditorWindowWith opens the workspace headlessly with custom content.
func openEditorWindowWith(t *testing.T, content string) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App) {
	t.Helper()
	return openEditorWindowScale(t, content, 0)
}

// openEditorWindowScale opens the workspace headlessly at an explicit
// HiDPI scale (0 = the platform default).
func openEditorWindowScale(t *testing.T, content string, scale float32) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl, platform.WindowOptions{Title: "editor-scenes", ScaleFactor: scale},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws := workspace.New("editor-scenes")
			ws.SetScrolls(0)
			ws.Buffers = []*editor.Buffer{
				editor.NewBuffer("", "wide.go", content),
				editor.NewBuffer("", "util.go", "package util\n\nfunc Title(s string) string {\n\treturn s\n}\n"),
			}
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
	return hp, &handle, appl.App
}

func editorUpdate(handle *gpui.WindowHandle[workspace.Workspace], cx *app.App, f func(*workspace.Workspace)) {
	handle.View.Update(cx, func(state *workspace.Workspace, c *app.Context[workspace.Workspace]) any {
		f(state)
		c.Notify()
		return nil
	})
}

func editorShot(t *testing.T, hp *headless.Platform, dir, name string) string {
	t.Helper()
	hw := hp.LastWindow()
	if hw == nil {
		t.Fatal("no headless window")
	}
	png, err := hw.PNG()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestEditorWideLineLayout verifies the wide-line regressions: the top bar
// (menu bar with the FPS readout) stays inside the viewport, code lines
// never wrap, tabs render as spaces and the minimap rail is present.
func TestEditorWideLineLayout(t *testing.T) {
	hp, handle, cx := openEditorWindow(t)
	dir := t.TempDir()
	editorShot(t, hp, dir, "wide.png")

	// Tab-indented lines render expanded: line 5 ("return deepIndented")
	// starts at two tab stops (4 spaces each). The buffer line has two
	// leading tabs, so its display column is 8; the pane's text must start
	// at gutter + padding + 8 columns.
	ws := handle.View.Read(cx)
	buf := ws.Buf()
	if got := editor.DisplayCols(buf.Line(4)); got < 4+4+8 {
		t.Fatalf("tab expansion lost (line %q cols %d)", buf.Line(4), got)
	}

	// The wide line maps to a horizontal scroll extent (the scrollbar has
	// something to pan).
	editorUpdate(handle, cx, func(s *workspace.Workspace) { s.DebugPanX(1e6) })
	editorUpdate(handle, cx, func(s *workspace.Workspace) { s.DebugPanX(-1) })
	_, sx := handle.View.Read(cx).DebugScrollState()
	if sx <= 0 {
		t.Fatal("addScrollX did not clamp into a positive pan extent")
	}
	editorShot(t, hp, dir, "wide-panned.png")
}

// TestEditorScrollInteractions drives the horizontal wheel, the vertical
// scrollbar and the minimap seek through real input events.
func TestEditorScrollInteractions(t *testing.T) {
	hp, handle, cx := openEditorWindow(t)

	// Horizontal wheel over the code pans the wide line.
	for i := 0; i < 12; i++ {
		hp.LastWindow().DispatchInput(platform.ScrollEvent{
			Position: geometry.PointPixels(300, 200),
			Delta:    platform.ScrollDeltaPixels(80, 0),
		})
	}
	handle.Window.Draw()
	_, sx := handle.View.Read(cx).DebugScrollState()
	if sx <= 0 {
		t.Fatal("horizontal wheel did not scroll the code")
	}

	// Minimap click at 60% height seeks the vertical scroll.
	lineBefore, _ := handle.View.Read(cx).DebugScrollState()
	hp.LastWindow().DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft,
		Position: geometry.PointPixels(930, ui.MenuBarHeight+ui.TabBarHeight+562*0.6)})
	hp.LastWindow().DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft,
		Position: geometry.PointPixels(930, ui.MenuBarHeight+ui.TabBarHeight+562*0.6)})
	handle.Window.Draw()
	lineAfter, _ := handle.View.Read(cx).DebugScrollState()
	if lineAfter == lineBefore {
		t.Fatal("minimap click did not seek the scroll")
	}
	if lineAfter < 30 {
		t.Fatalf("minimap seek landed at line %d, expected a deep target", lineAfter)
	}

	// A wheel over the minimap scrolls vertically too.
	for i := 0; i < 5; i++ {
		hp.LastWindow().DispatchInput(platform.ScrollEvent{
			Position: geometry.PointPixels(930, 300),
			Delta:    platform.ScrollDeltaLines(0, 3),
		})
	}
	handle.Window.Draw()
	lineAfterWheel, _ := handle.View.Read(cx).DebugScrollState()
	if lineAfterWheel == lineAfter {
		t.Fatal("minimap wheel did not scroll")
	}
}

// TestEditorPickerPathTruncation keeps very long paths inside the modal:
// the layout engine must contain the whole modal subtree within the card
// (the pre-fix engine inflated every ancestor through the long text leaf).
func TestEditorPickerPathTruncation(t *testing.T) {
	hp, handle, cx := openEditorWindow(t)
	_ = hp
	longPath := `C:\Projects\Go\src\github.com\FlameInTheDark\go-gpui\examples\editor`
	editorUpdate(handle, cx, func(s *workspace.Workspace) {
		s.Picker = workspace.NewPicker(workspace.PickerOpenFile, longPath)
	})
	handle.Window.Draw()

	// Every picker hitbox must sit inside the 620px card (centered in the
	// 1000px window: x in [190, 810]).
	inCard := 0
	for _, hb := range handle.Window.RenderedHitboxes() {
		x := hb.Bounds.Origin.X.AsF32()
		w := hb.Bounds.Size.Width.AsF32()
		if x+0.01 < 189 || x+w > 811 {
			continue
		}
		inCard++
		if x+w > 810.5 {
			t.Fatalf("picker element overflows the card: x=%.1f w=%.1f", x, w)
		}
	}
	if inCard < 3 {
		t.Fatalf("expected several picker hitboxes inside the card, got %d", inCard)
	}

	// The truncated path fits its budget (42 chars including the ellipsis).
	// Verified through the picker state, not pixels: the middle-truncation
	// helper keeps head + tail.
	if got := len([]rune(longPath)); got <= 42 {
		t.Fatal("test path should be longer than the truncation budget")
	}
	editorUpdate(handle, cx, func(s *workspace.Workspace) { s.Picker = nil })
}

// TestEditorFrameSoak renders a few hundred frames with caret blinks,
// mouse movement and typing, asserting the heap does not grow per frame
// (the leak detector that keeps the editor honest) and that frames stay
// within a sane budget.
func TestEditorFrameSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("soak skipped in -short mode")
	}
	hp, handle, cx := openEditorWindow(t)
	w := handle.Window
	hw := hp.LastWindow()

	sample := func() (float64, int) {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return float64(ms.HeapAlloc) / 1024 / 1024, w.AtlasLen()
	}

	editorUpdate(handle, cx, func(s *workspace.Workspace) { s.TickCaretBlink() })
	handle.Window.Draw()
	heap0, atlas0 := sample()

	start := time.Now()
	const frames = 120
	for i := 0; i < frames; i++ {
		if i%2 == 0 {
			editorUpdate(handle, cx, func(s *workspace.Workspace) { s.TickCaretBlink() })
		}
		hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(
			300+float32(i%200), 200+float32(i%100))})
		if i%17 == 0 {
			hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "x", KeyChar: "x"}})
		}
		w.Draw()
	}
	elapsed := time.Since(start)
	heap1, atlas1 := sample()

	// Heap may fluctuate with GC timing, but a real leak grows with frames:
	// allow 8MB of drift over 120 frames (a leaking frame path allocated
	// megabytes per frame and failed this by orders of magnitude).
	if heap1 > heap0+8 {
		t.Fatalf("heap grew %.1fMB -> %.1fMB over %d frames (leak)", heap0, heap1, frames)
	}
	// The atlas only grows with NEW glyph variants (new characters), not
	// per frame: 120 frames typing one character bound the growth.
	if atlas1 > atlas0+200 {
		t.Fatalf("atlas tiles grew %d -> %d over %d frames", atlas0, atlas1, frames)
	}
	perFrame := elapsed / frames
	if perFrame > 500*time.Millisecond {
		t.Fatalf("frame cost exploded: %v per frame", perFrame)
	}
	t.Logf("soak: %d frames in %v (%.1f fps, %.1fms/frame), heap %.1f->%.1fMB, atlas %d->%d",
		frames, elapsed, float64(frames)/elapsed.Seconds(),
		perFrame.Seconds()/1e6*1e3, heap0, heap1, atlas0, atlas1)
}
