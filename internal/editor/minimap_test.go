package editor

import (
	"testing"

	gc "github.com/FlameInTheDark/go-gpui/color"
)

func TestMinimapCacheReuseAndInvalidation(t *testing.T) {
	buf := NewBuffer("", "sample.txt", "\tαx\n\nlater")
	key := minimapKey{language: "text", scale: 1, palette: Palette{Text: gc.White()}, dim: gc.Red()}
	c := &buf.minimap
	c.prepare(key, 2)
	blocks := c.blocks(buf, 0)
	if len(blocks) != 1 || blocks[0].x != 4 || blocks[0].width != 6 || blocks[0].color != gc.Red().Alpha(0.55) {
		t.Fatalf("tab/multibyte block = %+v", blocks)
	}
	first := &blocks[0]
	if again := c.blocks(buf, 0); &again[0] != first {
		t.Fatal("unchanged row did not reuse its cached blocks")
	}
	if blocks := c.blocks(buf, 1); len(blocks) != 0 {
		t.Fatalf("blank line has blocks: %+v", blocks)
	}
	if blocks := c.blocks(buf, 2); len(blocks) != 1 || blocks[0].width != 5 || len(c.rows) != 2 {
		t.Fatalf("ring collision returned stale blocks: %+v", blocks)
	}
	if width := c.blocks(buf, 0)[0].width; width != 6 {
		t.Fatalf("revisited row width = %v, want 6", width)
	}
	buf.Cursor, buf.Anchor = len("\tαx"), len("\tαx")
	buf.Insert("!")
	key.revision = buf.Revision
	c.prepare(key, 2)
	if width := c.blocks(buf, 0)[0].width; width != 7 {
		t.Fatalf("edited row width = %v, want 7", width)
	}
	key.dim, key.scale = gc.Blue(), 2
	c.prepare(key, 2)
	if block := c.blocks(buf, 0)[0]; block.width != 14 || block.color != gc.Blue().Alpha(0.55) {
		t.Fatalf("new theme and scale produced stale block: %+v", block)
	}
}

// TestMinimapFoldMaskAndHeader pins the rail's fold walk: strictly-inside
// lines hide, the header line shows the band, and ranges outside the
// painted window cost nothing. The mask replaced a per-row scan of every
// fold (quadratic on large files) — this keeps its semantics pinned.
func TestMinimapFoldMaskAndHeader(t *testing.T) {
	folds := []FoldRange{
		{Start: 2, End: 8},
		{Start: 20, End: 30},
	}
	inside := minimapFoldMask(folds, 0, 12)
	for i := 0; i < 12; i++ {
		want := i > 2 && i <= 8
		if inside[i] != want {
			t.Fatalf("row %d inside=%v, want %v", i, inside[i], want)
		}
	}
	// A window that starts mid-fold still hides the tail rows.
	inside = minimapFoldMask(folds, 5, 12)
	for i := 5; i <= 8; i++ {
		if !inside[i-5] {
			t.Fatalf("row %d (inside the fold) not masked", i)
		}
	}
	if inside[9-5] {
		t.Fatal("row 9 masked, want visible (the fold ended on line 8)")
	}
	// Headers: only the exact start lines, via binary search.
	headerAt := minimapFoldHeaderAt(folds)
	if !headerAt(2) || !headerAt(20) || headerAt(3) || headerAt(30) || headerAt(0) {
		t.Fatal("header membership is wrong")
	}
	// Ranges entirely outside the window mark nothing and never panic.
	if m := minimapFoldMask(folds, 100, 110); len(m) != 10 {
		t.Fatalf("out-of-window mask = %v", m)
	}
}

// TestMinimapBlocksCoalesce pins the block coalescing: same-color runs
// separated by a sub-pixel gap paint as ONE rect (each block is a
// rasterized quad per frame; the miniature of a large file is hundreds of
// rows), while different colors never blend.
func TestMinimapBlocksCoalesce(t *testing.T) {
	// Go line with two number runs separated by one space; at a tiny
	// scale the gap between the two blocks is under a pixel, so the
	// numbers merge into a single block. The keyword prefix keeps its
	// own color and stays separate.
	key := minimapKey{language: "go", scale: 0.3, palette: Palette{
		Keyword: gc.Red(), Number: gc.Blue(), Text: gc.Black(),
	}}
	blocks := buildMinimapBlocks(nil, "x := 12 34", key, StateNormal)
	blue := 0
	for _, b := range blocks {
		if b.color == gc.Blue().Alpha(min(0.95, 0.95*gc.Blue().A+0.05)) {
			blue++
		}
	}
	if blue != 1 {
		t.Fatalf("%d blue blocks, want 1 (the two number runs coalesced): %+v", blue, blocks)
	}
	// At a large scale the same line keeps the runs apart.
	key.scale = 6
	blocks = buildMinimapBlocks(nil, "x := 12 34", key, StateNormal)
	blue = 0
	for _, b := range blocks {
		if b.color == gc.Blue().Alpha(min(0.95, 0.95*gc.Blue().A+0.05)) {
			blue++
		}
	}
	if blue != 2 {
		t.Fatalf("%d blue blocks at large scale, want 2 (gap is now multiple pixels): %+v", blue, blocks)
	}
}
