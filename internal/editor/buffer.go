// Package editor implements the editor example's buffer model, syntax
// highlighting and the interactive code pane (a condensed port of the upstream editor's
// Editor + MultiBuffer concepts that fits the example's scope).
package editor

import (
	"bytes"
	"image"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BufferKind selects how a buffer is presented: plain text editing, a
// read-only hex dump (unknown binaries — opening a 50MB .dll as text
// wrecks both the layout and the memory) or a zoomable image view.
type BufferKind uint8

const (
	// KindText is the editable code buffer.
	KindText BufferKind = 0
	// KindHex renders read-only bytes as a hex dump.
	KindHex BufferKind = 1
	// KindImage renders a decoded image with zoom/pan.
	KindImage BufferKind = 2
	// KindDiff is a read-only comparison tab provided by the workspace.
	KindDiff BufferKind = 3
)

// maxHexBytes caps how much of a binary file the hex view loads (a full
// multi-GB dump is neither useful nor safe; the view notes the cut).
const maxHexBytes = 16 << 20

// maxImageBytes caps the encoded image size the viewer will decode (a
// 200MB "image" is usually a decompression bomb).
const maxImageBytes = 96 << 20

// Buffer is an editable text buffer. Offsets are byte offsets into the
// whole text; Cursor/Anchor delimit the selection (equal = no selection).
//
// The text is stored as lines plus a lineStarts index, so every offset
// mapping is O(1)/O(log n) and no operation ever joins or re-splits the
// whole document: rendering a 12,000-line file costs the same as a 20-line
// one (the pane is virtualized, and per-frame work no longer scales with
// the buffer size - the previous implementation scanned/joined the entire
// text per visible row and per keystroke, collapsing the frame rate on
// large files).
type Buffer struct {
	// Revision changes whenever the text is edited, including undo and redo.
	Revision uint64
	// Path is the backing file ("" for an untitled buffer).
	Path string
	// Name is the display name (tab title).
	Name string
	// Lines is the buffer content split on newlines.
	Lines []string

	// lineStarts[i] is the byte offset where line i begins; the last
	// entry plus the last line's length is the total byte length.
	// Maintained together with Lines by setLines.
	lineStarts []int
	// total is the full text length including newline separators.
	total int

	// lineCols[i] is line i's width in display columns: tabs advance to
	// the next tab stop (like every editor's "render tabs as spaces"), so
	// indentation aligns and the horizontal scrollbar can size itself
	// without touching the text system. Maintained by setLines/apply.
	lineCols []int
	// maxCols is the widest line's display column count (max of lineCols).
	maxCols int
	minimap minimapCache

	// Cursor and Anchor are selection endpoints in byte offsets.
	Cursor int
	Anchor int

	// Dirty reports unsaved changes (text differs from the saved copy).
	Dirty bool

	// savedLines is the content as loaded/saved on disk (dirty compare is
	// line-wise: no full-text join per keystroke).
	savedLines []string

	undo []Op
	redo []Op

	// ---- non-text buffers (Kind selects the presentation) ----
	// Kind is the presentation mode (text, hex, image).
	Kind BufferKind
	// Data holds the raw bytes of a hex buffer (capped at maxHexBytes).
	Data []byte
	// DataTruncated is set when a hex buffer was cut at the cap.
	DataTruncated bool
	// Image is the decoded image of an image buffer.
	Image image.Image
	// LangOverride is the status-bar language selection ("" = detect
	// from the file name; the language picker writes it).
	LangOverride string
}

// Op is one invertible edit.
type Op struct {
	Start, End int // replaced range before the edit
	Before     string
	After      string
}

// NewBuffer creates a buffer from content.
func NewBuffer(path, name, content string) *Buffer {
	if name == "" {
		name = "untitled"
		if path != "" {
			name = baseName(path)
		}
	}
	b := &Buffer{
		Path: path,
		Name: name,
	}
	b.setLines(strings.Split(content, "\n"))
	b.savedLines = append([]string(nil), b.Lines...)
	return b
}

// OpenBuffer loads a file from disk: text files open as editable code
// buffers; supported images (png/jpg/jpeg/gif/bmp) open in the image
// viewer; anything else binary (a NUL byte in the first 8KB, like every
// editor's heuristic) opens as a read-only hex dump — huge binaries as
// text were the app-killer.
func OpenBuffer(path string) (*Buffer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := baseName(path)
	switch {
	case IsImageName(name) && len(data) <= maxImageBytes:
		if img, imgErr := DecodeImage(data); imgErr == nil {
			b := NewBuffer(path, name, "")
			b.Kind = KindImage
			b.Image = img
			return b, nil
		}
	case isBinaryData(data):
		b := NewBuffer(path, name, "")
		b.Kind = KindHex
		b.Data = data
		if len(b.Data) > maxHexBytes {
			b.Data = b.Data[:maxHexBytes]
			b.DataTruncated = true
		}
		return b, nil
	}
	return NewBuffer(path, name, string(data)), nil
}

// isBinaryData applies the editor-standard heuristic: a NUL byte in the
// first 8KB means binary.
func isBinaryData(data []byte) bool {
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

// HexRowCount returns the hex view's row count (16 bytes per row).
func (b *Buffer) HexRowCount() int { return (len(b.Data) + 15) / 16 }

// LanguageOf returns the buffer's effective language: the status-bar
// override when set, else the name-based detection.
func (b *Buffer) LanguageOf() string {
	if b.LangOverride != "" {
		return b.LangOverride
	}
	return Language(b.Name)
}

// SetLanguageOverride stores the status-bar language selection ("" or
// "auto" clears it back to detection).
func (b *Buffer) SetLanguageOverride(lang string) {
	if lang == "auto" || lang == Language(b.Name) {
		b.LangOverride = ""
	} else {
		b.LangOverride = lang
	}
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// setLines installs new content and rebuilds the offset index.
func (b *Buffer) setLines(lines []string) {
	b.Lines = lines
	b.lineStarts = make([]int, len(lines))
	b.lineCols = make([]int, len(lines))
	off := 0
	maxCols := 0
	for i, line := range lines {
		b.lineStarts[i] = off
		off += len(line) + 1
		b.lineCols[i] = DisplayCols(line)
		if b.lineCols[i] > maxCols {
			maxCols = b.lineCols[i]
		}
	}
	if len(lines) > 0 {
		off -= 1 // no newline after the last line
	}
	b.total = off
	b.maxCols = maxCols
}

// Text returns the whole buffer text (joins; used by save and tests).
func (b *Buffer) Text() string { return strings.Join(b.Lines, "\n") }

// TextLen returns the whole buffer length in bytes (O(1)).
func (b *Buffer) TextLen() int { return b.total }

// LineCount returns the number of lines.
func (b *Buffer) LineCount() int {
	if b.Kind == KindHex {
		return b.HexRowCount()
	}
	return len(b.Lines)
}

// Line returns one line (clamped).
func (b *Buffer) Line(i int) string {
	if i < 0 || i >= len(b.Lines) {
		return ""
	}
	return b.Lines[i]
}

// LineStart returns the byte offset where line i begins (O(1)).
func (b *Buffer) LineStart(i int) int {
	if i < 0 || i >= len(b.lineStarts) {
		return 0
	}
	return b.lineStarts[i]
}

// LineCols returns line i's width in display columns (tabs = TabSize spaces).
func (b *Buffer) LineCols(i int) int {
	if i < 0 || i >= len(b.lineCols) {
		return 0
	}
	return b.lineCols[i]
}

// MaxLineCols returns the widest line's display column count (O(1); the
// per-line widths are maintained incrementally by edits).
func (b *Buffer) MaxLineCols() int { return b.maxCols }

// locate maps a byte offset to (line, col-in-line) via binary search over
// the line index. An offset pointing at a line's newline belongs to that
// line (col == len(line)); offset == total maps to the last line's end.
func (b *Buffer) locate(off int) (int, int) {
	if len(b.Lines) == 0 {
		return 0, 0
	}
	if off >= b.total {
		last := len(b.Lines) - 1
		return last, len(b.Lines[last])
	}
	i := sort.Search(len(b.lineStarts), func(i int) bool {
		return b.lineStarts[i] > off
	}) - 1
	if i < 0 {
		i = 0
	}
	return i, off - b.lineStarts[i]
}

// OffsetToLC converts a byte offset to (line, byte-col), clamped.
func (b *Buffer) OffsetToLC(off int) (int, int) {
	if off < 0 {
		return 0, 0
	}
	line, col := b.locate(off)
	return line, min(col, len(b.Lines[line]))
}

// LCToOffset converts (line, byte-col) to a byte offset, clamped.
func (b *Buffer) LCToOffset(line, col int) int {
	if line < 0 {
		return 0
	}
	if line >= len(b.Lines) {
		return b.total
	}
	return b.lineStarts[line] + min(col, len(b.Lines[line]))
}

// LineLen returns line i's length in bytes.
func (b *Buffer) LineLen(i int) int { return len(b.Line(i)) }

// RuneCol returns the cursor's column in runes of its line.
func (b *Buffer) RuneCol() int {
	line, col := b.OffsetToLC(b.Cursor)
	return utf8.RuneCountInString(b.Line(line)[:col])
}

// RuneLineCol returns (1-based line, 1-based rune column) of the cursor.
func (b *Buffer) RuneLineCol() (int, int) {
	line, col := b.OffsetToLC(b.Cursor)
	return line + 1, utf8.RuneCountInString(b.Line(line)[:col]) + 1
}

// byteAt returns the buffer byte at off ('\n' for line separators, 0 past
// the end). O(log n): no whole-text scan (used by clampOff to snap to rune
// boundaries).
func (b *Buffer) byteAt(off int) byte {
	if off < 0 || off >= b.total {
		return 0
	}
	line, col := b.locate(off)
	if col >= len(b.Lines[line]) {
		return '\n' // the line separator
	}
	return b.Lines[line][col]
}

// decodeRuneAt returns the rune size at off (the newline separator counts
// as one byte). O(log n).
func (b *Buffer) decodeRuneAt(off int) int {
	if off < 0 || off >= b.total {
		return 0
	}
	line, col := b.locate(off)
	if col >= len(b.Lines[line]) {
		return 1 // '\n'
	}
	_, sz := utf8.DecodeRuneInString(b.Lines[line][col:])
	return sz
}

// decodeRuneBefore returns the rune size of the rune ending at off.
// O(log n).
func (b *Buffer) decodeRuneBefore(off int) int {
	if off <= 0 {
		return 0
	}
	if off > b.total {
		off = b.total
	}
	line, col := b.locate(off - 1)
	if col >= len(b.Lines[line]) {
		return 1 // the '\n' ending this line
	}
	_, sz := utf8.DecodeLastRuneInString(b.Lines[line][:col+1])
	return sz
}

// rangeText returns the substring [s,e) by splicing lines - O(selection),
// never joins the whole buffer.
func (b *Buffer) rangeText(s, e int) string {
	if s < 0 {
		s = 0
	}
	if e > b.total {
		e = b.total
	}
	if e <= s {
		return ""
	}
	si, so := b.locate(s)
	ei, eo := b.locate(e)
	if si == ei {
		return b.Lines[si][so:min(eo, len(b.Lines[si]))]
	}
	var sb strings.Builder
	sb.Grow(e - s)
	sb.WriteString(b.Lines[si][so:])
	for i := si + 1; i < ei; i++ {
		sb.WriteByte('\n')
		sb.WriteString(b.Lines[i])
	}
	sb.WriteByte('\n')
	sb.WriteString(b.Lines[ei][:min(eo, len(b.Lines[ei]))])
	return sb.String()
}

// ClampCursor keeps cursor and anchor inside the buffer.
func (b *Buffer) ClampCursor() {
	if b.Cursor < 0 {
		b.Cursor = 0
	}
	if b.Cursor > b.total {
		b.Cursor = b.total
	}
	if b.Anchor < 0 {
		b.Anchor = 0
	}
	if b.Anchor > b.total {
		b.Anchor = b.total
	}
}

// Selection returns the selection as (start, end) with start <= end.
func (b *Buffer) Selection() (int, int) {
	if b.Cursor < b.Anchor {
		return b.Cursor, b.Anchor
	}
	return b.Anchor, b.Cursor
}

// HasSelection reports whether the selection is non-empty.
func (b *Buffer) HasSelection() bool { return b.Cursor != b.Anchor }

// SelectedText returns the selected substring.
func (b *Buffer) SelectedText() string {
	s, e := b.Selection()
	return b.rangeText(s, e)
}

// SetCursor collapses the selection at off.
func (b *Buffer) SetCursor(off int) {
	b.Cursor = b.clampOff(off)
	b.Anchor = b.Cursor
}

// ExtendCursor moves the cursor, keeping the anchor (shift+click / drag).
func (b *Buffer) ExtendCursor(off int) {
	b.Cursor = b.clampOff(off)
}

// SelectAll selects the whole buffer.
func (b *Buffer) SelectAll() {
	b.Anchor = 0
	b.Cursor = b.total
}

// SelectWordAt selects the word around off (word-local; words never span
// lines, so no whole-text scan is needed).
func (b *Buffer) SelectWordAt(off int) {
	off = b.clampOff(off)
	line, col := b.locate(off)
	start, end := wordRange(b.Lines[line], col)
	b.Anchor = b.lineStarts[line] + start
	b.Cursor = b.lineStarts[line] + end
}

// SelectLineAt selects the line containing off (without the newline).
func (b *Buffer) SelectLineAt(off int) {
	line, _ := b.OffsetToLC(b.clampOff(off))
	b.Anchor = b.lineStarts[line]
	b.Cursor = b.lineStarts[line] + len(b.Lines[line])
}

// Insert replaces the selection with text and records an undo op. The
// selection collapses to just after the inserted text (editor convention).
func (b *Buffer) Insert(text string) {
	s, e := b.Selection()
	before := b.rangeText(s, e)
	b.apply(s, e, text)
	b.Cursor = s + len(text)
	b.Anchor = b.Cursor
	b.undo = append(b.undo, Op{Start: s, End: s + len(text), Before: before, After: text})
	b.redo = nil
	b.updateDirty()
}

// DeleteBackward deletes the selection, or one rune before the cursor.
func (b *Buffer) DeleteBackward() {
	if b.HasSelection() {
		b.Insert("")
		return
	}
	if b.Cursor == 0 {
		return
	}
	sz := b.decodeRuneBefore(b.Cursor)
	b.SetCursor(b.Cursor)
	b.Anchor = b.Cursor - sz
	b.Insert("")
}

// DeleteForward deletes the selection, or one rune after the cursor.
func (b *Buffer) DeleteForward() {
	if b.HasSelection() {
		b.Insert("")
		return
	}
	if b.Cursor >= b.total {
		return
	}
	sz := b.decodeRuneAt(b.Cursor)
	b.Anchor = b.Cursor
	b.Cursor = b.Cursor + sz
	b.Insert("")
}

// Move moves the cursor; extend keeps the anchor (shift+arrows). word
// makes left/right move by words. All paths are line-local (or O(log n)):
// moving the cursor never scans or joins the whole document.
func (b *Buffer) Move(dir string, extend, word bool, page int) {
	off := b.Cursor
	line, col := b.locate(off)
	lineText := b.Lines[line]
	runeCol := utf8.RuneCountInString(lineText[:col])

	switch dir {
	case "left":
		if word {
			start, _ := wordRange(lineText, col)
			if start >= col { // at a word start: jump to the previous word
				if col > 0 {
					start, _ = wordRange(lineText, col-1)
				} else if line > 0 {
					prev := b.Lines[line-1]
					line--
					_, start = wordRange(prev, len(prev))
					lineText = prev
				}
			}
			off = b.lineStarts[line] + start
		} else if off > 0 {
			off -= b.decodeRuneBefore(off)
		}
	case "right":
		if word {
			_, end := wordRange(lineText, col)
			if end <= col { // at a word end: jump to the next word
				if col < len(lineText) {
					_, end = wordRange(lineText, col+1)
				} else if line < len(b.Lines)-1 {
					line++
					lineText = b.Lines[line]
					_, end = wordRange(lineText, 0)
				}
			}
			off = b.lineStarts[line] + end
		} else if off < b.total {
			off += b.decodeRuneAt(off)
		}
	case "up":
		if line > 0 {
			prev := b.Lines[line-1]
			c := min(runeCol, utf8.RuneCountInString(prev))
			off = b.lineStarts[line-1] + runeOffset(prev, c)
		}
	case "down":
		if line < len(b.Lines)-1 {
			next := b.Lines[line+1]
			c := min(runeCol, utf8.RuneCountInString(next))
			off = b.lineStarts[line+1] + runeOffset(next, c)
		}
	case "home":
		off = b.lineStarts[line]
	case "end":
		off = b.lineStarts[line] + len(lineText)
	case "pageup":
		l := max(0, line-page)
		lt := b.Lines[l]
		off = b.lineStarts[l] + runeOffset(lt, min(runeCol, utf8.RuneCountInString(lt)))
	case "pagedown":
		l := min(len(b.Lines)-1, line+page)
		lt := b.Lines[l]
		off = b.lineStarts[l] + runeOffset(lt, min(runeCol, utf8.RuneCountInString(lt)))
	case "start":
		off = 0
	case "enddoc":
		off = b.total
	}

	b.Cursor = b.clampOff(off)
	if !extend {
		b.Anchor = b.Cursor
	}
}

// Undo reverts the last op.
func (b *Buffer) Undo() bool {
	if len(b.undo) == 0 {
		return false
	}
	op := b.undo[len(b.undo)-1]
	b.undo = b.undo[:len(b.undo)-1]
	b.apply(op.Start, op.End, op.Before)
	b.redo = append(b.redo, op)
	b.SetCursor(op.Start + len(op.Before))
	b.updateDirty()
	return true
}

// Redo re-applies the last undone op. Op.Start is the edit origin in both
// the pre-edit and post-edit texts (Insert pushes Start = s), so redo
// replaces [Start, Start+len(Before)) with After again.
func (b *Buffer) Redo() bool {
	if len(b.redo) == 0 {
		return false
	}
	op := b.redo[len(b.redo)-1]
	b.redo = b.redo[:len(b.redo)-1]
	s := op.Start
	e := op.Start + len(op.Before)
	b.apply(s, e, op.After)
	b.undo = append(b.undo, Op{Start: s, End: s + len(op.After), Before: op.Before, After: op.After})
	b.SetCursor(s + len(op.After))
	b.updateDirty()
	return true
}

// CanUndo reports undo availability.
func (b *Buffer) CanUndo() bool { return len(b.undo) > 0 }

// CanRedo reports redo availability.
func (b *Buffer) CanRedo() bool { return len(b.redo) > 0 }

// Save writes the buffer to Path.
func (b *Buffer) Save() error {
	if b.Path == "" {
		return os.ErrInvalid
	}
	// Hex and image buffers are read-only views: writing their (empty)
	// text model would truncate the backing file.
	if b.Kind != KindText {
		return nil
	}
	if err := os.WriteFile(b.Path, []byte(b.Text()), 0o644); err != nil {
		return err
	}
	b.savedLines = append([]string(nil), b.Lines...)
	b.Dirty = false
	return nil
}

// updateDirty recomputes Dirty against the saved copy (undoing all edits
// returns the buffer to a clean state, like the upstream editor). Line-wise compare: no
// whole-text join per keystroke.
func (b *Buffer) updateDirty() {
	b.Dirty = !linesEqual(b.Lines, b.savedLines)
}

func linesEqual(a, c []string) bool {
	if len(a) != len(c) {
		return false
	}
	for i := range a {
		if a[i] != c[i] {
			return false
		}
	}
	return true
}

// apply replaces [s,e) with text, fixing cursor/anchor. Only the affected
// lines are re-split; the offset index is rebuilt without joining the
// document (O(document lines), not O(document bytes)).
func (b *Buffer) apply(s, e int, text string) {
	b.Revision++
	s = b.clampOff(s)
	e = b.clampOff(e)
	if e < s {
		s, e = e, s
	}
	si, so := b.locate(s)
	ei, eo := b.locate(e)

	// Splice the affected lines: [prefix] + split(middle) + [suffix].
	prefix := b.Lines[si][:so]
	suffix := ""
	if ei < len(b.Lines) {
		l := b.Lines[ei]
		if eo >= len(l) {
			eo = len(l)
		}
		suffix = l[eo:]
	}
	middle := strings.Split(prefix+text+suffix, "\n")

	// Splice the display-column widths together with the lines: the
	// horizontal scrollbar needs the widest line without re-measuring
	// the whole document on every keystroke. When the old maximum came
	// from the replaced span (and shrank), rescan all lines.
	rescanMax := false
	for i := si; i <= ei && i < len(b.lineCols); i++ {
		if b.lineCols[i] >= b.maxCols {
			rescanMax = true
			break
		}
	}

	next := make([]string, 0, len(b.Lines)-(ei-si+1)+len(middle))
	nextCols := make([]int, 0, len(next))
	next = append(next, b.Lines[:si]...)
	nextCols = append(nextCols, b.lineCols[:si]...)
	for _, l := range middle {
		next = append(next, l)
		nextCols = append(nextCols, DisplayCols(l))
	}
	if ei+1 <= len(b.Lines) {
		next = append(next, b.Lines[ei+1:]...)
		nextCols = append(nextCols, b.lineCols[ei+1:]...)
	}
	b.setLinesFromParts(next, nextCols, rescanMax)

	// Reposition cursor/anchor: endpoints inside the replaced range move
	// to s; endpoints after shift by the delta.
	delta := len(text) - (e - s)
	fix := func(off int) int {
		switch {
		case off <= s:
			return off
		case off >= e:
			return off + delta
		default:
			return s + len(text)
		}
	}
	b.Cursor = fix(b.Cursor)
	b.Anchor = fix(b.Anchor)
}

// clampOff clamps off into the buffer and snaps forward to a rune
// boundary so the cursor never splits a rune. O(log n).
func (b *Buffer) clampOff(off int) int {
	if off < 0 {
		return 0
	}
	if off >= b.total {
		return b.total
	}
	for off < b.total && !utf8.RuneStart(b.byteAt(off)) {
		off++
	}
	return off
}

func substring(s string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	if end < start {
		return ""
	}
	return s[start:end]
}

func runeOffset(s string, runeIdx int) int {
	n := 0
	for i := range s {
		if n == runeIdx {
			return i
		}
		n++
	}
	return len(s)
}

// tabStop is the tab stop distance in display columns (editor
// convention: 4). It is view-wide state — like the font size — changed by
// the settings panel; every display-column measurement in this package
// goes through tabAdvance, so one var keeps them all consistent.
var tabStop = 4

// SetTabSize sets the tab stop distance, clamped to 2..8 columns.
func SetTabSize(n int) {
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	tabStop = n
}

// TabSize returns the current tab stop distance in display columns.
func TabSize() int { return tabStop }

// tabAdvance returns how many display columns a tab occupies at column cols:
// it advances to the NEXT multiple of the tab stop, exactly how other editors
// align mixed tab/space text ("a\tb" renders with b at column 4, not 5).
func tabAdvance(cols int) int {
	return tabStop - cols%tabStop
}

// DisplayCols returns a line's width in display columns: tabs advance to the
// next tab stop, everything else one column per rune. Editors render tabs
// as aligned spaces so indentation lines up regardless of how the file was
// indented.
func DisplayCols(s string) int {
	cols := 0
	for _, r := range s {
		if r == '\t' {
			cols += tabAdvance(cols)
		} else {
			cols++
		}
	}
	return cols
}

// DisplayLine expands tabs into spaces for rendering, advancing to the next
// tab stop like other editors.
func DisplayLine(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var sb strings.Builder
	cols := 0
	for _, r := range s {
		if r == '\t' {
			n := tabAdvance(cols)
			for i := 0; i < n; i++ {
				sb.WriteByte(' ')
			}
			cols += n
		} else {
			sb.WriteRune(r)
			cols++
		}
	}
	return sb.String()
}

// setLinesFromParts installs lines with precomputed widths (the apply fast
// path avoids re-measuring unchanged lines).
func (b *Buffer) setLinesFromParts(lines []string, cols []int, rescanMax bool) {
	b.Lines = lines
	b.lineCols = cols
	b.lineStarts = make([]int, len(lines))
	off := 0
	maxCols := 0
	if rescanMax {
		for i, line := range lines {
			b.lineStarts[i] = off
			off += len(line) + 1
			c := DisplayCols(line)
			b.lineCols[i] = c
			if c > maxCols {
				maxCols = c
			}
		}
	} else {
		for i, line := range lines {
			b.lineStarts[i] = off
			off += len(line) + 1
			if cols[i] > maxCols {
				maxCols = cols[i]
			}
		}
	}
	if len(lines) > 0 {
		off -= 1 // no newline after the last line
	}
	b.total = off
	b.maxCols = maxCols
}

// wordRange expands off to the surrounding word ([start, end) byte range)
// within one line's text. Words never span lines, so callers run this on
// the line text only (no whole-document scans).
func wordRange(text string, off int) (int, int) {
	if len(text) == 0 {
		return 0, 0
	}
	off = max(0, min(off, len(text)))
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

	// Classify the rune at off (or before it at the end).
	at := off
	if at >= len(text) {
		at = len(text) - 1
	}
	r, _ := utf8.DecodeRuneInString(text[at:])
	class := 0 // 0 whitespace, 1 word, 2 punctuation
	switch {
	case isWord(r):
		class = 1
	case !unicode.IsSpace(r):
		class = 2
	}
	if class == 0 {
		return off, off
	}

	start := off
	for start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if classOf(r) != class {
			break
		}
		start -= utf8.RuneLen(r)
	}
	end := off
	for end < len(text) {
		r, sz := utf8.DecodeRuneInString(text[end:])
		if classOf(r) != class {
			break
		}
		end += sz
	}
	return start, end
}

func classOf(r rune) int {
	switch {
	case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
		return 1
	case unicode.IsSpace(r):
		return 0
	default:
		return 2
	}
}
