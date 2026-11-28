package ui

import (
	"math"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
	"github.com/FlameInTheDark/go-gpui/style"
	"github.com/FlameInTheDark/go-gpui/text"
)

func TestInputCaretFollowsShapedText(t *testing.T) {
	a := gpui.NewApplication(headless.New(geometry.SizePixels(400, 100)))
	h, err := gpui.OpenWindow[struct{}](a, platform.WindowOptions{}, func(*gpui.Window, *app.Context[struct{}]) struct{} { return struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	w := h.Window
	theme := ThemeByName("Flint Dark")
	const fieldWidth = 250
	var input InputState
	input.Set("examples/éWii")
	w.SetRootView(h.View.ID(), func(*gpui.Window, *app.App) gpui.Element {
		return elements.NewDiv().Flex().W(Px(fieldWidth)).TextSize(AbsPx(18)).Child(RenderInput(InputConfig{State: &input, Theme: theme}))
	})
	caretX := func() float32 {
		t.Helper()
		w.Draw()
		for _, q := range w.PaintedQuads() {
			if q.Background.Solid == theme.Accent {
				return q.Bounds.Origin.X.AsF32()
			}
		}
		t.Fatal("caret was not painted")
		return 0
	}
	sys := w.TextSystem()
	fid, _ := sys.FontID(style.DefaultTextStyle().ToFont())
	l := sys.LayoutLine(input.String(), geometry.Px(18), []text.FontRun{{Len: len(input.String()), FontID: fid}})
	if l == nil || l.Width <= 0 {
		t.Fatal("missing text layout")
	}
	input.Cursor = 0
	start := caretX()
	if start < 8 || start > 9 {
		t.Fatalf("start caret x=%v, want at the field's left padding", start)
	}
	for _, prefix := range []string{"examples/é", input.String()} {
		input.Cursor = len([]rune(prefix))
		want := start + l.XForIndex(len(prefix)).AsF32() - l.XForIndex(0).AsF32()
		if got := caretX(); math.Abs(float64(got-want)) > 1 {
			t.Fatalf("caret after %q: x=%v, want %v", prefix, got, want)
		}
	}
	input.Set(strings.Repeat("examples/editor/", 12))
	if got := caretX(); got < fieldWidth-14 || got > fieldWidth-9 {
		t.Fatalf("long input panned caret to x=%v, want near the right padding", got)
	}
}

func TestInputSelectionAndClipboard(t *testing.T) {
	hp := headless.New(geometry.SizePixels(400, 100))
	a := gpui.NewApplication(hp)
	h, err := gpui.OpenWindow[struct{}](a, platform.WindowOptions{}, func(*gpui.Window, *app.Context[struct{}]) struct{} { return struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	w, hw := h.Window, hp.LastWindow()
	theme := ThemeByName("Flint Dark")
	focus := gpui.NewFocusHandle()
	var input InputState
	input.Set("éWii one")
	w.SetRootView(h.View.ID(), func(*gpui.Window, *app.App) gpui.Element {
		return elements.NewDiv().Flex().W(Px(250)).TextSize(AbsPx(18)).Child(RenderInput(InputConfig{
			State: &input, Theme: theme, Focus: focus, Update: func(f func(*InputState)) { f(&input) },
		}))
	})
	w.Draw()
	assertSelection := func(lo, hi int) {
		t.Helper()
		if a, b := input.Selection(); a != lo || b != hi {
			t.Fatalf("selection = [%d,%d), want [%d,%d)", a, b, lo, hi)
		}
	}
	left := platform.ButtonLeft
	start := geometry.PointPixels(9, 12)
	outside := geometry.PointPixels(300, 12)
	hw.DispatchInput(platform.MouseDownEvent{Button: left, Position: start})
	w.Draw() // the selection drag must survive a new frame
	hw.DispatchInput(platform.MouseMoveEvent{PressedButton: &left, Position: outside})
	hw.DispatchInput(platform.MouseUpEvent{Button: left, Position: outside})
	assertSelection(0, 8)
	w.Draw()
	found := false
	for _, q := range w.PaintedQuads() {
		if q.Background.Solid == theme.Selection && q.Bounds.Size.Width > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("selection was not highlighted")
	}
	key := func(k, ch string, mods keymap.Modifiers) {
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: k, KeyChar: ch, Modifiers: mods}})
		w.Draw()
	}
	ctrl := keymap.Modifiers{Control: true}
	key("c", "", ctrl)
	if item, _ := hw.ReadClipboard(); item.Text != "éWii one" {
		t.Fatalf("copy = %q", item.Text)
	}
	key("right", "", keymap.Modifiers{})
	assertSelection(8, 8)
	key("left", "", keymap.Modifiers{Control: true, Shift: true})
	assertSelection(5, 8)
	key("left", "", keymap.Modifiers{Shift: true})
	assertSelection(4, 8)
	key("left", "", keymap.Modifiers{})
	assertSelection(4, 4)
	key("a", "", ctrl)
	assertSelection(0, 8)
	key("z", "z", keymap.Modifiers{})
	if input.String() != "z" {
		t.Fatalf("typing over selection = %q", input.String())
	}
	key("a", "", ctrl)
	key("v", "", ctrl)
	if input.String() != "éWii one" {
		t.Fatalf("pasting over selection = %q", input.String())
	}
	key("home", "", keymap.Modifiers{Shift: true})
	assertSelection(0, 8)
	key("delete", "", keymap.Modifiers{})
	if input.String() != "" {
		t.Fatalf("Delete left %q", input.String())
	}
	key("v", "", ctrl)
	click := func(p geometry.Point[geometry.Pixels], count int, mods keymap.Modifiers) {
		hw.DispatchInput(platform.MouseDownEvent{Button: left, Position: p, ClickCount: count, Modifiers: mods})
		hw.DispatchInput(platform.MouseUpEvent{Button: left, Position: p})
		w.Draw()
	}
	click(start, 2, keymap.Modifiers{})
	assertSelection(0, 4)
	click(geometry.PointPixels(150, 12), 1, keymap.Modifiers{Shift: true})
	assertSelection(0, 8)
	key("a", "", ctrl)
	key("x", "", ctrl)
	if input.String() != "" {
		t.Fatalf("cut left %q", input.String())
	}
}
