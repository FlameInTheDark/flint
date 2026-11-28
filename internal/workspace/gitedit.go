package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/gpui"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
)

// ---------------------------------------------------------------------------
// Git working-tree decorations.
//
// Two features share this plumbing: the project tree colors files and
// folders by their git status (modified / staged / new / ignored / deleted
// / conflict), and the editor paints per-line change bars diffed against
// HEAD (or, on a repository with no commits yet, against the empty tree).
// Both run git OFF the UI thread through ws.dispatch — the same discipline
// as the git panel — and never block a paint or a keystroke on a
// subprocess. Without a dispatcher (headless scenes, tests) everything
// runs inline so behavior stays deterministic.
// ---------------------------------------------------------------------------

// gitFileState is one file's working-tree/index state, folded to the worst
// of its staged and unstaged halves (the project tree's coloring order:
// conflict > deleted > modified > staged > untracked > ignored).
type gitFileState uint8

const (
	gitClean gitFileState = iota
	gitIgnored
	gitUntracked
	gitRenamed
	gitStagedAdd
	gitStagedMod
	gitModified
	gitDeleted
	gitConflict
)

// rank orders states by how much attention they deserve (a directory
// inherits its worst descendant's state).
func (s gitFileState) rank() int {
	switch s {
	case gitConflict:
		return 7
	case gitDeleted:
		return 6
	case gitModified:
		return 5
	case gitStagedAdd, gitStagedMod, gitRenamed:
		return 4
	case gitUntracked:
		return 3
	case gitIgnored:
		return 1
	}
	return 0
}

// worse returns whichever state ranks higher.
func (s gitFileState) worse(o gitFileState) gitFileState {
	if o.rank() > s.rank() {
		return o
	}
	return s
}

// badge is the project tree's one-letter marker (the explorer convention:
// M/U/A/D/R/C). Ignored files only dim, like every editor's explorer.
func (s gitFileState) badge() string {
	switch s {
	case gitConflict:
		return "C"
	case gitDeleted:
		return "D"
	case gitModified, gitStagedMod:
		return "M"
	case gitStagedAdd:
		return "A"
	case gitRenamed:
		return "R"
	case gitUntracked:
		return "U"
	}
	return ""
}

// label is the status word for tooltips and tests.
func (s gitFileState) label() string {
	switch s {
	case gitConflict:
		return "Merge conflict"
	case gitDeleted:
		return "Deleted"
	case gitModified:
		return "Modified"
	case gitStagedAdd:
		return "Added (staged)"
	case gitStagedMod:
		return "Modified (staged)"
	case gitRenamed:
		return "Renamed"
	case gitUntracked:
		return "New (untracked)"
	case gitIgnored:
		return "Git-ignored"
	}
	return "Unmodified"
}

// gitStateColor maps a file state to the project tree's text color (the
// convention: red conflicts/deletions, amber modified, green staged/new,
// dimmed ignored).
func gitStateColor(t ui.Theme, s gitFileState) gc.Hsla {
	switch s {
	case gitConflict, gitDeleted:
		return t.Red
	case gitModified, gitRenamed:
		return t.Yellow
	case gitStagedAdd, gitStagedMod, gitUntracked:
		return t.Green
	case gitIgnored:
		return t.Overlay
	}
	return t.Text
}

// gitStateFromCode folds a porcelain v1 "XY" code into one file state
// (ignoring the unstaged half of a rename pair's cousin entries).
func gitStateFromCode(code string) gitFileState {
	if len(code) != 2 {
		return gitClean
	}
	x, y := code[0], code[1]
	if x == '!' || y == '!' {
		return gitIgnored
	}
	if x == '?' || y == '?' {
		return gitUntracked
	}
	switch code {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return gitConflict
	}
	worst := gitClean
	consider := func(c byte, staged bool) {
		var s gitFileState
		switch c {
		case 'A':
			s = gitStagedAdd
		case 'M', 'T':
			if staged {
				s = gitStagedMod
			} else {
				s = gitModified
			}
		case 'D':
			s = gitDeleted
		case 'R', 'C':
			s = gitRenamed
		default:
			return // ' ', '!' or unknown: nothing to fold
		}
		worst = worst.worse(s)
	}
	consider(x, true)
	consider(y, false)
	return worst
}

