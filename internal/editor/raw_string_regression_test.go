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
