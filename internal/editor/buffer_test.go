package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/font"
	"github.com/FlameInTheDark/go-gpui/text"
)

func mk(t *testing.T, content string) *Buffer {
	t.Helper()
	return NewBuffer("", "test.go", content)
}

func TestOffsetLineColRoundTrip(t *testing.T) {
	b := mk(t, "hello\nwörld\n")
	for off := 0; off <= len(b.Text()); off++ {
		line, col := b.OffsetToLC(off)
		if back := b.LCToOffset(line, col); back != off {
			t.Fatalf("offset %d: line %d col %d -> %d", off, line, col, back)
		}
	}
}

// TestTabStopExpansion pins the tab-stop rendering contract: a tab advances
// to the NEXT multiple of TabSize (the way other editors align mixed
// tab/space text), not a fixed TabSize-wide block.
func TestTabStopExpansion(t *testing.T) {
	cases := []struct {
		raw      string
		wantCols int
		wantLine string
	}{
		{"\treturn", 4 + 6, "    return"}, // leading tab: full width
		{"\t\t\tf()", 12 + 3, "            f()"},
		{"a\tb", 4 + 1, "a   b"},        // mid-line tab pads to col 4, not col 5
		{"ab\tc", 4 + 1, "ab  c"},       // pads to the next stop
		{"abc\td", 4 + 1, "abc d"},      // col 3 -> stop at 4 (one space)
		{"abcd\te", 8 + 1, "abcd    e"}, // col 4 -> next stop at 8
		{"\t", TabSize(), "    "},
		{"no tabs", 7, "no tabs"},
		{"", 0, ""},
	}

	for _, c := range cases {
		if got := DisplayCols(c.raw); got != c.wantCols {
			t.Errorf("DisplayCols(%q) = %d, want %d", c.raw, got, c.wantCols)
		}
		if got := DisplayLine(c.raw); got != c.wantLine {
			t.Errorf("DisplayLine(%q) = %q, want %q", c.raw, got, c.wantLine)
		}
	}

	// Buffer line widths agree with DisplayCols for tab-indented text.
	b := mk(t, "\tfunc main() {\n\t\tx := 1\n}")
	if got := b.LineCols(0); got != DisplayCols("\tfunc main() {") {
		t.Errorf("LineCols(0) = %d, want %d", got, DisplayCols("\tfunc main() {"))
	}
	if got := b.LineCols(1); got != DisplayCols("\t\tx := 1") {
		t.Errorf("LineCols(1) = %d, want %d", got, DisplayCols("\t\tx := 1"))
	}
}

func TestInsertAndSelection(t *testing.T) {
	b := mk(t, "hello world")
	b.SetCursor(6)
	b.ExtendCursor(11) // select "world"
	if !b.HasSelection() {
		t.Fatal("expected selection")
	}
	if got := b.SelectedText(); got != "world" {
		t.Fatalf("selected %q", got)
	}
	b.Insert("gpui")
	if got := b.Line(0); got != "hello gpui" {
		t.Fatalf("after replace: %q", got)
	}
	if b.HasSelection() {
		t.Fatal("selection should collapse after edit")
	}
}

func TestInsertMultilineSplits(t *testing.T) {
	b := mk(t, "ab")
	b.SetCursor(1)
	b.Insert("X\nY\n")
	if got := b.Text(); got != "aX\nY\nb" {
		t.Fatalf("text = %q", got)
	}
	if b.LineCount() != 3 {
		t.Fatalf("lines = %d (%v)", b.LineCount(), b.Lines)
	}
}

func TestDeleteBackwardForward(t *testing.T) {
	b := mk(t, "héllo")
	b.SetCursor(3) // after é (é is 2 bytes)
	b.DeleteBackward()
	if got := b.Text(); got != "hllo" {
		t.Fatalf("backspace rune: %q", got)
	}
	b.DeleteForward()
	if got := b.Text(); got != "hlo" {
		t.Fatalf("delete rune: %q", got)
	}
	// Selection delete.
	b.SetCursor(0)
	b.ExtendCursor(1)
	b.DeleteForward()
	if got := b.Text(); got != "lo" {
		t.Fatalf("selection delete: %q", got)
	}
}

func TestWordSelect(t *testing.T) {
	b := mk(t, "foo bar_baz, qux")
	b.SelectWordAt(5) // inside "bar_baz"
	if got := b.SelectedText(); got != "bar_baz" {
		t.Fatalf("word = %q", got)
	}
	b.SelectWordAt(14) // inside "qux"
	if got := b.SelectedText(); got != "qux" {
		t.Fatalf("last word = %q", got)
	}
}

