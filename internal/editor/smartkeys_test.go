package editor

import (
	"strings"
	"testing"

	gc "github.com/FlameInTheDark/go-gpui/color"
)

// smartPal is a palette with recognizable string/comment colors so the
// auto-pairing's "not inside a literal" gate can be exercised.
var smartPal = Palette{
	String:  gc.Green(),
	Comment: gc.White(),
	Text:    gc.HSLA(0.9, 0.1, 0.5, 1),
}

func TestAutoCloseBrackets(t *testing.T) {
	for _, c := range []string{"(", "[", "{"} {
		b := mk(t, "")
		b.SmartInsert(c, smartPal, true)
		want := c + map[string]string{"(": ")", "[": "]", "{": "}"}[c]
		if got := b.Text(); got != want {
			t.Fatalf("typing %q produced %q, want %q", c, got, want)
		}
		if b.Cursor != 1 {
			t.Fatalf("caret = %d, want between the halves (1)", b.Cursor)
		}
	}
}

func TestTypeOverCloser(t *testing.T) {
	b := mk(t, "f(x)")
	b.SetCursor(3) // before the ')' (the next character)
	b.SmartInsert(")", smartPal, true)
	if b.Text() != "f(x)" {
		t.Fatalf("text = %q, want unchanged (typed over)", b.Text())
	}
	if b.Cursor != 4 {
		t.Fatalf("caret = %d, want stepped past the closer (4)", b.Cursor)
	}
}

func TestTypeOverQuote(t *testing.T) {
	b := mk(t, `s := "abc"`)
	end := len(`s := "abc"`) // after the closing quote
	b.SetCursor(end - 1)     // between the last c and the quote
	b.SmartInsert(`"`, smartPal, true)
	if b.Text() != `s := "abc"` {
		t.Fatalf("text = %q, want unchanged", b.Text())
	}
	if b.Cursor != end {
		t.Fatalf("caret = %d, want %d (over the quote)", b.Cursor, end)
	}
}

func TestQuotePairsAfterSpace(t *testing.T) {
	b := mk(t, "x = ")
	b.SetCursor(4)
	b.SmartInsert(`"`, smartPal, true)
	if b.Text() != `x = ""` {
		t.Fatalf("text = %q, want x = \"\"", b.Text())
	}
	if b.Cursor != 5 {
		t.Fatalf("caret = %d, want 5", b.Cursor)
	}
}

func TestQuoteMidWordStaysPlain(t *testing.T) {
	// abc|def: a quote typed inside a word must not pair.
	b := mk(t, "abcdef")
	b.SetCursor(3)
	b.SmartInsert(`"`, smartPal, true)
	if b.Text() != `abc"def` {
		t.Fatalf("text = %q, want a single quote", b.Text())
	}
}

func TestApostropheAfterWordStaysPlain(t *testing.T) {
	// don| : an apostrophe after a word char must stay plain (don't).
	b := mk(t, "don")
	b.SetCursor(3)
	b.SmartInsert("'", smartPal, true)
	if b.Text() != "don'" {
		t.Fatalf("text = %q, want a single apostrophe", b.Text())
	}
}

