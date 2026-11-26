package workspace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// BenchmarkScrollFrame measures the editor frame cost while the wheel ease
// slides the view through a LONG file (20k lines): each frame renders at a
// new fractional scroll position, so every frame's element tree, row set
// and minimap ring rebuild.
//
// Run: go test ./internal/workspace -bench BenchmarkScrollFrame -benchtime 10x -cpuprofile cpu.out
func BenchmarkScrollFrame(b *testing.B) {
	lines := make([]string, 20000)
	for i := range lines {
		lines[i] = fmt.Sprintf("func demo%d(x int) int { return x*%d + %d } // tail comment", i, i%97, i)
	}
	buf := editor.NewBuffer("", "bench_scroll.go", strings.Join(lines, "\n"))
	hp, handle, ap := openLeftGapScene(b, true, buf)
	_ = hp
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Slide like the wheel ease: fractional positions, no animation run
		// (the render path is the same one the ease drives per frame).
		pos := float32(i%3000) + 0.37
		st := handle.View.Read(ap)
		st.scrolls[st.Active] = pos
		st.scrollAnims[st.Active] = pos
		st.scrollAnimRuns[st.Active] = false
		handle.Window.Draw()
	}
}

// BenchmarkStaticFrameLongFile is the control: same long file, same frame
// count, scroll never moves. The delta against BenchmarkScrollFrame is the
// per-frame cost specific to scrolling.
func BenchmarkStaticFrameLongFile(b *testing.B) {
	lines := make([]string, 20000)
	for i := range lines {
		lines[i] = fmt.Sprintf("func demo%d(x int) int { return x*%d + %d } // tail comment", i, i%97, i)
	}
	buf := editor.NewBuffer("", "bench_static.go", strings.Join(lines, "\n"))
	hp, handle, _ := openLeftGapScene(b, true, buf)
	_ = hp
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handle.Window.Draw()
	}
}