// treeGitState caches the project tree's git coloring for the open folder.
type treeGitState struct {
	repo    string // repository top ("" = no repository / not detected yet)
	tried   bool   // a refresh already ran for the current Root
	dirty   bool   // a save or Git action asked for a refresh
	running bool
	queued  bool
	gen     uint64
	// files/dirs map ABSOLUTE paths to states (files) and each directory
	// to its worst descendant's state (dirs), so the renderer's lookup is
	// one map read per row.
	files map[string]gitFileState
	dirs  map[string]gitFileState
}

// treeGitSnapshot is the async refresh's parsed result.
type treeGitSnapshot struct {
	repo  string
	files map[string]gitFileState
	dirs  map[string]gitFileState
}

// markTreeGitDirty asks for a tree coloring refresh after the next render
// (saves, Git actions, manual refreshes).
func (ws *Workspace) markTreeGitDirty() {
	if ws.treeGit == nil {
		ws.treeGit = &treeGitState{}
	}
	ws.treeGit.dirty = true
}

// pollTreeGit queues one git status refresh when the tree's coloring is
// stale (first folder open, a save, a finished Git action, the tree's
// refresh button). Work starts on the next frame — the same timing the
// git panel's refresh uses, because render-time state must not spawn work.
func (ws *Workspace) pollTreeGit(update updateFn, w *gpui.Window) {
	if ws.Root == "" || ws.Tree == nil {
		return
	}
	if ws.treeGit == nil {
		ws.treeGit = &treeGitState{}
	}
	st := ws.treeGit
	if st.running || st.queued {
		return
	}
	if st.tried && !st.dirty {
		return
	}
	st.queued = true
	w.OnNextFrame(func(win *gpui.Window) {
		update(func(s *Workspace) {
			if s.treeGit != nil {
				s.treeGit.queued = false
			}
			s.startTreeGit(update, win)
		})
		win.Refresh()
	})
}

// startTreeGit runs git status for the open folder off the UI thread and
// applies the parsed coloring. Concurrent requests collapse; a superseded
// generation's result is dropped.
func (ws *Workspace) startTreeGit(update updateFn, w *gpui.Window) {
	st := ws.treeGit
	if st == nil || st.running || ws.Root == "" {
		return
	}
	root := ws.Root
	st.running = true
	st.dirty = false
	st.gen++
	gen := st.gen
	if dispatch := ws.dispatch; dispatch != nil {
		go func() {
			result := collectTreeGit(root)
			defer func() { _ = recover() }()
			dispatch(func() {
				defer func() { _ = recover() }()
				update(func(s *Workspace) { s.applyTreeGit(gen, root, result) })
				w.Refresh()
			})
		}()
		return
	}
	ws.applyTreeGit(gen, root, collectTreeGit(root)) // headless/tests: inline
}

// applyTreeGit stores a refresh's snapshot unless a newer generation
// superseded it or the project changed mid-flight. A dirty flag set while
// this refresh ran survives: the next render re-queues.
func (ws *Workspace) applyTreeGit(gen uint64, root string, snap *treeGitSnapshot) {
	st := ws.treeGit
	if st == nil || st.gen != gen || root != ws.Root {
		return
	}
	st.running = false
	st.tried = true
	st.repo = snap.repo
	st.files = snap.files
	st.dirs = snap.dirs
}

// TreeGitView is the per-render snapshot of the tree's git coloring:
// absolute paths to states (files) and to each directory's worst
// descendant state (dirs), plus the repository top ("" = no repository).
// The renderer reads it without locking: it is built off-thread, stored
// through update() on the UI thread, and read during paint.
type TreeGitView struct {
	Repo  string
	Files map[string]gitFileState
	Dirs  map[string]gitFileState
}

