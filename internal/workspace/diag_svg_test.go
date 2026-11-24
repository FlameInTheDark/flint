package workspace_test

import (
	"fmt"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/ui"
)

// TestDiagSvgInTab reproduces the tab bar row with a file icon and dumps
// the icon's element bounds (icon fragments painted ~19px above the row in
// user screenshots).
func TestDiagSvgInTab(t *testing.T) {
	type view struct{}
	hp := headless.New(geometry.SizePixels(600, 200))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[view](appl, platform.WindowOptions{Title: "diag3"},
		func(w *gpui.Window, cx *app.Context[view]) view { return view{} })
	if err != nil {
		t.Fatal(err)
	}
	var icons []gpui.Element
	var tabs []gpui.Element
	var icon, tab gpui.Element
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		t0 := ui.DefaultTheme()
		bar := elements.NewDiv().Flex().ItemsStretch().H(ui.Px(ui.MenuBarHeight)).
			Child(elements.NewText("menubar"))
		// Variant 1: full tab (icon + spacer + text).
		tabbar := elements.NewDiv().Flex().ItemsStretch().H(ui.Px(ui.TabBarHeight))
		icon = ui.FileIcon(t0.Accent)
		label := elements.NewText("main.go")
		tab = elements.NewDiv().Flex().ItemsCenter().Px(ui.DefPx(10)).Gap(ui.DefPx(2)).
			Child(icon).Child(elements.NewDiv().W(ui.Px(6))).Child(label)
		tabbar = tabbar.Child(tab)

		// Variant 2: tab with icon only (no text, no gap).
		icon2 := ui.FileIcon(t0.Accent)
		tab2 := elements.NewDiv().Flex().ItemsCenter().Px(ui.DefPx(10)).Child(icon2)
		tabbar = tabbar.Child(tab2)

		// Variant 3: plain div icon (no svg) with text sibling.
		icon3 := elements.NewDiv().W(ui.Px(14)).H(ui.Px(14)).BgColor(t0.Red)
		tab3 := elements.NewDiv().Flex().ItemsCenter().Px(ui.DefPx(10)).Gap(ui.DefPx(2)).
			Child(icon3).Child(elements.NewDiv().W(ui.Px(6))).Child(elements.NewText("main.go"))
		tabbar = tabbar.Child(tab3)

		icons = []gpui.Element{icon, icon2, icon3}
		tabs = []gpui.Element{tab, tab2, tab3}
		return elements.NewDiv().Flex().FlexCol().FlexGrow(1).Child(bar).Child(tabbar)
	})
	handle.Window.Draw()

	for i := range icons {
		bi := handle.Window.ElementBounds(icons[i])
		bt := handle.Window.ElementBounds(tabs[i])
		fmt.Printf("tab%d  origin=(%.1f,%.1f) size=%.1fx%.1f | icon origin=(%.1f,%.1f) size=%.1fx%.1f\n",
			i+1, bt.Origin.X.AsF32(), bt.Origin.Y.AsF32(), bt.Size.Width.AsF32(), bt.Size.Height.AsF32(),
			bi.Origin.X.AsF32(), bi.Origin.Y.AsF32(), bi.Size.Width.AsF32(), bi.Size.Height.AsF32())
	}
}
