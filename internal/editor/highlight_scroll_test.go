package editor

import (
	"strings"
	"testing"
)

// TestMaxScrollF pins the wheel's pixel-exact scroll ceiling: the document
// end where the last line's bottom rests on the pane's bottom edge (VS
// Code's scrollBeyondLastLine=false). It sits at or below the whole-row
// ceiling (never more than one row below), because a whole-row rest
// overshoots the pixel-exact end whenever the viewport is not a whole
// number of lines tall.
func TestMaxScrollF(t *testing.T) {
	const lineH = 20.0
	// 10.5 lines tall viewport: the exact end is 100 - 10.5 = 89.5 (the
	// whole-row ceiling 90 would strand a 10px blank strip below the last
	// line).
	if got := MaxScrollF(100, 10, 10.5*lineH, lineH); got != 89.5 {
		t.Fatalf("MaxScrollF = %v, want 89.5 (pixel-exact document end)", got)
	}
	// A viewport that is a whole number of lines: the exact end coincides
	// with the whole-row ceiling.
	if got := MaxScrollF(100, 10, 10*lineH, lineH); got != 90 {
		t.Fatalf("MaxScrollF = %v, want 90 (exact multiple)", got)
	}
	// A short buffer cannot scroll at all.
	if got := MaxScrollF(5, 10, 10.5*lineH, lineH); got != 0 {
		t.Fatalf("MaxScrollF = %v, want 0 (document fits)", got)
	}
	// A lying viewportH (ratio above the row count + 1) clamps back into
	// one row of the whole-row ceiling.
	if got := MaxScrollF(100, 10, 12*lineH, lineH); got != 89 {
		t.Fatalf("MaxScrollF = %v, want 89 (clamped one row below the ceiling)", got)
	}
}

// TestHighlightCacheSurvivesGenerationSwap: scrolling a long file used to
// clear the whole highlight cache when it hit the cap, so the very next
// frame re-tokenized hundreds of lines at once (the periodic scroll
// stutter). The two-generation swap must keep the working set reachable:
// entries that aged into the previous generation still answer, and a hit
// there promotes them back into the current one.
func TestHighlightCacheSurvivesGenerationSwap(t *testing.T) {
	line := "func f(x int) int { return x }"
	// Warm the cache with one entry (any palette works).
	runs, _ := HighlightCachedIn(line, "go", StateNormal, statePal)
	if len(runs) == 0 {
		t.Fatal("no runs for a plain go line")
	}
	// Fill the current generation past the cap: the swap moves it to the
	// previous generation and the warmed entry must still answer (now from
	// the previous generation), with identical runs.
	for i := 0; i < highlightCacheCap+1; i++ {
		HighlightCachedIn("unique line "+strings.Repeat("x", 1)+" "+string(rune('a'+i%26))+" "+itoa(i), "go", StateNormal, statePal)
	}
	again, _ := HighlightCachedIn(line, "go", StateNormal, statePal)
	if len(again) != len(runs) {
		t.Fatalf("aged-out entry lost its runs: %d vs %d", len(again), len(runs))
	}
	// A repeat hit promotes the entry into the current generation.
	HighlightCachedIn(line, "go", StateNormal, statePal)
	if _, ok := highlightCacheCur[highlightKey{lang: "go", line: line, palette: statePal}]; !ok {
		t.Fatal("the previous-generation hit was not promoted")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
