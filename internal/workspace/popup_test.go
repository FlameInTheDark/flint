package workspace_test

import (
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/ui"
)

// TestPopupLayerLayout renders a popup overlay stack (dismiss layer +
// dropdown panel) to catch layout cycles that only appear with overlays.
func TestPopupLayerLayout(t *testing.T) {
	type view struct{}

	hp := headless.New(geometry.SizePixels(400, 300))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[view](appl, platform.WindowOptions{Title: "popup"},
		func(w *gpui.Window, cx *app.Context[view]) view { return view{} })
	if err != nil {
		t.Fatal(err)
	}
	open := true
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		root := elements.NewDiv().Flex().FlexCol().FlexGrow(1).
			Child(elements.NewText("under the overlay"))
		if open {
			st := &ui.PopupState{
				Items: []ui.MenuItem{
					{Label: "Cut", Shortcut: "Ctrl+X"},
					{Label: "Copy", Shortcut: "Ctrl+C"},
					{Separator: true},
					{Label: "Select All", Shortcut: "Ctrl+A"},
				},
				X: 10, Y: 20, Hover: -1,
			}
			root = root.Children(ui.RenderPopupLayer(ui.PopupConfig{
				State:      st,
				Theme:      ui.DefaultTheme(),
				WindowSize: geometry.SizePixels(400, 300),
				Focus:      gpui.NewFocusHandle(),
				Update:     func(f func(*ui.PopupState)) { f(st) },
				OnClose:    func() { open = false },
			})...)
		}
		return root
	})
	handle.Window.Draw()

	hw := hp.LastWindow()
	if hw == nil {
		t.Fatal("no headless window")
	}
	img := hw.Image()
	if img == nil || img.Width != 400 {
		t.Fatal("no image")
	}
}

// TestPopupEscape verifies that Escape closes a focused popup panel.
func TestPopupEscape(t *testing.T) {
	type view struct{}
	hp := headless.New(geometry.SizePixels(400, 300))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[view](appl, platform.WindowOptions{Title: "popup"},
		func(w *gpui.Window, cx *app.Context[view]) view { return view{} })
	if err != nil {
		t.Fatal(err)
	}
	open := true
	focus := gpui.NewFocusHandle()
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		root := elements.NewDiv().Flex().FlexCol().FlexGrow(1).
			Child(elements.NewText("under the overlay"))
		if open {
			st := &ui.PopupState{Items: []ui.MenuItem{{Label: "Cut"}}, X: 10, Y: 20, Hover: -1}
			root = root.Children(ui.RenderPopupLayer(ui.PopupConfig{
				State:      st,
				Theme:      ui.DefaultTheme(),
				WindowSize: geometry.SizePixels(400, 300),
				Focus:      focus,
				Update:     func(f func(*ui.PopupState)) { f(st) },
				OnClose:    func() { open = false },
			})...)
		}
		return root
	})
	handle.Window.Draw()

	// Focus the popup like the workspace does on open.
	handle.Window.Focus(focus)
	handle.Window.Draw()

	// Escape must close it.
	hp.LastWindow().DispatchInput(platform.KeyDownEvent{
		Keystroke: platform_keymap_escape(),
	})
	if open {
		t.Fatal("escape did not close the focused popup")
	}
}

func platform_keymap_escape() keymap.Keystroke {
	return keymap.Keystroke{Key: "escape"}
}

// TestPopupWidthFitsContent verifies the popup auto-sizing: the panel
// widens past MenuWidth for a long label + shortcut and never exceeds the
// window (RenderPopupLayer records the computed width in the state).
func TestPopupWidthFitsContent(t *testing.T) {
	type view struct{}
	hp := headless.New(geometry.SizePixels(400, 300))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[view](appl, platform.WindowOptions{Title: "popup"},
		func(w *gpui.Window, cx *app.Context[view]) view { return view{} })
	if err != nil {
		t.Fatal(err)
	}
	longLabel := "Open File with a very long label that does not fit"
	st := &ui.PopupState{Items: []ui.MenuItem{
		{Label: longLabel, Shortcut: "Ctrl+Shift+O"},
		{Label: "Cut", Shortcut: "Ctrl+X"},
	}, X: 10, Y: 20, Hover: -1}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		return elements.NewDiv().Children(ui.RenderPopupLayer(ui.PopupConfig{
			State:      st,
			Theme:      ui.DefaultTheme(),
			WindowSize: geometry.SizePixels(400, 300),
			Focus:      gpui.NewFocusHandle(),
			Measure:    func(s string) float32 { return float32(len(s)) * 8 },
			Update:     func(f func(*ui.PopupState)) { f(st) },
			OnClose:    func() {},
		})...)
	})
	handle.Window.Draw()

	// Need = chrome + 50*8 + gap + 12*8 ≈ 530 > MenuWidth: the panel must
	// widen, but the 400px window caps it at 400 - 2*8 = 384.
	if st.Width <= ui.MenuWidth {
		t.Fatalf("popup did not widen for long content: width %.0f", st.Width)
	}
	if st.Width > 400-2*float32(ui.ContextPad) {
		t.Fatalf("popup width %.0f overflows the 400px window", st.Width)
	}
}
