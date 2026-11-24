package workspace

import (
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// BenchmarkSoftwarePaint measures the full editor frame on the headless
// software backend, including layout, painting, rasterization and RGBA
// snapshot creation. It does not measure OS blitting or GPU work.
//
// Run: go test ./internal/workspace -bench BenchmarkSoftwarePaint -benchtime 10x
func BenchmarkSoftwarePaint(b *testing.B) {
	line := strings.Repeat("abcdefgh", 10) + " // comment trailing\n"
	buf := editor.NewBuffer("", "bench.go", strings.Repeat(line, 300))
	hp, handle, _ := openLeftGapScene(b, true, buf)
	_ = hp
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handle.Window.Draw()
	}
}
