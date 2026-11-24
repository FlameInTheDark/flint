package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/debugger"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

func TestDebugEditorWorkflow(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	write := func(name, text string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("go.mod", "module example.test/app\n\ngo 1.23\n")
	main := write("main.go", "package main\nfunc main(){ helper() }\n")
	helper := write("helper.go", "package main\nfunc helper(){\n n := 42\n println(n)\n}\n")
	hp := headless.New(geometry.SizePixels(1000, 650))
	a := gpui.NewApplication(hp)
	dispatch := make(chan func(), 16)
	handle, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{Title: "Go debugger"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("Go debugger")
		ws.Root = dir
		ws.ShowTree = false
		ws.ShowLineNumbers = false
		ws.tools.rail = true
		ws.OpenFile(helper)
		ws.Measure(w)
		ws.SetEnv(nil, func(f func()) { dispatch <- f }, nil)
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func() *Workspace { return handle.View.Read(a.App) }
	update := func(f func(*Workspace)) {
		handle.View.Update(a.App, func(s *Workspace, c *app.Context[Workspace]) any { f(s); c.Notify(); return nil })
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
	InstallZoomKeys(handle.Window)
	handle.Window.Draw()
	hw := hp.LastWindow()
	box := func(id string) geometry.Bounds[geometry.Pixels] {
		t.Helper()
		for _, hb := range handle.Window.RenderedHitboxes() {
			if hb.Key == "n:btn-"+id {
				return hb.Bounds
			}
		}
		t.Fatalf("missing hitbox %q", id)
		return geometry.Bounds[geometry.Pixels]{}
	}
	click := func(p geometry.Point[geometry.Pixels]) {
		hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
		hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
		handle.Window.Draw()
	}
	clickButton := func(id string) {
		b := box(id)
		click(geometry.PointPixels(b.Origin.X.AsF32()+b.Size.Width.AsF32()/2, b.Origin.Y.AsF32()+b.Size.Height.AsF32()/2))
	}
	press := func(key string) {
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: key}})
		handle.Window.Draw()
	}
	code := box("editor-code")
	click(geometry.PointPixels(code.Origin.X.AsF32()-8, code.Origin.Y.AsF32()+3.5*read().LineHeight))
	if got := read().debug.points[helper]; !slices.Equal(got, []int{4}) {
		t.Fatalf("breakpoint click with hidden line numbers: %v", got)
	}
	update(func(ws *Workspace) { ws.Buf().SetCursor(ws.Buf().LineStart(3)) })
	press("f9")
	if len(read().debug.points) != 0 {
		t.Fatal("F9 did not remove the gutter breakpoint")
	}
	press("f9")
	clickButton("tool-debug")
	if !read().ShowDebug || read().debug.tab != "Breakpoints" {
		t.Fatal("breakpoint list unavailable before launch")
	}
	clickButton("debug-hide")

	t.Run("Delve navigation", func(t *testing.T) {
		if dlv := os.Getenv("GO_GPUI_TEST_DLV"); dlv != "" {
			t.Setenv("PATH", filepath.Dir(dlv)+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
		if _, err := config.ResolveLanguageServerCommand("dlv"); err != nil {
			t.Skip("Delve not installed")
		}
		update(func(ws *Workspace) { ws.OpenFile(main) })
		handle.Window.Draw()
		press("f5")
		s := read().debug.session
		if s == nil || !read().ShowDebug || read().ShowTerminal {
			t.Fatalf("F5 did not open debugger: %q", read().StatusError)
		}
		defer func() { s.Stop(); <-s.Done() }()
		wait := func(ok func() bool) {
			t.Helper()
			timer := time.NewTimer(30 * time.Second)
			defer timer.Stop()
			for !ok() {
				select {
				case f := <-dispatch:
					f()
				case <-timer.C:
					t.Fatalf("editor did not receive expected debugger state: %+v", s.Snapshot())
				}
			}
			handle.Window.Draw()
		}
		wait(func() bool { return read().debug.location != 0 && read().debug.tab == "Inspect" })
		if ws := read(); !sameFilePath(ws.Buf().Path, helper) {
			t.Fatalf("did not open stopped source: %s", ws.Buf().Path)
		} else if _, exec := ws.debugMarkers(ws.Buf(), s.Snapshot()); exec != 4 {
			t.Fatalf("highlighted line = %d", exec)
		}
		clickButton("debug-frame-1")
		wait(func() bool { return sameFilePath(read().Buf().Path, main) })
		clickButton("debug-continue")
		wait(func() bool { return s.Snapshot().State == debugger.Finished })
		if got := s.Snapshot().Output; !strings.Contains(got, "42") {
			t.Fatalf("console output = %q", got)
		}
	})
}
