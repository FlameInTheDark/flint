package workspace_test

import (
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// The terminal stress suite hunts the reported "clipped under the bottom
// line / squeezed text" bugs across dock heights and window sizes: every
// scenario asserts the dock's structural invariants (strip, header, body,
// input stack exactly into the dock; the dock ends exactly at the status
// bar; nothing pokes past the window) and renders PNGs for review.

// findTerminalHitboxes locates the dock's chrome hitboxes by their
// unique geometry: the strip (5px), the header (26px), and the body
// between the header and the status bar (the shell's grid fills it — the
// old input row is gone, the shell owns line editing).
type termHitboxes struct {
	strip, header, body hitboxRect
	dockTop             float32
}

func findTerminalHitboxes(hitboxes []gpui.Hitbox, winW, winH, dockH float32) (termHitboxes, bool) {
	var out termHitboxes
	dockBottom := winH - ui.StatusHeight
	out.dockTop = dockBottom - dockH
	// The dock's pane spans the full window width minus the tree panel; in
	// the terminal scene the tree is hidden, so the dock starts at x=0.
	for _, hb := range hitboxes {
		r := rectOf(hb)
		if r.w < winW/2 { // dock chrome spans the pane
			continue
		}
		switch {
		case r.h >= 4.5 && r.h <= 5.5 && r.y >= out.dockTop-1 && r.y <= out.dockTop+1:
			out.strip = r
		case r.h >= ui.TermHeaderH-1 && r.h <= ui.TermHeaderH+1 && r.y >= out.dockTop+ui.TermResizeStrip-1 && r.y <= out.dockTop+ui.TermResizeStrip+1:
			out.header = r
		}
	}
	if out.strip.w == 0 || out.header.w == 0 {
		return out, false
	}
	// The body fills the space between the header and the dock bottom.
	for _, hb := range hitboxes {
		r := rectOf(hb)
		if r.w < winW/2 {
			continue
		}
		bodyH := dockBottom - out.header.bottom()
		if absf(r.h-bodyH) <= 1.2 && absf(r.y-out.header.bottom()) <= 1.2 {
			out.body = r
			break
		}
	}
	return out, out.body.w > 0 && out.body.y >= out.header.bottom()-1 && out.body.bottom() <= dockBottom+1
}

// setTermHeight targets a specific dock height the way a drag would
// (SetTermHeight takes the pointer's window y on the dock's top edge).
func setTermHeight(handle *gpui.WindowHandle[workspace.Workspace], cx *app.App, winH, h float32) {
	editorUpdate(handle, cx, func(s *workspace.Workspace) {
		s.SetTermHeight(winH - ui.StatusHeight - h)
	})
}

func TestTerminalStressHeights(t *testing.T) {
	hp, handle, cx := openTerminalScene(t)
	hw := hp.LastWindow()

	type scenario struct {
		winW, winH, termH int
	}
	ways := []scenario{
		{1000, 650, 220}, {1000, 650, 120}, {1000, 650, 121}, {1000, 650, 158},
		{1000, 650, 217}, {1000, 650, 259}, {1000, 650, 331},
		{800, 500, 120}, {800, 500, 200}, {800, 500, 260},
		{640, 460, 120}, {640, 460, 180},
	}
	for i, sc := range ways {
		hw.Resize(geometry.SizePixels(float32(sc.winW), float32(sc.winH)))
		setTermHeight(handle, cx, float32(sc.winH), float32(sc.termH))
		handle.Window.Draw()

		termHeff, _ := handle.View.Read(cx).DebugPanelSizes()
		hbs, ok := findTerminalHitboxes(handle.Window.RenderedHitboxes(), float32(sc.winW), float32(sc.winH), termHeff)
		if !ok {
			t.Fatalf("scenario %d (%dx%d, term %d): terminal chrome hitboxes not found: %+v",
				i, sc.winW, sc.winH, sc.termH, hbs)
		}
		winHf, winWf := float32(sc.winH), float32(sc.winW)
		dockBottom := winHf - ui.StatusHeight

		// (1) The dock ends exactly at the status bar's top edge.
		if absf(hbs.body.bottom()-dockBottom) > 0.6 {
			t.Fatalf("scenario %d (%dx%d term %d): body bottom %.1f != dock bottom %.1f (clipped under the bottom line)",
				i, sc.winW, sc.winH, sc.termH, hbs.body.bottom(), dockBottom)
		}
		// (2) The strip rides the dock's top edge (the panel's 1px top
		// border sits above it: H() is border-box).
		if absf(hbs.strip.y-hbs.dockTop) > 1.2 {
			t.Fatalf("scenario %d: strip top %.1f != dock top %.1f", i, hbs.strip.y, hbs.dockTop)
		}
		// (3) Header and body stack without gaps or overlap.
		if absf(hbs.header.bottom()-hbs.body.y) > 1 {
			t.Fatalf("scenario %d: chrome stack has gaps: header %.1f..%.1f body %.1f..%.1f",
				i, hbs.header.y, hbs.header.bottom(), hbs.body.y, hbs.body.bottom())
		}
		// (4) No hitbox pokes past the window (bounds ∩ mask must lie in the
		// window: raw bounds may overflow a clipping mask, that's fine).
		for j, hb := range handle.Window.RenderedHitboxes() {
			r := rectOf(hb)
			m := hb.ContentMask.Bounds
			inter := intersectRects(r, hitboxRect{
				m.Origin.X.AsF32(), m.Origin.Y.AsF32(),
				m.Size.Width.AsF32(), m.Size.Height.AsF32(),
			})
			if inter.w <= 0 && inter.h <= 0 {
				continue // fully masked out: invisible
			}
			if inter.x < -0.6 || inter.y < -0.6 || inter.right() > winWf+0.6 || inter.bottom() > winHf+0.6 {
				t.Fatalf("scenario %d: hitbox %d (%.1f,%.1f %.1fx%.1f) visible part escapes the window %dx%d",
					i, j, r.x, r.y, r.w, r.h, sc.winW, sc.winH)
			}
		}
	}

	// PNG review set: default, minimum, an odd height, and a short window.
	for _, name := range []struct {
		winW, winH, termH int
		png               string
	}{
		{1000, 650, 220, "stress_default.png"},
		{1000, 650, 120, "stress_min.png"},
		{1000, 650, 187, "stress_odd.png"},
		{640, 460, 180, "stress_small_win.png"},
	} {
		hw.Resize(geometry.SizePixels(float32(name.winW), float32(name.winH)))
		setTermHeight(handle, cx, float32(name.winH), float32(name.termH))
		handle.Window.Draw()
		shot(t, hp, "/tmp/render", name.png)
	}
}

// TestTerminalStressLongInput types a command wider than the dock:
// keystrokes stream to the shell (captured by the fake write sink — the
// grid never changes size) and the dock keeps its structural invariants.
func TestTerminalStressLongInput(t *testing.T) {
	hp, handle, cx := openTerminalScene(t)
	hw := hp.LastWindow()

	// Focus the terminal body by clicking it, then capture the writes.
	termHeff, _ := handle.View.Read(cx).DebugPanelSizes()
	dockTop := 650 - ui.StatusHeight - termHeff
	bodyY := dockTop + ui.TermResizeStrip + ui.TermHeaderH
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(400, bodyY+40)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(400, bodyY+40)})
	handle.Window.Draw()

	var written []byte
	ws := handle.View.Read(cx)
	ws.Term.SetTestWriteSink(func(b []byte) { written = append(written, b...) })

	// Type a very long command (5x the pane width).
	long := strings.Repeat("abcdefgh", 100) // 800 chars
	for _, ch := range long {
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: string(ch), KeyChar: string(ch)}})
	}
	handle.Window.Draw()
	shot(t, hp, "/tmp/render", "stress_long_input.png")
	ws.Term.SetTestWriteSink(nil)

	if string(written) != long {
		t.Fatalf("keystrokes did not reach the shell: got %d bytes want %d", len(written), len(long))
	}

	termHeff, _ = handle.View.Read(cx).DebugPanelSizes()
	hbs, ok := findTerminalHitboxes(handle.Window.RenderedHitboxes(), 1000, 650, termHeff)
	if !ok {
		t.Fatal("terminal chrome hitboxes not found after long input")
	}
	dockBottom := float32(650) - ui.StatusHeight
	if absf(hbs.body.bottom()-dockBottom) > 0.6 {
		t.Fatalf("long input pushed the body under the bottom line: %.1f != %.1f", hbs.body.bottom(), dockBottom)
	}
	// Every visible hitbox still lies within the window.
	for j, hb := range handle.Window.RenderedHitboxes() {
		r := rectOf(hb)
		m := hb.ContentMask.Bounds
		inter := intersectRects(r, hitboxRect{
			m.Origin.X.AsF32(), m.Origin.Y.AsF32(),
			m.Size.Width.AsF32(), m.Size.Height.AsF32(),
		})
		if inter.w <= 0 && inter.h <= 0 {
			continue
		}
		if inter.x < -0.6 || inter.y < -0.6 || inter.right() > 1000.6 || inter.bottom() > 650.6 {
			t.Fatalf("long input: hitbox %d (%.1f,%.1f %.1fx%.1f) escapes the window", j, r.x, r.y, r.w, r.h)
		}
	}
}

func intersectRects(a, b hitboxRect) hitboxRect {
	x0, y0 := maxf(a.x, b.x), maxf(a.y, b.y)
	x1, y1 := minf(a.right(), b.right()), minf(a.bottom(), b.bottom())
	return hitboxRect{x0, y0, x1 - x0, y1 - y0}
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
