package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
	"github.com/FlameInTheDark/go-gpui/render"
)

type toolHarness struct {
	read      func() *Workspace
	hitboxes  func() []gpui.Hitbox
	scene     func() *render.Scene
	click     func(string)
	typeText  func(string)
	key       func(string)
	keystroke func(keymap.Keystroke)
}

func openToolHarness(t *testing.T, root, file string) toolHarness {
	t.Helper()
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp := headless.New(geometry.SizePixels(1100, 700))
	a := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{Title: "Tools"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("Tools")
		if err := ws.OpenFolder(root); err != nil {
			t.Fatal(err)
		}
		ws.OpenFile(file)
		ws.Measure(w)
		ws.tools.rail = true
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
	InstallZoomKeys(handle.Window)
	handle.Window.Draw()
	hw := hp.LastWindow()
	return toolHarness{
		read:     func() *Workspace { return handle.View.Read(a.App) },
		hitboxes: handle.Window.RenderedHitboxes,
		scene:    handle.Window.RenderedScene,
		click: func(id string) {
			t.Helper()
			for _, box := range handle.Window.RenderedHitboxes() {
				if box.Key != "n:btn-"+id {
					continue
				}
				b := box.Bounds
				if b.Size.Width <= 0 || b.Size.Height <= 0 {
					t.Fatalf("empty button %q: %+v", id, b)
				}
				p := geometry.PointPixels(b.Origin.X.AsF32()+b.Size.Width.AsF32()/2, b.Origin.Y.AsF32()+b.Size.Height.AsF32()/2)
				hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
				hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
				handle.Window.Draw()
				return
			}
			t.Fatalf("missing button %q", id)
		},
		typeText: func(text string) {
			for _, r := range text {
				hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: string(r), KeyChar: string(r)}})
				handle.Window.Draw()
			}
		},
		key: func(k string) {
			hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: k}})
			handle.Window.Draw()
		},
		keystroke: func(k keymap.Keystroke) {
			hw.DispatchInput(platform.KeyDownEvent{Keystroke: k})
			handle.Window.Draw()
		},
	}
}

func writeToolFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRunControlReplacesRunUntilStopped(t *testing.T) {
	root := t.TempDir()
	writeToolFile(t, filepath.Join(root, "go.mod"), "module example.test/stop\n\ngo 1.23\n")
	file := filepath.Join(root, "main.go")
	writeToolFile(t, file, "package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Println(\"ready\"); os.Stdin.Read(make([]byte, 1)) }\n")
	h := openToolHarness(t, root, file)
	queue := make(chan func(), 32)
	h.read().SetEnv(nil, func(f func()) { queue <- f }, nil)
	hasButton := func(id string) bool {
		for _, hit := range h.hitboxes() {
			if hit.Key == "n:btn-"+id {
				return true
			}
		}
		return false
	}
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.NewTimer(30 * time.Second)
		defer deadline.Stop()
		for !ready() {
			select {
			case f := <-queue:
				f() // UI-thread task notifications should redraw without input
			case <-deadline.C:
				t.Fatalf("waiting for task UI: %+v", h.read().GoTask.Snapshot())
			}
		}
	}
	for _, stop := range []string{"toolbar", "shortcut", "natural exit"} {
		h.click("toolbar-run")
		task := h.read().GoTask
		if task == nil {
			t.Fatalf("could not start Run: %s", h.read().StatusError)
		}
		t.Cleanup(task.Stop)
		if hasButton("toolbar-run") || !hasButton("toolbar-stop") {
			t.Fatal("primary toolbar control did not switch to Stop")
		}
		wait(func() bool { return strings.Contains(task.Snapshot().Output, "\nready\n") })
		h.click("menubar-Run")
		if popup := h.read().Popup; popup == nil || len(popup.Items) == 0 || popup.Items[0].Label != "Stop Go Task" {
			t.Fatalf("Run menu did not switch to Stop: %+v", popup)
		}
		h.click("menubar-Run")
		h.click("tool-output") // Stop must still work with the Run panel hidden.
		want := "Stopped"
		switch stop {
		case "toolbar":
			h.click("toolbar-stop")
		case "shortcut":
			h.keystroke(keymap.Keystroke{Key: "f2", Modifiers: keymap.Modifiers{Control: true}})
		case "natural exit":
			want = "Finished"
			h.click("menubar-Run")
			if err := task.SendInput("quit"); err != nil {
				t.Fatal(err)
			}
		}
		wait(func() bool { return hasButton("toolbar-run") && !hasButton("toolbar-stop") })
		if snap := task.Snapshot(); snap.Running || snap.Status != want {
			t.Fatalf("%s: want %s, got %+v", stop, want, snap)
		}
		if stop == "natural exit" {
			if popup := h.read().Popup; popup == nil || len(popup.Items) == 0 || popup.Items[0].Label != "Run Go Package" {
				t.Fatalf("open Run menu did not refresh after exit: %+v", popup)
			}
		}
	}
}