// gitRowState looks a row's git status up (files for files, the worst
// descendant's for directories; clean outside a repository).
func gitRowState(git TreeGitView, node *TreeNode) gitFileState {
	if git.Repo == "" {
		return gitClean
	}
	if node.IsDir {
		return git.Dirs[node.Path]
	}
	return git.Files[node.Path]
}

// collectTreeGit parses `git status` for one folder: not a repository
// (or git unusable) yields an empty snapshot with no repo, which renders
// as clean coloring.
func collectTreeGit(root string) *treeGitSnapshot {
	snap := &treeGitSnapshot{}
	git, err := exec.LookPath("git")
	if err != nil {
		return snap
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	top, err := gitCommand(ctx, git, root, true, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return snap
	}
	repo := filepath.Clean(string(bytes.TrimRight(top, "\r\n")))
	cmd := gitCommand(ctx, git, repo, true,
		"status", "--porcelain=v1", "-z", "--no-renames",
		"--untracked-files=all", "--ignored=matching")
	out := &boundedGitOutput{limit: 512 << 10}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return snap
	}
	data := out.bytes
	if out.truncated {
		// Cut at the last complete record instead of rendering garbage.
		if last := bytes.LastIndexByte(data, 0); last >= 0 {
			data = data[:last+1]
		} else {
			return snap
		}
	}
	snap.repo = repo
	snap.files = map[string]gitFileState{}
	snap.dirs = map[string]gitFileState{}
	for _, record := range strings.Split(string(data), "\x00") {
		if len(record) < 4 || record[2] != ' ' || strings.HasPrefix(record, "## ") {
			continue
		}
		rel := strings.TrimSuffix(record[3:], "/") // whole ignored directories end in '/'
		if !filepath.IsLocal(rel) {
			continue
		}
		abs := filepath.Join(repo, filepath.FromSlash(rel))
		st := gitStateFromCode(record[:2])
		if st == gitClean {
			continue
		}
		snap.files[abs] = st
		colorDirs(snap.dirs, repo, filepath.Dir(abs), st)
	}
	return snap
}

// colorDirs folds a file's state into every ancestor directory up to the
// repository top (a folder shows its worst descendant's color).
func colorDirs(dirs map[string]gitFileState, repo, dir string, st gitFileState) {
	sep := string(os.PathSeparator)
	for dir == repo || strings.HasPrefix(dir, repo+sep) {
		cur, ok := dirs[dir]
		if !ok || st.rank() > cur.rank() {
			dirs[dir] = st
		}
		if dir == repo {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// ---------------------------------------------------------------------------
// Project tree VCS context menu: add to .gitignore, stage / unstage /
// discard a node's changes. The write stays OFF the UI thread (the same
// discipline as the git panel and the diff marks); .gitignore edits are a
// one-file append done through the menu action, like the tree's other
// rename/create filesystem operations.
// ---------------------------------------------------------------------------

// repoForNode returns the repository governing a tree node ("" = none):
// the tree's detected repo, only when the node lives under it.
func (ws *Workspace) repoForNode(node *TreeNode) string {
	if node == nil || node.Path == "" || ws.treeGit == nil || ws.treeGit.repo == "" {
		return ""
	}
	if !pathUnderRepo(node.Path, ws.treeGit.repo) {
		return ""
	}
	return ws.treeGit.repo
}

// gitignorePathFor is the .gitignore entry for a node: repo-relative
// forward slashes; directories end in '/' (the convention for ignoring a
// whole folder, and what the reference editors write).
func gitignorePathFor(repo, path string, isDir bool) string {
	rel := filepath.ToSlash(relPath(repo, path))
	if isDir && !strings.HasSuffix(rel, "/") {
		rel += "/"
	}
	return rel
}

// gitignoreHas reports whether the repo's .gitignore already lists entry
// (trimmed lines, exact match: the same text the add action writes, so a
// re-run can never duplicate an entry).
func gitignoreHas(repo, entry string) bool {
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return true
		}
	}
	return false
}

// addToGitignore appends entry to the repo's .gitignore, creating the
// file when missing. It reports whether the file was newly created (the
// project tree re-lists to show it).
func addToGitignore(repo, entry string) (bool, error) {
	p := filepath.Join(repo, ".gitignore")
	data, err := os.ReadFile(p)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		if err := os.WriteFile(p, []byte(entry+"\n"), 0o644); err != nil {
			return false, err
		}
		return true, nil
	}
	text := string(data)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += entry + "\n"
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		return false, err
	}
	return false, nil
}

