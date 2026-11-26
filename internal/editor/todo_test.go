package editor

import (
	"strings"
	"testing"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/text"
)

var todoPal = Palette{
	Comment: gc.White(),
	String:  gc.Green(),
	Text:    gc.HSLA(0.9, 0.1, 0.5, 1),
	Todo:    gc.HSLA(0.5, 1, 0.5, 1),
}

func runColors(line string, runs []text.TextRun) []struct {
	text  string
	color gc.Hsla
	bold  bool
} {
	out := make([]struct {
		text  string
		color gc.Hsla
		bold  bool
	}, 0)
	off := 0
	for _, r := range runs {
		seg := line[off:min(off+r.Len, len(line))]
		off += r.Len
		bold := r.Font.Weight == 700
		out = append(out, struct {
			text  string
			color gc.Hsla
			bold  bool
		}{seg, r.Color, bold})
	}
	return out
}

func TestTodoTagsInComments(t *testing.T) {
	line := "// TODO: fix this later"
	runs, _ := HighlightIn(line, "go", StateNormal, todoPal)
	parts := runColors(line, runs)
	sawTag := false
	for _, p := range parts {
		if p.text == "TODO" {
			sawTag = true
			if p.color != todoPal.Todo || !p.bold {
				t.Fatalf("TODO tag not repainted: %+v", p)
			}
		} else if p.color != todoPal.Comment {
			t.Fatalf("comment text lost its color: %+v", p)
		}
	}
	if !sawTag {
		t.Fatalf("TODO not split into its own run: %+v", parts)
	}
}

func TestTodoTagsCaseInsensitive(t *testing.T) {
	for _, line := range []string{"// todo: lowercase", "// FIXME: shouty"} {
		runs, _ := HighlightIn(line, "go", StateNormal, todoPal)
		parts := runColors(line, runs)
		found := false
		for _, p := range parts {
			if p.color == todoPal.Todo {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q: no tag run", line)
		}
	}
}

func TestTodoWordBoundary(t *testing.T) {
	line := "// todos and fixes are not tags, but FIXMEX too"
	runs, _ := HighlightIn(line, "go", StateNormal, todoPal)
	for _, p := range runColors(line, runs) {
		if p.color == todoPal.Todo {
			t.Fatalf("word-boundary leak: %+v in %q", p, line)
		}
	}
}

func TestTodoNotInStrings(t *testing.T) {
	line := `x := "TODO in a string" // real TODO here`
	runs, _ := HighlightIn(line, "go", StateNormal, todoPal)
	off := 0
	for _, r := range runs {
		seg := line[off : off+r.Len]
		off += r.Len
		if strings.Contains(seg, "TODO") && strings.Contains(seg, `"`) {
			if r.Color == todoPal.Todo {
				t.Fatalf("TODO inside a string literal repainted: %q", seg)
			}
		}
	}
}

func TestTodoPaletteWithoutTodoColorSkips(t *testing.T) {
	pal := todoPal
	pal.Todo = gc.Hsla{} // zero alpha: no tag pass
	line := "// TODO unchanged"
	runs, _ := HighlightIn(line, "go", StateNormal, pal)
	if len(runs) != 1 || runs[0].Len != len(line) {
		t.Fatalf("no-Todo palette altered the runs: %+v", runs)
	}
}

func TestTodoInBlockCommentAndGomod(t *testing.T) {
	for _, lang := range []string{"go", "gomod", "c", "javascript"} {
		line := "// TODO polish"
		runs, _ := HighlightIn(line, lang, StateNormal, todoPal)
		found := false
		for _, p := range runColors(line, runs) {
			if p.color == todoPal.Todo {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: TODO not highlighted", lang)
		}
	}
}
