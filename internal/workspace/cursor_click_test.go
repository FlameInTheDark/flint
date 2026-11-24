package workspace_test

import (
	"testing"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// TestCursorClickNearestBoundary clicks at fractional positions inside a
// character cell and expects the caret to land on the NEARER boundary:
// the left half of a cell puts the caret before the cell's character, the
// right half after it.
func TestCursorClickNearestBoundary(t *testing.T) {
	hp, handle, cx := openEditorWindow(t)
	hw := hp.LastWindow()
	ws := handle.View.Read(cx)
	buf := ws.Buf()
	if buf == nil {
		t.Fatal("no buffer")
	}
	advance := ws.CharAdvance
	if advance <= 0 {
		t.Fatal("no char advance measured")
	}

	// The editor pane hitbox gives the pane origin (the pane sits at the
	// workspace's left edge; the code-column hitbox starts at the gutter).
	var pane hitboxRect
	for _, hb := range handle.Window.RenderedHitboxes() {
		r := rectOf(hb)
		if r.w > 700 && r.h > 400 && r.y > 30 && r.x < 5 {
			pane = r
		}
	}
	if pane.w == 0 {
		t.Fatal("editor pane hitbox not found")
	}
	gutterW := editor.GutterWidth(buf.LineCount(), advance)
	line0Y := pane.y + 8 // first visible line row

	line0 := buf.Line(0)
	t.Logf("line0=%q advance=%.2f pane=(%.0f,%.0f) gutter=%.1f", line0, advance, pane.x, pane.y, gutterW)

	// The code column hitbox: its origin is the TRUE text origin (the
	// handlers map clicks against it — never against the gutter estimate).
	var codeCol hitboxRect
	for _, hb := range handle.Window.RenderedHitboxes() {
		r := rectOf(hb)
		if r.w > 400 && r.h > 300 && r.y >= pane.y && r.x > pane.x+gutterW-6 && r.x < pane.x+gutterW+6 {
			codeCol = r
		}
	}
	if codeCol.w == 0 {
		t.Fatal("editor code column hitbox not found")
	}

	// Clicks anchor to the code column's actual origin.
	click := func(cell float32) int {
		x := codeCol.x + cell*advance
		hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x, line0Y)})
		hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x, line0Y)})
		handle.Window.Draw()
		b := handle.View.Read(cx).Buf()
		_, col := b.OffsetToLC(b.Cursor)
		return col
	}

	// Line 0 is "package main" — cell k sits inside the k-th character.
	if len(line0) < 8 {
		t.Fatalf("unexpected line0 %q", line0)
	}
	cases := []struct {
		cell    float32 // cell index + fraction within the cell
		wantCol int
		note    string
	}{
		{0.25, 0, "left half of cell 0 -> caret before 'p'"},
		{0.75, 1, "right half of cell 0 -> caret after 'p'"},
		{1.25, 1, "left half of cell 1 -> caret before 'a'"},
		{1.75, 2, "right half of cell 1 -> caret after 'a'"},
		{4.5, 5, "middle boundary -> nearest is after 'a' (col 5)"}, /* boundary at 5 */
		{1.0, 1, "exact boundary between 'p' and 'a'"},
		{2.0, 2, "exact boundary between 'a' and 'c'"},
		{3.0, 3, "exact boundary inside 'kage'"},
		{11.0, 11, "exact boundary before the final 'n'"},
	}
	for _, tc := range cases {
		got := click(tc.cell)
		if got != tc.wantCol {
			t.Errorf("click at cell %.2f put the caret at col %d, want %d (%s)", tc.cell, got, tc.wantCol, tc.note)
		}
	}
}
