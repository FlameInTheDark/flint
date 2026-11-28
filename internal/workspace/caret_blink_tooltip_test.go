package workspace

import (
	"testing"
	"time"

	app "github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// TestDockTooltipSurvivesCaretBlink pins the live-app tooltip failure the
// earlier tests missed: the caret blink flipped a canvas element in and out
// of the editor's cursor row every 500ms, renumbering every positional
// element id AFTER the editor — the dock panels' interactive state keyed by
// that id path (hover, the tooltip's waiting phase) reset on each blink, and
// since the tooltip's own show delay is also 500ms, a stationary hover over
// any dock control (Profiler toolbar, flame cells, Vulnerabilities) never
// surfaced its tooltip in the live app.
//
// The fix keeps the caret overlay MOUNTED while hidden (only the paint
// skips), so a blink no longer restructures the tree. The test drives the
// exact live sequence: hover, arm, blink+draw across the delay, assert.
func TestDockTooltipSurvivesCaretBlink(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp := headless.New(geometry.SizePixels(1000, 650))
	a := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{Title: "caret-blink-tooltip"},
		func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
			ws := New("caret-blink-tooltip")
			ws.Measure(w)
			ws.Buffers = []*editor.Buffer{editor.NewBuffer("main.go", "main.go", "package main\n\nfunc main() {\n\tprintln(1)\n}\n")}
			ws.Active = 0
			ws.ensureScrollArrays()
			return *ws
		})
	if err != nil {
		t.Fatal(err)
	}
	hw := hp.LastWindow()
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element {
		return handle.View.Read(c).Render(&handle, c, w)
	})
	handle.Window.Draw()

	update := func(f func(*Workspace)) {
		handle.View.Update(a.App, func(s *Workspace, c *app.Context[Workspace]) any {
			f(s)
			c.Notify()
			return nil
		})
	}
	update(func(ws *Workspace) {
		ws.ShowProfile = true
		if ws.prof.status == "" {
			ws.prof.status = "test · samples · 1 count"
		}
	})
	handle.Window.Draw()

	find := func(key string) geometry.Bounds[geometry.Pixels] {
		t.Helper()
		for _, hb := range handle.Window.RenderedHitboxes() {
			if hb.Key == key {
				return hb.Bounds
			}
		}
		t.Fatalf("hitbox %q not found", key)
		return geometry.Bounds[geometry.Pixels]{}
	}

	// Hover the Profiler's capture button.
	hb := find("n:btn-prof-capture")
	hoverCenter(hw, hb)
	handle.Window.Draw() // arm the tooltip's waiting state (now+500ms)

	// The live app blinks the caret BETWEEN frames while the pointer rests.
	for i := 0; i < 3; i++ {
		update(func(ws *Workspace) { ws.TickCaretBlink() })
		handle.Window.Draw()
	}
	time.Sleep(600 * time.Millisecond)
	handle.Window.Draw() // past the delay: this paint must surface the tooltip
	if !handle.Window.TooltipVisible() {
		t.Fatal("the caret blink wiped the dock tooltip: hovering the Profiler capture button for >500ms showed nothing")
	}
}