// addGitignoreEntry performs the context menu's "Add to .gitignore": the
// append runs on the UI thread like the tree's other fs actions, then the
// tree re-colors (the entry turns dim) and re-lists when .gitignore is
// new (it becomes a visible root file).
func (ws *Workspace) addGitignoreEntry(repo, entry string) {
	created, err := addToGitignore(repo, entry)
	if err != nil {
		ws.StatusError = "Add to .gitignore: " + err.Error()
		return
	}
	ws.StatusError = ""
	ws.markTreeGitDirty()
	if created && ws.Tree != nil {
		ws.Tree.Reload()
		ws.treeTextW = nil
	}
}

// runTreeGitAction runs one mutating git operation (stage / unstage /
// discard) on a tree node's repo-relative path OFF the UI thread — never
// inside a paint or a keystroke — and lands the result through dispatch.
func (ws *Workspace) runTreeGitAction(update updateFn, w *gpui.Window, repo, action, path string, invalidate bool) {
	rel := filepath.ToSlash(relPath(repo, path))
	if rel == "" || rel == "." || !filepath.IsLocal(rel) {
		ws.StatusError = "Invalid Git path: " + filepath.Base(path)
		return
	}
	work := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return runGitChange(ctx, repo, action, rel)
	}
	if dispatch := ws.dispatch; dispatch != nil {
		go func() {
			err := work()
			defer func() { _ = recover() }()
			dispatch(func() {
				defer func() { _ = recover() }()
				update(func(s *Workspace) { s.applyTreeGitAction(err, invalidate) })
				if w != nil {
					w.Refresh()
				}
			})
		}()
		return
	}
	ws.applyTreeGitAction(work(), invalidate) // headless/tests: inline
}

// applyTreeGitAction lands one tree git action: errors surface in the
// status bar, success re-colors the tree — and a discard moved the
// worktree, so the editor's change bars re-base against HEAD.
func (ws *Workspace) applyTreeGitAction(err error, invalidate bool) {
	if err != nil {
		ws.StatusError = err.Error()
		return
	}
	ws.StatusError = ""
	ws.markTreeGitDirty()
	if invalidate {
		ws.invalidateEditorDiffs()
	}
}

// treeGitNodeState looks a tree node's folded git state up for the
// context menu's enablement (clean outside a repository or before the
// first status refresh lands).
func (ws *Workspace) treeGitNodeState(node *TreeNode) gitFileState {
	if ws.treeGit == nil || ws.treeGit.files == nil {
		return gitClean
	}
	return ws.treeGit.files[node.Path]
}

// ---------------------------------------------------------------------------
// Editor change bars: per-line working-tree marks vs HEAD.
// ---------------------------------------------------------------------------

// diffRevNone is the "never computed" revision sentinel (buffer revisions
// start at 0).
const diffRevNone = ^uint64(0)

// editorDiffDebounce batches a typing burst into one git subprocess per
// pause (editors update change bars live without re-running git per key).
const editorDiffDebounce = 250 * time.Millisecond

