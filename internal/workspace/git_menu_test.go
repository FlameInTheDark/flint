package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/editor"
)

// TestGitignoreHelpers pins the entry format (repo-relative forward
// slashes, directories trailing '/') and the create/append/dedup cycle.
func TestGitignoreHelpers(t *testing.T) {
	repo := newGitRepo(t)

	if got := gitignorePathFor(repo, filepath.Join(repo, "build"), true); got != "build/" {
		t.Fatalf("dir entry = %q, want \"build/\"", got)
	}
	if got := gitignorePathFor(repo, filepath.Join(repo, "debug.log"), false); got != "debug.log" {
		t.Fatalf("file entry = %q, want \"debug.log\"", got)
	}
	if got := gitignorePathFor(repo, filepath.Join(repo, "gen", "out.txt"), false); got != "gen/out.txt" {
		t.Fatalf("nested entry = %q, want \"gen/out.txt\"", got)
	}

	if gitignoreHas(repo, "debug.log") {
		t.Fatal("empty repository reported the entry as present")
	}
	created, err := addToGitignore(repo, "debug.log")
	if err != nil || !created {
		t.Fatalf("first add: created=%v err=%v", created, err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil || string(data) != "debug.log\n" {
		t.Fatalf("new .gitignore = %q err=%v, want \"debug.log\\n\"", data, err)
	}
	if !gitignoreHas(repo, "debug.log") {
		t.Fatal("written entry not detected")
	}
	created, err = addToGitignore(repo, "build/")
	if err != nil || created {
		t.Fatalf("second add: created=%v err=%v, want created=false", created, err)
	}
	data, _ = os.ReadFile(filepath.Join(repo, ".gitignore"))
	if string(data) != "debug.log\nbuild/\n" {
		t.Fatalf("appended .gitignore = %q", data)
	}
	// Re-adding the same entry is what the menu's disabled state prevents;
	// the detector must still see it after append.
	if !gitignoreHas(repo, "build/") {
		t.Fatal("appended dir entry not detected")
	}
}

// refreshTreeGitInTest runs one inline tree coloring refresh (the real
// poll path needs a live window for its next-frame callback).
func refreshTreeGitInTest(ws *Workspace) {
	if ws.treeGit == nil {
		ws.treeGit = &treeGitState{}
	}
	snap := collectTreeGit(ws.Root)
	ws.treeGit.repo, ws.treeGit.files, ws.treeGit.dirs = snap.repo, snap.files, snap.dirs
	ws.treeGit.tried = true
}

// treeMenuFixture builds a committed repository with one locally edited
// file, an untracked file and the workspace opened on it, so the tree
// menu's VCS section can be asserted per state.
func treeMenuFixture(t *testing.T) (*Workspace, string) {
	t.Helper()
	repo := newGitRepo(t)
	write := func(rel, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("kept.go", "package main\n")
	write("untracked.txt", "new\n")
	gitSh(t, repo, "add", "kept.go")
	gitSh(t, repo, "commit", "-qm", "base")
	write("kept.go", "package main // edited\n")

	ws := New("tree-vcs-menu")
	if err := ws.OpenFolder(repo); err != nil {
		t.Fatal(err)
	}
	refreshTreeGitInTest(ws)
	if ws.treeGit == nil || ws.treeGit.repo == "" {
		t.Fatal("the fixture repository was not detected")
	}
	return ws, repo
}

func treeFixtureNode(t *testing.T, ws *Workspace, rel string) *TreeNode {
	t.Helper()
	node := ws.Tree.Find(filepath.Join(ws.treeGit.repo, filepath.FromSlash(rel)))
	if node == nil {
		t.Fatalf("%s not in the project tree", rel)
	}
	return node
}

// TestTreeMenuVCSNoRepo: outside a repository the menu carries no VCS
// entries at all (the plain file actions only).
func TestTreeMenuVCSNoRepo(t *testing.T) {
	ws := New("plain")
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "note.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ws.OpenFolder(plain); err != nil {
		t.Fatal(err)
	}
	node := ws.Tree.Find(filepath.Join(plain, "note.txt"))
	if node == nil {
		t.Fatal("fixture file missing from the tree")
	}
	items := TreeContextItems(func(f func(*Workspace)) { f(ws) }, nil, ws, node)
	for _, item := range items {
		if strings.Contains(item.Label, "gitignore") || item.Label == "Stage" ||
			item.Label == "Unstage" || strings.HasPrefix(item.Label, "Discard") {
			t.Fatalf("no-repo menu leaked VCS item %q", item.Label)
		}
	}
}

// TestTreeMenuVCSRootSkipped: the project root's (empty-space) menu stays
// plain — the whole repo can't be staged or ignored from itself.
func TestTreeMenuVCSRootSkipped(t *testing.T) {
	ws, repo := treeMenuFixture(t)
	root := ws.Tree.Find(repo)
	items := TreeContextItems(func(f func(*Workspace)) { f(ws) }, nil, ws, root)
	for _, item := range items {
		if strings.Contains(item.Label, "gitignore") || item.Label == "Stage" {
			t.Fatalf("root menu leaked VCS item %q", item.Label)
		}
	}
}

// TestTreeMenuVCSItemsAndActions pins the section's shape and its
// enablement, then exercises the two mutating actions inline: adding to
// .gitignore writes the repo-relative entry, and staging an untracked
// file flips its status (the tree re-colors after both).
func TestTreeMenuVCSItemsAndActions(t *testing.T) {
	ws, repo := treeMenuFixture(t)
	update := func(f func(*Workspace)) { f(ws) }

	modified := treeFixtureNode(t, ws, "kept.go")
	untracked := treeFixtureNode(t, ws, "untracked.txt")

	items := TreeContextItems(update, nil, ws, untracked)
	labels := map[string]uiMenuItem{}
	for _, item := range items {
		labels[item.Label] = uiMenuItem{disabled: item.Disabled, separator: item.Separator}
	}
	gitignore, ok := labels["Add to .gitignore"]
	if !ok {
		t.Fatal("menu lacks Add to .gitignore")
	}
	if gitignore.disabled {
		t.Fatal("Add to .gitignore disabled on a fresh repository")
	}
	for _, label := range []string{"Stage", "Unstage", "Discard Changes…"} {
		if _, ok := labels[label]; !ok {
			t.Fatalf("menu lacks %q", label)
		}
	}
	// Untracked file: stageable, nothing staged to unstage, nothing to discard.
	if labels["Stage"].disabled {
		t.Fatal("Stage disabled on an untracked file")
	}
	if !labels["Unstage"].disabled {
		t.Fatal("Unstage enabled on an untracked file")
	}
	if !labels["Discard Changes…"].disabled {
		t.Fatal("Discard enabled on an untracked file")
	}

	// The modified file discards; staged files unstage.
	items = TreeContextItems(update, nil, ws, modified)
	for _, item := range items {
		switch item.Label {
		case "Stage", "Discard Changes…":
			if item.Disabled {
				t.Fatalf("%s disabled on a modified file", item.Label)
			}
		case "Unstage":
			if !item.Disabled {
				t.Fatal("Unstage enabled with nothing staged")
			}
		}
	}

	// Add to .gitignore writes the entry and re-colors the tree.
	for _, item := range items {
		if item.Label == "Add to .gitignore" {
			item.Action()
		}
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatalf("the menu action wrote no .gitignore: %v", err)
	}
	if strings.TrimSpace(string(data)) != "kept.go" {
		t.Fatalf(".gitignore = %q, want \"kept.go\"", data)
	}
	if ws.treeGit == nil || !ws.treeGit.dirty {
		t.Fatal("the gitignore action did not schedule a tree re-color")
	}
	// The same node's menu now offers no duplicate add.
	items = TreeContextItems(update, nil, ws, modified)
	for _, item := range items {
		if item.Label == "Add to .gitignore" && !item.Disabled {
			t.Fatal("Add to .gitignore stayed enabled after the entry landed")
		}
	}
}

// TestTreeStageUnstageActions pins the stage/unstage round trip through
// the menu runner: staging an untracked file turns it staged-added,
// unstaging returns it to untracked, both after a re-color poll.
func TestTreeStageUnstageActions(t *testing.T) {
	ws, repo := treeMenuFixture(t)
	node := treeFixtureNode(t, ws, "untracked.txt")
	update := func(f func(*Workspace)) { f(ws) }

	items := TreeContextItems(update, nil, ws, node)
	for _, item := range items {
		if item.Label == "Stage" {
			item.Action()
		}
	}
	refreshTreeGitInTest(ws)
	if got := ws.treeGitNodeState(node); got != gitStagedAdd {
		t.Fatalf("after stage = %v, want staged add", got)
	}

	for _, item := range TreeContextItems(update, nil, ws, node) {
		if item.Label == "Unstage" {
			if item.Disabled {
				t.Fatal("Unstage disabled right after staging")
			}
			item.Action()
		}
	}
	refreshTreeGitInTest(ws)
	if got := ws.treeGitNodeState(node); got != gitUntracked {
		t.Fatalf("after unstage = %v, want untracked", got)
	}
	_ = repo
}

// TestTreeDiscardAction restores a modified file's content through the
// menu (the worktree moves, so the editor's change bars re-base).
func TestTreeDiscardAction(t *testing.T) {
	ws, repo := treeMenuFixture(t)
	node := treeFixtureNode(t, ws, "kept.go")
	src := filepath.Join(repo, "kept.go")
	if data, _ := os.ReadFile(src); string(data) != "package main // edited\n" {
		t.Fatalf("fixture edit missing: %q", data)
	}

	for _, item := range TreeContextItems(func(f func(*Workspace)) { f(ws) }, nil, ws, node) {
		if item.Label == "Discard Changes…" {
			item.Action() // opens the confirmation dialog
		}
	}
	if ws.confirm == nil {
		t.Fatal("Discard Changes… ran without a confirmation dialog")
	}
	if err := ws.confirm.OnConfirm(ws); err != nil {
		t.Fatalf("confirm rejected: %v", err)
	}
	data, err := os.ReadFile(src)
	if err != nil || string(data) != "package main\n" {
		t.Fatalf("discard left %q err=%v, want the committed content", data, err)
	}
	refreshTreeGitInTest(ws)
	if got := ws.treeGitNodeState(node); got != gitClean {
		t.Fatalf("after discard = %v, want clean", got)
	}
}

// TestRunTreeGitActionRejectsOutsidePaths: a node resolved outside the
// repo (or the repo root itself) never runs a git path action.
func TestRunTreeGitActionRejectsOutsidePaths(t *testing.T) {
	ws, repo := treeMenuFixture(t)
	ws.runTreeGitAction(nil, nil, repo, "stage", repo, false) // the repo root
	if ws.StatusError == "" {
		t.Fatal("staging the repo root was not rejected")
	}
	ws.runTreeGitAction(nil, nil, repo, "stage", filepath.Join(repo, "..", "elsewhere"), false)
	if ws.StatusError == "" {
		t.Fatal("an outside path was not rejected")
	}
}

// TestEditorDiffLinesKinds is a tiny sanity gate tying the menu states to
// the editor's mark kinds (the scrollbar and gutter share them).
func TestEditorDiffLinesKinds(t *testing.T) {
	if editor.DiffAdded == editor.DiffModified || editor.DiffModified == editor.DiffDeleted {
		t.Fatal("diff kinds collapsed")
	}
}

// uiMenuItem mirrors ui.MenuItem for the label assertions above (kept
// local so the test does not depend on the menu row rendering).
type uiMenuItem struct {
	disabled  bool
	separator bool
}
