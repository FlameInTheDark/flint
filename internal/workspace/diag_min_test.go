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

// TestDiagPopupMinimal builds progressively larger div trees mimicking
// RenderPopupLayer's style chain and dumps the row bounds for each, to
// bisect which style combination overflows the panel.
func TestDiagPopupMinimal(t *testing.T) {
	type view struct{}
	hp := headless.New(geometry.SizePixels(1100, 700))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[view](appl, platform.WindowOptions{Title: "diag2"},
		func(w *gpui.Window, cx *app.Context[view]) view { return view{} })
	if err != nil {
		t.Fatal(err)
	}

	type probe struct {
		name string
		el   gpui.Element
	}
	var probes []probe

	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		probes = nil
		// Variant A: absolute panel + plain stretch child with Mx(4).
		panelA := elements.NewDiv().
			Absolute().Left(ui.Px(40)).Top(ui.Px(30)).
			W(ui.Px(260)).H(ui.Px(144)).
			Flex().FlexCol()
		rowA := elements.NewDiv().ID(gpui.NameElementId("rowA")).
			Flex().ItemsCenter().H(ui.Px(26)).Mx(ui.Px(4)).
			Child(elements.NewText("Toggle Project Panel"))
		panelA = panelA.Child(rowA)
		probes = append(probes, probe{"panelA", panelA}, probe{"rowA", rowA})

		// Variant B: adds Py + border + rounded (full popup panel style).
		panelB := elements.NewDiv().
			Absolute().Left(ui.Px(40)).Top(ui.Px(230)).
			W(ui.Px(260)).
			MaxH(ui.Px(700 - 2*ui.ContextPad)).
			Flex().FlexCol().
			BgColor(ui.DefaultTheme().Crust).
			Border_1().BorderColor(ui.DefaultTheme().Surface1).
			Rounded(ui.AbsPx(6)).
			Py(ui.DefPx(ui.MenuPadY))
		rowB := elements.NewDiv().ID(gpui.NameElementId("rowB")).
			Flex().ItemsCenter().H(ui.Px(26)).Mx(ui.Px(4)).
			Child(elements.NewText("Toggle Project Panel"))
		panelB = panelB.Child(rowB)
		probes = append(probes, probe{"panelB", panelB}, probe{"rowB", rowB})

		// Variant C: plain (non-absolute) column panel.
		panelC := elements.NewDiv().
			W(ui.Px(260)).H(ui.Px(60)).
			Flex().FlexCol()
		rowC := elements.NewDiv().ID(gpui.NameElementId("rowC")).
			Flex().ItemsCenter().H(ui.Px(26)).Mx(ui.Px(4)).
			Child(elements.NewText("Toggle Project Panel"))
		panelC = panelC.Child(rowC)
		probes = append(probes, probe{"panelC", panelC}, probe{"rowC", rowC})

		return elements.NewDiv().Flex().FlexCol().FlexGrow(1).Child(panelA).Child(panelB).Child(panelC)
	})
	handle.Window.Draw()

	for _, p := range probes {
		b := handle.Window.ElementBounds(p.el)
		fmt.Printf("%-8s origin=(%7.1f,%7.1f) size=%.1f x %.1f\n", p.name,
			b.Origin.X.AsF32(), b.Origin.Y.AsF32(), b.Size.Width.AsF32(), b.Size.Height.AsF32())
	}
}
