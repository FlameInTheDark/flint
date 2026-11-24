package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

func TestIntelliJThemeIsDefaultButSavedThemeIsPreserved(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	if got := config.Default().Appearance.Theme; got != "IntelliJ Dark" {
		t.Fatalf("default theme = %q", got)
	}
	cfg := config.Default()
	cfg.Appearance.Theme = "Catppuccin Mocha"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	ws := New("theme")
	if ws.Cfg.Appearance.Theme != "Catppuccin Mocha" || ws.Theme() != ui.ThemeByName("Catppuccin Mocha") {
		t.Fatal("saved theme was overwritten")
	}
}

func TestIDEToolbarAndProjectActions(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	sub := filepath.Join(root, "folder")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file.go"), []byte("package p\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hp := headless.New(geometry.SizePixels(1000, 650))
	application := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](application, platform.WindowOptions{Title: "IDE chrome"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("IDE chrome")
		ws.tools.rail = true
		if err := ws.OpenFolder(root); err != nil {
			t.Fatal(err)
		}
		ws.Tree.Children[0].Expanded = true
		ws.Tree.Children[0].EnsureLoaded()
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
	click := func(id string) {
		t.Helper()
		var target gpui.Hitbox
		for _, hb := range handle.Window.RenderedHitboxes() {
			if hb.Key == "n:btn-"+id {
				target = hb
				break
			}
		}
		if target.Bounds.Size.Width == 0 {
			t.Fatalf("button %s missing", id)
		}
		p := geometry.PointPixels(target.Bounds.Origin.X.AsF32()+target.Bounds.Size.Width.AsF32()/2, target.Bounds.Origin.Y.AsF32()+target.Bounds.Size.Height.AsF32()/2)
		hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
		hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
		handle.Window.Draw()
	}
	click("tree-collapse")
	if handle.View.Read(application.App).Tree.Children[0].Expanded {
		t.Fatal("collapse all did not close project folder")
	}
	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package p\n"), 0644); err != nil {
		t.Fatal(err)
	}
	click("tree-refresh")
	found := false
	for _, n := range handle.View.Read(application.App).Tree.Children {
		if n.Name == "new.go" {
			found = true
		}
	}
	if !found {
		t.Fatal("refresh did not discover the new file")
	}
	click("tool-project")
	if handle.View.Read(application.App).ShowTree {
		t.Fatal("project toolbar button did not hide panel")
	}
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "b", Modifiers: keymap.Modifiers{Control: true}}})
	handle.Window.Draw()
	if !handle.View.Read(application.App).ShowTree {
		t.Fatal("Ctrl+B did not restore project panel")
	}
	click("toolbar-search")
	if handle.View.Read(application.App).FileSearch == nil {
		t.Fatal("search toolbar did not open project file finder")
	}
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "escape"}})
	handle.Window.Draw()
	click("toolbar-settings")
	if !handle.View.Read(application.App).SettingsOpen {
		t.Fatal("settings toolbar button did not open settings")
	}
}