func TestToolRailStructureAndTodos(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	writeToolFile(t, file, "package main\n\nvar s = \"TODO in a string\"\n\nfunc main() {\n  // TODO: implement\n}\n")
	h := openToolHarness(t, root, file)
	t.Cleanup(func() {
		for _, term := range h.read().Terms {
			term.Close()
		}
	})
	// The two groups grow in opposite directions, and the rest of the chrome
	// no longer offers a second button for the same panel.
	order := []string{"project", "structure", "git", "todo", "debug", "problems", "terminal"}
	ys := make(map[string]float32)
	for _, hit := range h.hitboxes() {
		switch hit.Key {
		case "n:btn-toolbar-project", "n:btn-toolbar-terminal", "n:btn-toolbar-debug", "n:btn-status-terminal-toggle", "n:btn-status-problems", "n:btn-status-debug", "n:btn-status-go-output":
			t.Fatalf("duplicate panel control %q", hit.Key)
		}
		if name, ok := strings.CutPrefix(hit.Key, "n:btn-tool-"); ok {
			ys[name] = hit.Bounds.Origin.Y.AsF32()
		}
	}
	for i, name := range order {
		if y, exists := ys[name]; !exists || (i > 0 && y <= ys[order[i-1]]) {
			t.Fatalf("wrong tool rail order: %v", ys)
		}
	}
	if ys["todo"]-ys["git"] < 100 || ys["terminal"] < 600 {
		t.Fatalf("rail groups are not anchored at opposite ends: %v", ys)
	}
	h.click("tool-terminal")
	if !h.read().ShowTerminal {
		t.Fatal("rail terminal button did not open the dock")
	}
	h.click("tool-terminal")
	if h.read().ShowTerminal {
		t.Fatal("rail terminal button did not close the dock")
	}
	h.click("tool-structure")
	if h.read().ShowTree || h.read().tools.left != "structure" {
		t.Fatal("Structure did not replace Project")
	}
	h.click("structure-item-1")
	if line, _ := h.read().Buf().RuneLineCol(); line != 5 {
		t.Fatalf("Structure did not navigate, line = %d", line)
	}
	h.click("tool-todo")
	if r := h.read().tools.todos.result; len(r.entries) != 1 || r.entries[0].where.line != 6 {
		t.Fatalf("TODO results: %+v", r)
	}
	h.click("todo-item-0")
	if line, _ := h.read().Buf().RuneLineCol(); line != 6 {
		t.Fatalf("TODO did not navigate, line = %d", line)
	}
	h.click("tool-debug")
	if !h.read().ShowDebug || h.read().ShowTodos {
		t.Fatal("Debug did not replace TODOs")
	}
	h.click("tool-problems")
	if !h.read().ShowProblems || h.read().ShowDebug {
		t.Fatal("Problems did not replace Debug")
	}
	h.click("tool-project")
	if !h.read().ShowTree || h.read().tools.left != "" {
		t.Fatal("Project did not replace Structure")
	}
}

func TestRunConfigurationFromToolbar(t *testing.T) {
	root := t.TempDir()
	writeToolFile(t, filepath.Join(root, "go.mod"), "module example.test/run\n\ngo 1.23\n")
	readme := filepath.Join(root, "README.md")
	writeToolFile(t, readme, "# project\n")
	bin := filepath.Join(root, "cmd")
	if err := os.Mkdir(bin, 0755); err != nil {
		t.Fatal(err)
	}
	writeToolFile(t, filepath.Join(bin, "main.go"), "package main\nimport \"os\"\nfunc main(){println(os.Args[1])}\n")
	h := openToolHarness(t, root, readme)
	h.click("toolbar-run-config")
	if h.read().launchEditor == nil {
		t.Fatal("run configuration dialog did not open")
	}
	h.typeText("Worker")
	h.click("launch-field-1")
	h.key("backspace")
	h.typeText("cmd")
	h.click("launch-field-2")
	h.typeText(`["two words"]`)
	h.click("launch-save")
	if h.read().launchEditor != nil {
		t.Fatalf("could not save: %+v", h.read().launchEditor)
	}
	ws := New("reopened")
	if err := ws.OpenFolder(root); err != nil {
		t.Fatal(err)
	}
	if dir, name, args, err := ws.launchTarget(goDebug); err != nil || dir != bin || name != "Worker" || !slices.Equal(args, []string{"two words"}) {
		t.Fatalf("reopened profile: %q %q %v %v", dir, name, args, err)
	}
	h.click("toolbar-run")
	task := h.read().GoTask
	if task == nil {
		t.Fatalf("profile did not run: %s", h.read().StatusError)
	}
	defer task.Stop()
	select {
	case <-task.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("configured run timed out")
	}
	if snap := task.Snapshot(); snap.Status != "Finished" || !strings.Contains(snap.Output, "two words") {
		t.Fatalf("configured run: %+v", snap)
	}
	h.click("tool-output")
	if h.read().ShowGoOutput {
		t.Fatal("Go Output button did not hide the dock")
	}
	h.click("tool-output")
	if !h.read().ShowGoOutput {
		t.Fatal("Go Output button did not reopen the dock")
	}
	h.click("toolbar-run-config")
	h.click("launch-default")
	if h.read().launches.Active != "" || len(h.read().launches.Profiles) != 1 {
		t.Fatal("switching to active editor did not preserve saved profiles")
	}
}

