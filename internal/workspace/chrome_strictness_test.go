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

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// This file pins the strict chrome layout contract after the Windows 125%
// incident: with a tall project tree the workspace row's content-sized flex
// basis inflated past the window, the shrink phase collapsed the only
// shrinkable fixed bar (the menu bar, which lacked flex-shrink: 0) and its
// labels were clipped by the tab strip. The contract now is:
//
//  1. every flex-filling wrapper carries flex-basis: 0 (its basis is the
//     available space, never its content), and
//  2. fixed-height chrome bars carry flex-shrink: 0, so the menu bar / tab
//     strip / status bar stack EXACTLY at their declared heights and never
//     overlap, at any window size, document length or HiDPI scale.
func tallTreeDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for d := 0; d < 12; d++ {
		dir := filepath.Join(root, fmt.Sprintf("dir-%02d", d))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < 4; f++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%d.go", f)),
				[]byte("package dir\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

// expandAllRows expands every directory node so the flattened tree shows
// ~60 rows, mirroring the user's open go-gpui project tree.
func expandAllRows(n *workspace.TreeNode) {
	if n == nil {
		return
	}
	if n.IsDir {
		n.Expanded = true
		n.EnsureLoaded()
	}
	for _, c := range n.Children {
		expandAllRows(c)
	}
}

func openWorkspaceWithTree(t *testing.T, scale float32) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(960, 600))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl,
		platform.WindowOptions{Title: "chrome-strict", ScaleFactor: scale},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws := workspace.New("chrome-strict")
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

	// Windows open->resize sequence: the first frame lays out at the
	// creation size, then the frame lands on the user's client size.
	hw := hp.LastWindow()
	hw.Resize(geometry.SizePixels(1031, 644.8))

	editorUpdate(&handle, appl.App, func(s *workspace.Workspace) {
		dir := tallTreeDir(t)
		if err := s.OpenFolder(dir); err != nil {
			t.Fatalf("open folder: %v", err)
		}
		expandAllRows(s.Tree)
		s.Buffers = []*editor.Buffer{
			editor.NewBuffer("", "main.go", strings.Repeat("// fairly long comment line to force the horizontal scrollbar in the editor pane\n", 28)),
			editor.NewBuffer("", "buffer.go", "package editor\n"),
		}
	})
	handle.Window.Draw()
	return hp, &handle, appl.App
}

