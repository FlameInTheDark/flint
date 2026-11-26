// Code folding: collapsible regions anchored to brace structure, block
// parens (Go imports, go.mod require blocks) or indentation (Python,
// YAML). Ranges recompute per revision like the highlight states; the
// user's folded set persists across edits keyed by header line, so an
// edit inside a folded function keeps it folded.

package editor

import (
	"sort"
	"strings"

	gc "github.com/FlameInTheDark/go-gpui/color"
)

// FoldRange is one collapsible region: buffer lines [Start, End], where
// Start is the visible header (the opening brace line) and End the last
// hidden line.
type FoldRange struct {
	Start, End int
	// Paren marks require/import-style (...) blocks: the collapsed
	// placeholder shows "…)" instead of "{…}".
	Paren bool
}

// foldableBraceLangs can fold on brace structure; the tokenizers'
// string/comment runs keep brace characters inside literals from
// affecting the depth.
var foldableBraceLangs = map[string]bool{
	"go": true, "c": true, "cpp": true, "csharp": true, "java": true,
	"javascript": true, "typescript": true, "rust": true, "php": true,
	"json": true, "css": true,
}

// foldableParenLangs fold require/import-style paren blocks in addition
// to (or instead of) braces: Go folds `import (`, go.mod folds every
// directive block.
var foldableParenLangs = map[string]bool{
	"go":    true, // import ( ... )
	"gomod": true, // require ( ... ), replace ( ... ), ...
}

// foldableIndentLangs fold on indentation instead of braces.
var foldableIndentLangs = map[string]bool{
	"python": true,
	"yaml":   true,
}

// Foldable reports whether a highlighting language produces fold ranges.
func Foldable(lang string) bool {
	return foldableBraceLangs[lang] || foldableParenLangs[lang] || foldableIndentLangs[lang]
}

// foldPalette is the highlight palette used ONLY for fold computation:
// the depth counter only needs to tell literal/comment runs apart, and a
// fixed palette keeps the highlight cache hot across buffers.
var foldPalette = Palette{
	String:  gc.RGB(0x7F0001).Hsla(),
	Comment: gc.RGB(0x007F02).Hsla(),
}

// foldKey memoizes the fold ranges per (revision, language).
type foldKey struct {
	revision uint64
	lang     string
}

// FoldRanges returns the buffer's fold ranges (memoized per revision and
// language; nil when the language cannot fold).
func (b *Buffer) FoldRanges() []FoldRange {
	lang := b.LanguageOf()
	if b.foldKey.revision == b.Revision && b.foldKey.lang == lang {
		return b.foldList
	}
	var folds []FoldRange
	if Foldable(lang) {
		if foldableIndentLangs[lang] {
			folds = indentFolds(b.Lines)
		} else {
			folds = braceFolds(b.Lines, lang)
			if foldableParenLangs[lang] {
				folds = append(folds, parenFolds(b.Lines, lang)...)
			}
		}
		folds = mergeFoldStarts(folds)
	}
	b.foldKey = foldKey{revision: b.Revision, lang: lang}
	b.foldList = folds
	b.foldActiveValid = false
	// The folded set only keeps headers that still exist: an edit can
	// turn a fold header into ordinary text.
	for start := range b.foldFolded {
		if _, ok := b.foldStart(start); !ok {
			delete(b.foldFolded, start)
		}
	}
	return folds
}