func TestGitToolStagesDiffsAndCommits(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git not installed")
	}
	root := t.TempDir()
	gitAt := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	gitLocal := func(args ...string) string { return gitAt(root, args...) }
	remote := t.TempDir()
	gitAt(remote, "init", "--bare", "-q")
	gitLocal("init", "-q", "-b", "editor-test")
	gitLocal("remote", "add", "origin", remote)
	gitLocal("config", "user.name", "Editor Test")
	gitLocal("config", "user.email", "editor@example.test")
	gitLocal("config", "commit.gpgsign", "false")
	label := "[main].go"
	file := filepath.Join(root, label)
	writeToolFile(t, file, "package main\n// original\n")
	for _, action := range []string{"stage", "unstage", "stage"} {
		if err := runGitChange(context.Background(), root, action, label); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("%s removed worktree file: %v", action, err)
		}
	}
	gitLocal("commit", "-qm", "Initial")
	writeToolFile(t, file, "package main\n// edited\n")
	writeToolFile(t, filepath.Join(root, "other.go"), "package main\n")
	h := openToolHarness(t, root, file)
	h.click("tool-git")
	var messageBottom, commitTop geometry.Pixels
	for _, hit := range h.hitboxes() {
		switch hit.Key {
		case "n:btn-git-commit-message":
			messageBottom = hit.Bounds.Bottom()
		case "n:btn-git-commit":
			commitTop = hit.Bounds.Origin.Y
		}
	}
	if messageBottom == 0 || commitTop < messageBottom {
		t.Fatalf("Commit button was not visible below the message box: bottom=%v commit=%v", messageBottom, commitTop)
	}
	h.click("git-commit")
	if got := gitLocal("log", "-1", "--format=%s"); got != "Initial" {
		t.Fatalf("disabled commit changed HEAD: %q", got)
	}
	id := func(stage bool) string {
		t.Helper()
		r := h.read().tools.git.result
		for i, e := range r.entries {
			if e.label == label && e.staged == stage {
				return fmt.Sprint(i)
			}
		}
		t.Fatalf("missing staged=%v %s in %+v", stage, label, r)
		return ""
	}
	h.click("git-diff-" + id(false))
	ws := h.read()
	if ws.Buf().Kind != editor.KindDiff {
		t.Fatal("click did not open a diff tab")
	}
	diff := ws.diffs[ws.Buf()].result
	if len(diff.rows) != 2 || !diff.rows[1].changed || diff.rows[1].left.text != "// original" || diff.rows[1].right.text != "// edited" {
		t.Fatalf("working tree diff: %+v", diff)
	}
	assertDiffPainted(t, h)
	h.click("git-stage-" + id(false))
	h.click("git-diff-" + id(true))
	ws = h.read()
	if d := ws.diffs[ws.Buf()]; !d.entry.staged || len(d.result.rows) != 2 || d.result.rows[1].right.text != "// edited" {
		t.Fatalf("staged diff: %+v", d)
	}
	assertDiffPainted(t, h)
	h.click("git-unstage-" + id(true))
	h.click("git-stage-" + id(false))
	h.click("git-commit-message")
	h.typeText("Commit from editor")
	h.key("enter")
	h.key("enter")
	h.typeText("A second paragraph.")
	h.click("git-commit")
	if latest := gitLocal("log", "-1", "--format=%s"); latest != "Commit from editor" {
		t.Fatalf("latest commit = %q (%s)", latest, h.read().tools.gitMessage)
	}
	if got := gitLocal("log", "-1", "--format=%B"); got != "Commit from editor\n\nA second paragraph." {
		t.Fatalf("multiline commit message = %q", got)
	}
	if files := gitLocal("diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); files != label {
		t.Fatalf("committed files = %q", files)
	}
	if status := gitLocal("status", "--porcelain"); status != "?? other.go" {
		t.Fatalf("unstaged file was modified: %q", status)
	}
	firstCommit := gitLocal("rev-parse", "HEAD")
	h.click("git-push")
	if got := gitAt(remote, "rev-parse", "refs/heads/editor-test"); got != firstCommit {
		t.Fatalf("remote commit = %q, want %q", got, firstCommit)
	}
	if upstream := gitLocal("rev-parse", "--abbrev-ref", "@{upstream}"); upstream != "origin/editor-test" {
		t.Fatalf("first push did not set the upstream: %q", upstream)
	}
	h.click("git-history")
	if commits := h.read().tools.git.result.history; len(commits) < 2 || commits[0].subject != "Commit from editor" {
		t.Fatalf("History did not show recent commits: %+v", commits)
	}
	h.click("git-changes")
	h.click("git-fetch")
	if msg := h.read().tools.gitMessage; msg != "Fetch complete" {
		t.Fatalf("fetch status: %q", msg)
	}

	// Combined action commits only the index and uses the configured
	// upstream even when its branch name differs from the local name.
	gitLocal("config", "branch.editor-test.merge", "refs/heads/review")
	gitLocal("config", "push.default", "matching")
	writeToolFile(t, file, "package main\n// reviewed\n")
	h.click("git-refresh")
	h.click("git-stage-" + id(false))
	h.click("git-commit-message")
	h.typeText("Commit and push from editor")
	h.click("git-commit-push")
	if msg := h.read().tools.gitMessage; msg != "Commit created and branch pushed" {
		t.Fatalf("commit and push status: %q", msg)
	}
	if got := gitAt(remote, "log", "-1", "--format=%s", "refs/heads/review"); got != "Commit and push from editor" {
		t.Fatalf("upstream commit message = %q", got)
	}
	if got := gitAt(remote, "rev-parse", "refs/heads/editor-test"); got != firstCommit {
		t.Fatalf("push also updated another branch: %q", got)
	}

	diverged := gitAt(remote, "-c", "user.name=Other Writer", "-c", "user.email=other@example.test", "-c", "commit.gpgsign=false",
		"commit-tree", "refs/heads/review^{tree}", "-m", "Independent remote commit")
	gitAt(remote, "update-ref", "refs/heads/review", diverged)
	writeToolFile(t, file, "package main\n// local only\n")
	h.click("git-refresh")
	h.click("git-stage-" + id(false))
	h.click("git-commit-message")
	h.typeText("Keep the local commit")
	h.click("git-commit-push")
	if msg := h.read().tools.gitMessage; !strings.Contains(msg, "commit created, but push failed") {
		t.Fatalf("did not report partial success: %q", msg)
	}
	if got := gitLocal("log", "-1", "--format=%s"); got != "Keep the local commit" {
		t.Fatalf("push failure lost the local commit: %q", got)
	}
	if got := gitAt(remote, "rev-parse", "refs/heads/review"); got != diverged {
		t.Fatalf("rejected push overwrote the remote: %q", got)
	}

	writeToolFile(t, file, "package main\n"+strings.Repeat("// long diff row\n", 250))
	h.click("git-refresh")
	h.click("git-diff-" + id(false))
	d := h.read().diffs[h.read().Buf()]
	h.key("end")
	if d.top < 100 {
		t.Fatalf("End did not scroll the long diff: top = %.1f", d.top)
	}
	h.key("home")
	if d.top != 0 {
		t.Fatalf("Home did not return to the start: top = %.1f", d.top)
	}
	h.click("git-stage-all")
	if got := gitLocal("diff", "--cached", "--name-only"); !strings.Contains(got, label) || !strings.Contains(got, "other.go") {
		t.Fatalf("Stage All missed changes: %q", got)
	}
}

func assertDiffPainted(t *testing.T, h toolHarness) {
	t.Helper()
	theme := h.read().Theme()
	var old, new bool
	for _, q := range h.scene().Quads {
		visible := render.IntersectScaled(q.Bounds, q.ContentMask.Bounds)
		if visible.Size.Width <= 0 || visible.Size.Height <= 0 {
			continue
		}
		old = old || q.Background.Solid == theme.Red.Alpha(.16)
		new = new || q.Background.Solid == theme.Green.Alpha(.16)
	}
	if !old || !new {
		t.Fatalf("diff data loaded but changed lines were not painted: old=%v new=%v", old, new)
	}
}
