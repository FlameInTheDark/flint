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
