package editor

import (
	"testing"
)

const foldGoSample = `package main

import (
        "fmt"
        "os"
)

// greeting returns a hello line.
func greeting(name string) string {
        if name == "" {
                name = "world"
        }
        return fmt.Sprintf("hello %s with a brace { inside a string", name)
}

func main() {
        fmt.Println(greeting(os.Args[1]))
}
`

func TestBraceFoldsFunctions(t *testing.T) {
	b := NewBuffer("", "main.go", foldGoSample)
	folds := b.FoldRanges()
	if len(folds) == 0 {
		t.Fatal("no folds computed")
	}
	// 0-based lines: import ( ... ) folds as a paren block {2,5}; greeting
	// opens at 8, its inner if at 9 (nested), closes at 13; main opens at
	// 15 and closes at 17.
	type rng struct {
		s, e int
	}
	var got []rng
	sawParen := false
	for _, f := range folds {
		got = append(got, rng{f.Start, f.End})
		if f.Paren {
			sawParen = true
		}
	}
	want := []rng{{2, 5}, {8, 13}, {9, 11}, {15, 17}}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing fold %v in %v", w, got)
		}
	}
	if !sawParen {
		t.Fatalf("import ( ... ) block not folded as paren: %v", got)
	}
}

func TestFoldVisibilityAndNavigation(t *testing.T) {
	b := NewBuffer("", "main.go", foldGoSample)
	b.FoldRanges()
	total := b.LineCount()
	if visible := b.VisibleLineCount(); visible != total {
		t.Fatalf("fresh buffer hides lines: %d of %d", visible, total)
	}
	// Fold the greeting function (header line 8; the range hides the five
	// lines 9..13).
	b.Fold(8)
	if !b.IsFolded(8) {
		t.Fatal("fold(8) did not collapse")
	}
	if b.IsLineVisible(9) || b.IsLineVisible(13) {
		t.Fatal("lines inside the collapsed range are visible")
	}
	if !b.IsLineVisible(8) {
		t.Fatal("the fold header must stay visible")
	}
	if got := b.VisibleLineCount(); got != total-5 {
		t.Fatalf("visible count after folding = %d, want %d", got, total-5)
	}
	// Navigation skips the hidden region: 8 -> 14.
	if got := b.NextVisible(8); got != 14 {
		t.Fatalf("NextVisible(8) = %d, want 14", got)
	}
	if got := b.PrevVisible(14); got != 8 {
		t.Fatalf("PrevVisible(14) = %d, want 8", got)
	}
	// Clicking inside the hidden region (e.g. a stale scroll target)
	// unfolds it via UnfoldAt.
	b.UnfoldAt(10)
	if b.IsFolded(8) {
		t.Fatal("UnfoldAt inside the range did not expand")
	}
	if got := b.VisibleLineCount(); got != total {
		t.Fatalf("unfold restored %d of %d lines", got, total)
	}
}

func TestFoldAllUnfoldAll(t *testing.T) {
	b := NewBuffer("", "main.go", foldGoSample)
	b.FoldRanges()
	total := b.LineCount()
	b.FoldAll()
	if b.FoldedCount() == 0 {
		t.Fatal("FoldAll collapsed nothing")
	}
	if b.VisibleLineCount() >= total {
		t.Fatal("FoldAll hid nothing")
	}
	b.UnfoldAll()
	if b.FoldedCount() != 0 || b.VisibleLineCount() != total {
		t.Fatal("UnfoldAll did not restore")
	}
}

func TestFoldSurvivesEditsAbove(t *testing.T) {
	b := NewBuffer("", "main.go", foldGoSample)
	b.FoldRanges()
	b.Fold(8)
	// Insert two lines before the folded function (a new import).
	b.SetCursor(b.LineStart(3))
	b.Insert("import \"strings\"\n\n")
	if !b.IsFolded(10) {
		t.Fatalf("fold flag did not shift with the edit: folded=%v", b.foldFolded)
	}
	if b.IsLineVisible(11) {
		t.Fatal("the shifted range no longer hides its body")
	}
	// Editing INSIDE the folded region unfolds it.
	b.SetCursor(b.LineStart(11))
	b.Insert("x")
	if b.IsFolded(10) {
		t.Fatal("an edit inside the folded region did not unfold it")
	}
}

