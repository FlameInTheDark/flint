package editor

import (
	"strings"
	"testing"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/text"
)

var statePal = Palette{
	Keyword:  gc.Red(),
	Type:     gc.Blue(),
	String:   gc.Green(),
	Comment:  gc.White(),
	Number:   gc.Black(),
	Function: gc.HSLA(0.1, 1, 0.5, 1),
	Field:    gc.HSLA(0.2, 1, 0.5, 1),
	Text:     gc.HSLA(0.9, 0.1, 0.5, 1),
}

// covered asserts the runs tile the whole line without gaps or overruns.
func covered(t *testing.T, line string, runs []text.TextRun) {
	t.Helper()
	n := 0
	for _, r := range runs {
		n += r.Len
	}
	if n != len(line) {
		t.Fatalf("runs cover %d of %d bytes: %s -> %+v", n, len(line), line, runs)
	}
}

func TestGoRawStringSpansLines(t *testing.T) {
	lines := []string{
		"const demo = `package main",
		"func Fib(n int) int {",
		"for i := 0; i < n; i++ {",
		"print(fib(i))",
		"}`",
		"done := true",
	}
	st := StateNormal
	for i, line := range lines {
		runs, end := goRunsIn(line, st, statePal)
		covered(t, line, runs)
		switch i {
		case 0:
			last := runs[len(runs)-1]
			if last.Color != statePal.String {
				t.Fatalf("opening line must color the raw string start: %+v", runs)
			}
			if end != StateRawString {
				t.Fatalf("line 0 must open a raw string, got %d", end)
			}
		case 1, 2, 3:
			if len(runs) != 1 || runs[0].Color != statePal.String || runs[0].Len != len(line) {
				t.Fatalf("line %d must be one string-colored run, got %+v", i, runs)
			}
			if end != StateRawString {
				t.Fatalf("line %d must stay in the raw string, got %d", i, end)
			}
		case 4:
			// The `}` is raw-string content and the backtick closes the
			// literal: the whole line is one string run.
			if len(runs) != 1 || runs[0].Color != statePal.String || runs[0].Len != 2 || end != StateNormal {
				t.Fatalf("closing line must be one string run ending the literal: %+v (%d)", runs, end)
			}
		case 5:
			if runs[0].Color != statePal.Text || end != StateNormal {
				t.Fatalf("code after the block must highlight normally: %+v (%d)", runs, end)
			}
		}
		st = end
	}
	if st != StateNormal {
		t.Fatalf("context leaked past the block: %d", st)
	}
}

func TestGoBlockCommentSpansLines(t *testing.T) {
	lines := []string{
		"a := 1 /* start of",
		"a long comment",
		"end */ b := 2",
	}
	st := StateNormal
	for i, line := range lines {
		runs, end := goRunsIn(line, st, statePal)
		covered(t, line, runs)
		switch i {
		case 0:
			if runs[len(runs)-1].Color != statePal.Comment || end != StateBlockComment {
				t.Fatalf("line 0 must open a block comment: %+v (%d)", runs, end)
			}
		case 1:
			if len(runs) != 1 || runs[0].Color != statePal.Comment {
				t.Fatalf("line 1 must be one comment run, got %+v", runs)
			}
			if end != StateBlockComment {
				t.Fatalf("line 1 must stay in the comment, got %d", end)
			}
		case 2:
			if runs[0].Color != statePal.Comment || runs[0].Len != 6 {
				t.Fatalf("line 2 must start with the comment tail: %+v", runs)
			}
			if runs[2].Color != statePal.Text || end != StateNormal {
				t.Fatalf("code after */ must highlight normally: %+v (%d)", runs, end)
			}
		}
		st = end
	}
}

func TestGoContextsDoNotLeak(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"backtick inside interpreted string", `s := "a` + "`" + `b"`},
		{"backtick inside line comment", "// see `docs` for more"},
		{"closed single-line raw string", "`tick`"},
		{"closed single-line block comment", "/* closed */ x := 1"},
		{"escaped quote", `s := "a\"b + ` + "`" + `"`},
	}
	for _, c := range cases {
		runs, end := goRunsIn(c.line, StateNormal, statePal)
		covered(t, c.line, runs)
		if end != StateNormal {
			t.Fatalf("%s: leaked context %d from %q", c.name, end, c.line)
		}
	}
	// Line comments color the whole line.
	runs, end := goRunsIn("// `tick` and /* block", StateNormal, statePal)
	if len(runs) != 1 || runs[0].Color != statePal.Comment || end != StateNormal {
		t.Fatalf("line comment must swallow the line: %+v (%d)", runs, end)
	}
	// An unterminated interpreted string ends at the newline (Go strings
	// cannot span lines).
	_, end = goRunsIn(`s := "open`, StateNormal, statePal)
	if end != StateNormal {
		t.Fatalf("interpreted string leaked across lines: %d", end)
	}
}

