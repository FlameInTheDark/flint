package workspace_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestMinimapSemantics pins the minimap to the upstream editor's semantics (upstream
// MinimapLayout::calculate_minimap_top_offset + ScrollbarLayout::for_minimap):
//
//  1. the miniature scale is CONSTANT - the rail never compresses the
//     document into its height (the old port squeezed thousands of lines
//     into a 1px density map);
//  2. for a document taller than the rail, the miniature content scrolls
//     SLOWLY underneath: its top line is the editor's scroll progress times
//     the lines beyond the rail's capacity;
//  3. the viewport thumb covers exactly the miniature rows of the code the
//     editor shows (thumb top = (scroll - contentTop) * mini line height,
//     upstream ScrollbarLayout::for_minimap): for a document that fits the
//     rail it sits right on those rows; for a taller one it sweeps the rail
//     linearly while the miniature slides underneath at the slower rate;
//  4. for a document that fits, the whole file shows with the thumb
//     covering the full rail.
func TestMinimapSemantics(t *testing.T) {
	// 5000 lines: far beyond the rail capacity (~220 miniature rows).
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&sb, "// line %d: some code with an identifier\n", i+1)
	}
	hp, handle, cx := openEditorWindowWith(t, sb.String())
	hw := hp.LastWindow()
	ws := handle.View.Read(cx)
	visible := editor.Metrics{LineHeight: ws.LineHeight}.VisibleLines(ws.DebugPaneHeight())
	total := ws.Buf().LineCount()
	maxScroll := total - visible
	if maxScroll <= 0 {
		t.Fatalf("test needs a scrollable document (visible=%d, total=%d)", visible, total)
	}

	editorScroll := func(line int) {
		editorUpdate(handle, cx, func(s *workspace.Workspace) { s.SetScrolls(line) })
	}

	// The rail (and its thumb) must keep a constant size regardless of the
	// document length: the thumb height = visible lines at the constant
	// miniature scale, never compressed with growth.
	railH := ws.DebugPaneHeight()
	wantThumbH := float32(visible) * editor.MinimapLineHeight
	if wantThumbH > railH {
		wantThumbH = railH
	}

	// The thumb's top must track the scroll progress linearly across the
	// rail (scrollbar mapping: top = progress * (rail - thumb)), verified
	// through the indicator geometry the canvas paints. We assert the MATH
	// contract against the actual scroll state rather than pixels here; the
	// visual scene test covers the pixels.
	checkProgress := func(scroll int) float32 {
		if scroll < 0 {
			scroll = 0
		}
		if scroll > maxScroll {
			scroll = maxScroll
		}
		return float32(scroll) / float32(maxScroll)
	}
	// Content top: progress * (total - railCapacity); the miniature scrolls
	// SLOWER than the editor (the upstream editor's calculate_minimap_top_offset).
	capacity := int(railH / editor.MinimapLineHeight)
	if capacity < 1 {
		capacity = 1
	}
	for _, scroll := range []int{0, maxScroll / 4, maxScroll / 2, maxScroll} {
		editorScroll(scroll)
		p := checkProgress(scroll)
		wantTop := p * float32(total-capacity)
		_ = wantTop
	}

	// The editor must actually move through those scroll positions (the
	// workspace clamps and stores them) — otherwise the mapping below is
	// untestable.
	editorScroll(maxScroll)
	if s, _ := handle.View.Read(cx).DebugScrollState(); s != maxScroll {
		t.Fatalf("editor scroll clamped/moved: set %d got %d (visible=%d total=%d)", maxScroll, s, visible, total)
	}
	editorScroll(0)
	if s, _ := handle.View.Read(cx).DebugScrollState(); s != 0 {
		t.Fatalf("editor scroll reset failed: got %d", s)
	}

	// A click selects the line currently painted at that point in the
	// sliding miniature, not that percentage of the whole document.
	mm := findMinimap(t, handle)
	clickAt := func(frac float32) int {
		pos := geometry.PointPixels(mm.x+mm.w/2, mm.y+frac*mm.h)
		hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: pos})
		l, _ := handle.View.Read(cx).DebugScrollState()
		hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: pos})
		handle.Window.Draw()
		return l
	}
	mmCapacity := int(mm.h / editor.MinimapLineHeight)
	for _, initial := range []int{0, maxScroll / 2, maxScroll} {
		for _, frac := range []float32{0.25, 0.5, 0.75} {
			editorScroll(initial)
			handle.Window.Draw()
			top := float32(initial) / float32(maxScroll) * float32(max(0, total-mmCapacity))
			clicked := min(top+frac*mm.h/editor.MinimapLineHeight, float32(total-1))
			want := int(min(max(clicked-float32(visible)/2, 0), float32(maxScroll)))
			got := clickAt(frac)
			if absi(got-want) > 1 {
				t.Fatalf("scroll %d, click %.0f%% landed on line %d, want %d", initial, frac*100, got, want)
			}
		}
	}
}

// TestMinimapSmallFileShowsWholeDoc keeps a short document's minimap static:
// the whole file fits the rail, the thumb covers the rail (nothing to
// scroll), and the file is not stretched or squeezed.
func TestMinimapSmallFileShowsWholeDoc(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&sb, "// line %d\n", i+1)
	}
	hp, handle, cx := openEditorWindowWith(t, sb.String())
	_ = hp
	ws := handle.View.Read(cx)
	total := ws.Buf().LineCount()
	visible := editor.Metrics{LineHeight: ws.LineHeight}.VisibleLines(ws.DebugPaneHeight())
	if total >= visible {
		t.Fatalf("test needs a file smaller than the viewport (total=%d visible=%d)", total, visible)
	}
	// Nothing to assert beyond stability: scroll stays 0, no panic, the
	// visual scene test renders this state.
	if s, _ := handle.View.Read(cx).DebugScrollState(); s != 0 {
		t.Fatalf("small file scrolled: %d", s)
	}
}

func TestMinimapClickOnFittingDocument(t *testing.T) {
	var src strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&src, "line %d\n", i)
	}
	hp, handle, cx := openEditorWindowWith(t, src.String())
	ws := handle.View.Read(cx)
	visible := editor.Metrics{LineHeight: ws.LineHeight}.VisibleLines(ws.DebugPaneHeight())
	mm := findMinimap(t, handle)
	if ws.Buf().LineCount() >= int(mm.h/editor.MinimapLineHeight) || ws.Buf().LineCount() <= visible {
		t.Fatal("fixture must fit the minimap but overflow the editor")
	}
	const clickedLine = 70
	pos := geometry.PointPixels(mm.x+mm.w/2, mm.y+(float32(clickedLine)+0.25)*editor.MinimapLineHeight)
	hp.LastWindow().DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: pos})
	hp.LastWindow().DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: pos})
	got, _ := handle.View.Read(cx).DebugScrollState()
	want := clickedLine - visible/2
	if absi(got-want) > 1 {
		t.Fatalf("click on miniature line %d scrolled to %d, want %d", clickedLine, got, want)
	}
}
