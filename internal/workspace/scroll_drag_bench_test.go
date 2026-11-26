package workspace

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"
)

// BenchmarkDragScrollLongFile jumps through a file with many foldable
// functions, as dragging the minimap or scrollbar does.
func BenchmarkDragScrollLongFile(b *testing.B) {
	var src strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&src, "func demo%d() {\n\treturn\n}\n", i)
	}
	buf := editor.NewBuffer("", "drag.go", src.String())
	_, handle, ap := openLeftGapScene(b, true, buf)
	handle.Window.Draw()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pos := float32((i * 197) % 14000)
		ws := handle.View.Read(ap)
		ws.scrolls[ws.Active] = pos
		ws.scrollAnims[ws.Active] = pos
		ws.scrollAnimRuns[ws.Active] = false
		handle.Window.Draw()
	}
}

// BenchmarkRailDragEvents measures actual mouse dispatch and drawing, not
// just a single frame. Fast mice can deliver many moves between frames.
func BenchmarkRailDragEvents(b *testing.B) {
	for _, coalesced := range []bool{false, true} {
		name := "uncoalesced"
		if coalesced {
			name = "coalesced"
		}
		b.Run(name, func(b *testing.B) {
			var src strings.Builder
			for i := 0; i < 5000; i++ {
				fmt.Fprintf(&src, "func demo%d() {\n\treturn\n}\n", i)
			}
			buf := editor.NewBuffer("", "drag.go", src.String())
			_, handle, _ := openLeftGapScene(b, true, buf)
			var bounds geometry.Bounds[geometry.Pixels]
			for _, hb := range handle.Window.RenderedHitboxes() {
				if hb.Key == "n:btn-vscroll" {
					bounds = hb.Bounds
				}
			}
			if bounds.Size.Height == 0 {
				b.Fatal("missing scrollbar")
			}
			point := func(f float32) geometry.Point[geometry.Pixels] {
				return geometry.PointPixels(bounds.Origin.X.AsF32()+bounds.Size.Width.AsF32()/2, bounds.Origin.Y.AsF32()+bounds.Size.Height.AsF32()*f)
			}
			var pending func()
			d := newRailDragInput(handle.Window.HandleInput, func(pos geometry.Point[geometry.Pixels]) bool {
				return editorRailHit(handle.Window, pos)
			}, func(_ time.Duration, f func()) { pending = f })
			input := handle.Window.HandleInput
			if coalesced {
				input = d.handle
			}
			input(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: point(0.2)})
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < 20; j++ {
					input(platform.MouseMoveEvent{Position: point(0.2 + float32((i*20+j)%100)*0.006)})
				}
				if coalesced {
					pending()
				}
			}
			b.StopTimer()
			input(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: point(0.8)})
		})
	}
}

func TestScrollIntoFoldEndingAtEOF(t *testing.T) {
	src := "func long() {\n" + strings.Repeat("\tvalue++\n", 200) + "}"
	buf := editor.NewBuffer("", "fold.go", src)
	buf.Fold(0)
	_, handle, ap := openLeftGapScene(t, true, buf)
	ws := handle.View.Read(ap)
	ws.scrolls[ws.Active] = 150
	ws.scrollAnims[ws.Active] = 150
	// Rendering a seek into a collapsed range ending at EOF must not loop
	// forever trying to advance beyond the last line.
	handle.Window.Draw()
}
