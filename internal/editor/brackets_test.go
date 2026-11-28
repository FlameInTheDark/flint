package editor

import (
	"strings"
	"testing"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/text"
)

// rainbowPal is a test palette with the six bracket colors set (depth d
// gets rainbowPal.Brackets[(d-1)%6]); the distinct hues double as
// assertions — a repaint is visible only as a run color change.
var rainbowPal = Palette{
	Keyword:  gc.Red(),
	Type:     gc.Blue(),
	String:   gc.Green(),
	Comment:  gc.White(),
	Number:   gc.Black(),
	Function: gc.HSLA(0.1, 1, 0.5, 1),
	Field:    gc.HSLA(0.2, 1, 0.5, 1),
	Text:     gc.HSLA(0.9, 0.1, 0.5, 1),
	Brackets: [6]gc.Hsla{
		gc.HSLA(0.55, 1, 0.6, 1), // depth 1: blue
		gc.HSLA(0.08, 1, 0.6, 1), // depth 2: orange
		gc.HSLA(0.75, 1, 0.6, 1), // depth 3: purple
		gc.HSLA(0.33, 1, 0.6, 1), // depth 4: green
		gc.HSLA(0.15, 1, 0.6, 1), // depth 5: yellow
		gc.HSLA(0.95, 1, 0.6, 1), // depth 6: pink
	},
	BracketMatch: gc.HSLA(0.6, 0.3, 0.3, 1),
}

// runsColorAt flattens runs to per-byte colors.
func runsColorAt(runs []text.TextRun, pos int) gc.Hsla {
	start := 0
	for _, r := range runs {
		if pos < start+r.Len {
			return r.Color
		}
		start += r.Len
	}
	return gc.Hsla{}
}

// hitsString renders a scan's hits compactly for failure messages.
func hitsString(hits []bracketHit) string {
	parts := make([]string, len(hits))
	for i, h := range hits {
		openc := "("
		if !h.open {
			openc = ")"
		}
		parts[i] = openc + strings.Repeat(".", h.depth)
	}
	return strings.Join(parts, " ")
}

func TestBracketScanSkipsLiterals(t *testing.T) {
	// Brackets inside strings, rune literals and comments never count;
	// only the four code brackets on the line do.
	line := `x := f(")(", ')') /* ( */ (a) // [`
	hits, end := bracketScanIn(line, "go", StateNormal, 0)
	// f's opener+closer, then the (a) pair — 4 hits, depth back to 0.
	if len(hits) != 4 {
		t.Fatalf("hits = %v (%s), want 4", hitsString(hits), hitsString(hits))
	}
	for _, h := range hits {
		if line[h.pos] != '(' && line[h.pos] != ')' {
			t.Fatalf("hit at %d is %q, not a bracket", h.pos, line[h.pos])
		}
	}
	if end != 0 {
		t.Fatalf("end depth = %d, want 0", end)
	}
}

func TestBracketScanDepths(t *testing.T) {
	// Nested same-line pairs: each opener deepens, each closer matches.
	hits, end := bracketScanIn("{ [ ( ) ] }", "go", StateNormal, 0)
	wantDepths := []int{1, 2, 3, 3, 2, 1}
	if len(hits) != len(wantDepths) {
		t.Fatalf("hits = %s, want %d", hitsString(hits), len(wantDepths))
	}
	for i, w := range wantDepths {
		if hits[i].depth != w {
			t.Fatalf("hit %d depth = %d, want %d", i, hits[i].depth, w)
		}
	}
	if end != 0 {
		t.Fatalf("end depth = %d, want 0", end)
	}
}

func TestBracketScanUnbalancedCloserIgnored(t *testing.T) {
	// A closer at depth 0 is stray text: no hit, no negative depth.
	hits, end := bracketScanIn(") ] } (", "go", StateNormal, 0)
	if len(hits) != 1 || !hits[0].open || hits[0].depth != 1 {
		t.Fatalf("hits = %s, want one opener at depth 1", hitsString(hits))
	}
	if end != 1 {
		t.Fatalf("end depth = %d, want 1", end)
	}
}

func TestBracketScanJSON(t *testing.T) {
	hits, end := bracketScanIn(`{"a": [1, "]}"]}`, "json", StateNormal, 0)
	if len(hits) != 4 {
		t.Fatalf("hits = %s, want 4 (string brackets excluded)", hitsString(hits))
	}
	if end != 0 {
		t.Fatalf("end depth = %d, want 0", end)
	}
}

