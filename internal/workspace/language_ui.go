package workspace

import (
	"fmt"
	"strings"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/style"
)

func completionKind(k int) string {
	switch k {
	case 2, 3, 4:
		return "ƒ"
	case 5, 22:
		return "◆"
	case 6, 10:
		return "◇"
	case 7, 8, 9:
		return "T"
	case 14:
		return "K"
	default:
		return "·"
	}
}
func renderCompletions(ws *Workspace, t ui.Theme, update updateFn, winW, winH float32) gpui.Element {
	c := ws.completion
	b := c.buffer
	line, _ := b.OffsetToLC(b.Cursor)
	x := ws.paneLeft()
	if ws.ShowLineNumbers {
		x += editor.GutterWidth(b.LineCount(), ws.CharAdvance)
	} else {
		x += editor.GutterLeftInset
	}
	caretX, _ := ws.codeShaper.CaretMetrics(b, t)
	x += caretX - min(ws.scrollX(), ws.maxScrollX())
	y := ws.chromeTop() + ui.TabBarHeight + (float32(line)-ws.scrollF()+1)*ws.LineHeight
	if ws.Find != nil {
		y += ui.FindBarHeight
	}
	width := max(float32(0), min(float32(410), winW-16))
	rows := min(len(c.items), max(1, min(8, int((winH-73)/27))))
	height := float32(32 + rows*27 + 25)
	x = max(float32(8), min(x, winW-width-8))
	if y+height > winH-ui.StatusHeight {
		y -= height + ws.LineHeight
	}
	y = max(float32(8), min(y, winH-height-8))
	box := elements.NewDiv().ID(elements.ButtonID("completion-popup")).Absolute().Left(ui.Px(x)).Top(ui.Px(y)).W(ui.Px(width)).
		Flex().FlexCol().BgColor(t.Surface).Border_1().BorderColor(t.Surface2).Rounded(ui.AbsPx(7)).OverflowHidden()
	header := elements.NewDiv().H(ui.Px(32)).Flex().ItemsCenter().Px(ui.DefPx(12)).BorderB_1().BorderColor(t.Surface1).
		Child(elements.NewText("SUGGESTIONS").WithTextColor(t.Accent)).Child(elements.NewDiv().FlexGrow(1)).
		Child(elements.NewText(fmt.Sprintf("%d results", len(c.items))).WithTextColor(t.Dim))
	box = box.Child(header)
	first := max(0, min(c.selected-rows+1, len(c.items)-rows))
	for i := first; i < first+rows; i++ {
		idx := i
		item := c.items[i]
		bg := t.Surface
		if i == c.selected {
			bg = t.Surface2
		}
		row := elements.NewDiv().ID(elements.ButtonID(fmt.Sprintf("completion-%d", i))).H(ui.Px(27)).Flex().ItemsCenter().Gap(ui.DefPx(9)).Px(ui.DefPx(12)).BgColor(bg).CursorPointer().OverflowHidden().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
			OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
				update(func(s *Workspace) { s.acceptCompletion(idx) })
				w.Focus(ws.FocusEditor)
				w.Refresh()
			}).
			Child(elements.NewDiv().W(ui.Px(16)).FlexShrink(0).Child(ui.MonoText(completionKind(item.Kind), t.Mauve))).
			Child(elements.NewText(item.Label).WithTextColor(t.Text)).Child(elements.NewDiv().FlexGrow(1))
		if item.Detail != "" {
			detail := strings.ReplaceAll(item.Detail, "\n", " ")
			if len([]rune(detail)) > 36 {
				detail = string([]rune(detail)[:36]) + "…"
			}
			row = row.Child(elements.NewText(detail).WithTextColor(t.Dim))
		}
		box = box.Child(row)
	}
	return box.Child(elements.NewDiv().H(ui.Px(25)).Flex().ItemsCenter().Px(ui.DefPx(12)).BorderT_1().BorderColor(t.Surface1).TextSize(ui.AbsPx(11)).Child(elements.NewText("↑ ↓ navigate    Enter / Tab insert    Esc dismiss").WithTextColor(t.Dim)))
}

func renderDocumentation(ws *Workspace, t ui.Theme, update updateFn, w *gpui.Window, winW, winH float32) gpui.Element {
	d := ws.documentation
	width := max(float32(0), min(float32(520), winW-16))
	height := max(float32(0), min(float32(330), winH-16))
	x := max(float32(8), min(d.x, winW-width-8))
	y := d.y + 18
	if y+height > winH-8 {
		y = d.y - height - 8
	}
	y = max(float32(8), min(y, winH-height-8))
	if d.view == nil {
		d.view = newMarkdownView(d.text)
		// Scroll metrics are known after layout; a follow-up frame exposes
		// the scrollbar immediately rather than waiting for a wheel event.
		w.OnNextFrame(func(nw *gpui.Window) { nw.Refresh() })
	}
	d.view.measureCode = func(s string) float32 { return measureTextWidth(w, s, true) }
	body := elements.NewDiv().Flex().FlexCol().W(ui.Px(width - 2)).MinW_0().Px(ui.DefPx(14)).Py(ui.DefPx(10)).Child(renderMarkdownBlocks(d.view.root, d.view.source, t, false, d.buffer.Path, markdownLinkHandler(ws, d.buffer.Path, update, w), width-30, d.view))
	title := "Documentation"
	if d.symbol != "" {
		title += " · " + d.symbol
	}
	return elements.NewDiv().ID(elements.ButtonID("documentation-popup")).Absolute().Left(ui.Px(x)).Top(ui.Px(y)).W(ui.Px(width)).H(ui.Px(height)).
		// The popup is opaque to input too: editor scroll and press/capture
		// handlers must not receive events anywhere inside its bounds.
		Flex().FlexCol().BgColor(t.Surface).Border_1().BorderColor(t.Surface2).Rounded(ui.AbsPx(7)).OverflowHidden().Occlude().
		Child(elements.NewDiv().H(ui.Px(36)).FlexShrink(0).Flex().ItemsCenter().Gap(ui.DefPx(8)).Px(ui.DefPx(14)).BorderB_1().BorderColor(t.Surface1).
			Child(elements.NewText("◆").WithTextColor(t.Mauve)).
			Child(elements.NewDiv().Flex1().MinW_0().OverflowHidden().WhitespaceNowrap().TextEllipsis().Child(elements.NewText(title).WithTextColor(t.Text))).
			Child(elements.NewDiv().ID(elements.ButtonID("documentation-close")).W(ui.Px(24)).H(ui.Px(24)).FlexShrink(0).
				Flex().ItemsCenter().JustifyCenter().Rounded(ui.AbsPx(4)).Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
				Tooltip("Close documentation (Esc)").CursorPointer().OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
				update(func(s *Workspace) { s.documentation = nil })
				w.Focus(ws.FocusEditor)
				w.Refresh()
			}).Child(ui.Icon("x", t.Dim, 16)))).
		Child(markdownScroll(d.view, body, t, "documentation", gpui.FocusHandle{}, nil))
}