func TestLineSelect(t *testing.T) {
	b := mk(t, "one\ntwo\nthree")
	b.SelectLineAt(5)
	if got := b.SelectedText(); got != "two" {
		t.Fatalf("line = %q", got)
	}
}

func TestMove(t *testing.T) {
	b := mk(t, "ab\ncdéf\ngh")
	b.SetCursor(b.LCToOffset(1, 2)) // end of "cdéf"
	b.Move("down", false, false, 20)
	if line, col := b.OffsetToLC(b.Cursor); line != 2 || col != 2 {
		t.Fatalf("down: line %d col %d", line, col)
	}
	b.Move("up", false, false, 20) // col 2 clamps into rune boundary of "cdéf"
	if line, _ := b.OffsetToLC(b.Cursor); line != 1 {
		t.Fatalf("up: line %d", line)
	}
	b.Move("home", false, false, 20)
	if _, col := b.OffsetToLC(b.Cursor); col != 0 {
		t.Fatalf("home: col %d", col)
	}
	b.Move("end", false, false, 20)
	if _, col := b.OffsetToLC(b.Cursor); col != 5 {
		t.Fatalf("end: col %d (want len(cdéf)=5 bytes)", col)
	}
}

func TestUndoRedo(t *testing.T) {
	b := mk(t, "abc")
	b.SetCursor(3)
	b.Insert("def")
	b.Insert("ghi")
	if got := b.Text(); got != "abcdefghi" {
		t.Fatalf("text = %q", got)
	}
	if !b.Undo() || b.Text() != "abcdef" {
		t.Fatalf("undo1 = %q", b.Text())
	}
	if !b.Undo() || b.Text() != "abc" {
		t.Fatalf("undo2 = %q", b.Text())
	}
	if b.Undo() {
		t.Fatal("undo on empty stack should fail")
	}
	if !b.Redo() || b.Text() != "abcdef" {
		t.Fatalf("redo1 = %q", b.Text())
	}
	if !b.Redo() || b.Text() != "abcdefghi" {
		t.Fatalf("redo2 = %q", b.Text())
	}
	// Undo a deletion.
	b.SetCursor(0)
	b.ExtendCursor(3)
	b.Insert("")
	if b.Text() != "defghi" {
		t.Fatalf("delete = %q", b.Text())
	}
	b.Undo()
	if b.Text() != "abcdefghi" {
		t.Fatalf("undo delete = %q", b.Text())
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	b := NewBuffer(path, "x.txt", "hello")
	b.SetCursor(5)
	b.Insert(" world")
	if !b.Dirty {
		t.Fatal("expected dirty")
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if b.Dirty {
		t.Fatal("expected clean after save")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Fatalf("saved %q", data)
	}
}

func TestOverlaySplitsRuns(t *testing.T) {
	line := "abc def"
	runs := []text.TextRun{{Len: len(line), Font: font.Font{Family: "Monospace"}, Color: gc.White()}}
	out := Overlay(runs, 0, line, 2, 6, nil, selPtr(), nil)
	// Collect highlighted spans.
	var got []int
	pos := 0
	for _, r := range out {
		if r.BackgroundColor != nil {
			got = append(got, pos, pos+r.Len)
		}
		pos += r.Len
	}
	if len(got) != 2 || got[0] != 2 || got[1] != 6 {
		t.Fatalf("highlighted spans = %v", got)
	}
	if total := totalLen(out); total != len(line) {
		t.Fatalf("run length drift: %d", total)
	}
}

func TestOverlayAcrossRuns(t *testing.T) {
	// "foo bar" tokenized into 3 runs by the Go highlighter; select "o b".
	runs := goRuns("foo bar", testPalette())
	out := Overlay(runs, 0, "foo bar", 1, 6, nil, selPtr(), nil)
	var selText strings.Builder
	pos := 0
	for _, r := range out {
		if r.BackgroundColor != nil {
			selText.WriteString("foo bar"[pos : pos+r.Len])
		}
		pos += r.Len
	}
	if selText.String() != "oo ba" {
		t.Fatalf("selected glyphs = %q", selText.String())
	}
}

// --- helpers ---

func selPtr() *gc.Hsla { h := gc.White(); return &h }

func totalLen(runs []text.TextRun) int {
	n := 0
	for _, r := range runs {
		n += r.Len
	}
	return n
}

func testPalette() Palette {
	return Palette{
		Keyword:  gc.White(),
		Type:     gc.White(),
		String:   gc.White(),
		Comment:  gc.White(),
		Number:   gc.White(),
		Function: gc.White(),
		Field:    gc.White(),
		Text:     gc.White(),
	}
}
