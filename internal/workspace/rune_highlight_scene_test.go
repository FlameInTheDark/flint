package workspace_test

import (
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// runelineContent reproduces the reported breakage: the scanQuoted
// dispatch line of the rainbow-bracket scanner carries a backtick inside
// a rune literal, and the lines after it used to inherit a phantom raw
// string (everything painted as a string).
func runelineContent() string {
	const bt = "`"
	var b strings.Builder
	b.WriteString("package editor\n\n")
	b.WriteString("// scanQuoted scans a quoted literal from s[0].\n")
	b.WriteString("func scanQuoted(s string, quote byte, escapes bool) (int, bool) {\n")
	b.WriteString("\tfor i := 1; i < len(s); i++ {\n")
	b.WriteString("\t\tif escapes && s[i] == '\\\\' && i+1 < len(s) {\n")
	b.WriteString("\t\t\ti++\n")
	b.WriteString("\t\t\tcontinue\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tif s[i] == quote {\n")
	b.WriteString("\t\t\treturn i + 1, true\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn len(s), false\n")
	b.WriteString("}\n\n")
	b.WriteString("// The dispatcher compares all three quote forms; the backtick\n")
	b.WriteString("// sits in a rune literal, not a raw string.\n")
	b.WriteString("func dispatch(c byte) bool {\n")
	b.WriteString("\treturn c == '\"' || c == '\\'' || c == '" + bt + "'\n")
	b.WriteString("}\n\n")
	b.WriteString("func after() {\n")
	b.WriteString("\tdepth := 1\n")
	b.WriteString("\tdepth++\n")
	b.WriteString("\thits = append(hits, bracketHit{pos: 3, open: true, depth: depth})\n")
	b.WriteString("}\n")
	return b.String()
}

// TestRuneLiteralHighlightScene pins the backtick-in-rune-literal scene:
// the dispatch line highlights its three quote literals, and the lines
// after it keep code colors (no phantom raw-string state).
func TestRuneLiteralHighlightScene(t *testing.T) {
	hp, handle, cx := openEditorWindowWith(t, runelineContent())
	handle.Window.Draw()
	saveShot(t, hp, "/tmp/render", "rune_literal_fixed.png")

	content := runelineContent()
	ws := handle.View.Read(cx)
	pal := editor.PaletteOfTheme(ws.Theme())

	// The dispatch line: every quote literal is a string run, the rest
	// stays code, and the line ends in the normal state.
	dispatch := strings.Index(content, "return c ==")
	line := content[dispatch : strings.Index(content[dispatch:], "\n")+dispatch]
	runs, end := editor.HighlightIn(line, "go", editor.StateNormal, pal)
	if end != editor.StateNormal {
		t.Fatal("dispatch line leaked a cross-line state")
	}
	strRuns := 0
	for _, r := range runs {
		if r.Color == pal.String {
			strRuns++
		}
	}
	if strRuns != 3 {
		t.Fatalf("dispatch line string runs = %d, want 3 (quote, rune, backtick rune)", strRuns)
	}

	// Every line starts in the normal state through the buffer-level
	// state walk (the whole-file view of the reported bug).
	states := ws.Buf().LineStates("go")
	for i, st := range states {
		if st != editor.StateNormal {
			t.Fatalf("line %d start state = %d, want StateNormal", i, st)
		}
	}
}
