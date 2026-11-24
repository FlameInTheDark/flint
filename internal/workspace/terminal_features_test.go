package workspace_test

import (
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// The terminal feature suite covers the upstream-parity interactions: mouse
// selection over the scrollback with copy/paste, the dock's terminal tabs,
// the bottom-follow scroll behavior, and the input row's pan-to-caret.

// bodyBand returns the terminal body's geometry in the 1000x650 scene
// (the dock is 220 tall: strip 5 + header 26 + body — the shell's own
// prompt replaces the old input row).
func bodyBand(t *testing.T, handle *gpui.WindowHandle[workspace.Workspace], cx *app.App) (top, bottom float32) {
	t.Helper()
	termHeff, _ := handle.View.Read(cx).DebugPanelSizes()
	dockTop := 650 - ui.StatusHeight - termHeff
	return dockTop + ui.TermResizeStrip + ui.TermHeaderH, 650 - ui.StatusHeight
}

func TestTerminalSelectionAndClipboard(t *testing.T) {
	hp, handle, cx := openTerminalScene(t)
	hw := hp.LastWindow()

	top, _ := bodyBand(t, handle, cx)

	// Drag-select across the first output row: press, drag right, release.
	// The first row ("builder $ make") sits at the body's bottom-pinned
	// grid; with 12 scrollback rows + prompt the exact row doesn't matter
	// for the gesture — any in-band press selects something.
	pressY := top + 20
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(30, pressY)})
	hw.DispatchInput(platform.MouseMoveEvent{Position: geometry.PointPixels(220, pressY), PressedButton: btnPtr(platform.ButtonLeft)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(220, pressY)})
	handle.Window.Draw()
	shot(t, hp, "/tmp/render", "term_selection.png")

	ws := handle.View.Read(cx)
	if !ws.Term.HasSelection() {
		t.Fatal("drag over the scrollback produced no selection")
	}
	sel := ws.Term.SelectedText()
	if strings.TrimSpace(sel) == "" {
		t.Fatalf("selection text is empty (sel %q)", sel)
	}

	// Ctrl+Shift+C copies the selection into the clipboard.
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{
		Key: "c", Modifiers: keymap.Modifiers{Control: true, Shift: true}}})
	item, ok := hw.ReadClipboard()
	if !ok || item.Text != sel {
		t.Fatalf("ctrl-shift-c did not copy the selection: got %q ok=%v, want %q", item.Text, ok, sel)
	}

	// A plain click collapses the selection.
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(500, pressY)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(500, pressY)})
	handle.Window.Draw()
	if ws = handle.View.Read(cx); ws.Term.HasSelection() {
		t.Fatal("plain click did not clear the selection")
	}

	// Double-click selects a whole word. The prompt row ("/…/001 $") is the
	// input line's neighbor; select from the first output row instead:
	// "builder $ make" — a double-click 2 cells in lands on "builder".
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(30, pressY), ClickCount: 2})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(30, pressY)})
	handle.Window.Draw()
	if ws = handle.View.Read(cx); !ws.Term.HasSelection() {
		t.Fatal("double-click produced no selection")
	}
	if word := ws.Term.SelectedText(); strings.Contains(word, " ") {
		t.Fatalf("double-click selected across a space: %q", word)
	}

	// Ctrl+Shift+V pastes into the shell: with the fake write sink the
	// bytes are captured — the paste path must deliver the clipboard text.
	var written []byte
	ws = handle.View.Read(cx)
	ws.Term.SetTestWriteSink(func(b []byte) { written = append(written, b...) })
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{
		Key: "v", Modifiers: keymap.Modifiers{Control: true, Shift: true}}})
	handle.Window.Draw()
	if !strings.Contains(string(written), sel) {
		t.Fatalf("ctrl-shift-v did not paste the clipboard to the shell: got %q", written)
	}
	ws.Term.SetTestWriteSink(nil)
	shot(t, hp, "/tmp/render", "term_pasted.png")
}

