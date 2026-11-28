package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/FlameInTheDark/go-gpui/gpui"
)

func gitCommand(ctx context.Context, git, root string, readOnly bool, args ...string) *exec.Cmd {
	flags := []string{"--no-pager", "--literal-pathspecs", "-c", "core.fsmonitor=false", "-C", root}
	if readOnly {
		flags = append([]string{"--no-optional-locks"}, flags...)
	}
	cmd := exec.CommandContext(ctx, git, append(flags, args...)...)
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		switch strings.ToUpper(key) {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_PREFIX", "GIT_TERMINAL_PROMPT":
			continue // the selected project's index/worktree, not the parent shell's
		}
		cmd.Env = append(cmd.Env, env)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = time.Second
	toolCommandPlatform(cmd)
	return cmd
}

type gitOperation struct{ cancel context.CancelFunc }

// gitPushArgs selects exactly one local branch and destination. Avoid the
// user's push.default, push refspecs and follow-tags settings from sending
// other branches or tags when Flint's Push button is used.
func gitPushArgs(ctx context.Context, git, root string) ([]string, error) {
	read := func(args ...string) (string, error) {
		out, err := gitCommand(ctx, git, root, true, args...).Output()
		return strings.TrimRight(string(out), "\r\n"), err
	}
	branch, err := read("symbolic-ref", "--quiet", "HEAD")
	if err != nil || !strings.HasPrefix(branch, "refs/heads/") {
		return nil, fmt.Errorf("check out a branch before pushing")
	}
	prefix := "branch." + strings.TrimPrefix(branch, "refs/heads/") + "."
	remote, _ := read("config", "--get", prefix+"remote")
	target, _ := read("config", "--get", prefix+"merge")
	if remote == "." {
		return nil, fmt.Errorf("the current branch tracks a local branch; set a remote upstream before pushing")
	}
	if remote == "" {
		remotes, err := read("remote")
		if err != nil {
			return nil, fmt.Errorf("cannot list Git remotes: %w", err)
		}
		names := strings.Fields(remotes)
		for _, name := range names {
			if name == "origin" {
				remote = name
				break
			}
		}
		if remote == "" && len(names) == 1 {
			remote = names[0]
		}
		if remote == "" {
			if len(names) == 0 {
				return nil, fmt.Errorf("no Git remote is configured; add one before pushing")
			}
			return nil, fmt.Errorf("multiple Git remotes are configured; set an upstream for this branch before pushing")
		}
		target = "" // a merge ref without a remote isn't a configured upstream
	}
	args := []string{"push", "--no-follow-tags"}
	if target == "" {
		target = branch
		args = append(args, "--set-upstream")
	}
	if !strings.HasPrefix(target, "refs/heads/") {
		return nil, fmt.Errorf("the configured upstream is not a remote branch")
	}
	return append(args, "--", remote, branch+":"+target), nil
}

func runGitChange(ctx context.Context, root, operation, value string) error {
	if operation == "commit-push" {
		if err := runGitChange(ctx, root, "commit", value); err != nil {
			return err
		}
		if err := runGitChange(ctx, root, "push", ""); err != nil {
			return fmt.Errorf("commit created, but push failed: %w", err)
		}
		return nil
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return err
	}
	if (operation == "stage" || operation == "unstage" || operation == "discard") && !filepath.IsLocal(value) {
		return fmt.Errorf("invalid Git path: %q", value)
	}
	var args []string
	switch operation {
	case "stage":
		args = []string{"add", "--", value}
	case "stage-all":
		args = []string{"add", "--all", "--"}
	case "fetch":
		args = []string{"fetch"}
	case "unstage":
		if err := gitCommand(ctx, git, root, true, "rev-parse", "--verify", "HEAD").Run(); err == nil {
			args = []string{"restore", "--staged", "--", value}
		} else {
			// An unborn branch has no HEAD to restore from. Only remove the
			// index entry; never change or remove the working-tree file.
			args = []string{"update-index", "--force-remove", "--", value}
		}
	case "discard":
		// Unstaged working-tree changes: restore the path from the index
		// (the tree menu asks for confirmation first). Untracked files are
		// never touched here — discarding them deletes content.
		args = []string{"checkout", "--", value}
	case "commit":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("enter a commit message")
		}
		args = []string{"commit", "--message=" + value}
	case "push":
		args, err = gitPushArgs(ctx, git, root)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown Git action: %s", operation)
	}
	cmd := gitCommand(ctx, git, root, false, args...)
	buf := &boundedGitOutput{limit: 16 << 10}
	cmd.Stdout, cmd.Stderr = buf, buf
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("git %s: %w", operation, ctx.Err())
		}
		if text := strings.TrimSpace(string(buf.bytes)); text != "" {
			return fmt.Errorf("git %s: %s", operation, text)
		}
		return fmt.Errorf("git %s: %w", operation, err)
	}
	return nil
}

func (ws *Workspace) startGitChange(root, action, value string, update updateFn, w *gpui.Window) error {
	if ws.tools.gitOp != nil {
		return fmt.Errorf("a Git action is already running")
	}
	if g := ws.tools.git; g == nil || g.result.repo != root {
		return fmt.Errorf("refresh Git status first")
	}
	timeout := 30 * time.Second
	if action == "push" || action == "commit-push" || action == "fetch" {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	op := &gitOperation{cancel: cancel}
	ws.tools.gitOp = op
	ws.StatusError = ""
	ws.tools.gitMessage = ""
	apply := func(s *Workspace, err error) {
		if s.tools.gitOp != op {
			return
		}
		s.tools.gitOp = nil
		s.tools.gitMessageError = err != nil
		if err != nil {
			s.tools.gitMessage = err.Error()
		} else if action == "commit" {
			s.tools.gitMessage = "Commit created"
		} else if action == "push" {
			s.tools.gitMessage = "Branch pushed"
		} else if action == "commit-push" {
			s.tools.gitMessage = "Commit created and branch pushed"
		} else if action == "fetch" {
			s.tools.gitMessage = "Fetch complete"
		} else {
			s.tools.gitMessage = ""
		}
		if err == nil && (action == "commit" || action == "commit-push") && s.tools.gitDraft.String() == value {
			s.tools.gitDraft.Set("")
		}
		// Stage/commit move files between index and HEAD: the project tree's
		// coloring refreshes after every action, and a commit re-bases the
		// editor's change bars (they diff the working tree against HEAD).
		s.markTreeGitDirty()
		if err == nil && (action == "commit" || action == "commit-push") {
			s.invalidateEditorDiffs()
		}
		s.startToolLoad("git", update, w)
	}
	if dispatch := ws.dispatch; dispatch != nil {
		go func() {
			err := runGitChange(ctx, root, action, value)
			cancel()
			defer func() { _ = recover() }()
			dispatch(func() {
				defer func() { _ = recover() }()
				update(func(s *Workspace) { apply(s, err) })
				w.Refresh()
			})
		}()
	} else {
		err := runGitChange(ctx, root, action, value)
		cancel()
		apply(ws, err)
	}
	return nil
}
