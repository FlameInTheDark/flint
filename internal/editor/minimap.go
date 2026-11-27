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
		dst = append(dst, minimapBlock{x: x, width: width, color: color})
	}
	return dst
}
