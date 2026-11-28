// Rainbow brackets: depth-colored bracket pairs plus the caret's
// matching-pair highlight, the "bracket pair colorization" /
// behavior set. Bracket colors ride the per-line highlight runs,
// so a bracket keeps its rainbow color through selection and find
// backgrounds; brackets inside string and comment literals never count
// toward a depth because the scanner below skips exactly the same
// literal regions the tokenizers do.
//
// The nesting depth each line STARTS in is memoized per (revision,
// language) on the buffer like the highlight states: a pair spanning
// hundreds of lines colors the closer with the opener's depth without
// ever rescanning the file. The caret's matching bracket walks the same
// scanner line-by-line outward from the cursor, capped, so an unbalanced
// pathological file cannot turn a keystroke into an O(document) scan.

package editor

import (
	"strings"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/text"
)

// bracketHit is one bracket character found outside literals, at a byte
// offset within its line, at its 1-based nesting depth (an opener and
// its closer share the depth, and therefore the color).
type bracketHit struct {
	pos   int
	open  bool
	depth int
}

// hasBrackets reports whether the language tokenizes brackets. Plain
// text, markdown (its [] is link syntax) and go.sum carry no pairs.
func hasBrackets(lang string) bool {
	switch lang {
	case "go", "gomod", "json", "yaml", "toml":
		return true
	}
	return genericLang[lang] != nil
}

// isOpenerByte / isCloserByte classify the six pair characters.
func isOpenerByte(c byte) bool { return c == '(' || c == '[' || c == '{' }
func isCloserByte(c byte) bool { return c == ')' || c == ']' || c == '}' }

// closerOf maps an opener to its closer.
func closerOf(c byte) (byte, bool) {
	switch c {
	case '(':
		return ')', true
	case '[':
		return ']', true
	case '{':
		return '}', true
	}
	return 0, false
}

// openerOf maps a closer to its opener.
func openerOf(c byte) (byte, bool) {
	switch c {
	case ')':
		return '(', true
	case ']':
		return '[', true
	case '}':
		return '{', true
	}
	return 0, false
}

// bracketScanIn collects the bracket characters of one line that live
// outside strings, chars and comments, at the given start depth. It
// returns the hits (nil when the language has no brackets or the line
// has none) and the depth after the line. Every skip rule mirrors the
// matching tokenizer (goRunsIn / genericRunsIn / jsonRuns / yamlRuns):
// when they disagree, the painted colors and the cached depths drift
// apart, so each case below is a deliberate copy of its tokenizer's
// transitions — including Go's rune literals (goRunsIn colors them like
// strings, but their brackets are not real pairs and must not count)
// and YAML/TOML's mid-line `#` (yamlRuns paints it as plain text, so it
// must not hide brackets either).
func bracketScanIn(line, lang string, st LineState, depth int) (hits []bracketHit, end int) {
	if !hasBrackets(lang) {
		return nil, 0
	}
	switch lang {
	case "go", "gomod":
		return scanBracketsGo(line, st, depth)
	case "json":
		return scanBracketsJSON(line, depth)
	case "yaml", "toml":
		return scanBracketsFlow(line, depth)
	default:
		if def := genericLang[lang]; def != nil {
			return scanBracketsGeneric(line, def, st, depth)
		}
		return nil, 0
	}
}

// scanBracketsGo is the Go/go.mod scanner: interpreted strings and rune
// literals end at the newline, raw strings and /* */ comments carry over
// through st (mirroring goRunsIn).
func scanBracketsGo(line string, st LineState, depth int) (hits []bracketHit, end int) {
	i := 0
	if st&StateRawString != 0 {
		e := strings.IndexByte(line, '`')
		if e < 0 {
			return nil, depth
		}
		i = e + 1
	} else if st&StateBlockComment != 0 {
		e := strings.Index(line, "*/")
		if e < 0 {
			return nil, depth
		}
		i = e + 2
	}
	for i < len(line) {
		c := line[i]
		switch {
		case strings.HasPrefix(line[i:], "//"):
			return hits, depth
		case strings.HasPrefix(line[i:], "/*"):
			e := strings.Index(line[i+2:], "*/")
			if e < 0 {
				return hits, depth
			}
			i += e + 4
		case c == '"':
			n, _ := scanQuoted(line[i:], '"', true)
			i += n
		case c == '\'':
			// A rune literal. Not a string for the tokenizer, but its
			// content is not code: brackets inside it never count.
			n, _ := scanQuoted(line[i:], '\'', true)
			i += n
		case c == '`':
			n, closed := scanQuoted(line[i:], '`', false)
			i += n
			if !closed {
				return hits, depth
			}
		case isOpenerByte(c):
			depth++
			hits = append(hits, bracketHit{pos: i, open: true, depth: depth})
			i++
		case isCloserByte(c):
			if depth > 0 {
				hits = append(hits, bracketHit{pos: i, open: false, depth: depth})
				depth--
			}
			i++
		default:
			i++
		}
	}
	return hits, depth
}

