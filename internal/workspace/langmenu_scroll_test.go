package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestLanguageMenuScrollsAndSelects pins the status-bar language selector's
// full-catalog scrolling (the "no scroll, only shows 6" report): the panel
// is sized for six rows but the whole alphabetized catalog is reachable —
// the wheel scrolls freely (the view no longer snaps back to the selected
// row), the arrow keys move the selection and scroll it into view, page
// keys jump, filtering resets the cursor, and enter applies the highlight.
func TestLanguageMenuScrollsAndSelects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.py")
	if err := os.WriteFile(path, []byte("def hello():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newSceneWorkspace(t)
	ws.OpenFile(path)

	hp, handle, cx := openFileScene(t, ws)
	hw := hp.LastWindow()

	update := func(f func(*workspace.Workspace)) {
		handle.View.Update(cx, func(state *workspace.Workspace, c *app.Context[workspace.Workspace]) any {
			f(state)
			c.Notify()
			return nil
		})
	}
	update(func(s *workspace.Workspace) { s.OpenLangMenu() })
	handle.Window.Focus(handle.View.Read(cx).FocusLangMenu)
	handle.Window.Draw()

	total := len(editor.Languages)
	if total <= 6 {
		t.Fatalf("catalog has %d languages, want more than the 6 visible rows", total)
	}

	// The wheel scrolls the six-row window freely — the regression: the
	// render used to clamp the view back to the selected row, so the list
	// looked stuck on its first page forever.
	scrollPos := geometry.PointPixels(820, 520)
	for i := 0; i < 8; i++ {
		hw.DispatchInput(platform.ScrollEvent{
			Position: scrollPos,
			Delta:    platform.ScrollDeltaLines(0, -3),
		})
		handle.Window.Draw()
	}
	m := handle.View.Read(cx).LangMenu
	maxScroll := total - 6
	if m == nil {
		t.Fatal("language menu closed unexpectedly")
	}
	if m.Scroll != maxScroll {
		t.Fatalf("wheel did not scroll the full catalog: scroll=%d want %d", m.Scroll, maxScroll)
	}
	// And back up to the top.
	for i := 0; i < 8; i++ {
		hw.DispatchInput(platform.ScrollEvent{
			Position: scrollPos,
			Delta:    platform.ScrollDeltaLines(0, 3),
		})
	}
	handle.Window.Draw()
	if m = handle.View.Read(cx).LangMenu; m == nil || m.Scroll != 0 {
		t.Fatalf("wheel up did not return to the top: menu=%v", m)
	}

	// Arrow keys move the selection and bring it into view. The regression:
	// every keydown reset Selected/Scroll via the filter's update hook, so
	// the selection never moved at all.
	for i := 0; i < 10; i++ {
		hw.DispatchInput(keyDown("down", keymap.Modifiers{}))
		handle.Window.Draw()
	}
	m = handle.View.Read(cx).LangMenu
	if m == nil {
		t.Fatal("language menu closed unexpectedly")
	}
	if m.Selected != 10 {
		t.Fatalf("down keys did not move the selection: selected=%d want 10", m.Selected)
	}
	if m.Scroll < 10-6+1 || m.Scroll > 10 {
		t.Fatalf("selection out of view: selected=%d scroll=%d", m.Selected, m.Scroll)
	}
	hw.DispatchInput(keyDown("up", keymap.Modifiers{}))
	handle.Window.Draw()
	if m = handle.View.Read(cx).LangMenu; m == nil || m.Selected != 9 {
		t.Fatalf("up key did not move the selection: menu=%v", m)
	}

	// Page keys jump by a page and stay in view.
	hw.DispatchInput(keyDown("pageup", keymap.Modifiers{}))
	handle.Window.Draw()
	m = handle.View.Read(cx).LangMenu
	if m == nil {
		t.Fatal("language menu closed unexpectedly")
	}
	if m.Selected != 3 {
		t.Fatalf("pageup did not jump a page: selected=%d want 3", m.Selected)
	}
	if m.Selected < m.Scroll || m.Selected >= m.Scroll+6 {
		t.Fatalf("pageup left the selection out of view: selected=%d scroll=%d", m.Selected, m.Scroll)
	}

	// Filtering resets the cursor to the first match; enter applies it.
	// ("javas" matches only JavaScript; the shorter "jav" also matches
	// Java, which sorts first.)
	for _, ch := range "javas" {
		hw.DispatchInput(keyDown(string(ch), keymap.Modifiers{}))
		handle.Window.Draw()
	}
	m = handle.View.Read(cx).LangMenu
	if m == nil || m.Selected != 0 || m.Filter.String() != "javas" {
		t.Fatalf("filter did not reset the cursor: %+v", m)
	}
	hw.DispatchInput(keyDown("enter", keymap.Modifiers{}))
	handle.Window.Draw()

	b := handle.View.Read(cx).Buf()
	if got := b.LanguageOf(); got != "javascript" {
		t.Fatalf("selected language = %q want javascript", got)
	}
	if handle.View.Read(cx).LangMenu != nil {
		t.Fatal("language menu stayed open after selection")
	}
}