// fileDiffMarks holds one buffer's per-line git marks. Results arrive
// asynchronously; a revision older than the buffer's current one re-queues
// through pollEditorDiffs.
type fileDiffMarks struct {
	marks   map[int]editor.LineDiffKind
	segs    []editor.ScrollSegment
	rev     uint64
	gen     uint64
	running bool
	timer   *time.Timer
}

// diffKey canonicalizes a buffer path for the marks map (case-folded on
// Windows, like sameFilePath).
func diffKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs
}

// openBufferByPath finds the open buffer for a canonical path.
func (ws *Workspace) openBufferByPath(path string) *editor.Buffer {
	for _, b := range ws.Buffers {
		if b.Path != "" && diffKey(b.Path) == path {
			return b
		}
	}
	return nil
}

// editorDiffLines returns the buffer's marks for the gutter (read-only:
// pollEditorDiffs does the queueing). Marks may lag the newest revision by
// one debounce while edits settle — the change bars catch up on the next
// frame, exactly like every editor's live diff gutter.
func (ws *Workspace) editorDiffLines(b *editor.Buffer) map[int]editor.LineDiffKind {
	if b == nil || b.Path == "" {
		return nil
	}
	if st := ws.editorDiffs[diffKey(b.Path)]; st != nil {
		return st.marks
	}
	return nil
}

// editorDiffSegments returns the same marks pre-folded into scrollbar
// spans (see editor.DiffScrollSegments). Folded ONCE per marks update —
// the rail paints every frame, and re-folding per frame re-sorted every
// marked line on each paint, which made scrolling a heavily-changed large
// file hitch.
func (ws *Workspace) editorDiffSegments(b *editor.Buffer) []editor.ScrollSegment {
	if b == nil || b.Path == "" {
		return nil
	}
	if st := ws.editorDiffs[diffKey(b.Path)]; st != nil {
		return st.segs
	}
	return nil
}

// pollEditorDiffs queues stale buffers' mark recomputes (once per frame).
func (ws *Workspace) pollEditorDiffs(update updateFn, w *gpui.Window) {
	for _, b := range ws.Buffers {
		if b.Path == "" || b.Kind != editor.KindText {
			continue
		}
		ws.queueEditorDiff(b, update, w)
	}
}

// queueEditorDiff schedules one buffer's recompute: inline when there is
// no dispatcher (headless/tests), otherwise debounced so a typing burst
// costs one git subprocess per pause, never per keystroke.
func (ws *Workspace) queueEditorDiff(b *editor.Buffer, update updateFn, w *gpui.Window) {
	if b.Path == "" || b.Kind != editor.KindText {
		return
	}
	key := diffKey(b.Path)
	if ws.editorDiffs == nil {
		ws.editorDiffs = map[string]*fileDiffMarks{}
	}
	st := ws.editorDiffs[key]
	if st == nil {
		st = &fileDiffMarks{rev: diffRevNone}
		ws.editorDiffs[key] = st
	}
	if st.running || st.rev == b.Revision {
		return
	}
	if ws.dispatch == nil {
		// Headless/tests: no dispatcher. Apply inline and pass no update —
		// calling update during a render would re-enter the view.
		ws.computeEditorDiff(b, key, st, nil, nil)
		return
	}
	if st.timer != nil {
		return // a debounced compute is already pending
	}
	st.timer = time.AfterFunc(editorDiffDebounce, func() {
		defer func() { _ = recover() }()
		ws.dispatch(func() {
			defer func() { _ = recover() }()
			if cur := ws.editorDiffs[key]; cur != st {
				return // the buffer closed or its state was replaced
			}
			st.timer = nil
			if buf := ws.openBufferByPath(key); buf != nil {
				ws.computeEditorDiff(buf, key, st, update, w)
			}
		})
	})
}

