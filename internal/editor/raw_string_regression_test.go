package editor

import (
	"strings"
	"testing"
)

func TestGoRawStringTrailingBackslash(t *testing.T) {
	for _, lang := range []string{"go", "gomod"} {
		t.Run(lang, func(t *testing.T) {
			// A backslash does not escape the closing backtick in Go raw strings.
			lines := []string{
				"value := `/\\` // comment with ` and /*",
				"next := 1",
				"value := `open",
				"middle\\` // closed",
				"after := 2",
			}
			buf := NewBuffer("", "example."+lang, strings.Join(lines, "\n"))
			states := buf.LineStates(lang)
			want := []LineState{StateNormal, StateNormal, StateNormal, StateRawString, StateNormal}
			for i, line := range lines {
				if states[i] != want[i] {
					t.Fatalf("line %d start state = %d, want %d", i, states[i], want[i])
				}
				runs, end := HighlightCachedIn(line, lang, states[i], statePal)
				covered(t, line, runs)
				if i+1 < len(want) && end != want[i+1] {
					t.Fatalf("line %d end state = %d, want %d", i, end, want[i+1])
				}
				if i == 0 || i == 3 {
					comment := strings.Index(line, "//")
					pos := 0
					found := false
					for _, run := range runs {
						if pos <= comment && comment < pos+run.Len {
							found = true
							if run.Color != statePal.Comment {
								t.Fatalf("line %d comment not highlighted: %+v", i, runs)
							}
						}
						pos += run.Len
					}
					if !found {
						t.Fatalf("line %d comment not covered: %+v", i, runs)
					}
				}
				if i == 1 || i == 4 {
					for _, run := range runs {
						if run.Color == statePal.String || run.Color == statePal.Comment {
							t.Fatalf("line %d code incorrectly highlighted: %+v", i, runs)
						}
					}
				}
			}
		})
	}
}

func TestGoRawStringBackslashesAreLiteral(t *testing.T) {
	for _, lang := range []string{"go", "gomod"} {
		for _, n := range []int{0, 1, 2, 3} {
			literal := "`/* // " + strings.Repeat("\\", n) + "`"
			line := literal + " // actual comment"
			runs, end := HighlightIn(line, lang, StateNormal, statePal)
			covered(t, line, runs)
			if end != StateNormal || nextLineState(line, lang, StateNormal) != StateNormal {
				t.Fatalf("%s with %d backslashes: raw string did not close", lang, n)
			}
			if len(runs) < 2 || runs[0].Len != len(literal) || runs[0].Color != statePal.String || runs[len(runs)-1].Color != statePal.Comment {
				t.Fatalf("%s with %d backslashes: incorrect runs %+v", lang, n, runs)
			}
		}
	}
}

// TestGoRuneLiteralWithBacktick pins the one-char rune literal regression:
// in `if c == '"' || c == '\” || c == '`' {` the backtick is ordinary
// content of a rune literal. goRunsIn had no rune-literal case, so the
// backtick opened a phantom raw string that swallowed the line tail and
// leaked StateRawString — every following line painted as a string.
func TestGoRuneLiteralWithBacktick(t *testing.T) {
	line := "\t\tif c == '\"' || c == '\\'' || c == '`' {"
	runs, end := HighlightIn(line, "go", StateNormal, statePal)
	covered(t, line, runs)
	if end != StateNormal {
		t.Fatalf("rune literal with backtick leaked state %d (want StateNormal)", end)
	}
	if nextLineState(line, "go", StateNormal) != StateNormal {
		t.Fatal("goScanState leaked a raw-string state after a '`' rune literal")
	}
	// The rune literal '`' is one string run; the trailing " {" stays code.
	bt := strings.Index(line, "`")
	pos := 0
	litString, tailCode := false, false
	for _, run := range runs {
		if pos <= bt && bt < pos+run.Len {
			litString = run.Color == statePal.String && run.Len == 3 // '`' incl. quotes
		}
		if pos+run.Len == len(line) {
			tailCode = run.Color != statePal.String && run.Color != statePal.Comment
		}
		pos += run.Len
	}
	if !litString {
		t.Fatalf("the '`' rune literal is not one string run: %+v", runs)
	}
	if !tailCode {
		t.Fatalf("code after the rune literal painted as string: %+v", runs)
	}

	// The whole-file view: the line above followed by plain code — the
	// following lines must not inherit a string state.
	lines := []string{
		line,
		"\t\tdepth++",
		"\t}",
	}
	buf := NewBuffer("", "example.go", strings.Join(lines, "\n"))
	states := buf.LineStates("go")
	for i, st := range states {
		if st != StateNormal {
			t.Fatalf("line %d start state = %d, want StateNormal", i, st)
		}
	}

	// Other rune-literal shapes tokenize as one string run and end the
	// line in the normal state.
	for _, lit := range []string{"'a'", "'\\''", "'\\n'"} {
		code := "r := " + lit + " // note"
		runs, end := HighlightIn(code, "go", StateNormal, statePal)
		covered(t, code, runs)
		if end != StateNormal {
			t.Fatalf("rune literal %s leaked state %d", lit, end)
		}
		if nextLineState(code, "go", StateNormal) != StateNormal {
			t.Fatalf("rune literal %s leaked state in goScanState", lit)
		}
	}
}