// mergeFoldStarts keeps one range per start line (the longest End), so a
// line never renders two chevrons, and sorts by Start.
func mergeFoldStarts(folds []FoldRange) []FoldRange {
	if len(folds) == 0 {
		return nil
	}
	byStart := map[int]FoldRange{}
	for _, f := range folds {
		if f.End <= f.Start {
			continue // a fold must hide at least one line
		}
		if prev, ok := byStart[f.Start]; !ok || f.End > prev.End {
			byStart[f.Start] = f
		}
	}
	out := make([]FoldRange, 0, len(byStart))
	for _, f := range byStart {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// braceFolds walks the buffer with the language tokenizer's string/comment
// runs (so braces inside literals never affect depth) and emits one range
// per open brace that closes on a later line — nested blocks get their own
// ranges, like every editor's nested folding. Rune literals ('{' in Go)
// are skipped at the counter level: Go's tokenizer paints them as plain
// text.
func braceFolds(lines []string, lang string) []FoldRange {
	var folds []FoldRange
	var stack []int // open brace header lines, in nesting order
	st := StateNormal
	for i, line := range lines {
		runs, next := HighlightIn(line, lang, st, foldPalette)
		st = next
		off := 0
		for _, r := range runs {
			end := min(off+r.Len, len(line))
			seg := line[off:end]
			off = end
			// Braces inside strings and comments are text, not structure.
			if r.Color == foldPalette.String || r.Color == foldPalette.Comment {
				continue
			}
			for j := 0; j < len(seg); j++ {
				switch seg[j] {
				case '\'':
					// Rune literal ('{' etc.): skip to its close.
					j++
					for j < len(seg) && seg[j] != '\'' {
						if seg[j] == '\\' {
							j++
						}
						j++
					}
				case '{':
					stack = append(stack, i)
				case '}':
					if n := len(stack); n > 0 {
						open := stack[n-1]
						stack = stack[:n-1]
						if i > open {
							folds = append(folds, FoldRange{Start: open, End: i})
						}
					}
				}
			}
		}
	}
	return folds
}

// parenFolds folds require/import-style paren blocks: a line whose first
// word is a block directive and which ends with "(" opens a range that
// closes at the bare ")" line. In Go only import blocks fold (require is
// go.mod's); in go.mod every directive can open a block.
func parenFolds(lines []string, lang string) []FoldRange {
	var folds []FoldRange
	open := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if open >= 0 {
			if trimmed == ")" {
				if i > open {
					folds = append(folds, FoldRange{Start: open, End: i, Paren: true})
				}
				open = -1
			}
			continue
		}
		if !strings.HasSuffix(trimmed, "(") {
			continue
		}
		head := strings.TrimSuffix(trimmed, "(")
		head = strings.TrimSpace(head)
		word := head
		if k := strings.IndexAny(head, " \t"); k >= 0 {
			word = head[:k]
		}
		switch lang {
		case "gomod":
			if gomodDirectives[word] {
				open = i
			}
		case "go":
			if word == "import" {
				open = i
			}
		}
	}
	return folds
}

// indentFolds folds indentation-delimited regions (Python, YAML): a
// content line whose following non-blank lines are indented deeper folds
// until the first line at its own indent or shallower. Every nesting
// level gets its own range, like every editor's indent folding.
func indentFolds(lines []string) []FoldRange {
	var folds []FoldRange
	for i := 0; i < len(lines); i++ {
		indent := lineIndent(lines[i])
		last := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "" {
				continue // blank lines belong to the enclosing region
			}
			if lineIndent(lines[j]) > indent {
				last = j
				continue
			}
			break
		}
		if last > i {
			folds = append(folds, FoldRange{Start: i, End: last})
		}
	}
	return folds
}

