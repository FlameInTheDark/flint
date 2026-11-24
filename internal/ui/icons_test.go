package ui

import (
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

func TestLucideToolbarIconsPaint(t *testing.T) {
	a := gpui.NewApplication(headless.New(geometry.SizePixels(48, 48)))
	h, err := gpui.OpenWindow[struct{}](a, platform.WindowOptions{}, func(*gpui.Window, *app.Context[struct{}]) struct{} { return struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	name := ""
	w := h.Window
	w.SetRootView(h.View.ID(), func(*gpui.Window, *app.App) gpui.Element {
		return elements.NewDiv().Flex().ItemsCenter().JustifyCenter().Child(ToolbarIcon(name, DefaultTheme().Text))
	})
	for name = range toolbarIconNames {
		w.Draw()
		got := len(w.RenderedScene().Paths)
		want := 1
		switch name {
		case "close":
			want = 2
		case "refresh":
			want = 4
		}
		if got < want {
			t.Errorf("%s painted %d paths, want at least %d", name, got, want)
		}
	}
}
