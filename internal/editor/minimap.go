package editor

import (
	"strings"
	"unicode/utf8"

	gc "github.com/FlameInTheDark/go-gpui/color"
)

type minimapBlock struct {
	x, width float32
	color    gc.Hsla
}

type minimapKey struct {
	revision uint64
	language string
	palette  Palette
	dim      gc.Hsla
	scale    float32
}

type minimapRow struct {
	line   int
	valid  bool
	blocks []minimapBlock
}

// The active viewport and nearby rows fit in this per-buffer ring. We keep
// token geometry, not absolute bounds, so smooth scrolling changes only Y.
type minimapCache struct {
	key  minimapKey
	rows []minimapRow
}

func (c *minimapCache) prepare(key minimapKey, capacity int) {
	if len(c.rows) < capacity {
		c.rows = make([]minimapRow, capacity)
	}
	if c.key != key {
		c.key = key
		for i := range c.rows {
			c.rows[i].valid = false
		}
	}
}

func (c *minimapCache) blocks(buf *Buffer, line int) []minimapBlock {
	row := &c.rows[line%len(c.rows)]
	if row.valid && row.line == line {
		return row.blocks
	}
	row.line, row.valid = line, true
	// The row's start context comes from the buffer's per-line states, so
	// the minimap colors lines inside raw strings and block comments the
	// same way the code pane does.
	st := StateNormal
	if states := buf.LineStates(c.key.language); line < len(states) {
		st = states[line]
	}
	row.blocks = buildMinimapBlocks(row.blocks[:0], buf.Line(line), c.key, st)
	return row.blocks
}

func buildMinimapBlocks(dst []minimapBlock, line string, key minimapKey, st LineState) []minimapBlock {
	if strings.TrimSpace(line) == "" {
		return dst
	}
	display := DisplayLine(line)
	end := 0
	runs, _ := HighlightCachedIn(display, key.language, st, key.palette)
	for _, run := range runs {
		if len(dst) >= maxMinimapRunsPerLine {
			break
		}
		start := end
		end = min(end+run.Len, len(display))
		if end <= start || strings.TrimSpace(display[start:end]) == "" {
			continue
		}
		// Use prefix lengths: some tokenizers can split a UTF-8 rune
		// across runs, so counting each run independently is different.
		startCol := utf8.RuneCountInString(display[:start])
		width := float32(utf8.RuneCountInString(display[:end])-startCol) * key.scale
		x := 4 + float32(startCol)*key.scale
		width = max(width, 1)
		if x+width > MinimapWidth-2 {
			width = MinimapWidth - 2 - x
		}
		if width <= 0 {
			continue
		}
		color := run.Color
		if color == key.palette.Text {
			color = key.dim.Alpha(0.55)
		} else {
			color = color.Alpha(min(0.95, color.A*0.95+0.05))
		}
		// Coalesce with the previous block when the color matches and the
		// gap between them is under a pixel: runs of one color separated
		// by dropped whitespace runs (or split by run boundaries at the
		// cap) paint as one rect. Each block is one rasterized quad per
		// frame, and the miniature of a large file is hundreds of rows —
		// halving the quad count matters more than sub-pixel fidelity.
		if n := len(dst); n > 0 && dst[n-1].color == color && x-(dst[n-1].x+dst[n-1].width) < 1 {
			dst[n-1].width = max(dst[n-1].width, x+width-dst[n-1].x)
			continue
		}
		dst = append(dst, minimapBlock{x: x, width: width, color: color})
	}
	return dst
}

// minimapFoldMask marks the miniature rows hidden inside a collapsed fold.
// Row r (file line firstRow+r) is hidden when some folded range satisfies
// Start < line <= End — one pass over the folds marking their span on the
// row grid, O(folds + rows) with no per-frame map (the rail repaints every
// frame; a per-row linear scan of every fold was quadratic in practice on
// large files).
func minimapFoldMask(folds []FoldRange, firstRow, lastRow int) []bool {
	if firstRow >= lastRow {
		return nil
	}
	inside := make([]bool, lastRow-firstRow)
	for _, f := range folds {
		lo := max(f.Start+1, firstRow)
		hi := min(f.End, lastRow-1)
		for i := lo; i <= hi; i++ {
			inside[i-firstRow] = true
		}
	}
	return inside
}

// minimapFoldHeaderAt reports whether line is a folded range's header (the
// row that paints the collapsed-region band). Folds are sorted by Start
// (mergeFoldStarts), so membership is a binary search — safe for the
// thousands of ranges a large file carries.
func minimapFoldHeaderAt(folds []FoldRange) func(int) bool {
	return func(line int) bool {
		lo, hi := 0, len(folds)
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			if folds[mid].Start < line {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo < len(folds) && folds[lo].Start == line
	}
}
