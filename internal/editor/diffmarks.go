package editor

import "sort"

// LineDiffKind classifies one file line's working-tree git state for the
// gutter's change bars (PaneConfig.DiffLines). The zero value means the
// line is unchanged and draws nothing.
type LineDiffKind uint8

const (
	// DiffAdded marks a line that exists only in the working tree (green
	// bar: content that is not in the compared revision yet).
	DiffAdded LineDiffKind = iota + 1
	// DiffModified marks a line whose content changed against the compared
	// revision (blue bar).
	DiffModified
	// DiffDeleted marks the line where removed content lands: removed
	// ranges collapse into one marker drawn on the first line after the
	// removal (the wide red bar).
	DiffDeleted
)

// Gutter constants for the git change bars. The band lives between the
// line numbers and the pane divider (the clear half of GutterCodeGap the
// numbers' right padding already reserves), so turning the marks on never
// changes the gutter width, the text origin or the pointer mapping.
const (
	// GitGutterW is the change-bar band's width (logical px).
	GitGutterW = 9.0
	// GitMarkW is the added/modified bar's width.
	GitMarkW = 3.0
)

// GutterRun is one merged span of the viewport's change bars. Contiguous
// changed lines of one kind draw as a SINGLE continuous bar — the gutter
// convention — instead of a stack of per-line stubs with gaps; unmarked
// rows fold into a spacing run (Kind 0) so the drawn cells still stack to
// exactly the visible rows' height.
type GutterRun struct {
	Kind    LineDiffKind // 0 = unmarked spacing run
	Rows    int          // rendered rows the run spans
	EndLine int          // the run's last file line (1-based)
}

// DiffGutterRuns folds the viewport's visible rows into merged runs. A
// run only grows while the file lines stay consecutive (a folded gap
// must break it — the bar may not bridge hidden lines), and DiffDeleted
// never merges: every removal marker stays on its own landing line.
func DiffGutterRuns(rows []int, marks map[int]LineDiffKind) []GutterRun {
	runs := make([]GutterRun, 0, len(rows)/4+1)
	for _, row := range rows {
		kind := marks[row+1]
		if n := len(runs); n > 0 && runs[n-1].Kind == kind &&
			kind != DiffDeleted && row == runs[n-1].EndLine {
			runs[n-1].Rows++
			runs[n-1].EndLine = row + 1
			continue
		}
		runs = append(runs, GutterRun{Kind: kind, Rows: 1, EndLine: row + 1})
	}
	return runs
}

// ScrollSegment is one merged changed region for the vertical scrollbar's
// track: the diff summary drawn beside the minimap (added regions green,
// modified ones blue, a removal a red tick on its landing line).
type ScrollSegment struct {
	Kind      LineDiffKind
	StartLine int // 1-based, inclusive
	EndLine   int // 1-based, inclusive
}

// DiffScrollSegments folds the buffer's marks into merged spans of
// consecutive same-kind lines (removal markers never merge, mirroring the
// gutter). Callers map the spans onto the track proportionally.
func DiffScrollSegments(marks map[int]LineDiffKind) []ScrollSegment {
	if len(marks) == 0 {
		return nil
	}
	lines := make([]int, 0, len(marks))
	for ln := range marks {
		lines = append(lines, ln)
	}
	sort.Ints(lines)
	segs := make([]ScrollSegment, 0, len(lines))
	for _, ln := range lines {
		kind := marks[ln]
		if n := len(segs); n > 0 && segs[n-1].Kind == kind &&
			kind != DiffDeleted && ln == segs[n-1].EndLine+1 {
			segs[n-1].EndLine = ln
			continue
		}
		segs = append(segs, ScrollSegment{Kind: kind, StartLine: ln, EndLine: ln})
	}
	return segs
}

// PixelSegment is one scrollbar-track span in track pixels: the half-open
// range [Top, End) where the changed region's code scrolls past.
type PixelSegment struct {
	Kind LineDiffKind
	Top  float32
	End  float32
}

// MergeScrollSegmentsPixel projects line-space segments onto the track and
// merges spans that overlap or touch in pixels. A long file with hundreds
// of one-line hunks would otherwise paint one quad per hunk every frame —
// after the merge the count is bounded by the track height (disjoint
// merged spans, each at least a pixel apart), and each is a tiny
// axis-aligned rect the rasterizers' fast path handles. The FIRST segment's
// kind wins a merged span (deterministic; at track scale overlapping
// regions' colors are indistinguishable). One-line hunks keep the 2px
// minimum so they stay visible on long files.
func MergeScrollSegmentsPixel(segs []ScrollSegment, total, trackH float32) []PixelSegment {
	if len(segs) == 0 || total <= 0 || trackH <= 0 {
		return nil
	}
	px := make([]PixelSegment, 0, len(segs))
	for _, s := range segs {
		top := (float32(s.StartLine) - 1) / total * trackH
		end := float32(s.EndLine) / total * trackH
		if end <= top {
			end = top
		}
		if end-top < 2 {
			end = top + 2 // one-line hunks stay visible on long files
		}
		if top >= trackH {
			continue
		}
		if end > trackH {
			end = trackH
		}
		if n := len(px); n > 0 && top <= px[n-1].End {
			// Overlaps or touches the previous span: fold into it.
			if end > px[n-1].End {
				px[n-1].End = end
			}
			continue
		}
		px = append(px, PixelSegment{Kind: s.Kind, Top: top, End: end})
	}
	return px
}
