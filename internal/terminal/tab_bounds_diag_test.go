package terminal_test

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

// TestDiagTabBounds replicates the terminal dock header structure (header
// row + tab + close button) and dumps the bounds of every piece, to find
// why the close glyph hangs below the tab in screenshots.
func TestDiagTabBounds(t *testing.T) {
	type view struct{}
	hp := headless.New(geometry.SizePixels(600, 200))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[view](appl, platform.WindowOptions{Title: "diag-tab"},
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
		t := ui.DefaultTheme()

		header := elements.NewDiv().
			Flex().ItemsCenter().H(ui.Px(ui.TermHeaderH)).FlexShrink(0).
			Px(ui.DefPx(10)).Gap(ui.DefPx(6)).
			TextSize(ui.AbsPx(12)).LineHeight(ui.DefPx(16)).
			BgColor(t.Surface)

		tab := elements.NewDiv().
			Flex().ItemsCenter().Gap(ui.DefPx(5)).
			H(ui.Px(22)).Px(ui.DefPx(7)).Rounded(ui.AbsPx(4)).
			MaxW(ui.Px(200)).WhitespaceNowrap().TextEllipsis().OverflowHidden().
			BgColor(t.Surface2)
		title := elements.NewText("go-gpui")
		tab = tab.Child(title)

		closeBtn := elements.NewDiv().
			Flex().ItemsCenter().JustifyCenter().
			W(ui.Px(16)).H(ui.Px(16)).
			Child(elements.NewText("×"))
		tab = tab.Child(closeBtn)

		header = header.Child(tab)

		// A tab whose title is far wider than the 200px cap: the tab must
		// clamp to MaxW and the title must truncate (ellipsis) inside it.
		longTab := elements.NewDiv().
			Flex().ItemsCenter().Gap(ui.DefPx(5)).
			H(ui.Px(22)).Px(ui.DefPx(7)).Rounded(ui.AbsPx(4)).
			MaxW(ui.Px(200)).WhitespaceNowrap().TextEllipsis().OverflowHidden().
			BgColor(t.Surface2)
		longTitle := elements.NewText("very-long-working-directory-name-that-goes-on-and-on-for-a-while")
		longTab = longTab.Child(longTitle)
		header = header.Child(longTab)

		probes = append(probes,
			probe{"header", header},
			probe{"tab", tab},
			probe{"title", title},
			probe{"close", closeBtn},
			probe{"longtab", longTab},
			probe{"longtitle", longTitle},
		)
		return elements.NewDiv().Flex().FlexCol().Child(header)
	})
	handle.Window.Draw()

	for _, p := range probes {
		b := handle.Window.ElementBounds(p.el)
		fmt.Printf("%-8s origin=(%7.2f,%7.2f) size=%.2f x %.2f\n", p.name,
			b.Origin.X.AsF32(), b.Origin.Y.AsF32(), b.Size.Width.AsF32(), b.Size.Height.AsF32())
	}
}