// scanBracketsGeneric mirrors genericRunsIn: line comments, block
// comments, Python triple quotes, and the three quote forms (" and '
// strings end at the newline; backtick template/command literals carry
// over through st).
func scanBracketsGeneric(line string, def *genericLangDef, st LineState, depth int) (hits []bracketHit, end int) {
	i := 0
	if st&(StateTripleDouble|StateTripleSingle) != 0 {
		quote := `"""`
		if st&StateTripleSingle != 0 {
			quote = "'''"
		}
		e := strings.Index(line[i:], quote)
		if e < 0 {
			return nil, depth
		}
		i += e + 3
	} else if st&StateBlockComment != 0 {
		e := strings.Index(line[i:], "*/")
		if e < 0 {
			return nil, depth
		}
		i += e + 2
	} else if st&StateRawString != 0 {
		e := strings.IndexByte(line[i:], '`')
		if e < 0 {
			return nil, depth
		}
		i += e + 1
	}
	for i < len(line) {
		rest := line[i:]
		c := rest[0]
		lineComment := false
		for _, p := range def.lineComments {
			if strings.HasPrefix(rest, p) {
				lineComment = true
				break
			}
		}
		if lineComment {
			return hits, depth
		}
		if def.blockComments && strings.HasPrefix(rest, "/*") {
			e := strings.Index(rest[2:], "*/")
			if e < 0 {
				return hits, depth
			}
			i += e + 4
			continue
		}
		if def.tripleQuotes && (strings.HasPrefix(rest, `"""`) || strings.HasPrefix(rest, "'''")) {
			quote := `"""`
			if rest[0] == '\'' {
				quote = "'''"
			}
			e := strings.Index(rest[3:], quote)
			if e < 0 {
				return hits, depth
			}
			i += e + 6
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			n, closed := scanQuoted(rest, c, true)
			i += n
			if !closed && c == '`' {
				return hits, depth // the literal spans lines (st carries it)
			}
			continue
		}
		switch c {
		case '(', '[', '{':
			depth++
			hits = append(hits, bracketHit{pos: i, open: true, depth: depth})
		case ')', ']', '}':
			if depth > 0 {
				hits = append(hits, bracketHit{pos: i, open: false, depth: depth})
				depth--
			}
		}
		i++
	}
	return hits, depth
}

// scanBracketsJSON mirrors jsonRuns: quoted strings hide brackets; JSON
// carries no comments.
func scanBracketsJSON(line string, depth int) (hits []bracketHit, end int) {
	for i := 0; i < len(line); {
		c := line[i]
		if c == '"' {
			n, _ := scanQuoted(line[i:], '"', true)
			i += n
			continue
		}
		switch c {
		case '{', '[':
			depth++
			hits = append(hits, bracketHit{pos: i, open: true, depth: depth})
		case '}', ']':
			if depth > 0 {
				hits = append(hits, bracketHit{pos: i, open: false, depth: depth})
				depth--
			}
		}
		i++
	}
	return hits, depth
}

// scanBracketsFlow mirrors yamlRuns for YAML/TOML: quoted strings hide
// brackets; the tokenizer paints a mid-line # as plain text, so # must
// not hide brackets here either.
func scanBracketsFlow(line string, depth int) (hits []bracketHit, end int) {
	for i := 0; i < len(line); {
		c := line[i]
		if c == '"' || c == '\'' {
			n, _ := scanQuoted(line[i:], c, true)
			i += n
			continue
		}
		switch c {
		case '{', '[':
			depth++
			hits = append(hits, bracketHit{pos: i, open: true, depth: depth})
		case '}', ']':
			if depth > 0 {
				hits = append(hits, bracketHit{pos: i, open: false, depth: depth})
				depth--
			}
		}
		i++
	}
	return hits, depth
}

// bracketColor picks the depth's color (cycling past the last entry,
// like every rainbow-bracket implementation's configurable cycle).
func bracketColor(p Palette, depth int) gc.Hsla {
	if depth < 1 {
		depth = 1
	}
	return p.Brackets[(depth-1)%len(p.Brackets)]
}