func TestBracketScanRawStringCarriesOver(t *testing.T) {
	// Line 1 opens a raw string containing brackets; line 2 closes it.
	// Neither line's brackets count, and the depth carries through.
	_, end1 := bracketScanIn("s := `([{", "go", StateNormal, 2)
	if end1 != 2 {
		t.Fatalf("raw string line end = %d, want the start depth 2", end1)
	}
	hits, end2 := bracketScanIn("more )]}`", "go", StateRawString, 2)
	if len(hits) != 0 || end2 != 2 {
		t.Fatalf("inside raw string: hits = %s end = %d, want none at depth 2", hitsString(hits), end2)
	}
}

func TestBracketScanGenericPython(t *testing.T) {
	// Python: # comment and strings hide brackets; code pairs count.
	line := `d = {"k": (1,)}  # ) ] } not code`
	hits, end := bracketScanIn(line, "python", StateNormal, 0)
	if len(hits) != 4 || end != 0 {
		t.Fatalf("hits = %s end = %d, want 4 pairs back to 0", hitsString(hits), end)
	}
}

func TestLineBracketDepthsCarryAcrossLines(t *testing.T) {
	src := "func a() {\n\tif x {\n\t\ty()\n\t}\n}\n"
	buf := mk(t, src)
	depths := buf.LineBracketDepths("go")
	// Line 0 starts at 0 and ends inside the function (depth 1); line 1
	// starts at 1 and ends at 2; line 2 starts at 2; line 3 (the first
	// closer) starts at 2; line 4 starts at 1.
	want := []uint8{0, 1, 2, 2, 1, 0}
	if len(depths) != len(want) {
		t.Fatalf("depths = %v, want %v", depths, want)
	}
	for i := range want {
		if depths[i] != want[i] {
			t.Fatalf("depths[%d] = %d, want %d", i, depths[i], want[i])
		}
	}
	// Memoized: a second call must return the same slice without a
	// revision change.
	if buf.LineBracketDepths("go") == nil || &buf.LineBracketDepths("go")[0] != &depths[0] {
		t.Fatal("depths recomputed without an edit or language change")
	}
}

func TestLineBracketDepthsInvalidateOnEdit(t *testing.T) {
	buf := mk(t, "func a() {\n}\n")
	before := buf.LineBracketDepths("go")
	buf.Insert("x") // revision bump
	after := buf.LineBracketDepths("go")
	if &before[0] == &after[0] {
		t.Fatal("depth cache survived an edit")
	}
}

func TestPaintBracketsColorsByDepth(t *testing.T) {
	line := "{ [ ( ) ] }"
	hits, _ := bracketScanIn(line, "go", StateNormal, 0)
	runs := []text.TextRun{{Len: len(line), Font: mono(false), Color: rainbowPal.Text}}
	painted := paintBrackets(runs, hits, rainbowPal)
	// "{ [ ( ) ] }": byte 0 = depth 1, byte 2 = depth 2, byte 4 = depth 3,
	// byte 6 = depth 3, byte 8 = depth 2, byte 10 = depth 1.
	wantDepth := map[int]int{0: 1, 2: 2, 4: 3, 6: 3, 8: 2, 10: 1}
	for pos, d := range wantDepth {
		if got := runsColorAt(painted, pos); got != bracketColor(rainbowPal, d) {
			t.Fatalf("byte %d (depth %d) color = %v, want %v", pos, d, got, bracketColor(rainbowPal, d))
		}
	}
	if got := runsColorAt(painted, 1); got != rainbowPal.Text {
		t.Fatalf("byte 1 (space) changed color: %v", got)
	}
	// Depths 1, 2, 3 paint three DIFFERENT colors.
	c1, c2, c3 := runsColorAt(painted, 0), runsColorAt(painted, 2), runsColorAt(painted, 4)
	if c1 == c2 || c2 == c3 || c1 == c3 {
		t.Fatalf("depths 1..3 share colors: %v %v %v", c1, c2, c3)
	}
	// The total run length still covers the line exactly.
	total := 0
	for _, r := range painted {
		total += r.Len
	}
	if total != len(line) {
		t.Fatalf("painted runs cover %d bytes, want %d", total, len(line))
	}
}

