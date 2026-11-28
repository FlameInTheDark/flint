package workspace

import (
        "fmt"
        "path/filepath"
        "testing"

        "github.com/FlameInTheDark/flint/internal/editor"
        "github.com/FlameInTheDark/go-gpui/app"
        "github.com/FlameInTheDark/go-gpui/gpui"
        "github.com/FlameInTheDark/go-gpui/platform/headless"
)

// benchTreeScene opens the workspace headlessly with a project tree shaped
// like the user's report: a root with a few top-level files and one folder
// ("internal/workspace") holding `files` entries, expanded, with `changed`
// of them colored by git status (the tree's git coloring snapshot).
func benchTreeScene(b *testing.B, files, changed int) (*headless.Platform, *gpui.WindowHandle[Workspace], *app.App) {
        b.Helper()
        buf := editor.NewBuffer("", "test.go", "package main\n")
        hp, handle, ap := openLeftGapScene(b, true, buf)

        repo := "/bench/flint"
        root := &TreeNode{Name: "flint", Path: repo, IsDir: true, Loaded: true, Expanded: true}
        for i := 0; i < 20; i++ {
                root.Children = append(root.Children, &TreeNode{
                        Name: fmt.Sprintf("top%02d.go", i), Path: fmt.Sprintf(repo+"/top%02d.go", i), Parent: root,
                })
        }
        sub := &TreeNode{Name: "workspace", Path: repo + "/internal/workspace", IsDir: true, Loaded: true, Expanded: true, Parent: root}
        for i := 0; i < files; i++ {
                sub.Children = append(sub.Children, &TreeNode{
                        Name: fmt.Sprintf("file%03d.go", i), Path: fmt.Sprintf(repo+"/internal/workspace/file%03d.go", i), Parent: sub,
                })
        }
        root.Children = append(root.Children, sub)

        st := handle.View.Read(ap)
        st.Tree, st.ShowTree = root, true
        st.tools.left = ""

        // The tree's git coloring snapshot as applyTreeGit would store it:
        // `changed` modified files under the expanded folder, folders folded.
        tg := &treeGitState{repo: repo, tried: true,
                files: map[string]gitFileState{}, dirs: map[string]gitFileState{}}
        for i := 0; i < changed && i < files; i++ {
                p := fmt.Sprintf(repo+"/internal/workspace/file%03d.go", i)
                tg.files[p] = gitModified
                colorDirs(tg.dirs, repo, filepath.Dir(p), gitModified)
        }
        st.treeGit = tg
        handle.Window.Draw()
        return hp, handle, ap
}

// BenchmarkTreeFrameDraw measures one full frame (element build + layout +
// paint) with the tree panel visible at several folder sizes, half of the
// folder's files carrying git modifications. A warm-up loop runs first so
// the measured frames reflect steady state (shaped-text caches, measure
// caches), not the one-time cold shaping of every label.
func BenchmarkTreeFrameDraw(b *testing.B) {
        for _, n := range []int{25, 100, 400, 1000} {
                b.Run(fmt.Sprintf("files=%d", n), func(b *testing.B) {
                        hp, handle, ap := benchTreeScene(b, n, n/2)
                        _ = hp
                        _ = ap
                        for i := 0; i < 60; i++ {
                                handle.Window.Draw()
                        }
                        b.ResetTimer()
                        for i := 0; i < b.N; i++ {
                                handle.Window.Draw()
                        }
                })
        }
}

// BenchmarkTreeFrameDrawClean is the control with no git coloring.
func BenchmarkTreeFrameDrawClean(b *testing.B) {
        hp, handle, ap := benchTreeScene(b, 400, 0)
        _ = hp
        _ = ap
        for i := 0; i < 60; i++ {
                handle.Window.Draw()
        }
        b.ResetTimer()
        for i := 0; i < b.N; i++ {
                handle.Window.Draw()
        }
}