// TestChromeBarsStackExactly asserts, at scale 1.0 and 1.25 with a tall
// expanded project tree, that the menu bar keeps its full declared height,
// the tab strip starts below it (no overlap) and the status bar fills the
// window bottom.
func TestChromeBarsStackExactly(t *testing.T) {
	for _, scale := range []float32{0, 1.25} {
		name := "scale1.0"
		if scale > 0 {
			name = "scale1.25"
		}
		t.Run(name, func(t *testing.T) {
			hp, handle, cx := openWorkspaceWithTree(t, scale)
			hw := hp.LastWindow()

			// Hitboxes are in logical units at every scale.
			var menuTop, menuBottom float32 = 1e9, -1
			var tabTop, tabBottom float32 = 1e9, -1
			for _, hb := range handle.Window.RenderedHitboxes() {
				r := rectOf(hb)
				if r.w < 40 || r.h < 12 || r.h > 40 {
					continue // rows and chrome bands only
				}
				if r.y < ui.MenuBarHeight {
					if r.y < menuTop {
						menuTop = r.y
					}
					if r.bottom() > menuBottom {
						menuBottom = r.bottom()
					}
				} else if r.y < ui.MenuBarHeight+ui.TabBarHeight && r.w >= 60 && r.w <= 400 {
					if r.y < tabTop {
						tabTop = r.y
					}
					if r.bottom() > tabBottom {
						tabBottom = r.bottom()
					}
				}
			}
			if menuBottom < 0 {
				t.Fatal("no menu-bar hitboxes found")
			}
			if tabTop < 0 {
				t.Fatal("no tab hitboxes found")
			}
			if menuTop > 0.5 {
				t.Fatalf("menu bar starts at y=%.1f, want 0 (chrome shifted)", menuTop)
			}
			if menuBottom > ui.MenuBarHeight+0.5 {
				t.Fatalf("menu bar content reaches y=%.1f, want <= %.0f (bar collapsed/overlapped)",
					menuBottom, ui.MenuBarHeight)
			}
			if tabTop < ui.MenuBarHeight-0.5 {
				t.Fatalf("tab strip starts at y=%.1f, want >= %.0f (overlaps the menu bar)", tabTop, ui.MenuBarHeight)
			}

			// Framebuffer bands (device px): the menu bar's bottom hairline
			// and the tab strip's bottom hairline sit at their declared
			// heights; the status bar fills the window bottom.
			img := hw.Image()
			border := handle.View.Read(cx).Theme().Surface1.ToRgb()
			isSurf1 := func(i int) bool {
				return absf(float32(img.Pixels[i])-border.R*255) < 3 && absf(float32(img.Pixels[i+1])-border.G*255) < 3 && absf(float32(img.Pixels[i+2])-border.B*255) < 3
			}
			fullWidthLine := func(y, left int, percent int) bool {
				n := 0
				for x := left; x < img.Width; x += 7 {
					i := (y*img.Width + x) * 4
					if isSurf1(i) {
						n++
					}
				}
				// The active tab's background covers its own slice of the
				// tab strip's bottom hairline (the tab merges with the
				// editor surface, upstream-style), so a majority suffices.
				return n > ((img.Width-left)/7)*percent/100
			}
			wantMenuH := float32(ui.MenuBarHeight)
			wantTabH := float32(ui.TabBarHeight)
			if scale > 0 {
				wantMenuH *= scale
				wantTabH *= scale
			}
			hairlineAt := func(from, to, left, percent int) int {
				for y := from; y < to && y < img.Height; y++ {
					if fullWidthLine(y, left, percent) {
						return y
					}
				}
				return -1
			}
			mh := hairlineAt(int(wantMenuH)-3, int(wantMenuH)+4, 0, 55)
			if mh < 0 {
				t.Fatalf("menu bar bottom hairline not found near y=%.0f (bar collapsed?)", wantMenuH)
			}
			if absf(float32(mh)-wantMenuH) > 3 {
				t.Fatalf("menu bar height %.0f (hairline y=%d), want ~%.0f (bar collapsed; the menu must never shrink)",
					float32(mh), mh, wantMenuH)
			}
			// Tabs are now confined to the editor column beside Project.
			_, treeWidth := handle.View.Read(cx).DebugPanelSizes()
			deviceTreeWidth := treeWidth
			if scale > 0 {
				deviceTreeWidth *= scale
			}
			th := hairlineAt(int(wantMenuH+wantTabH)-3, int(wantMenuH+wantTabH)+4, int(deviceTreeWidth)+2, 35)
			if th < 0 {
				t.Fatalf("tab strip bottom hairline not found near y=%.0f", wantMenuH+wantTabH)
			}
			if absf(float32(th)-float32(mh)-wantTabH) > 3 {
				t.Fatalf("tab strip height %.0f (hairline y=%d), want ~%.0f", float32(th-mh), th, wantTabH)
			}
			wantStatusH := float32(ui.StatusHeight)
			if scale > 0 {
				wantStatusH *= scale
			}
			sh := hairlineAt(img.Height-int(wantStatusH)-4, img.Height-int(wantStatusH)+3, 0, 55)
			if sh < 0 {
				t.Fatalf("status bar top hairline not found near y=%.0f", float32(img.Height)-wantStatusH)
			}
			if absf(float32(img.Height-sh)-wantStatusH) > 3 {
				t.Fatalf("status bar height %.0f, want ~%.0f", float32(img.Height-sh), wantStatusH)
			}
		})
	}
}
