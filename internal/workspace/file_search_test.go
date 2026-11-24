package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

func TestProjectFileSearchIgnoresHiddenAndRanksNames(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"cmd/app", ".git", "node_modules"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"cmd/app/main.go", "README.md", ".git/config", "node_modules/ignored.go"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	files, truncated, err := projectFiles(context.Background(), root)
	if err != nil || truncated || len(files) != 2 {
		t.Fatalf("files=%+v truncated=%v err=%v", files, truncated, err)
	}
	s := &FileSearchState{Files: files}
	s.Filter.Set("main")
	visible := s.visible()
	if len(visible) != 1 || visible[0].relative != "cmd/app/main.go" {
		t.Fatalf("matches = %+v", visible)
	}
	s.Filter.Set("cmng")
	if got := s.visible(); len(got) != 1 || got[0].relative != "cmd/app/main.go" {
		t.Fatalf("fuzzy matches = %+v", got)
	}
}

func TestCtrlPFindsAndOpensProjectFile(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	sub := filepath.Join(root, "cmd")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hp := headless.New(geometry.SizePixels(1000, 650))
	appx := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](appx, platform.WindowOptions{Title: "find file"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("find file")
		if err := ws.OpenFolder(root); err != nil {
			t.Fatal(err)
		}
		ws.Measure(w)
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
	handle.Window.Draw()
	hw := hp.LastWindow()
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "p", Modifiers: keymap.Modifiers{Control: true}}})
	handle.Window.Draw()
	if s := handle.View.Read(appx.App).FileSearch; s == nil || s.Loading {
		t.Fatal("Ctrl+P did not open the indexed file finder")
	}
	for _, r := range "main" {
		ch := string(r)
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: ch, KeyChar: ch}})
		handle.Window.Draw()
	}
	s := handle.View.Read(appx.App).FileSearch
	if s == nil || !strings.EqualFold(s.Filter.String(), "main") || len(s.visible()) != 1 {
		t.Fatalf("file filter did not apply: %+v", s)
	}
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "enter"}})
	handle.Window.Draw()
	ws := handle.View.Read(appx.App)
	if ws.FileSearch != nil || ws.Buf() == nil || !sameFilePath(ws.Buf().Path, path) {
		t.Fatal("Enter did not open the matching file")
	}
	InstallZoomKeys(handle.Window)
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "f", Modifiers: keymap.Modifiers{Control: true, Shift: true}}})
	handle.Window.Draw()
	if s := handle.View.Read(appx.App).FileSearch; s == nil || s.Mode != searchText {
		t.Fatal("Ctrl+Shift+F did not open the Text tab")
	}
}

func TestProjectContentSearchUsesOpenEditsAndLiteralText(t *testing.T) {
	root := t.TempDir()
	goFile := filepath.Join(root, "source.go")
	bin := filepath.Join(root, "binary.dat")
	writeToolFile(t, goFile, "package source\n// old text\n")
	writeToolFile(t, bin, "prefix\x00[needle]")
	files := []projectFile{{path: goFile, relative: "source.go"}, {path: bin, relative: "binary.dat"}}
	// The source text differs from disk; byte offsets must survive Unicode.
	open := map[string]string{searchPathKey(goFile): "package source\n\n// 界 [Needle]\nfunc LoadWidget() {}\ntype Server struct{}\nfunc (*Server) SaveWidget() {}\n"}
	hits, _ := findProjectContent(context.Background(), files, open, searchText, "[needle]")
	if len(hits) != 1 || hits[0].line != 3 || hits[0].column != len("// 界 ")+1 || hits[0].file.path != goFile {
		t.Fatalf("literal text results: %+v", hits)
	}
	if old, _ := findProjectContent(context.Background(), files, open, searchText, "old text"); len(old) != 0 {
		t.Fatalf("returned stale disk text: %+v", old)
	}
	hits, _ = findProjectContent(context.Background(), files, open, searchStructure, "savewid")
	if len(hits) != 1 || hits[0].label != "*Server.SaveWidget" || hits[0].kind != "method" || hits[0].line != 6 {
		t.Fatalf("structure results: %+v", hits)
	}
}