// paintBrackets repaints the hit bytes with the palette's depth colors,
// splitting runs as needed (the same run-splitting discipline the
// selection overlay uses, so backgrounds keep applying afterwards). A
// palette without bracket colors (zero alpha) leaves the runs untouched:
// palettes built without the field keep the plain token colors, which
// also keeps older highlight tests byte-identical.
func paintBrackets(runs []text.TextRun, hits []bracketHit, p Palette) []text.TextRun {
	if p.Brackets[0].A == 0 || len(hits) == 0 {
		return runs
	}
	var out []text.TextRun
	pos := 0
	hi := 0
	for _, r := range runs {
		start, end := pos, pos+r.Len
		pos = end
		segStart := start
		for hi < len(hits) && hits[hi].pos < end {
			h := hits[hi]
			hi++
			if h.pos < segStart {
				continue
			}
			if h.pos > segStart {
				out = append(out, text.TextRun{Len: h.pos - segStart, Font: r.Font, Color: r.Color})
			}
			out = append(out, text.TextRun{Len: 1, Font: r.Font, Color: bracketColor(p, h.depth)})
			segStart = h.pos + 1
		}
		if segStart < end {
			out = append(out, text.TextRun{Len: end - segStart, Font: r.Font, Color: r.Color})
		}
	}
	return out
}

// maxBracketScanLines bounds the matching-bracket search: an unbalanced
// pathological file must not turn a caret move into an O(document) walk.
const maxBracketScanLines = 10000

// bracketAtCursor reports the bracket byte the caret sits on or right
// after (the two positions editors match from).
func (b *Buffer) bracketAtCursor() (int, byte, bool) {
	if c := b.byteAt(b.Cursor); isOpenerByte(c) || isCloserByte(c) {
		return b.Cursor, c, true
	}
	if c := b.byteAt(b.Cursor - 1); isOpenerByte(c) || isCloserByte(c) {
		return b.Cursor - 1, c, true
	}
	return 0, 0, false
}

// bracketMatchOf finds the pair offset of the bracket at off. The scan
// verifies the bracket is real code (a bracket inside a string or
// comment produces no hit in the scanner, so it never matches). Forward
// and backward walks share the cached per-line depths, capped at
// maxBracketScanLines. ok=false leaves the highlight off.
func (b *Buffer) bracketMatchOf(off int, lang string, states []LineState, depths []uint8) (int, bool) {
	line, col := b.OffsetToLC(off)
	if line >= len(b.Lines) || col >= len(b.Lines[line]) || line >= len(states) {
		return 0, false
	}
	startDepth := 0
	if line < len(depths) {
		startDepth = int(depths[line])
	}
	hits, _ := bracketScanIn(b.Lines[line], lang, states[line], startDepth)
	var d int
	found := false
	for _, h := range hits {
		if h.pos == col {
			d, found = h.depth, true
			break
		}
	}
	if !found {
		return 0, false // not a code bracket (literal or unknown byte)
	}
	if isOpenerByte(b.Lines[line][col]) {
		// Forward: the closer with the same depth, in this line after the
		// opener or in later lines while the depth stays above d.
		for _, h := range hits {
			if h.pos > col && !h.open && h.depth == d {
				return b.LCToOffset(line, h.pos), true
			}
		}
		for j := line + 1; j < len(b.Lines) && j-line <= maxBracketScanLines; j++ {
			if int(depths[j]) < d {
				return 0, false // depth already dropped: unbalanced
			}
			hj, _ := bracketScanIn(b.Lines[j], lang, states[j], int(depths[j]))
			for _, h := range hj {
				if !h.open && h.depth == d {
					return b.LCToOffset(j, h.pos), true
				}
			}
		}
		return 0, false
	}
	// Backward: the last opener at this depth before the closer, in this
	// line or in earlier lines (one whose end depth reaches d while its
	// start depth does not).
	last := -1
	for _, h := range hits {
		if h.pos < col && h.open && h.depth == d {
			last = h.pos
		}
	}
	if last >= 0 {
		return b.LCToOffset(line, last), true
	}
	for j := line - 1; j >= 0 && line-j <= maxBracketScanLines; j-- {
		endD := 0
		if j+1 < len(depths) {
			endD = int(depths[j+1])
		}
		if endD < d {
			return 0, false // the opener never existed: unbalanced
		}
		if int(depths[j]) < d {
			hj, _ := bracketScanIn(b.Lines[j], lang, states[j], int(depths[j]))
			for _, h := range hj {
				if h.open && h.depth == d {
					last = h.pos // the last opener at depth d in line j
				}
			}
			if last >= 0 {
				return b.LCToOffset(j, last), true
			}
			return 0, false
		}
	}
	return 0, false
}
