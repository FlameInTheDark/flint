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

	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// bigTreeProject builds a temp project whose tree needs scrolling: 40 root
// files plus three folders with 15 files each.
func bigTreeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var big strings.Builder
	big.WriteString("package x\n\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&big, "// filler line %d: value = %d\n", i+1, i*i)
	}
	os.WriteFile(filepath.Join(dir, "big.go"), []byte(big.String()), 0o644)
	for i := 0; i < 40; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%02d.go", i)), []byte("package x\n"), 0o644)
	}
	for f := 0; f < 3; f++ {
		sub := filepath.Join(dir, fmt.Sprintf("dir%d", f))
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 15; i++ {
			os.WriteFile(filepath.Join(sub, fmt.Sprintf("nested%02d.go", i)), []byte("package y\n"), 0o644)
		}
	}
	return dir
}

func openTreeWindow(t *testing.T, dir string, openFile bool) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl, platform.WindowOptions{Title: "tree-scroll"},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws := workspace.New("tree-scroll")
			ws.Measure(w)
			if err := ws.OpenFolder(dir); err != nil {
				t.Fatal(err)
			}
			if openFile {
				// A real file inside the project dir: a missing path would raise the
				// error prompt, whose blocking hitbox swallows wheel events meant for
				// the editor pane.
				ws.OpenFile(filepath.Join(dir, "big.go"))
			}
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

// TestTreeScrollStaysInsideWindow is the regression test for "opening a
// folder with many files scrolls the entire app content out of the screen":
// (a) the status bar stays at the window bottom, (b) wheeling over the tree
// moves tree rows by exactly the wheel delta and nothing else, (c) clicks
// below the scrolled-out rows cannot reach tree rows (content-mask hit
// testing), they land on the status bar band instead.
func TestTreeScrollStaysInsideWindow(t *testing.T) {
	dir := bigTreeProject(t)
	hp, handle, cx := openTreeWindow(t, dir, true)
	hw := hp.LastWindow()
	const winH = float32(650)

	// (a) The tree list (scroll container, ~TreeWidth wide) fits between
	// the menu/tab bar and the status bar; the status band at the bottom
	// stays clear of it.
	listHitbox := func() hitboxRect {
		for _, hb := range handle.Window.RenderedHitboxes() {
			r := rectOf(hb)
			if r.w > 150 && r.w < 260 && r.h > 100 && r.x < 5 {
				return r
			}
		}
		return hitboxRect{}
	}
	lb := listHitbox()
	if lb.w == 0 {
		t.Fatal("tree list hitbox not found")
	}
	if lb.y < ui.MenuBarHeight-0.5 {
		t.Fatalf("tree list top %.1f overlaps the menu bar", lb.y)
	}
	if lb.bottom() > winH-ui.StatusHeight+0.5 {
		t.Fatalf("tree list bottom %.1f overlaps the status band (win %.0f, status %.0f)", lb.bottom(), winH, ui.StatusHeight)
	}

	// (b) Wheel over the tree: rows move by the delta; the editor line and
	// the status band do not move.
	rows := func() (float32, float32) {
		minY, maxY := float32(1e9), float32(-1)
		for _, hb := range handle.Window.RenderedHitboxes() {
			r := rectOf(hb)
			if r.h >= 20 && r.h <= 28 && r.x < 5 && r.w > 100 {
				if r.y < minY {
					minY = r.y
				}
				if r.bottom() > maxY {
					maxY = r.bottom()
				}
			}
		}
		return minY, maxY
	}
	line0, _ := handle.View.Read(cx).DebugScrollState()
	ry0, _ := rows()
	hw.DispatchInput(platform.ScrollEvent{Position: geometry.PointPixels(120, 400), Delta: platform.ScrollDeltaPixels(0, -60)})
	hw.DispatchInput(platform.ScrollEvent{Position: geometry.PointPixels(120, 400), Delta: platform.ScrollDeltaPixels(0, -60)})
	handle.Window.Draw()
	ry1, _ := rows()
	line1, _ := handle.View.Read(cx).DebugScrollState()
	if d := ry0 - ry1; d < 119.9 || d > 120.1 {
		t.Fatalf("two -60px wheel events moved tree rows %.1fpx, want 120", d)
	}
	if line1 != line0 {
		t.Fatalf("wheeling the tree scrolled the editor: %d -> %d", line0, line1)
	}
	lb1 := listHitbox()
	if lb1.y != lb.y || lb1.bottom() != lb.bottom() {
		t.Fatalf("tree list moved after tree wheel: %.1f..%.1f -> %.1f..%.1f", lb.y, lb.bottom(), lb1.y, lb1.bottom())
	}

	// (c) Wheel over the editor: only the editor scrolls.
	line2, _ := handle.View.Read(cx).DebugScrollState()
	hw.DispatchInput(platform.ScrollEvent{Position: geometry.PointPixels(600, 400), Delta: platform.ScrollDeltaPixels(0, -120)})
	handle.Window.Draw()
	line3, _ := handle.View.Read(cx).DebugScrollState()
	if line3 == line2 {
		t.Fatal("wheel over the editor did not scroll it")
	}
	ry2, _ := rows()
	if ry2 != ry1 {
		t.Fatalf("wheel over the editor moved tree rows %.1f -> %.1f", ry1, ry2)
	}
	lb2 := listHitbox()
	if lb2.y != lb.y || lb2.bottom() != lb.bottom() {
		t.Fatalf("tree list moved after editor wheel: %.1f..%.1f -> %.1f..%.1f", lb.y, lb.bottom(), lb2.y, lb2.bottom())
	}

	// (d) A click in the status-band area of the tree column must not reach
	// a scrolled-out tree row (content-mask hit testing); the status bar is
	// not interactive, so the open buffer cannot change.
	activePath := ""
	if b := handle.View.Read(cx).Buf(); b != nil {
		activePath = b.Path
	}
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(120, winH-12)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(120, winH-12)})
	handle.Window.Draw()
	nowPath := ""
	if b := handle.View.Read(cx).Buf(); b != nil {
		nowPath = b.Path
	}
	if nowPath != activePath {
		t.Fatalf("click in the status band switched files: %q -> %q", activePath, nowPath)
	}
}