func TestProjectSearchTabsNavigateToTextAndStructure(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first.go"), filepath.Join(root, "second.go")
	writeToolFile(t, first, "package sample\n\n// café [needle]\n")
	writeToolFile(t, second, "package sample\n\nfunc SearchWidget() {}\n")
	h := openToolHarness(t, root, first)
	h.click("toolbar-search")
	h.click("search-tab-text")
	h.typeText("wrong query")
	h.keystroke(keymap.Keystroke{Key: "a", Modifiers: keymap.Modifiers{Control: true}})
	h.typeText("[needle]")
	if s := h.read().FileSearch; s.Mode != searchText || s.Filter.String() != "[needle]" || len(s.hits) != 1 || s.hits[0].line != 3 {
		t.Fatalf("text tab: %+v", s)
	}
	h.key("enter")
	if line, col := h.read().Buf().RuneLineCol(); line != 3 || col != 9 || h.read().FileSearch != nil {
		t.Fatalf("text navigation at %d:%d", line, col)
	}
	h.click("toolbar-search")
	h.typeText("searchwid")
	h.key("tab")
	h.key("tab")
	if s := h.read().FileSearch; s.Mode != searchStructure || s.Filter.String() != "searchwid" || len(s.hits) != 1 {
		t.Fatalf("structure tab: %+v", s)
	}
	h.click("file-search-0")
	if line, col := h.read().Buf().RuneLineCol(); !sameFilePath(h.read().Buf().Path, second) || line != 3 || col != 6 || h.read().FileSearch != nil {
		t.Fatalf("structure navigation at %s:%d:%d", h.read().Buf().Path, line, col)
	}
}

func TestProjectSearchDropsStaleAsyncResults(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	writeToolFile(t, filepath.Join(root, "contents.txt"), "a\nab\n")
	dispatch := make(chan func(), 8)
	hp := headless.New(geometry.SizePixels(900, 600))
	a := gpui.NewApplication(hp)
	h, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{}, func(w *gpui.Window, _ *app.Context[Workspace]) Workspace {
		ws := New("search test")
		if err := ws.OpenFolder(root); err != nil {
			t.Fatal(err)
		}
		ws.SetEnv(nil, func(f func()) { dispatch <- f }, nil)
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	w := h.Window
	w.SetRootView(h.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return h.View.Read(c).Render(&h, c, w) })
	InstallZoomKeys(w)
	w.Draw()
	hw := hp.LastWindow()
	send := func(k keymap.Keystroke) { hw.DispatchInput(platform.KeyDownEvent{Keystroke: k}); w.Draw() }
	next := func() func() {
		t.Helper()
		select {
		case f := <-dispatch:
			return f
		case <-time.After(3 * time.Second):
			t.Fatal("no async search result")
		}
		return nil
	}
	send(keymap.Keystroke{Key: "f", Modifiers: keymap.Modifiers{Control: true, Shift: true}})
	next()() // publish the file index
	send(keymap.Keystroke{Key: "a", KeyChar: "a"})
	stale := next() // result has been dispatched, but not applied
	send(keymap.Keystroke{Key: "b", KeyChar: "b"})
	stale()
	s := h.View.Read(a.App).FileSearch
	if len(s.hits) != 0 || !s.queryLoading {
		t.Fatal("older query overwrote the current search")
	}
	next()()
	if s.queryLoading || len(s.hits) != 1 || s.hits[0].line != 2 {
		t.Fatalf("latest query did not win: %+v", s.hits)
	}
	send(keymap.Keystroke{Key: "escape"})
	if h.View.Read(a.App).FileSearch != nil {
		t.Fatal("escape did not close the search")
	}
}