func TestFoldHeaderEditKeepsFold(t *testing.T) {
	b := NewBuffer("", "main.go", foldGoSample)
	b.FoldRanges()
	b.Fold(8)
	// Typing at the END of the header line keeps the fold (the header
	// still opens the region, the hidden body is untouched).
	b.SetCursor(b.LineStart(8) + len(b.Line(8)))
	b.Insert(" ")
	if !b.IsFolded(8) {
		t.Fatal("editing the header line end dropped the fold")
	}
	// Pressing Enter after the `{` keeps the fold on the same header line.
	b.SetCursor(b.LineStart(8) + len(b.Line(8)))
	b.Insert("\n")
	if !b.IsFolded(8) {
		t.Fatal("Enter on the header line dropped the fold")
	}
	if b.IsLineVisible(9) {
		t.Fatal("the body is visible after the header edit")
	}
}

func TestFoldDroppedWhenHeaderVanishes(t *testing.T) {
	b := NewBuffer("", "main.go", foldGoSample)
	b.FoldRanges()
	b.Fold(8)
	// Replace the header line's content: commenting out the func removes
	// the brace from code, so the fold range (and flag) must drop on
	// recompute.
	start := b.LineStart(8)
	b.apply(start, b.LineStart(9)-1, "// no longer a function")
	b.FoldRanges()
	if b.IsFolded(8) {
		t.Fatal("fold flag survived the header's removal")
	}
}

func TestIndentFoldsPython(t *testing.T) {
	src := "def a():\n    if x:\n        pass\n    return 1\n"
	b := NewBuffer("", "calc.py", src)
	folds := b.FoldRanges()
	if len(folds) != 2 {
		t.Fatalf("python folds = %v, want 2 ranges", folds)
	}
	if folds[0].Start != 0 || folds[0].End != 3 {
		t.Fatalf("outer fold = %v", folds[0])
	}
	if folds[1].Start != 1 || folds[1].End != 2 {
		t.Fatalf("inner fold = %v", folds[1])
	}
	if !Foldable("python") || Foldable("text") {
		t.Fatal("Foldable misclassifies languages")
	}
}

func TestGomodRequireBlockFolds(t *testing.T) {
	src := "module example.com/app\n\ngo 1.23\n\nrequire (\n\tgithub.com/foo/bar v1.0.0\n\tgithub.com/baz/qux v2.1.0\n)\n"
	b := NewBuffer("", "go.mod", src)
	folds := b.FoldRanges()
	if len(folds) != 1 {
		t.Fatalf("go.mod folds = %v", folds)
	}
	f := folds[0]
	if f.Start != 4 || f.End != 7 || !f.Paren {
		t.Fatalf("require block fold = %+v", f)
	}
	b.Fold(4)
	if b.IsLineVisible(5) || b.IsLineVisible(7) || !b.IsLineVisible(4) {
		t.Fatal("require block visibility wrong after folding")
	}
	if b.NextVisible(4) != 8 {
		t.Fatalf("NextVisible after require block = %d", b.NextVisible(4))
	}
}

func TestFoldStateBracesInsideStringsIgnored(t *testing.T) {
	src := "func a() {\n\ts := \"}{\"\n\t_ = s\n}\n\nfunc b() {\n\t_ = 1\n}\n"
	b := NewBuffer("", "main.go", src)
	folds := b.FoldRanges()
	var starts []int
	for _, f := range folds {
		starts = append(starts, f.Start)
	}
	if len(starts) != 2 || starts[0] != 0 || starts[1] != 5 {
		t.Fatalf("braces inside strings broke the fold structure: %v", folds)
	}
}

func TestUnfoldAtCursor(t *testing.T) {
	b := NewBuffer("", "main.go", foldGoSample)
	b.FoldRanges()
	b.Fold(8)
	// Put the caret inside the hidden region (as undo/redo can).
	b.SetCursor(b.LineStart(10))
	b.UnfoldAtCursor()
	if b.IsFolded(8) {
		t.Fatal("UnfoldAtCursor did not expand the fold around the caret")
	}
}
