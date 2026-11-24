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

// TestDiagPopupBounds dumps the computed bounds of every hitbox in a
// rendered popup overlay (panel vs rows vs shortcut text) so a layout
// overflow can be spotted numerically. Not a pass/fail assertion - a
// diagnostic dump.
func TestDiagPopupBounds(t *testing.T) {
	type view struct{}
	hp := headless.New(geometry.SizePixels(1100, 700))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[view](appl, platform.WindowOptions{Title: "diag"},
		func(w *gpui.Window, cx *app.Context[view]) view { return view{} })
	if err != nil {
		t.Fatal(err)
	}
	st := &ui.PopupState{
		Items: []ui.MenuItem{
			{Label: "Toggle Project Panel", Shortcut: "Ctrl+B", HasCheck: true},
			{Label: "Toggle Terminal", Shortcut: "Ctrl+`", HasCheck: true},
			{Label: "Zoom In", Shortcut: "Ctrl+="},
			{Label: "Cut", Shortcut: "Ctrl+X"},
			{Label: "Find in File", Shortcut: "Ctrl+F"},
		},
		X: 40, Y: 30, Hover: -1,
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		return elements.NewDiv().Flex().FlexCol().FlexGrow(1).Children(ui.RenderPopupLayer(ui.PopupConfig{
			State:      st,
			Theme:      ui.DefaultTheme(),
			WindowSize: geometry.SizePixels(1100, 700),
			Focus:      gpui.NewFocusHandle(),
			Update:     func(f func(*ui.PopupState)) { f(st) },
			OnClose:    func() {},
		})...)
	})
	handle.Window.Draw()

	fmt.Printf("popup state width=%.1f height=%.1f origin=(%.1f,%.1f)\n",
		st.Width, st.Height(), st.X, st.Y)
	fmt.Printf("%-28s %-40s %s\n", "element", "origin", "size")
	for i, hb := range handle.Window.RenderedHitboxes() {
		o := hb.Bounds.Origin
		s := hb.Bounds.Size
		tag := ""
		if i == 0 {
			tag = "(dismiss)"
		} else if i == 1 {
			tag = "(panel)"
		}
		fmt.Printf("%-28s (%7.1f,%7.1f) %7.1f x %7.1f %s\n", hbID(hb), o.X.AsF32(), o.Y.AsF32(),
			s.Width.AsF32(), s.Height.AsF32(), tag)
	}
}

func hbID(hb gpui.Hitbox) string {
	return fmt.Sprintf("hb@%.0fx%.0f %.0fx%.0f",
		hb.Bounds.Origin.X.AsF32(), hb.Bounds.Origin.Y.AsF32(),
		hb.Bounds.Size.Width.AsF32(), hb.Bounds.Size.Height.AsF32())
}