func TestPaintBracketsCyclesPastSix(t *testing.T) {
	// Seven nested pairs: depth 7 wraps to the depth-1 color (mod 6).
	line := "{ [ ( { [ ( { ) } ) ] } ) ] }"
	hits, _ := bracketScanIn(line, "go", StateNormal, 0)
	if len(hits) != 14 {
		t.Fatalf("hits = %d, want 14", len(hits))
	}
	runs := []text.TextRun{{Len: len(line), Font: mono(false), Color: rainbowPal.Text}}
	painted := paintBrackets(runs, hits, rainbowPal)
	// The depth-7 opener (byte 12) must equal depth 1's color; the
	// depth-6 opener (byte 10) the depth-6 color.
	if got := runsColorAt(painted, 12); got != rainbowPal.Brackets[0] {
		t.Fatalf("depth 7 color = %v, want the depth-1 color %v", got, rainbowPal.Brackets[0])
	}
	if got := runsColorAt(painted, 10); got != rainbowPal.Brackets[5] {
		t.Fatalf("depth 6 color = %v, want %v", got, rainbowPal.Brackets[5])
	}
}

func TestPaintBracketsZeroPaletteNoop(t *testing.T) {
	// A palette without bracket colors must leave the runs untouched (the
	// older highlight tests' expectation).
	line := "{( )}"
	hits, _ := bracketScanIn(line, "go", StateNormal, 0)
	runs := []text.TextRun{{Len: len(line), Font: mono(false), Color: rainbowPal.Text}}
	plain := Palette{Text: rainbowPal.Text, String: rainbowPal.String}
	if got := paintBrackets(runs, hits, plain); len(got) != 1 || got[0].Len != len(line) {
		t.Fatalf("palette without brackets repainted runs: %v", got)
	}
}

func TestBracketMatchOfPairs(t *testing.T) {
	src := "func a() {\n\tf(g(x), (1))\n}\n"
	buf := mk(t, src)
	lang := buf.LanguageOf()
	states := buf.LineStates(lang)
	depths := buf.LineBracketDepths(lang)

	// buf.Cursor on the '{' (offset 10): match is the '}' on line 2.
	buf.SetCursor(10)
	off, _, ok := buf.bracketAtCursor()
	if !ok || src[off] != '{' {
		t.Fatalf("bracketAtCursor = %d %v, want the '{' at 10", off, ok)
	}
	pair, ok := buf.bracketMatchOf(off, lang, states, depths)
	if !ok || src[pair] != '}' {
		t.Fatalf("match of '{' = %d (%q), want the '}'", pair, src[pair])
	}
	// From the '}' side (cursor after it): same pair.
	closeOff := pair
	buf.SetCursor(closeOff + 1)
	back, _, ok := buf.bracketAtCursor()
	if !ok || back != closeOff {
		t.Fatalf("bracketAtCursor after closer = %d, want %d", back, closeOff)
	}
	opener, ok := buf.bracketMatchOf(closeOff, lang, states, depths)
	if !ok || opener != 9 {
		t.Fatalf("match of '}' = %d, want 9", opener)
	}
	// The nested g( pair on line 1.
	gOpen := strings.Index(src, "g(")
	pair, ok = buf.bracketMatchOf(gOpen+1, lang, states, depths)
	if !ok || src[pair] != ')' {
		t.Fatalf("match of g's opener = %q, want ')'", src[pair])
	}
}

func TestBracketMatchIgnoresLiteralBrackets(t *testing.T) {
	src := "s := \"(\"\n"
	buf := mk(t, src)
	lang := buf.LanguageOf()
	states := buf.LineStates(lang)
	depths := buf.LineBracketDepths(lang)
	paren := strings.Index(src, "(")
	if _, ok := buf.bracketMatchOf(paren, lang, states, depths); ok {
		t.Fatal("a bracket inside a string must not match")
	}
}

func TestBracketMatchUnbalancedFails(t *testing.T) {
	src := "func a() {\n\tif x {\n}\n"
	buf := mk(t, src) // two openers, one closer
	lang := buf.LanguageOf()
	states := buf.LineStates(lang)
	depths := buf.LineBracketDepths(lang)
	brace := strings.IndexByte(src, '{')
	if _, ok := buf.bracketMatchOf(brace, lang, states, depths); ok {
		t.Fatal("an unbalanced opener must not match")
	}
}

func TestBracketScanDisabledLanguages(t *testing.T) {
	for _, lang := range []string{"text", "markdown", "gosum", "css"} {
		hits, end := bracketScanIn("{ ( [", lang, StateNormal, 0)
		if len(hits) != 0 || end != 0 {
			t.Fatalf("lang %q produced hits: %s", lang, hitsString(hits))
		}
	}
}
