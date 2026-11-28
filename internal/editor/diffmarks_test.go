package editor

import "testing"

// TestDiffGutterRunsMergeContiguous pins the gutter convention: a block
// of contiguous changed lines folds into ONE run (drawn as a single
// continuous bar), never a stack of per-line stubs.
func TestDiffGutterRunsMergeContiguous(t *testing.T) {
	marks := map[int]LineDiffKind{
		1: DiffAdded, 2: DiffAdded, 3: DiffAdded,
		5: DiffModified,
		7: DiffAdded, 9: DiffAdded, // one unmarked line between them
	}
	runs := DiffGutterRuns([]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, marks)
	var got []GutterRun
	for _, r := range runs {
		if r.Kind != 0 {
			got = append(got, r)
		}
	}
	want := []GutterRun{
		{Kind: DiffAdded, Rows: 3, EndLine: 3},
		{Kind: DiffModified, Rows: 1, EndLine: 5},
		{Kind: DiffAdded, Rows: 1, EndLine: 7},
		{Kind: DiffAdded, Rows: 1, EndLine: 9},
	}
	if len(got) != len(want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("run[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	// The cells must still stack to exactly the visible rows' height.
	total := 0
	for _, r := range runs {
		total += r.Rows
	}
	if total != 11 {
		t.Fatalf("run rows sum = %d, want 11", total)
	}
}

// TestDiffGutterRunsBreakAtFoldGap guards the fold interaction: a run may
// not bridge hidden lines — the bar stops at the fold and a new one
// starts after it (the two blocks stay visually separate).
func TestDiffGutterRunsBreakAtFoldGap(t *testing.T) {
	marks := map[int]LineDiffKind{
		3: DiffAdded, 4: DiffAdded,
		30: DiffAdded, 31: DiffAdded,
	}
	// Rows skip 4..28 (folded): consecutive slots, non-consecutive lines —
	// two merged added blocks with the fold gap between them.
	runs := DiffGutterRuns([]int{2, 3, 29, 30}, marks)
	if len(runs) != 2 {
		t.Fatalf("runs = %+v, want two added runs across the fold gap", runs)
	}
	if runs[0].Rows != 2 || runs[0].EndLine != 4 || runs[0].Kind != DiffAdded {
		t.Fatalf("first added run = %+v, want lines 3-4 merged", runs[0])
	}
	if runs[1].Rows != 2 || runs[1].EndLine != 31 || runs[1].Kind != DiffAdded {
		t.Fatalf("second added run = %+v, want lines 30-31 merged", runs[1])
	}
}

// TestDiffGutterRunsDeletedNeverMerges: removal markers each stay on
// their landing line, so adjacent deletions draw separate wide ticks.
func TestDiffGutterRunsDeletedNeverMerges(t *testing.T) {
	marks := map[int]LineDiffKind{
		4: DiffDeleted, 5: DiffDeleted,
	}
	runs := DiffGutterRuns([]int{3, 4, 5, 6}, marks)
	var dels int
	for _, r := range runs {
		if r.Kind == DiffDeleted {
			dels++
			if r.Rows != 1 {
				t.Errorf("deleted run spanned %d rows, want 1", r.Rows)
			}
		}
	}
	if dels != 2 {
		t.Fatalf("got %d deleted runs, want 2 (one per landing line)", dels)
	}
}

// TestDiffScrollSegments pins the scrollbar's merged diff summary:
// consecutive same-kind lines join, removals stay separate, and the
// spans cover exactly the marked lines.
func TestDiffScrollSegments(t *testing.T) {
	marks := map[int]LineDiffKind{
		1: DiffAdded, 2: DiffAdded, 3: DiffAdded,
		5: DiffModified, 6: DiffModified,
		8:  DiffDeleted,
		10: DiffDeleted, 11: DiffDeleted,
	}
	segs := DiffScrollSegments(marks)
	want := []ScrollSegment{
		{Kind: DiffAdded, StartLine: 1, EndLine: 3},
		{Kind: DiffModified, StartLine: 5, EndLine: 6},
		{Kind: DiffDeleted, StartLine: 8, EndLine: 8},
		{Kind: DiffDeleted, StartLine: 10, EndLine: 10},
		{Kind: DiffDeleted, StartLine: 11, EndLine: 11},
	}
	if len(segs) != len(want) {
		t.Fatalf("segments = %+v, want %+v", segs, want)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Errorf("segment[%d] = %+v, want %+v", i, segs[i], want[i])
		}
	}
	if DiffScrollSegments(nil) != nil {
		t.Fatal("no marks produced segments")
	}
}

// TestMergeScrollSegmentsPixel pins the scrollbar's pixel projection: the
// per-frame paint must stay bounded no matter how many separate hunks the
// working tree carries — overlapping or touching spans fold, disjoint ones
// stay apart, one-line hunks keep their 2px minimum, and nothing runs past
// the track.
func TestMergeScrollSegmentsPixel(t *testing.T) {
	// Empty input paints nothing.
	if px := MergeScrollSegmentsPixel(nil, 100, 200); px != nil {
		t.Fatalf("empty segments produced %+v", px)
	}
	// Two disjoint blocks on a 100-line track keep their own spans.
	px := MergeScrollSegmentsPixel([]ScrollSegment{
		{Kind: DiffAdded, StartLine: 1, EndLine: 10},
		{Kind: DiffModified, StartLine: 50, EndLine: 60},
	}, 100, 200)
	if len(px) != 2 {
		t.Fatalf("disjoint spans = %+v, want 2", px)
	}
	if px[0].Kind != DiffAdded || px[0].Top != 0 || px[0].End != 20 {
		t.Errorf("span 0 = %+v, want added [0,20)", px[0])
	}
	if px[1].Kind != DiffModified || absF(px[1].Top-98) > 0.01 || absF(px[1].End-120) > 0.01 {
		t.Errorf("span 1 = %+v, want modified [98,120)", px[1])
	}
	// A one-line hunk keeps the 2px minimum.
	px = MergeScrollSegmentsPixel([]ScrollSegment{
		{Kind: DiffAdded, StartLine: 50, EndLine: 50},
	}, 100, 200)
	if len(px) != 1 || px[0].End-px[0].Top < 2 {
		t.Fatalf("one-line hunk = %+v, want a visible 2px span", px)
	}
	// Spans that overlap or touch in pixels fold into one; the first
	// segment's kind wins (deterministic at track scale).
	px = MergeScrollSegmentsPixel([]ScrollSegment{
		{Kind: DiffAdded, StartLine: 1, EndLine: 10},
		{Kind: DiffModified, StartLine: 11, EndLine: 20},
		{Kind: DiffDeleted, StartLine: 12, EndLine: 12},
	}, 100, 20)
	// The deletion tick (line 12 -> [2.2, 4.2) after its 2px minimum)
	// folds into the same span, extending it a hair past line 20's end.
	if len(px) != 1 || px[0].Kind != DiffAdded || px[0].Top != 0 || absF(px[0].End-4.2) > 0.01 {
		t.Fatalf("overlapping spans = %+v, want one added span [0,~4.2)", px)
	}
	// A span past the track's end clamps; a start beyond it disappears.
	px = MergeScrollSegmentsPixel([]ScrollSegment{
		{Kind: DiffAdded, StartLine: 90, EndLine: 120},
		{Kind: DiffModified, StartLine: 130, EndLine: 140},
	}, 100, 100)
	if len(px) != 1 || px[0].End != 100 {
		t.Fatalf("clamped spans = %+v, want one span ending at the track bottom", px)
	}
	// Hundreds of separate one-line hunks on a short track collapse to a
	// handful of pixel-disjoint spans — the count is bounded by the track,
	// never by the hunk count (the scroll-lag regression this guards).
	segs := make([]ScrollSegment, 0, 300)
	for i := 1; i <= 300; i++ {
		segs = append(segs, ScrollSegment{Kind: DiffModified, StartLine: i, EndLine: i})
	}
	px = MergeScrollSegmentsPixel(segs, 300, 100)
	if len(px) > 50 {
		t.Fatalf("%d one-line hunks produced %d pixel spans, want a track-bounded handful", len(segs), len(px))
	}
	if len(px) == 0 {
		t.Fatal("the hunks vanished entirely")
	}
}