// computeEditorDiff snapshots the buffer on the UI thread, then diffs it
// against HEAD (or the empty tree on an unborn branch) in git subprocesses
// and stores the parsed marks. An untracked file (from the tree status, or
// from a status query when that is unknown) renders all-added without a
// diff — the convention for a brand-new file.
func (ws *Workspace) computeEditorDiff(b *editor.Buffer, key string, st *fileDiffMarks, update updateFn, w *gpui.Window) {
	if st.running {
		return
	}
	st.gen++
	gen := st.gen
	st.running = true
	rev := b.Revision
	path := b.Path
	text := b.Text()
	repo := ""
	if s := ws.treeGit; s != nil && s.repo != "" && pathUnderRepo(path, s.repo) {
		repo = s.repo
	}
	untracked := false
	if s := ws.treeGit; s != nil && s.files != nil && s.files[diffKey(path)] == gitUntracked {
		untracked = true
	}
	finish := func(marks map[int]editor.LineDiffKind) {
		if update != nil && w != nil {
			update(func(s *Workspace) { s.applyEditorDiff(key, st, gen, rev, marks) })
			w.Refresh()
		} else {
			ws.applyEditorDiff(key, st, gen, rev, marks)
		}
	}
	if untracked {
		finish(allLinesAdded(text))
		return
	}
	work := func() map[int]editor.LineDiffKind {
		if repo == "" {
			repo = detectGitRepo(filepath.Dir(path))
			if repo == "" || !pathUnderRepo(path, repo) {
				return nil
			}
		}
		return diffEditorBuffer(repo, path, text)
	}
	if dispatch := ws.dispatch; dispatch != nil {
		go func() {
			marks := work()
			defer func() { _ = recover() }()
			dispatch(func() {
				defer func() { _ = recover() }()
				finish(marks)
			})
		}()
		return
	}
	finish(work()) // headless/tests: inline
}

// applyEditorDiff stores a compute's marks unless a newer generation
// superseded it (an applied result always re-renders, and the next poll
// re-queues if the buffer moved on while the diff ran).
func (ws *Workspace) applyEditorDiff(key string, st *fileDiffMarks, gen, rev uint64, marks map[int]editor.LineDiffKind) {
	if cur := ws.editorDiffs[key]; cur != st || cur.gen != gen {
		return
	}
	st.running = false
	st.marks = marks
	st.segs = editor.DiffScrollSegments(marks)
	st.rev = rev
}

// invalidateEditorDiffs forces every open buffer's change bars to recompute
// (HEAD moved: a commit re-bases the diff the bars show).
func (ws *Workspace) invalidateEditorDiffs() {
	for _, st := range ws.editorDiffs {
		st.rev = diffRevNone
	}
}

// forgetEditorDiff drops one buffer's marks and cancels its pending
// debounce (the tab closed).
func (ws *Workspace) forgetEditorDiff(path string) {
	key := diffKey(path)
	if st := ws.editorDiffs[key]; st != nil && st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	delete(ws.editorDiffs, key)
}

