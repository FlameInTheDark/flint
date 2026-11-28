package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// gitSh runs one git command in dir and fails the test on errors (the
// fixture commands must all succeed; only the assertions decide the test).
func gitSh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// newGitRepo builds a repository with one committed file and returns the
// repo root (the fixture config pins identity and signing off).
func newGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitSh(t, root, "init", "-q", "-b", "main")
	gitSh(t, root, "config", "user.name", "Marks Test")
	gitSh(t, root, "config", "user.email", "marks@example.test")
	gitSh(t, root, "config", "commit.gpgsign", "false")
	return root
}

func TestParseGitDiffMarks(t *testing.T) {
	patch := `diff --git a/app.go b/app.go
index 1111111..2222222 100644
--- a/app.go
+++ b/app.go
@@ -1,3 +1,3 @@
-fmt.Println("old one")
+fmt.Println("new one")
+fmt.Println("new two")
 fmt.Println("kept")
@@ -10,2 +11,3 @@
+fmt.Println("inserted")
 fmt.Println("context")
@@ -20,4 +23,1 @@
 fmt.Println("tail")
-fmt.Println("gone one")
-fmt.Println("gone two")
`
	marks := parseGitDiffMarks([]byte(patch))
	// Hunk 1: one removed, two added lines at new lines 1-2 → the first
	// added line pairs with the removal (modified), the second is added.
	if marks[1] != editor.DiffModified {
		t.Fatalf("line 1 = %v, want modified", marks[1])
	}
	if marks[2] != editor.DiffAdded {
		t.Fatalf("line 2 = %v, want added", marks[2])
	}
	// Hunk 2: pure insertion at new line 11.
	if marks[11] != editor.DiffAdded {
		t.Fatalf("line 11 = %v, want added", marks[11])
	}
	// Hunk 3: the kept line stays unmarked (a context line), and the two
	// removals collapse into the marker on new line 24 (the line right
	// after the removed range).
	if _, ok := marks[23]; ok {
		t.Fatalf("line 23 = %v, want no mark on the unchanged line", marks[23])
	}
	if marks[24] != editor.DiffDeleted {
		t.Fatalf("line 24 = %v, want the deletion marker", marks[24])
	}
	if len(marks) != 4 {
		t.Fatalf("marks = %v, want exactly 4 lines marked", marks)
	}
}

func TestParseGitDiffMarksDeletionAtFileStart(t *testing.T) {
	patch := `--- a/top.go
+++ b/top.go
@@ -1,2 +0,0 @@
-package top
-var A = 1
`
	marks := parseGitDiffMarks([]byte(patch))
	if marks[1] != editor.DiffDeleted {
		t.Fatalf("line 1 = %v, want the deletion marker at the file's first line", marks[1])
	}
}

func TestParseGitDiffMarksAllAdded(t *testing.T) {
	// A new file's diff: one hunk, only additions.
	patch := `--- /dev/null
+++ b/fresh.go
@@ -0,0 +1,2 @@
+package fresh
+var B = 2
`
	marks := parseGitDiffMarks([]byte(patch))
	if marks[1] != editor.DiffAdded || marks[2] != editor.DiffAdded || len(marks) != 2 {
		t.Fatalf("marks = %v, want lines 1-2 added", marks)
	}
}