// lineIndent measures a line's leading whitespace (tabs count as four
// columns so tab-indented files still nest monotonically).
func lineIndent(line string) int {
	n := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Fold state on the buffer
// ---------------------------------------------------------------------------

// foldStart returns the range starting exactly at line (nil = not a
// header). Callers must have called FoldRanges for the current revision.
func (b *Buffer) foldStart(line int) (FoldRange, bool) {
	i := sort.Search(len(b.foldList), func(i int) bool { return b.foldList[i].Start >= line })
	if i < len(b.foldList) && b.foldList[i].Start == line {
		return b.foldList[i], true
	}
	return FoldRange{}, false
}

// FoldAt reports whether line is a fold header and returns its range.
func (b *Buffer) FoldAt(line int) (FoldRange, bool) {
	b.FoldRanges()
	return b.foldStart(line)
}

// IsFolded reports whether the fold starting at line is collapsed.
func (b *Buffer) IsFolded(line int) bool {
	b.FoldRanges()
	return b.foldFolded[line]
}

// collapsedFolds caches the sorted collapsed ranges and their prefix-max
// ends, so rendering and seeking need not scan every fold for every row.
// Call FoldRanges first to refresh after an edit or language change.
func (b *Buffer) collapsedFolds() []FoldRange {
	if b.foldActiveValid {
		return b.foldActive
	}
	b.foldActive = b.foldActive[:0]
	b.foldEnds = b.foldEnds[:0]
	end := -1
	if len(b.foldFolded) > 0 {
		for _, f := range b.foldList {
			if b.foldFolded[f.Start] {
				b.foldActive = append(b.foldActive, f)
				end = max(end, f.End)
				b.foldEnds = append(b.foldEnds, end)
			}
		}
	}
	b.foldActiveValid = true
	return b.foldActive
}

// foldedRangeContaining returns the first collapsed range hiding line
// (header lines are always visible), or nil.
func (b *Buffer) foldedRangeContaining(line int) *FoldRange {
	active := b.collapsedFolds()
	i := sort.Search(len(active), func(i int) bool { return b.foldEnds[i] >= line })
	if i < len(active) && active[i].Start < line {
		return &active[i]
	}
	return nil
}

// IsLineVisible reports whether line is painted (fold headers and lines
// outside collapsed ranges are visible).
func (b *Buffer) IsLineVisible(line int) bool {
	if line < 0 || line >= len(b.Lines) {
		return false
	}
	b.FoldRanges() // refresh the memoized ranges after edits
	return b.foldedRangeContaining(line) == nil
}

// NextVisible returns the next visible line after line (the fold's end+1
// when line sits inside a collapsed region), or len(Lines) past the end.
func (b *Buffer) NextVisible(line int) int {
	b.FoldRanges()
	n := line + 1
	for n < len(b.Lines) {
		f := b.foldedRangeContaining(n)
		if f == nil {
			break
		}
		n = f.End + 1
	}
	return min(n, len(b.Lines))
}

// PrevVisible returns the previous visible line before line, or -1.
func (b *Buffer) PrevVisible(line int) int {
	b.FoldRanges()
	n := line - 1
	for n >= 0 {
		f := b.foldedRangeContaining(n)
		if f == nil {
			break
		}
		n = f.Start // the header itself is visible; nested folds keep walking
	}
	return n
}

// VisibleLineCount returns the painted line count (all lines minus the
// ones hidden inside collapsed ranges).
func (b *Buffer) VisibleLineCount() int {
	b.FoldRanges()
	n := len(b.Lines)
	for i := range b.foldList {
		if b.foldFolded[b.foldList[i].Start] {
			n -= b.foldList[i].End - b.foldList[i].Start
		}
	}
	return n
}

// ToggleFold collapses or expands the fold starting at line.
func (b *Buffer) ToggleFold(line int) {
	b.FoldRanges()
	if _, ok := b.foldStart(line); ok {
		b.ensureFoldSet()
		if b.foldFolded[line] {
			delete(b.foldFolded, line)
		} else {
			b.foldFolded[line] = true
		}
		b.foldActiveValid = false
	}
}

// Fold collapses the fold starting at line (a no-op when line is not a
// header).
func (b *Buffer) Fold(line int) {
	b.FoldRanges()
	if _, ok := b.foldStart(line); ok {
		b.ensureFoldSet()
		b.foldFolded[line] = true
		b.foldActiveValid = false
	}
}

// Unfold expands the fold starting at line.
func (b *Buffer) Unfold(line int) {
	delete(b.foldFolded, line)
	b.foldActiveValid = false
}

// UnfoldAt expands any collapsed range hiding line (the auto-unfold when
// the caret or an edit lands inside a folded region).
func (b *Buffer) UnfoldAt(line int) {
	if f := b.foldedRangeContaining(line); f != nil {
		delete(b.foldFolded, f.Start)
		b.foldActiveValid = false
	}
}

// UnfoldAtCursor expands any collapsed fold hiding the caret line (every
// editor's auto-unfold when keyboard navigation enters a folded region).
func (b *Buffer) UnfoldAtCursor() {
	b.FoldRanges()
	if line, _ := b.OffsetToLC(b.Cursor); b.foldedRangeContaining(line) != nil {
		b.UnfoldAt(line)
	}
}

// FoldAll collapses every fold range.
func (b *Buffer) FoldAll() {
	b.FoldRanges()
	b.ensureFoldSet()
	for _, f := range b.foldList {
		b.foldFolded[f.Start] = true
	}
	b.foldActiveValid = false
}

// ensureFoldSet lazily creates the folded-flag map (a freshly opened
// buffer with nothing folded carries no set).
func (b *Buffer) ensureFoldSet() {
	if b.foldFolded == nil {
		b.foldFolded = map[int]bool{}
	}
}

// UnfoldAll expands every collapsed fold.
func (b *Buffer) UnfoldAll() {
	b.foldFolded = map[int]bool{}
	b.foldActiveValid = false
}

// FoldedCount returns the number of collapsed ranges (the status bar's
// "N regions collapsed" hint; 0 = none).
func (b *Buffer) FoldedCount() int {
	b.FoldRanges()
	return len(b.collapsedFolds())
}