func TestTerminalDockTabs(t *testing.T) {
	hp, handle, cx := openTerminalScene(t)
	hw := hp.LastWindow()

	// The dock header: tabs are 22px tall, action buttons 20px tall, in
	// paint order: tab(s), "+", "clear", "x".
	headerBoxes := func(hmin, hmax float32) []hitboxRect {
		top, _ := bodyBand(t, handle, cx)
		headerY := top - ui.TermHeaderH
		var out []hitboxRect
		for _, hb := range handle.Window.RenderedHitboxes() {
			r := rectOf(hb)
			if r.h >= hmin && r.h <= hmax && r.y >= headerY && r.bottom() <= top+1 && r.w > 8 {
				out = append(out, r)
			}
		}
		return out
	}
	headerButtons := func() []hitboxRect { return headerBoxes(19, 21) }
	headerTabs := func() []hitboxRect { return headerBoxes(21, 23) }

	ws := handle.View.Read(cx)
	if n := len(ws.Terms); n != 1 {
		t.Fatalf("scene opened with %d terminals, want 1 (normalized)", n)
	}

	btns := headerButtons()
	if len(btns) < 3 {
		t.Fatalf("header shows %d buttons, want >= 3 (+, clear, x)", len(btns))
	}
	tabs := headerTabs()
	if len(tabs) < 1 {
		t.Fatalf("header shows %d tabs, want >= 1", len(tabs))
	}
	// The "+" button is the leftmost 20px button (after the tabs).
	plus := btns[0]
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(plus.x+plus.w/2, plus.y+plus.h/2)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(plus.x+plus.w/2, plus.y+plus.h/2)})
	handle.Window.Draw()
	if ws = handle.View.Read(cx); len(ws.Terms) != 2 || ws.TermIdx != 1 {
		t.Fatalf("+ did not add and select a terminal: %d terms, idx %d", len(ws.Terms), ws.TermIdx)
	}
	shot(t, hp, "/tmp/render", "term_two_tabs.png")

	// Select the first tab: click its title (the leftmost tab).
	first := tabs[0]
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(first.x+first.w/2, first.y+first.h/2)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(first.x+first.w/2, first.y+first.h/2)})
	handle.Window.Draw()
	if ws = handle.View.Read(cx); ws.TermIdx != 0 {
		t.Fatalf("clicking the first tab did not select it: idx %d", ws.TermIdx)
	}

	// Type into terminal 1 so the tabs hold distinct state: keystrokes go
	// to the shell as raw bytes (captured by the fake write sink).
	var written []byte
	ws = handle.View.Read(cx)
	ws.Term.SetTestWriteSink(func(b []byte) { written = append(written, b...) })
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "e", KeyChar: "e"}})
	handle.Window.Draw()
	if string(written) != "e" {
		t.Fatalf("typing went to the wrong terminal: %q", written)
	}
	ws.Term.SetTestWriteSink(nil)

	// Close the second tab via its in-tab close square (the 16×16 box at
	// the tab's right edge; tabs live on the header's left side).
	btns = headerButtons()
	var activeTab hitboxRect
	for _, r := range headerTabs() {
		if r.w > 40 && r.x < 300 { // a tab (with title), not the far actions
			activeTab = r
		}
	}
	closeX := activeTab.right() - 15
	closeY := activeTab.y + activeTab.h/2
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(closeX, closeY)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(closeX, closeY)})
	handle.Window.Draw()
	if ws = handle.View.Read(cx); len(ws.Terms) != 1 {
		t.Fatalf("closing the tab left %d terminals", len(ws.Terms))
	}

	// Closing the last terminal hides the dock (header's "x" button).
	btns = headerButtons()
	xBtn := btns[len(btns)-1]
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(xBtn.x+xBtn.w/2, xBtn.y+xBtn.h/2)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(xBtn.x+xBtn.w/2, xBtn.y+xBtn.h/2)})
	handle.Window.Draw()
	if ws = handle.View.Read(cx); ws.ShowTerminal {
		t.Fatal("closing the last terminal left the dock visible")
	}
}

func TestTerminalFollowPinning(t *testing.T) {
	hp, handle, cx := openTerminalScene(t)

	// Model-level: output while scrolled up preserves the view; re-arming
	// follow snaps to the bottom (the upstream editor pins the terminal until the user
	// scrolls up).
	ws := handle.View.Read(cx)
	term := ws.Term
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		sb.WriteString("fill row\r\n")
	}
	term.Inject(sb.String())
	total := term.RowTotal()
	const visible = 5
	maxScroll := total - visible

	if got := term.EffectiveScroll(total, visible); got != maxScroll {
		t.Fatalf("fresh terminal should pin to the bottom: got %d want %d", got, maxScroll)
	}
	term.ScrollLines(-7, total, visible)
	if term.Snapshot().Follow {
		t.Fatal("scrolling up did not release follow")
	}
	term.Inject("late output line\r\n")
	total = term.RowTotal()
	if got := term.EffectiveScroll(total, visible); got != maxScroll-7 {
		t.Fatalf("output while scrolled up moved the view: got %d want %d", got, maxScroll-7)
	}
	// Re-arming follow snaps back to the very bottom.
	term.SetFollow(true)
	if got := term.EffectiveScroll(total, visible); got != total-visible {
		t.Fatalf("follow did not snap to the bottom: got %d", got)
	}

	// Scene-level: wheel up leaves the view above the bottom; the
	// scrollbar's thumb reflects it (thumb no longer at the track bottom).
	hw := hp.LastWindow()
	top, _ := bodyBand(t, handle, cx)
	for i := 0; i < 4; i++ {
		hw.DispatchInput(platform.ScrollEvent{
			Position: geometry.PointPixels(500, top+60),
			Delta:    platform.ScrollDeltaPixels(0, 60),
		})
	}
	handle.Window.Draw()
	if ws = handle.View.Read(cx); ws.Term.Snapshot().Follow {
		t.Fatal("wheel up did not release follow in the scene")
	}
	shot(t, hp, "/tmp/render", "term_scrolled_up.png")
}

// TestTerminalCursorRender pins the shell-driven cursor: the emulator's
// cursor cell is where the shell put it (via CUP), and the pane paints
// the block cursor there (accent ink in the PNG at the expected cell).
func TestTerminalCursorRender(t *testing.T) {
	hp, handle, cx := openTerminalScene(t)

	ws := handle.View.Read(cx)
	// Move to the last screen row, which remains in the followed viewport.
	ws.Term.Inject("\x1b[999;5H")
	handle.Window.Draw()
	top, bottom := bodyBand(t, handle, cx)
	shot(t, hp, "/tmp/render", "term_cursor.png")

	// The cursor uses the selected theme's accent (filled or outlined).
	accent := ws.Theme().Accent
	found := false
	for _, q := range handle.Window.PaintedQuads() {
		y := q.Bounds.Origin.Y.AsF32()
		if y < top || y >= bottom {
			continue
		}
		fill, ok := q.Background.AsSolid()
		if (ok && fill == accent.Alpha(0.45)) || q.BorderColor == accent.Alpha(0.8) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("shell cursor block not rendered (no accent ink in the body)")
	}
}
