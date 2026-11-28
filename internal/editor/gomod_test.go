package editor

import (
	"strings"
	"testing"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/font"
)

var modPal = Palette{
	Keyword:  gc.Red(),
	Type:     gc.Blue(),
	String:   gc.Green(),
	Comment:  gc.White(),
	Number:   gc.Black(),
	Function: gc.HSLA(0.1, 1, 0.5, 1),
	Field:    gc.HSLA(0.2, 1, 0.5, 1),
	Text:     gc.HSLA(0.9, 0.1, 0.5, 1),
	Todo:     gc.HSLA(0.5, 1, 0.5, 1),
}

// runText extracts a run's text from the line (runs tile the whole line).

func TestLanguageDetectionModuleFiles(t *testing.T) {
	if got := Language("go.mod"); got != "gomod" {
		t.Fatalf("go.mod -> %q, want gomod", got)
	}
	if got := Language("go.sum"); got != "gosum" {
		t.Fatalf("go.sum -> %q, want gosum", got)
	}
	if got := Language("GO.MOD"); got != "gomod" {
		t.Fatalf("GO.MOD -> %q, want gomod", got)
	}
}

func TestGomodRuns(t *testing.T) {
	line := `require github.com/foo/bar v1.2.3 // indirect`
	runs, _ := gomodRunsIn(line, StateNormal, modPal)
	covered(t, line, runs)
	text := line
	off := 0
	var directiveColor, pathColor, versionColor, commentColor gc.Hsla
	for _, r := range runs {
		seg := text[off : off+r.Len]
		off += r.Len
		switch strings.TrimSpace(seg) {
		case "require":
			directiveColor = r.Color
		case "github.com/foo/bar":
			pathColor = r.Color
		case "v1.2.3":
			versionColor = r.Color
		case "// indirect":
			commentColor = r.Color
		}
	}
	if directiveColor != modPal.Keyword {
		t.Fatalf("require not keyword-colored: %+v", directiveColor)
	}
	// Bare module paths render in the plain text color in the go.mod
	// editor; quoted paths (rare) take the string color.
	if pathColor != modPal.Text {
		t.Fatalf("module path not text-colored: %+v", pathColor)
	}
	if versionColor != modPal.Number {
		t.Fatalf("version not number-colored: %+v", versionColor)
	}
	if commentColor != modPal.Comment {
		t.Fatalf("comment not comment-colored: %+v", commentColor)
	}
}

func TestGomodQuotedPath(t *testing.T) {
	line := `require "github.com/foo/bar" v1.2.3`
	runs, _ := gomodRunsIn(line, StateNormal, modPal)
	covered(t, line, runs)
	off := 0
	sawQuoted := false
	for _, r := range runs {
		seg := line[off : off+r.Len]
		off += r.Len
		if seg == `"github.com/foo/bar"` && r.Color == modPal.String {
			sawQuoted = true
		}
	}
	if !sawQuoted {
		t.Fatalf("quoted module path not string-colored: %+v", runs)
	}
}

func TestGomodDirectiveBold(t *testing.T) {
	runs, _ := gomodRunsIn("module example.com/app", StateNormal, modPal)
	off := 0
	for _, r := range runs {
		seg := "module example.com/app"[off : off+r.Len]
		off += r.Len
		if seg == "module" && r.Font.Weight != font.WeightBold {
			t.Fatalf("directive not bold: %+v", r)
		}
	}
}

func TestGomodBlockCommentState(t *testing.T) {
	_, st := gomodRunsIn("/* module docs", StateNormal, modPal)
	if st != StateBlockComment {
		t.Fatalf("unterminated block comment state = %v", st)
	}
	runs, st2 := gomodRunsIn("still inside */ module m", StateBlockComment, modPal)
	if st2 != StateNormal {
		t.Fatalf("comment close state = %v", st2)
	}
	covered(t, "still inside */ module m", runs)
	if next := nextLineState("/* open", "gomod", StateNormal); next != StateBlockComment {
		t.Fatalf("nextLineState block comment = %v", next)
	}
	if next := nextLineState("module m", "gomod", StateNormal); next != StateNormal {
		t.Fatalf("nextLineState plain = %v", next)
	}
}

func TestGomodReplaceArrow(t *testing.T) {
	line := `replace old.com/v1 => new.com/v2 v1.0.0`
	runs, _ := gomodRunsIn(line, StateNormal, modPal)
	covered(t, line, runs)
	off := 0
	sawArrow := false
	for _, r := range runs {
		seg := line[off : off+r.Len]
		off += r.Len
		if strings.TrimSpace(seg) == "=>" && r.Color == modPal.Keyword {
			sawArrow = true
		}
	}
	if !sawArrow {
		t.Fatalf("=> not keyword-colored: %+v", runs)
	}
}

func TestGosumRuns(t *testing.T) {
	line := "github.com/foo/bar v1.2.3 h1:AbCdEfGh=="
	runs := gosumRuns(line, modPal)
	covered(t, line, runs)
	off := 0
	var pathColor, versionColor, hashColor gc.Hsla
	for _, r := range runs {
		seg := line[off : off+r.Len]
		off += r.Len
		switch strings.TrimSpace(seg) {
		case "github.com/foo/bar":
			pathColor = r.Color
		case "v1.2.3":
			versionColor = r.Color
		case "h1:AbCdEfGh==":
			hashColor = r.Color
		}
	}
	if pathColor != modPal.Text {
		t.Fatalf("module path color %+v", pathColor)
	}
	if versionColor != modPal.Number {
		t.Fatalf("version color %+v", versionColor)
	}
	if hashColor != modPal.Comment {
		t.Fatalf("hash color %+v", hashColor)
	}
}

func TestGosumAnnotationComment(t *testing.T) {
	line := "## explicit; go 1.22"
	runs := gosumRuns(line, modPal)
	if len(runs) == 0 || runs[0].Color != modPal.Comment {
		t.Fatalf("## line not comment: %+v", runs)
	}
	covered(t, line, runs)
}

func TestGomodLanguagesListed(t *testing.T) {
	found := map[string]bool{}
	for _, l := range Languages {
		found[l.Id] = true
	}
	if !found["gomod"] || !found["gosum"] {
		t.Fatalf("gomod/gosum missing from the language catalog: %v", found)
	}
}
