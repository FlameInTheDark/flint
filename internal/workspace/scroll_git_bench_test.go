package workspace

import (
        "fmt"
        "strings"
        "testing"

        "github.com/FlameInTheDark/flint/internal/editor"
)

// bigGitFile builds a debug.go-sized buffer (the user's slow-scroll report)
// and a marks map shaped like an actively-edited working tree: a few large
// contiguous changed blocks plus scattered one-line edits, the shape the
// merged gutter bars and scrollbar segments fold over.
func bigGitFile(lines int) (*editor.Buffer, map[int]editor.LineDiffKind) {
        src := make([]string, lines)
        for i := range src {
                src[i] = fmt.Sprintf("\tfmt.Printf(\"line %d: value=%d\\n\", %d, %d)", i, i%97, i, i%97)
        }
        // The buffer carries a real path: editorDiffLines keys marks by the
        // buffer's path, so an empty path silently renders nothing (the bug
        // the first version of this benchmark had).
        buf := editor.NewBuffer("/bench/debug.go", "debug.go", strings.Join(src, "\n"))
        marks := map[int]editor.LineDiffKind{}
        addBlock := func(from, n int, kind editor.LineDiffKind) {
                for i := 0; i < n; i++ {
                        marks[from+i] = kind
                }
        }
        addBlock(1, 9, editor.DiffModified)   // one contiguous modified block
        addBlock(120, 40, editor.DiffAdded)   // a wide added block
        addBlock(400, 3, editor.DiffModified) // small edits
        addBlock(404, 2, editor.DiffDeleted)  // removals
        addBlock(700, 120, editor.DiffAdded)  // a big tail block
        for i := 900; i < lines; i += 7 {     // scattered single-line edits
                marks[i] = editor.DiffModified
        }
        return buf, marks
}

// benchScrollFrameWithMarks measures a frame while the wheel ease slides
// through a big file carrying git change marks (the gutter bars + scrollbar
// segments render on every one of those frames). minimap toggles the rail
// (the code map paints hundreds of token blocks per frame when on).
func benchScrollFrameWithMarks(b *testing.B, lines int, minimap bool) {
        buf, marks := bigGitFile(lines)
        hp, handle, ap := openLeftGapScene(b, true, buf)
        _ = hp
        st := handle.View.Read(ap)
        if st.editorDiffs == nil {
                st.editorDiffs = map[string]*fileDiffMarks{}
        }
        st.editorDiffs[diffKey("/bench/debug.go")] = &fileDiffMarks{
                marks: marks,
                segs:  editor.DiffScrollSegments(marks), // what applyEditorDiff folds once per update
                rev:   buf.Revision,
        }
        st.Cfg.Editor.ShowMinimap = minimap
        b.ResetTimer()
        for i := 0; i < b.N; i++ {
                pos := float32(i%max(1, lines-60)) + 0.37
                s := handle.View.Read(ap)
                s.scrolls[s.Active] = pos
                s.scrollAnims[s.Active] = pos
                s.scrollAnimRuns[s.Active] = false
                handle.Window.Draw()
        }
}

func BenchmarkScrollFrameGitMarks1100(b *testing.B) { benchScrollFrameWithMarks(b, 1100, true) }
func BenchmarkScrollFrameGitMarks20k(b *testing.B)  { benchScrollFrameWithMarks(b, 20000, true) }

// Minimap off isolates the code map's share of the same scroll.
func BenchmarkScrollFrameGitMarks20kNoMinimap(b *testing.B) {
        benchScrollFrameWithMarks(b, 20000, false)
}

// BenchmarkStaticFrameGitMarks1100 is the control: marks present, no motion.
func BenchmarkStaticFrameGitMarks1100(b *testing.B) {
        buf, marks := bigGitFile(1100)
        hp, handle, ap := openLeftGapScene(b, true, buf)
        _ = hp
        st := handle.View.Read(ap)
        if st.editorDiffs == nil {
                st.editorDiffs = map[string]*fileDiffMarks{}
        }
        st.editorDiffs[diffKey("/bench/debug.go")] = &fileDiffMarks{
                marks: marks,
                segs:  editor.DiffScrollSegments(marks),
                rev:   buf.Revision,
        }
        b.ResetTimer()
        for i := 0; i < b.N; i++ {
                handle.Window.Draw()
        }
}
