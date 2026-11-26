package workspace_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"
)

// Returning to the same pointer position must return to the same line.
func TestScrollDragTracksPointer(t *testing.T) {
	var src strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&src, "line %d\n", i)
	}
	for _, key := range []string{"n:btn-minimap", "n:btn-vscroll"} {
		t.Run(key, func(t *testing.T) {
			hp, handle, cx := openEditorWindowWith(t, src.String())
			var track hitboxRect
			for _, hb := range handle.Window.RenderedHitboxes() {
				if hb.Key == key {
					track = rectOf(hb)
					break
				}
			}
			if track.h == 0 {
				t.Fatalf("missing hitbox %s", key)
			}
			pos := func(frac float32) geometry.Point[geometry.Pixels] {
				return geometry.PointPixels(track.x+track.w/2, track.y+track.h*frac)
			}
			hw := hp.LastWindow()
			hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: pos(0.2)})
			hw.DispatchInput(platform.MouseMoveEvent{Position: pos(0.3), PressedButton: btnPtr(platform.ButtonLeft)})
			first, _ := handle.View.Read(cx).DebugScrollState()
			hw.DispatchInput(platform.MouseMoveEvent{Position: pos(0.4), PressedButton: btnPtr(platform.ButtonLeft)})
			hw.DispatchInput(platform.MouseMoveEvent{Position: pos(0.3), PressedButton: btnPtr(platform.ButtonLeft)})
			last, _ := handle.View.Read(cx).DebugScrollState()
			hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: pos(0.3)})
			if first == 0 || first != last {
				t.Fatalf("drag returned to same point but scrolled to %d then %d", first, last)
			}
		})
	}
}
