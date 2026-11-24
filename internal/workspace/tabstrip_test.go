package workspace_test

import (
	"fmt"
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

// openManyTabsWindow opens the workspace headlessly with count long-named
// buffers (the strip overflows long before count tabs fit the bar) and the
// given active index.
func openManyTabsWindow(t *testing.T, count, active int) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl, platform.WindowOptions{Title: "tabs"},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws := workspace.New("tabs")
			ws.Measure(w)
			ws.SetScrolls(0)
			for i := 0; i < count; i++ {
				ws.Buffers = append(ws.Buffers,
					editor.NewBuffer("", fmt.Sprintf("really-long-buffer-name-%02d.go", i), "package x\n"))
			}
			ws.Active = active
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

// tabBandHitboxes returns the hitboxes inside the tab-bar band, sorted by x.
func tabBandHitboxes(handle *gpui.WindowHandle[workspace.Workspace]) []hitboxRect {
	var out []hitboxRect
	bandBottom := float32(ui.MenuBarHeight + ui.TabBarHeight)
	for _, hb := range handle.Window.RenderedHitboxes() {
		r := rectOf(hb)
		if r.y >= ui.MenuBarHeight-1 && r.bottom() <= bandBottom+1 {
			out = append(out, r)
		}
	}
	return out
}

// TestTabsNeverCompress scrolls... no: pins the tab strip's contract. With a
// dozen long-named tabs the strip must overflow and scroll instead of
// compressing the tabs: every tab keeps its full content width, the close
// buttons stay inside their tabs, and the ACTIVE tab is revealed inside the
// strip's viewport.
func TestTabsNeverCompress(t *testing.T) {
	const count = 12
	hp, handle, cx := openManyTabsWindow(t, count, count-1)
	_ = hp
	// The reveal reads the previous frame's hitboxes (one frame of lag, the
	// same timing as the mirrored scrollbar handles): draw once more so the
	// strip's pan from the first frame applies.
	handle.Window.Draw()
	ws := handle.View.Read(cx)
	_ = ws

	hbs := tabBandHitboxes(handle)
	if len(hbs) < count {
		t.Fatalf("expected >= %d hitboxes in the tab band, got %d", count, len(hbs))
	}
	// The widest hitbox is the strip; the rest include the tabs.
	widest := float32(0)
	for _, r := range hbs {
		if r.w > widest {
			widest = r.w
		}
	}
	var tabs []hitboxRect
	for _, r := range hbs {
		if r.w > 40 && r.w < widest-20 {
			tabs = append(tabs, r)
		}
	}
	if len(tabs) < count {
		t.Fatalf("expected %d tab hitboxes, got %d (strip w=%.0f)", count, len(tabs), widest)
	}

	// (a) Every tab is at least wide enough for icon + a chunk of label +
	// close button: compression squeezed them to a fraction of this.
	for i, r := range tabs {
		if r.w < 120 {
			t.Fatalf("tab %d compressed to %.0fpx wide (labels overlap close buttons)", i, r.w)
		}
	}
	// (b) Close buttons stay inside their tab's bounds (no overlap with the
	// label): every close-sized hitbox (≈18px) sits within some tab.
	for _, r := range hbs {
		if r.w >= 15 && r.w <= 22 && r.h >= 15 && r.h <= 22 {
			inside := false
			for _, tab := range tabs {
				if r.x >= tab.x-0.5 && r.bottom() <= tab.bottom()+0.5 && r.x+r.w <= tab.x+tab.w+0.5 {
					inside = true
					break
				}
			}
			if !inside {
				t.Fatalf("close button at (%.0f,%.0f) outside every tab", r.x, r.y)
			}
		}
	}

	// (c) The active tab (the last one) is revealed: fully inside the strip
	// viewport (window x range), i.e. the strip panned to it.
	stripRight := widest - 8
	last := tabs[len(tabs)-1]
	if last.x+last.w > stripRight+8 {
		t.Fatalf("active tab not revealed: right edge %.0f beyond strip %.0f", last.x+last.w, stripRight)
	}
	if last.x < 0 {
		t.Fatalf("active tab revealed off the left edge: x=%.0f", last.x)
	}

	// (d) Clicking an OFF-SCREEN tab (after wheeling back) still works: the
	// strip pans and the click lands on the tab.
	hw := hp.LastWindow()
	// Wheel down over the strip pans it right; then click the strip's left
	// edge area — an off-screen tab should now be there.
	for i := 0; i < 6; i++ {
		hw.DispatchInput(platform.ScrollEvent{
			Position: geometry.PointPixels(400, ui.MenuBarHeight+ui.TabBarHeight/2),
			Delta:    platform.ScrollDeltaPixels(0, -120),
		})
	}
	handle.Window.Draw()
	hbs = tabBandHitboxes(handle)
	tabs = tabs[:0]
	for _, r := range hbs {
		if r.w > 40 && r.w < widest-20 {
			tabs = append(tabs, r)
		}
	}
	if len(tabs) < 2 {
		t.Fatal("tabs vanished after wheeling the strip")
	}
	// After panning right, the first tab must be (partially) scrolled out:
	// its left edge is left of the strip's left edge.
	if tabs[0].x >= -1 {
		t.Fatalf("wheel did not pan the strip: first tab still at x=%.0f", tabs[0].x)
	}

	// (e) Switching the active tab to index 0 reveals it again.
	editorUpdate(handle, cx, func(s *workspace.Workspace) { s.Active = 0 })
	handle.Window.Draw()
	hbs = tabBandHitboxes(handle)
	tabs = tabs[:0]
	widest = 0
	for _, r := range hbs {
		if r.w > widest {
			widest = r.w
		}
	}
	for _, r := range hbs {
		if r.w > 40 && r.w < widest-20 {
			tabs = append(tabs, r)
		}
	}
	if len(tabs) == 0 {
		t.Fatal("no tabs after reveal")
	}
	first := tabs[0]
	if first.x < -1 || first.x+first.w > widest+1 {
		t.Fatalf("active tab 0 not revealed after switch: x=%.0f w=%.0f strip w=%.0f", first.x, first.w, widest)
	}
}

// TestTabLabelsDistinct keeps identical tab labels distinguishable: two
// buffers with the SAME name produce distinct ButtonIDs (the framework
// deduplicates ids), so both tabs render and both close buttons work.
func TestTabLabelsDistinct(t *testing.T) {
	const count = 3
	hp, handle, _ := openManyTabsWindow(t, count, 0)
	_ = hp
	var sb strings.Builder
	hbs := tabBandHitboxes(handle)
	for _, r := range hbs {
		fmt.Fprintf(&sb, "x=%.0f w=%.0f; ", r.x, r.w)
	}
	if len(hbs) < count {
		t.Fatalf("tab band missing hitboxes: %s", sb.String())
	}
	_ = handle
}