func TestGitStateFromCode(t *testing.T) {
	cases := map[string]gitFileState{
		"M ": gitStagedMod,
		" M": gitModified,
		"MM": gitModified, // staged and edited again: the worse half wins
		"A ": gitStagedAdd,
		"AM": gitModified,
		" D": gitDeleted,
		"D ": gitDeleted,
		"??": gitUntracked,
		"!!": gitIgnored,
		"UU": gitConflict,
		"DU": gitConflict,
		"R ": gitRenamed,
		"  ": gitClean,
	}
	for code, want := range cases {
		if got := gitStateFromCode(code); got != want {
			t.Errorf("gitStateFromCode(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestCollectTreeGit(t *testing.T) {
	root := newGitRepo(t)
	writeFile := func(rel, text string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("kept.go", "package main\n")
	writeFile(".gitignore", "sealed.log\n")
	gitSh(t, root, "add", ".")
	gitSh(t, root, "commit", "-qm", "base")
	writeFile("kept.go", "package main // edited\n") // modified (unstaged)
	writeFile("fresh/new.go", "package fresh\n")     // untracked
	writeFile("sealed.log", "sealed\n")              // ignored
	gitSh(t, root, "add", "kept.go")                 // then stage it too

	snap := collectTreeGit(root)
	if snap.repo == "" {
		t.Fatal("collectTreeGit missed the repository top")
	}
	if got := snap.files[filepath.Join(root, "kept.go")]; got != gitStagedMod {
		t.Fatalf("kept.go = %v, want staged modified", got)
	}
	if got := snap.files[filepath.Join(root, "fresh", "new.go")]; got != gitUntracked {
		t.Fatalf("fresh/new.go = %v, want untracked", got)
	}
	if got := snap.files[filepath.Join(root, "sealed.log")]; got != gitIgnored {
		t.Fatalf("sealed.log = %v, want ignored", got)
	}
	// Directory aggregation: fresh/ carries its untracked file's state, and
	// the repository top carries the worst descendant (staged modified).
	if got := snap.dirs[filepath.Join(root, "fresh")]; got != gitUntracked {
		t.Fatalf("fresh/ = %v, want untracked", got)
	}
	if got := snap.dirs[root]; got != gitStagedMod {
		t.Fatalf("repo top = %v, want staged modified", got)
	}
}

func TestCollectTreeGitNoRepo(t *testing.T) {
	snap := collectTreeGit(t.TempDir())
	if snap.repo != "" || snap.files != nil {
		t.Fatalf("a plain folder produced %+v, want an empty snapshot", snap)
	}
}

func TestEditorDiffMarksFlow(t *testing.T) {
	root := newGitRepo(t)
	src := filepath.Join(root, "app.go")
	body := "package main\n\nfunc main() {\n\tfmt.Println(\"one\")\n\tfmt.Println(\"two\")\n}\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitSh(t, root, "add", ".")
	gitSh(t, root, "commit", "-qm", "base")

	ws := New("marks")
	if err := ws.OpenFolder(root); err != nil {
		t.Fatal(err)
	}
	ws.OpenFile(src)
	b := ws.Buf()
	if b == nil {
		t.Fatal("the fixture file did not open")
	}

	// Clean file, no repository status yet: the first poll computes empty
	// marks (no change bars on a committed, unedited file).
	ws.pollEditorDiffs(nil, nil)
	if marks := ws.editorDiffLines(b); len(marks) != 0 {
		t.Fatalf("clean file produced %v, want no marks", marks)
	}
	if st := ws.editorDiffs[diffKey(src)]; st == nil || st.rev != b.Revision {
		t.Fatalf("marks state did not settle on the buffer revision: %+v", st)
	}

	// Edit two lines: rewrite one (selection across the line, then insert
	// the replacement) and append another after the closing brace. The next
	// poll shows one modified line and one added line.
	edit := func(f func(*editor.Buffer)) { f(b) }
	edit(func(b *editor.Buffer) {
		b.SetCursor(b.LineStart(3)) // 0-based line 3 = "\tfmt.Println(\"one\")"
		b.ExtendCursor(b.LineStart(4))
		b.DeleteForward()
		b.Insert("\tfmt.Println(\"ONE\")\n")
	})
	edit(func(b *editor.Buffer) { b.SetCursor(len(b.Text())); b.Insert("\tfmt.Println(\"three\")\n") })
	ws.pollEditorDiffs(nil, nil)
	marks := ws.editorDiffLines(b)
	if marks[4] != editor.DiffModified {
		t.Fatalf("line 4 = %v, want modified", marks[4])
	}
	if marks[7] != editor.DiffAdded {
		t.Fatalf("line 7 = %v, want added", marks[7])
	}

	// A commit re-bases the diff: save the buffer, commit, then after
	// invalidation and a poll the bars disappear again.
	if err := b.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	gitSh(t, root, "add", ".")
	gitSh(t, root, "commit", "-qm", "edit")
	ws.invalidateEditorDiffs()
	ws.pollEditorDiffs(nil, nil)
	if marks := ws.editorDiffLines(b); len(marks) != 0 {
		t.Fatalf("committed file still carries marks: %v", marks)
	}
}

func TestEditorDiffMarksUntrackedAndNoRepo(t *testing.T) {
	plain := t.TempDir()
	src := filepath.Join(plain, "notes.md")
	if err := os.WriteFile(src, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := New("marks")
	if err := ws.OpenFolder(plain); err != nil {
		t.Fatal(err)
	}
	ws.OpenFile(src)
	b := ws.Buf()
	ws.pollEditorDiffs(nil, nil)
	if marks := ws.editorDiffLines(b); len(marks) != 0 {
		t.Fatalf("no repository produced %v, want no marks", marks)
	}

	// An untracked file inside a repository: every line counts as added.
	repo := newGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "readme.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws2 := New("marks")
	if err := ws2.OpenFolder(repo); err != nil {
		t.Fatal(err)
	}
	ws2.OpenFile(filepath.Join(repo, "readme.txt"))
	b2 := ws2.Buf()
	ws2.pollEditorDiffs(nil, nil)
	marks := ws2.editorDiffLines(b2)
	if len(marks) != 2 || marks[1] != editor.DiffAdded || marks[2] != editor.DiffAdded {
		t.Fatalf("untracked file marks = %v, want both lines added", marks)
	}
}

// TestEditorDiffSegmentsFoldedWithMarks pins the scrollbar's data path: the
// workspace folds the marks into segments once per marks update (never per
// frame — the re-fold on every paint was the scroll-lag regression on
// heavily-changed large files), and the segments always agree with the
// gutter's marks.
func TestEditorDiffSegmentsFoldedWithMarks(t *testing.T) {
	root := newGitRepo(t)
	src := filepath.Join(root, "app.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n\tprintln()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitSh(t, root, "add", ".")
	gitSh(t, root, "commit", "-qm", "base")

	ws := New("marks")
	if err := ws.OpenFolder(root); err != nil {
		t.Fatal(err)
	}
	ws.OpenFile(src)
	b := ws.Buf()
	ws.pollEditorDiffs(nil, nil)
	if segs := ws.editorDiffSegments(b); len(segs) != 0 {
		t.Fatalf("clean file produced segments %+v", segs)
	}

	// Edit two contiguous lines: the marks show one modified block and
	// one added line; the segments must fold exactly that.
	b.SetCursor(len(b.Text()))
	b.Insert("\tprintln(\"more\")\n")
	ws.pollEditorDiffs(nil, nil)
	marks := ws.editorDiffLines(b)
	segs := ws.editorDiffSegments(b)
	if len(segs) == 0 {
		t.Fatal("edited file has marks but no segments")
	}
	want := editor.DiffScrollSegments(marks)
	if len(segs) != len(want) {
		t.Fatalf("segments = %+v, want the fold of the marks %+v", segs, want)
	}
	for i := range want {
		if segs[i] != want[i] {
			t.Errorf("segment[%d] = %+v, want %+v", i, segs[i], want[i])
		}
	}
}
