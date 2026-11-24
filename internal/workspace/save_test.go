package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

func TestCtrlSSavesActiveTabAndShowsDirtyDots(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	a, b, md := filepath.Join(dir, "alpha.txt"), filepath.Join(dir, "beta.txt"), filepath.Join(dir, "notes.md")
	for path, content := range map[string]string{a: "alpha", b: "beta", md: "# Notes\n"} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	hp := headless.New(geometry.SizePixels(1000, 650))
	application := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](application, platform.WindowOptions{Title: "save test"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("save test")
		ws.OpenFile(a)
		ws.OpenFile(b)
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
	update := func(f func(*Workspace)) {
		handle.View.Update(application.App, func(s *Workspace, c *app.Context[Workspace]) any { f(s); c.Notify(); return nil })
	}
	findTab := func(name string) gpui.Hitbox {
		t.Helper()
		for _, hb := range handle.Window.RenderedHitboxes() {
			if hb.Key == "n:btn-tab-"+name {
				return hb
			}
		}
		t.Fatalf("tab %s not found", name)
		return gpui.Hitbox{}
	}
	dotCount := func(name string) int {
		t.Helper()
		tab := findTab(name)
		n := 0
		for _, q := range handle.Window.PaintedQuads() {
			color, ok := q.Background.AsSolid()
			if !ok || color != handle.View.Read(application.App).Theme().Text {
				continue
			}
			r := q.Bounds
			if r.Size.Width.AsF32() >= 7.5 && r.Size.Width.AsF32() <= 8.5 && r.Size.Height.AsF32() >= 7.5 && r.Size.Height.AsF32() <= 8.5 && r.Origin.X.AsF32() >= tab.Bounds.Origin.X.AsF32() && r.Origin.X.AsF32() < tab.Bounds.Origin.X.AsF32()+tab.Bounds.Size.Width.AsF32() && r.Origin.Y.AsF32() >= ui.MenuBarHeight && r.Origin.Y.AsF32() < ui.MenuBarHeight+ui.TabBarHeight {
				n++
			}
		}
		return n
	}
	clickTab := func(name string) {
		t.Helper()
		tab := findTab(name)
		p := geometry.PointPixels(tab.Bounds.Origin.X.AsF32()+10, tab.Bounds.Origin.Y.AsF32()+tab.Bounds.Size.Height.AsF32()/2)
		hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
		hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
		handle.Window.Draw()
	}
	typeKey := func(key string) {
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: key, KeyChar: key}})
		handle.Window.Draw()
	}
	save := func() {
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "s", Modifiers: keymap.Modifiers{Control: true}}})
		handle.Window.Draw()
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	typeKey("x")
	if !handle.View.Read(application.App).Buf().Dirty || dotCount("beta.txt") != 1 || dotCount("alpha.txt") != 0 || read(b) != "beta" {
		t.Fatal("edited beta tab must show an unsaved dot without writing to disk")
	}
	clickTab("alpha.txt")
	typeKey("y")
	if dotCount("alpha.txt") != 1 || dotCount("beta.txt") != 1 {
		t.Fatal("both edited tabs must show their own unsaved dot")
	}
	save()
	if read(a) != "yalpha" || read(b) != "beta" || handle.View.Read(application.App).Buf().Dirty || dotCount("alpha.txt") != 0 || dotCount("beta.txt") != 1 {
		t.Fatal("Ctrl+S must save only the active tab and clear only its dot")
	}
	clickTab("beta.txt")
	save()
	if read(b) != "xbeta" || dotCount("beta.txt") != 0 {
		t.Fatal("Ctrl+S did not save the second tab")
	}
	update(func(s *Workspace) { s.OpenFile(md) })
	handle.Window.Draw()
	handle.Window.Focus(handle.View.Read(application.App).FocusEditor)
	typeKey("z")
	update(func(s *Workspace) { s.toggleMarkdownPreview(s.Buf()) })
	handle.Window.Draw()
	handle.Window.Focus(handle.View.Read(application.App).FocusEditor)
	save()
	if read(md) != "z# Notes\n" || handle.View.Read(application.App).Buf().Dirty {
		t.Fatal("Ctrl+S did not save the active Markdown file from preview")
	}
}

func TestFailedSaveKeepsDirtyBuffer(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(path, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	ws := New("save failure")
	ws.OpenFile(path)
	buf := ws.Buf()
	buf.Insert("modified")
	// The open buffer remains intact even if its original path is unavailable.
	buf.Path = filepath.Join(dir, "missing-parent", "exists.txt")
	ws.Save()
	if !buf.Dirty || ws.StatusError == "" || buf.Text() != "modifiedbefore" {
		t.Fatalf("failed save lost state: dirty=%v status=%q text=%q", buf.Dirty, ws.StatusError, buf.Text())
	}
}

func TestSaveUneditedBufferDoesNotOverwriteDisk(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	ws := New("clean save")
	ws.OpenFile(path)
	if err := os.WriteFile(path, []byte("updated externally"), 0644); err != nil {
		t.Fatal(err)
	}
	ws.Save()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "updated externally" {
		t.Fatalf("clean buffer overwrote disk: %q", data)
	}
}
