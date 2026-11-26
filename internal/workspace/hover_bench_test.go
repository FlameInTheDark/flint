package workspace

import (
	"fmt"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"
)

// BenchmarkTreeHoverEvents measures a burst of pointer moves over a project
// tree with many expanded entries, including complete frame rendering.
func BenchmarkTreeHoverEvents(b *testing.B) {
	for _, coalesced := range []bool{false, true} {
		name := "uncoalesced"
		if coalesced {
			name = "coalesced"
		}
		b.Run(name, func(b *testing.B) {
			buf := editor.NewBuffer("", "test.go", "package main\n")
			_, handle, ap := openLeftGapScene(b, true, buf)
			root := &TreeNode{Name: "project", IsDir: true, Loaded: true}
			for i := 0; i < 500; i++ {
				root.Children = append(root.Children, &TreeNode{Name: fmt.Sprintf("file%d.go", i), Path: fmt.Sprintf("project/file%d.go", i)})
			}
			ws := handle.View.Read(ap)
			ws.Tree, ws.ShowTree = root, true
			ws.tools.left = ""
			handle.Window.Draw()
			var pending func()
			d := newRailDragInput(handle.Window.HandleInput, func(geometry.Point[geometry.Pixels]) bool { return false }, func(_ time.Duration, f func()) { pending = f })
			input := handle.Window.HandleInput
			if coalesced {
				input = d.handle
			}
			input(platform.MouseMoveEvent{Position: geometry.PointPixels(60, 120)})
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < 20; j++ {
					input(platform.MouseMoveEvent{Position: geometry.PointPixels(60, float32(120+(i*20+j)%300))})
				}
				if coalesced && pending != nil {
					pending()
					pending = nil
				}
			}
		})
	}
}
