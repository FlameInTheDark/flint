package workspace_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestBacktickRawStringScene pins the reported bug: the demo seeds in
// run.go are backtick raw strings, and every line between the opening and
// the closing backtick must tokenize as one string — not as Go code —
// both in the buffer's runs and on screen (PNGs for the manual review).
func TestBacktickRawStringScene(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("run.go"))
	if err != nil {
		t.Skipf("run.go not readable: %v", err)
	}
	buf := editor.NewBuffer("", "run.go", string(source))
	states := buf.LineStates("go")

	// Locate the demoMain and demoUtil raw-string blocks: the seed line
	// opens the literal, the next line containing a backtick closes it.
	type block struct{ open, close int }
	var blocks []block
	for i, line := range buf.Lines {
		if strings.Contains(line, "= `") {
			for j := i + 1; j < len(buf.Lines); j++ {
				if strings.Contains(buf.Lines[j], "`") {
					blocks = append(blocks, block{open: i, close: j})
					break
				}
			}
		}
	}
	if len(blocks) < 2 {
		t.Fatalf("expected at least two raw-string seeds, found %d", len(blocks))
	}
	pal := editor.PaletteOfTheme(ui.DefaultTheme())
	for bi, b := range blocks {
		if b.close <= b.open {
			t.Fatalf("block %d has a broken range: %+v", bi, b)
		}
		if states[b.open+1] != editor.StateRawString || states[b.close] != editor.StateRawString {
			t.Fatalf("block %d lost its raw-string state: first=%d close=%d", bi, states[b.open+1], states[b.close])
		}
		if b.close+1 < len(states) && states[b.close+1] != editor.StateNormal {
			t.Fatalf("block %d context leaks past the closing backtick: %d", bi, states[b.close+1])
		}
		for i := b.open + 1; i < b.close; i++ {
			runs, _ := editor.HighlightCachedIn(buf.Line(i), "go", states[i], pal)
			n := 0
			for _, r := range runs {
				n += r.Len
				if r.Color != pal.String {
					t.Fatalf("line %d inside raw string %d has a non-string run: %q -> %+v", i, bi, buf.Line(i), runs)
				}
			}
			if n != len(buf.Line(i)) {
				t.Fatalf("line %d runs cover %d of %d bytes", i, n, len(buf.Line(i)))
			}
		}
		// The closing line starts inside the string: its first run (through
		// the closing backtick) is string-colored; the rest may be code.
		runs, end := editor.HighlightCachedIn(buf.Line(b.close), "go", states[b.close], pal)
		if len(runs) == 0 || runs[0].Color != pal.String || end != editor.StateNormal {
			t.Fatalf("closing line %d of block %d wrong: %+v (%d)", b.close, bi, runs, end)
		}
	}

	out := os.Getenv("EDITOR_SHOT_DIR")
	if out == "" {
		out = t.TempDir()
	}
	hp := headless.New(geometry.SizePixels(1548, 952))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl,
		platform.WindowOptions{Title: "backtick", ScaleFactor: 1.0},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws := workspace.New("go-gpui")
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

	// Frame 1: the demoMain block; frame 2: the demoUtil block.
	main := blocks[0]
	editorUpdate(&handle, appl.App, func(s *workspace.Workspace) {
		s.Buffers = []*editor.Buffer{buf}
		s.SetScrolls(main.open - 2)
	})
	handle.Window.Draw()
	editorShot(t, hp, out, "backtick_demoMain.png")

	util := blocks[1]
	editorUpdate(&handle, appl.App, func(s *workspace.Workspace) { s.SetScrolls(util.open - 2) })
	handle.Window.Draw()
	editorShot(t, hp, out, "backtick_demoUtil.png")
}