func TestGoWordAtLineEnd(t *testing.T) {
	// A line ending inside a word used to fall into a per-char fallback
	// that skipped keyword classification.
	runs, _ := goRunsIn("return", StateNormal, statePal)
	if len(runs) != 1 || runs[0].Len != 6 || runs[0].Color != statePal.Keyword {
		t.Fatalf("bare keyword mis-tokenized: %+v", runs)
	}
	runs, _ = goRunsIn("DemoMain", StateNormal, statePal)
	if len(runs) != 1 || runs[0].Color != statePal.Type {
		t.Fatalf("bare capitalized identifier mis-tokenized: %+v", runs)
	}
}

func TestGoScanStateMatchesTokenizer(t *testing.T) {
	cases := []struct {
		line string
		st   LineState
	}{
		{"x := `abc", StateNormal},
		{"plain text", StateRawString},
		{"abc` def /* c", StateRawString},
		{"x /* c", StateNormal},
		{"c */ y", StateBlockComment},
		{"// `b` /*", StateNormal},
		{`s := "a\` + "`" + `b"`, StateNormal},
		{"", StateRawString},
		{"", StateBlockComment},
		{"`", StateNormal},
		{"``", StateNormal},
		{"/*", StateNormal},
		{"/*/", StateNormal},
		{"*/", StateBlockComment},
		{"`/*`", StateNormal},
		{"/* ` */", StateNormal},
	}
	for _, c := range cases {
		_, fromRuns := goRunsIn(c.line, c.st, statePal)
		fromScan := goScanState(c.line, c.st)
		if fromRuns != fromScan {
			t.Errorf("go state mismatch on %q (st=%d): tokenizer=%d scanner=%d", c.line, c.st, fromRuns, fromScan)
		}
	}
}

func TestGenericBlockCommentSpansLines(t *testing.T) {
	def := genericLang["c"]
	lines := []string{
		"int x = 1; /* start",
		"still inside */ y = 2; // tail",
	}
	st := StateNormal
	for i, line := range lines {
		runs, end := genericRunsIn(line, def, st, statePal)
		covered(t, line, runs)
		if i == 0 && (end != StateBlockComment || runs[len(runs)-1].Color != statePal.Comment) {
			t.Fatalf("c line 0 must open a block comment: %+v (%d)", runs, end)
		}
		if i == 1 && (end != StateNormal || runs[0].Color != statePal.Comment) {
			t.Fatalf("c line 1 must close the comment and resume code: %+v (%d)", runs, end)
		}
		st = end
	}
}

func TestGenericTemplateAndTripleQuotes(t *testing.T) {
	// JS template literal spanning lines.
	def := genericLang["javascript"]
	_, end := genericRunsIn("const s = `row ${i}", def, StateNormal, statePal)
	if end != StateRawString {
		t.Fatalf("js template literal must span lines, got %d", end)
	}
	runs, end := genericRunsIn("  and ${j} more`;", def, StateRawString, statePal)
	if len(runs) == 0 || runs[0].Color != statePal.String || end != StateNormal {
		t.Fatalf("js template continuation wrong: %+v (%d)", runs, end)
	}
	// Python triple quotes (both quote styles) span lines.
	py := genericLang["python"]
	_, end = genericRunsIn(`s = """first`, py, StateNormal, statePal)
	if end != StateTripleDouble {
		t.Fatalf("python triple double must span lines, got %d", end)
	}
	_, end = genericRunsIn(`still """inside`, py, StateTripleDouble, statePal)
	if end != StateNormal {
		t.Fatalf("python triple double must close, got %d", end)
	}
	_, end = genericRunsIn(`t = '''one`, py, StateNormal, statePal)
	if end != StateTripleSingle {
		t.Fatalf("python triple single must span lines, got %d", end)
	}
	_, end = genericRunsIn(`two'''`, py, StateTripleSingle, statePal)
	if end != StateNormal {
		t.Fatalf("python triple single must close, got %d", end)
	}
	// Python single-quoted strings must NOT span lines.
	_, end = genericRunsIn(`u = 'open`, py, StateNormal, statePal)
	if end != StateNormal {
		t.Fatalf("python single quote leaked across lines: %d", end)
	}
	// Shell command substitution spans lines.
	sh := genericLang["shell"]
	_, end = genericRunsIn("x=$(date `date +%Y", sh, StateNormal, statePal)
	if end != StateRawString {
		t.Fatalf("shell backtick must span lines, got %d", end)
	}
	_, end = genericRunsIn("`)", sh, StateRawString, statePal)
	if end != StateNormal {
		t.Fatalf("shell backtick must close, got %d", end)
	}
}

func TestGenericScanStateMatchesTokenizer(t *testing.T) {
	defs := []string{"c", "cpp", "java", "javascript", "typescript", "python", "shell", "php", "rust"}
	lines := []string{"x = `abc", "plain", "x /* c", "c */ y", "// `b` /*", "", "`", "/*", "*/", `s = """a`, `b"""`, `t = '''a`, `b'''`, `u = 'open`, `v = "open`}
	for _, lang := range defs {
		def := genericLang[lang]
		for _, line := range lines {
			for _, st := range []LineState{StateNormal, StateRawString, StateBlockComment, StateTripleDouble, StateTripleSingle} {
				_, fromRuns := genericRunsIn(line, def, st, statePal)
				fromScan := genericScanState(line, def, st)
				if fromRuns != fromScan {
					t.Errorf("%s state mismatch on %q (st=%d): tokenizer=%d scanner=%d", lang, line, st, fromRuns, fromScan)
				}
			}
		}
	}
}

func TestBufferLineStates(t *testing.T) {
	content := "a := 1\ns := `raw\nmiddle\nend`\nb := 2\n/* blk\ncmm */ d := 3\n"
	buf := NewBuffer("", "demo.go", content)
	states := buf.LineStates("go")
	want := []LineState{
		StateNormal,       // a := 1
		StateNormal,       // s := `raw  (opens)
		StateRawString,    // middle
		StateRawString,    // end`  (closes)
		StateNormal,       // b := 2
		StateNormal,       // /* blk  (opens)
		StateBlockComment, // cmm */ d := 3
		StateNormal,       // trailing empty line
	}
	if len(states) != len(want) {
		t.Fatalf("states len = %d, want %d", len(states), len(want))
	}
	for i := range want {
		if states[i] != want[i] {
			t.Errorf("states[%d] = %d, want %d", i, states[i], want[i])
		}
	}
	// Memoized per revision+lang: the same slice comes back.
	again := buf.LineStates("go")
	if &again[0] != &states[0] {
		t.Fatal("LineStates recomputed without an edit or language change")
	}
	// Closing the raw string on line 1 must re-flow the states below it.
	buf.SetCursor(buf.LineStart(1) + len("s := `raw"))
	buf.Insert("`")
	fresh := buf.LineStates("go")
	if fresh[1] != StateNormal || fresh[2] != StateNormal || fresh[3] != StateNormal {
		t.Fatalf("states did not re-flow after edit: %+v", fresh)
	}
	if fresh[4] != StateRawString {
		t.Fatalf("the backtick on the `end` line must open a new raw string: %+v", fresh)
	}
	// A language switch re-computes with the new language's rules.
	buf.SetLanguageOverride("text")
	if st := buf.LineStates("text"); st[1] != StateNormal {
		t.Fatalf("text language must not carry raw-string state: %d", st[1])
	}
}

func TestHighlightCachedInSeparatesContexts(t *testing.T) {
	line := "middle"
	normal, _ := HighlightCachedIn(line, "go", StateNormal, statePal)
	raw, _ := HighlightCachedIn(line, "go", StateRawString, statePal)
	if len(raw) != 1 || raw[0].Color != statePal.String {
		t.Fatalf("raw-string context must color the line as a string: %+v", raw)
	}
	if len(normal) == 1 && normal[0].Color == statePal.String {
		t.Fatalf("normal context must not inherit the string color: %+v", normal)
	}
}

func TestMinimapUsesLineStates(t *testing.T) {
	buf := NewBuffer("", "demo.go", "s := `abc\nmid\nend`\n")
	key := minimapKey{language: "go", scale: 1, palette: statePal, dim: gc.Red()}
	c := &buf.minimap
	c.prepare(key, 4)
	blocks := c.blocks(buf, 1)
	if len(blocks) == 0 {
		t.Fatal("minimap row inside the raw string produced no blocks")
	}
	want := statePal.String.Alpha(0.95)
	if blocks[0].color != want {
		t.Fatalf("minimap row inside a raw string is not string-colored: %v, want %v", blocks[0].color, want)
	}
}

// TestDemoContentRendersAsOneString pins the reported bug: the whole
// backtick block of the demo seeds must tokenize as string runs, not as
// Go code.
func TestDemoContentRendersAsOneString(t *testing.T) {
	content := "const demoMain = `package main\n\nimport \"fmt\"\n\nfunc Fib(n int) int {\n\tif n < 2 {\n\t\treturn n\n\t}\n\treturn fib(n-1) + fib(n-2)\n}\n`\n"
	buf := NewBuffer("", "main.go", content)
	states := buf.LineStates("go")
	for i, line := range buf.Lines {
		runs, _ := HighlightCachedIn(line, "go", states[i], statePal)
		covered(t, line, runs)
		switch i {
		case 0:
			// The code before the opening backtick keeps its colors.
			last := runs[len(runs)-1]
			if last.Color != statePal.String {
				t.Fatalf("line 0 must color the raw string start as a string: %q -> %+v", line, runs)
			}
		case 1, 2, 3, 4, 5, 6, 7, 8, 9:
			// Inside the raw string every byte is string-colored.
			for _, r := range runs {
				if r.Color != statePal.String {
					t.Fatalf("line %d inside the raw string has a non-string run: %q -> %+v", i, line, runs)
				}
			}
		case 10:
			if len(runs) != 1 || runs[0].Color != statePal.String || runs[0].Len != 1 {
				t.Fatalf("the closing backtick must be a string run: %+v", runs)
			}
		}
	}
	if states[10] != StateRawString || states[11] != StateNormal {
		t.Fatalf("states around the closing backtick wrong: %+v", states)
	}
	if !strings.Contains(content, "`\n") {
		t.Fatal("sanity")
	}
}
