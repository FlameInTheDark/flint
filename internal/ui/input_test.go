package ui

import (
	"math"
	"strings"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
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
	theme := ThemeByName("IntelliJ Dark")
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
