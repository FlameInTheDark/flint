package workspace

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// easeWorkspace builds a workspace with a tall buffer for scroll tests.
func easeWorkspace(t *testing.T) *Workspace {
	t.Helper()
	ws := New("ease")
	ws.Buffers = []*editor.Buffer{editor.NewBuffer("", "main.go", strings.Repeat("line\n", 300))}
	ws.LineHeight = 18
	ws.WindowSize = geometry.SizePixels(1000, 700)
	return ws
}

// TestScrollEaseGlidesAndSettles: a wheel delta starts an ease from the
// rendered position, glides monotonically toward the target and snaps.
func TestScrollEaseGlidesAndSettles(t *testing.T) {
	ws := easeWorkspace(t)
	ws.addScrollDelta(3)
	if !ws.scrollAnimating() {
		t.Fatal("a wheel delta did not start the ease")
	}
	if ws.scrollF() != 0 {
		t.Fatalf("the ease must start from the rendered position, got %v", ws.scrollF())
	}
	base := time.Now()
	ws.lastScrollFrame = base
	progressed := false
	for i := 1; i <= 120; i++ {
		ws.advanceScrollAnimAt(base.Add(time.Duration(i) * 16 * time.Millisecond))
		p := ws.scrollF()
		if p < 0 || p > 3 {
			t.Fatalf("ease position %v out of range", p)
		}
		if i == 2 && p > 0 && p < 3 {
			progressed = true
		}
		if ws.scrollAnimating() && i > 2 && p >= 3 {
			t.Fatalf("ease overshot the target: %v", p)
		}
		if !ws.scrollAnimating() {
			break
		}
	}
	if progressed {
		t.Log("ease progressed through intermediate positions (good)")
	} else {
		t.Fatal("the ease never showed intermediate positions (no glide)")
	}
	if ws.scrollAnimating() {
		t.Fatalf("ease did not settle within 120 frames: %v -> %v", ws.scrollF(), ws.scrolls[ws.Active])
	}
	if ws.scrollF() != 3 {
		t.Fatalf("settle left the rendered position at %v, want 3", ws.scrollF())
	}
	if ws.scroll() != 3 {
		t.Fatalf("scroll() reported %d, want 3", ws.scroll())
	}
}

// TestScrollEaseSnapsOnWholeRowPaths: keys, caret and seek paths land
// exactly (no trailing animation), while the target stays authoritative.
func TestScrollEaseSnapsOnWholeRowPaths(t *testing.T) {
	ws := easeWorkspace(t)
	ws.addScrollDelta(3)
	ws.advanceScrollAnimAt(time.Now().Add(16 * time.Millisecond))
	ws.setScroll(10) // a caret/key/seek move
	if ws.scrollAnimating() {
		t.Fatal("setScroll left an ease running")
	}
	if ws.scrollF() != 10 || ws.scroll() != 10 {
		t.Fatalf("setScroll did not snap: scrollF=%v scroll=%d", ws.scrollF(), ws.scroll())
	}
}

// TestScrollDeltaClamps: wheel deltas cannot scroll past the last page.
func TestScrollDeltaClamps(t *testing.T) {
	ws := easeWorkspace(t)
	ws.addScrollDelta(1e6)
	max := float32(ws.maxScrollRows())
	if ws.scrolls[ws.Active] != max {
		t.Fatalf("target %v exceeds max %v", ws.scrolls[ws.Active], max)
	}
	ws.advanceScrollAnimAt(time.Now().Add(time.Second))
	ws.addScrollDelta(-1e6)
	if ws.scrolls[ws.Active] != 0 {
		t.Fatalf("upward wheel overscrolled to %v", ws.scrolls[ws.Active])
	}
}

// TestScrollStallDoesNotTeleport: a long gap between frames advances the
// ease by at most the stall cap (it must not jump straight to the target).
func TestScrollStallDoesNotTeleport(t *testing.T) {
	ws := easeWorkspace(t)
	ws.addScrollDelta(20)
	base := time.Now()
	ws.lastScrollFrame = base
	ws.advanceScrollAnimAt(base.Add(10 * time.Second))
	p := ws.scrollF()
	if p >= 20 {
		t.Fatalf("a stalled frame teleported the ease to %v", p)
	}
	if p <= 0 {
		t.Fatal("a stalled frame advanced nothing")
	}
}

// TestWheelGlidesThroughTheRealInputPath: a raw wheel event over the pane
// moves the target immediately and the rendered position glides through
// next-frame callbacks until the ease settles at the fractional rest.
func TestWheelGlidesThroughTheRealInputPath(t *testing.T) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "row " + strings.Repeat(".", i%7)
	}
	buf := editor.NewBuffer("", "rows.go", strings.Join(lines, "\n"))
	hp, _, handle, cx := openScene(t, buf)
	hw := hp.LastWindow()

	// One wheel notch: -120px of wheel travel = 120/lineHeight rows.
	lineH := handle.View.Read(cx).LineHeight
	wantRows := 120.0 / float64(lineH)
	hw.DispatchInput(platform.ScrollEvent{Position: geometry.PointPixels(600, 400), Delta: platform.ScrollDeltaPixels(0, -120)})
	handle.Window.Draw()
	ws := handle.View.Read(cx)
	if ws.scrollAnimating() {
		if ws.scrollF() == 0 {
			t.Fatal("the ease did not start gliding (still at the top)")
		}
	} else if ws.scrollF() == 0 {
		t.Fatal("the wheel did not scroll")
	}
	// Pump frames until the ease settles (Render schedules the next-frame
	// advance; each Draw runs it).
	settled := false
	for i := 0; i < 200; i++ {
		handle.Window.Draw()
		if !handle.View.Read(cx).scrollAnimating() {
			settled = true
			break
		}
	}
	if !settled {
		t.Fatalf("ease never settled: rendered %v target %v", handle.View.Read(cx).scrollF(), handle.View.Read(cx).scrolls[handle.View.Read(cx).Active])
	}
	ws = handle.View.Read(cx)
	want := int(math.Round(wantRows))
	if line, _ := ws.DebugScrollState(); line != want {
		t.Fatalf("wheel landed on row %d, want %d (120px / %.1fpx)", line, want, lineH)
	}
	if ws.scrollF() < float32(wantRows)-0.2 || ws.scrollF() > float32(wantRows)+0.2 {
		t.Fatalf("the fractional rest position drifted: %v, want ~%.2f", ws.scrollF(), wantRows)
	}
}
