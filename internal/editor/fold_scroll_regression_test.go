package editor

import "testing"

func TestCollapsedFoldIndexTracksChanges(t *testing.T) {
	b := NewBuffer("", "main.go", foldGoSample)
	check := func() {
		t.Helper()
		b.FoldRanges()
		for line := range b.Lines {
			var want *FoldRange
			for i := range b.foldList {
				f := &b.foldList[i]
				if b.foldFolded[f.Start] && f.Start < line && line <= f.End {
					want = f
					break
				}
			}
			got := b.foldedRangeContaining(line)
			if (got == nil) != (want == nil) || got != nil && *got != *want {
				t.Fatalf("line %d: containing fold = %v, want %v", line, got, want)
			}
		}
	}
	check()
	b.Fold(8)
	check()
	b.Fold(9) // nested collapsed range
	check()
	b.Unfold(8)
	check()
	b.ToggleFold(9)
	check()
	b.FoldAll()
	check()
	b.UnfoldAt(10)
	check()
	b.SetCursor(0)
	b.Insert("// inserted\n")
	check()
	b.UnfoldAll()
	check()
}

func TestRailSeekUsesAbsolutePosition(t *testing.T) {
	position := 0
	cfg := railConfig{
		Scroll:     0, // the listeners can still belong to an older frame
		OnScroll:   func(delta int) { position += delta },
		OnScrollTo: func(target int) { position = target },
	}
	for _, target := range []int{200, 300, 200} {
		cfg.seek(target)
		if position != target {
			t.Fatalf("seek to %d landed at %d", target, position)
		}
	}
	// Callers without an absolute seek retain the relative-scroll fallback.
	cfg.OnScrollTo = nil
	cfg.Scroll = position
	cfg.seek(250)
	if position != 250 {
		t.Fatalf("relative fallback landed at %d, want 250", position)
	}
}

func BenchmarkFoldLookupLongFile(b *testing.B) {
	buf := &Buffer{foldList: make([]FoldRange, 50000), foldFolded: map[int]bool{}}
	for i := range buf.foldList {
		buf.foldList[i] = FoldRange{Start: i * 3, End: i*3 + 2}
	}
	buf.foldFolded[3] = true
	buf.foldFolded[120000] = true
	buf.collapsedFolds()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		line := (i * 197) % 150000
		buf.foldStart(line)
		buf.foldedRangeContaining(line)
	}
}