// pathUnderRepo reports whether path lives inside repo (filepath.Rel
// returns a ".." prefix outside it).
func pathUnderRepo(path, repo string) bool {
	rel, err := filepath.Rel(repo, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// detectGitRepo resolves the repository containing dir ("" = none). Runs
// only off the UI thread (or in headless inline mode).
func detectGitRepo(dir string) string {
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := gitCommand(ctx, git, dir, true, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return filepath.Clean(string(bytes.TrimRight(out, "\r\n")))
}

// diffEditorBuffer produces one buffer's per-line marks: the in-memory
// text diffed against the HEAD revision (a missing HEAD entry — untracked
// file, unborn branch — counts as empty, so everything shows as added,
// exactly like an editor's live gutter for new work). The path must live
// inside repo; text is the buffer's current content, so marks track
// typing before the file is saved.
func diffEditorBuffer(repo, path, text string) map[int]editor.LineDiffKind {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rel, err := filepath.Rel(repo, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil
	}
	rel = filepath.ToSlash(rel)

	// The compared side is HEAD's blob; an unborn branch or a path HEAD
	// does not know (untracked, staged-new) has no blob and diffs against
	// nothing, which renders the whole buffer as additions.
	headOut, headErr := gitCommand(ctx, git, repo, true, "cat-file", "blob", "HEAD:"+rel).Output()
	headText := ""
	if headErr == nil {
		headText = string(headOut)
	}

	// git diff --no-index takes the two sides as files: park the HEAD
	// snapshot and the buffer snapshot in temp files (the buffer's marks
	// must reflect memory, not the possibly-stale disk file).
	headFile, err := os.CreateTemp("", "flint-gitside-*")
	if err != nil {
		return nil
	}
	mineFile, err := os.CreateTemp("", "flint-myside-*")
	if err != nil {
		os.Remove(headFile.Name())
		return nil
	}
	defer os.Remove(headFile.Name())
	defer os.Remove(mineFile.Name())
	if _, err := headFile.WriteString(headText); err != nil {
		return nil
	}
	headFile.Close()
	if _, err := mineFile.WriteString(text); err != nil {
		return nil
	}
	mineFile.Close()

	cmd := gitCommand(ctx, git, repo, true,
		"diff", "--no-index", "-U0", "--no-color", "--no-ext-diff", "--no-textconv",
		"--", headFile.Name(), mineFile.Name())
	out := &boundedGitOutput{limit: 2 << 20}
	cmd.Stdout = out
	err = cmd.Run()
	if err != nil {
		// Exit code 1 just means "the files differ" — the diff is the
		// output we came for. Anything else (or truncation) gives up:
		// a cut patch would render half-hunks as phantom bars.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || out.truncated {
			return nil
		}
	} else if out.truncated {
		return nil
	}
	return parseGitDiffMarks(out.bytes)
}

// allLinesAdded marks every non-empty line of a new file as added.
func allLinesAdded(text string) map[int]editor.LineDiffKind {
	if text == "" {
		return nil
	}
	n := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	marks := make(map[int]editor.LineDiffKind, n)
	for i := 1; i <= n; i++ {
		marks[i] = editor.DiffAdded
	}
	return marks
}

// hunkHeader matches `@@ -a[,b] +c[,d] @@`.
var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// parseGitDiffMarks converts a `git diff -U0` patch into per-line marks
// (1-based new-file line numbers). Within each hunk the deleted-then-added
// line runs pair up as changed lines (the change-bar convention): the
// shorter run decides how many lines count as modified, surplus added
// lines are additions, and surplus deleted lines collapse into one removal
// marker on the line where the removal lands.
func parseGitDiffMarks(data []byte) map[int]editor.LineDiffKind {
	marks := map[int]editor.LineDiffKind{}
	newLine := 0
	inHunk := false
	var minus, plus int
	flush := func() {
		paired := min(minus, plus)
		for i := 0; i < plus; i++ {
			if i < paired {
				marks[newLine+i] = editor.DiffModified
			} else {
				marks[newLine+i] = editor.DiffAdded
			}
		}
		if minus > plus {
			at := newLine + plus
			if at < 1 {
				at = 1 // a removal at the file's very start lands on line 1
			}
			marks[at] = editor.DiffDeleted
		}
		newLine += plus
		minus, plus = 0, 0
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "@@") {
			flush()
			if m := hunkHeader.FindStringSubmatch(line); m != nil {
				c, _ := strconv.Atoi(m[1])
				newLine = c
				inHunk = true
			} else {
				newLine = 0 // unrecognizable header: skip its body
				inHunk = false
			}
			continue
		}
		if !inHunk {
			continue // file headers and metadata between hunks
		}
		switch {
		case strings.HasPrefix(line, "+"):
			plus++
		case strings.HasPrefix(line, "-"):
			if plus > 0 {
				flush() // a new minus run starts: the previous pair is done
			}
			minus++
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file": a note, not content.
		default:
			flush() // context (never with -U0) or a stray line ends the runs
			newLine++
		}
	}
	flush()
	return marks
}
