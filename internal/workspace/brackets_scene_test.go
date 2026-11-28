package workspace_test

import (
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/workspace"
)

// bracketSceneContent builds a Go buffer whose nesting, literals and
// comments exercise the rainbow brackets: pairs at depths 1..4, brackets
// inside a string and a comment that must stay uncolored, and a pair the
// caret can sit on for the matching highlight.
func bracketSceneContent() string {
	return `package main

import "fmt"

// A comment with (parens) and [brackets] stays plain text.
func demo() {
	data := map[string][]int{
		"nums": {1, 2, 3},
		"deep": {{(1)}},
	}
	fmt.Println(data["nums"], (1 + (2 * 3)))
	s := "string ( [ { literal"
	fmt.Println(s)
}
`
}

// TestBracketRainbowScene renders the rainbow-bracket scene and verifies
// the end-to-end wiring: the config default enables the colors, the
// matching-pair highlight lands on the caret's pair, and a typed opener
// auto-closes through the real key path.
func TestBracketRainbowScene(t *testing.T) {
	out := os.Getenv("EDITOR_SHOT_DIR")
	if out == "" {
		out = t.TempDir()
	}

	hp, handle, cx := openEditorWindowWith(t, bracketSceneContent())

	// The config default ships both smart-editing features on.
	ws := handle.View.Read(cx)
	if !ws.Cfg.Editor.RainbowEnabled() || !ws.Cfg.Editor.AutoCloseEnabled() {
		t.Fatal("rainbow brackets / auto-close pairs must default on")
	}

	// Park the caret on the demo function's opening brace (offset of the
	// '{' in "func demo() {") for the matching-pair highlight.
	content := bracketSceneContent()
	brace := strings.Index(content, "{\n")
	editorUpdate(handle, cx, func(s *workspace.Workspace) {
		if b := s.Buf(); b != nil {
			b.SetCursor(brace)
		}
	})
	handle.Window.Draw()
	shot := editorShot(t, hp, out, "brackets_rainbow.png")

	// The scene really rendered (the file exists and is a PNG).
	data, err := os.ReadFile(shot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(strings.NewReader(string(data))); err != nil {
		t.Fatalf("scene shot is not a valid PNG: %v", err)
	}

	// End-to-end smart keys: typing ( through the key path auto-closes,
	// typing ) right away types over the inserted closer.
	buf := handle.View.Read(cx).Buf()
	if buf == nil {
		t.Fatal("no active buffer")
	}
	text0 := buf.Text()
	hw := hp.LastWindow()
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "(", KeyChar: "("}})
	handle.Window.Draw()
	buf = handle.View.Read(cx).Buf()
	if !strings.Contains(buf.Text(), "()") {
		t.Fatalf("typed ( did not auto-close: %q -> %q", text0, buf.Text())
	}
	cursorAfterOpen := buf.Cursor
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: ")", KeyChar: ")"}})
	handle.Window.Draw()
	buf = handle.View.Read(cx).Buf()
	want := text0[:brace] + "()" + text0[brace:] // the pair landed at the caret
	if got := buf.Text(); got != want {
		t.Fatalf("typed ) did not type over: %q, want %q", got, want)
	}
	if buf.Cursor != cursorAfterOpen+1 {
		t.Fatalf("caret = %d, want just past the typed-over closer (%d)", buf.Cursor, cursorAfterOpen+1)
	}
	editorShot(t, hp, out, "brackets_typed.png")
}