func TestQuoteAfterBackslashStaysPlain(t *testing.T) {
	b := mk(t, `\`)
	b.SetCursor(1)
	b.SmartInsert(`"`, smartPal, true)
	if b.Text() != `\"` {
		t.Fatalf("text = %q, want an escaped quote, no pair", b.Text())
	}
}

func TestBracketInsideStringStaysPlain(t *testing.T) {
	// Typing ( inside a string literal is content, not code.
	b := mk(t, `s := "abc"`)
	b.SetCursor(9) // before the closing quote, inside the string
	b.SmartInsert("(", smartPal, true)
	if b.Text() != `s := "abc("` {
		t.Fatalf("text = %q, want the bare paren inside the string", b.Text())
	}
}

func TestBracketInsideCommentStaysPlain(t *testing.T) {
	b := mk(t, "// hello")
	b.SetCursor(8)
	b.SmartInsert("[", smartPal, true)
	if b.Text() != "// hello[" {
		t.Fatalf("text = %q, want the bare bracket inside the comment", b.Text())
	}
}

func TestSelectionWrapsInBrackets(t *testing.T) {
	b := mk(t, "call me")
	b.SetCursor(7)
	b.Anchor = 0 // select "call me"
	b.SmartInsert("(", smartPal, true)
	if b.Text() != "(call me)" {
		t.Fatalf("text = %q, want the selection wrapped", b.Text())
	}
	s, e := b.Selection()
	if s != 1 || e != 8 {
		t.Fatalf("selection = %d..%d, want the wrapped text reselected (1..8)", s, e)
	}
	if !b.CanUndo() || (func() bool { b.Undo(); return b.Text() != "call me" }()) {
		t.Fatal("wrap is not one invertible op")
	}
}

func TestSelectionWrapsInQuotes(t *testing.T) {
	b := mk(t, "word")
	b.SelectAll()
	b.SmartInsert(`"`, smartPal, true)
	if b.Text() != `"word"` {
		t.Fatalf("text = %q, want the selection quoted", b.Text())
	}
	s, e := b.Selection()
	if s != 1 || e != 5 {
		t.Fatalf("selection = %d..%d, want 1..5", s, e)
	}
}

func TestCloserWithSelectionReplacesIt(t *testing.T) {
	// A closer over a selection replaces it (wrap is for openers).
	b := mk(t, "ab")
	b.SetCursor(2)
	b.Anchor = 0
	b.SmartInsert(")", smartPal, true)
	if b.Text() != ")" {
		t.Fatalf("text = %q, want the selection replaced", b.Text())
	}
}

func TestBackspaceDeletesEmptyPair(t *testing.T) {
	for _, pair := range []string{"()", "[]", "{}", `""`, "''", "``"} {
		b := mk(t, pair)
		b.SetCursor(1)
		b.DeleteBackward()
		if b.Text() != "" {
			t.Fatalf("backspace between %q left %q, want both halves gone", pair, b.Text())
		}
		if b.CanUndo() {
			b.Undo()
			if b.Text() != pair {
				t.Fatalf("undo after pair-delete = %q, want %q", b.Text(), pair)
			}
		}
	}
}

func TestBackspacePlainStillWorks(t *testing.T) {
	b := mk(t, "a)b")
	b.SetCursor(2) // after ')'
	b.DeleteBackward()
	if b.Text() != "ab" {
		t.Fatalf("text = %q, want a normal single-rune delete", b.Text())
	}
}

func TestEnterBetweenBracesExpands(t *testing.T) {
	b := mk(t, "func f() {}")
	b.SetCursor(10) // between { }
	if !b.EnterBetweenPair() {
		t.Fatal("EnterBetweenPair must fire between {}")
	}
	want := "func f() {\n    \n}"
	if b.Text() != want {
		t.Fatalf("text = %q, want %q", b.Text(), want)
	}
	line, _ := b.OffsetToLC(b.Cursor)
	if line != 1 {
		t.Fatalf("caret on line %d, want the middle line 1", line)
	}
	b.Undo()
	if b.Text() != "func f() {}" {
		t.Fatalf("undo = %q, want the original line", b.Text())
	}
}

func TestEnterBetweenParensAndBracketsExpand(t *testing.T) {
	for _, tc := range []struct{ in, closer string }{{"x ()", ")"}, {"x []", "]"}} {
		b := mk(t, tc.in)
		b.SetCursor(len(tc.in) - 1) // between the pair
		if !b.EnterBetweenPair() {
			t.Fatalf("EnterBetweenPair must fire between %s", tc.in)
		}
		want := "x (\n    \n" + tc.closer
		if tc.in[2] == '[' {
			want = "x [\n    \n" + tc.closer
		}
		if b.Text() != want {
			t.Fatalf("text = %q, want %q", b.Text(), want)
		}
	}
}

func TestEnterOutsidePairStaysNormal(t *testing.T) {
	b := mk(t, "ab")
	b.SetCursor(1)
	if b.EnterBetweenPair() {
		t.Fatal("EnterBetweenPair must not fire on plain text")
	}
	b.Insert("\n" + b.NewlineIndent())
	if b.Text() != "a\nb" {
		t.Fatalf("text = %q, want the plain newline", b.Text())
	}
}

func TestEnterBetweenNonEmptyPairStaysNormal(t *testing.T) {
	// {|x} — the pair is not empty: plain newline.
	b := mk(t, "{x}")
	b.SetCursor(1)
	if b.EnterBetweenPair() {
		t.Fatal("EnterBetweenPair must not fire inside a non-empty pair")
	}
}

func TestSmartInsertDisabledIsPlain(t *testing.T) {
	for _, c := range []string{"(", "[", "{", `"`, "'"} {
		b := mk(t, "")
		b.SmartInsert(c, smartPal, false)
		if b.Text() != c {
			t.Fatalf("disabled auto-close: %q produced %q, want the bare char", c, b.Text())
		}
		if b.Cursor != 1 {
			t.Fatalf("disabled auto-close: caret = %d, want 1", b.Cursor)
		}
	}
}

func TestAutoCloseUndoIsOneOp(t *testing.T) {
	b := mk(t, "")
	b.SmartInsert("(", smartPal, true)
	b.SmartInsert("x", smartPal, true)
	// Ctrl+Z twice: first removes x, second removes the whole pair.
	b.Undo()
	if b.Text() != "()" {
		t.Fatalf("after first undo = %q, want ()", b.Text())
	}
	b.Undo()
	if b.Text() != "" {
		t.Fatalf("after second undo = %q, want empty", b.Text())
	}
}

func TestAutoCloseThenTypeOverFlow(t *testing.T) {
	// The full flow: ( auto-closes, x lands inside, the typed ) types
	// over the auto-inserted one — exactly one closer ends up in the text.
	b := mk(t, "")
	b.SmartInsert("(", smartPal, true)
	b.SmartInsert("x", smartPal, true)
	b.SmartInsert(")", smartPal, true)
	if b.Text() != "(x)" {
		t.Fatalf("text = %q, want (x)", b.Text())
	}
	if b.Cursor != 3 {
		t.Fatalf("caret = %d, want past the closer (3)", b.Cursor)
	}
}

func TestInStringOrCommentAt(t *testing.T) {
	src := "s := \"ab\" // (c\n"
	b := mk(t, src)
	if !b.inStringOrCommentAt(7, smartPal) { // inside "ab"
		t.Fatal("offset 7 is inside the string, gate must report true")
	}
	if b.inStringOrCommentAt(4, smartPal) {
		t.Fatal("offset 4 is plain code")
	}
	if !b.inStringOrCommentAt(13, smartPal) { // inside the // comment
		t.Fatal("offset 13 is inside the comment")
	}
	// The end-of-line caret belongs to the line's last run (the comment).
	if !b.inStringOrCommentAt(len(src)-1, smartPal) {
		t.Fatal("the end-of-line caret inside a comment line must report true")
	}
}

func TestSmartInsertMultiBytePlain(t *testing.T) {
	b := mk(t, "")
	b.SmartInsert("é", smartPal, true) // IME compositions insert plainly
	if b.Text() != "é" {
		t.Fatalf("text = %q, want the composed rune alone", b.Text())
	}
}

func TestLineBracketDepthsGoDepthCycleThroughPaint(t *testing.T) {
	// End-to-end: the cached depths + the line scans agree, so painting
	// line 1 alone (start depth from the cache) colors its closer with
	// the opener's depth.
	src := "func a() {\n\tif x {\n\t}\n}\n"
	buf := mk(t, src)
	depths := buf.LineBracketDepths("go")
	hits, _ := bracketScanIn(buf.Line(3), "go", StateNormal, int(depths[3]))
	if len(hits) != 1 || hits[0].open {
		t.Fatalf("line 3 hits = %v, want the closing }", hits)
	}
	if hits[0].depth != 1 {
		t.Fatalf("closing } depth = %d, want 1 (the opener's depth)", hits[0].depth)
	}
	if !strings.Contains(buf.Text(), "if x {") {
		t.Fatal("buffer content drifted")
	}
}
