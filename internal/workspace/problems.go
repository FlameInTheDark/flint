package workspace

import (
	"fmt"
	"strings"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/lsp"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

type workspaceProblem struct {
	buffer     *editor.Buffer
	diagnostic lsp.Diagnostic
}

func (ws *Workspace) toggleProblems() {
	ws.ShowProblems = !ws.ShowProblems
	if ws.ShowProblems {
		ws.ShowTerminal = false
		ws.ShowGoOutput = false
		ws.ShowDebug = false
		ws.ShowTodos = false
		ws.ShowVulns = false
		ws.ShowProfile = false
	}
}

func (ws *Workspace) problems() []workspaceProblem {
	var out []workspaceProblem
	for _, b := range ws.Buffers {
		if doc := ws.languageDocs[b]; doc != nil {
			for _, d := range doc.diagnostics {
				out = append(out, workspaceProblem{b, d})
			}
		}
	}
	return out
}

func (ws *Workspace) openProblem(p workspaceProblem) {
	index := -1
	for i, b := range ws.Buffers {
		if b == p.buffer {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	offset, ok := lsp.OffsetAt(p.buffer.Text(), p.diagnostic.Range.Start)
	if !ok {
		return
	}
	previous := ws.Buf()
	ws.Active = index
	ws.languageRequest++
	ws.completion = nil
	ws.documentation = nil
	if previous != p.buffer && ws.Find != nil {
		ws.RefreshFind()
	}
	p.buffer.SetCursor(offset)
	line, _ := p.buffer.OffsetToLC(offset)
	visible := (editor.Metrics{LineHeight: ws.LineHeight}).VisibleLines(ws.paneHeight())
	if line < ws.scroll() || line >= ws.scroll()+visible {
		ws.setScroll(line - visible/3)
	}
	ws.scrollCaretIntoViewX()
	ws.NoteCaretActivity()
}

func renderProblems(ws *Workspace, t ui.Theme, update updateFn) gpui.Element {
	problems := ws.problems()
	h := ws.problemScroll
	persistScroll(h, &ws.problemTop)
	header := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(32)).FlexShrink(0).Px(ui.DefPx(12)).Gap(ui.DefPx(8)).BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).
		Child(elements.NewText("Problems").WithTextColor(t.Text)).Child(elements.NewText(fmt.Sprintf("%d", len(problems))).WithTextColor(t.Dim)).Child(elements.NewDiv().FlexGrow(1)).
		Child(elements.NewDiv().ID(elements.ButtonID("problems-close")).CursorPointer().Px(ui.DefPx(8)).Rounded(ui.AbsPx(4)).Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
			update(func(s *Workspace) { s.ShowProblems = false })
			w.Focus(ws.FocusEditor)
			w.Refresh()
		}).Child(elements.NewText("×").WithTextColor(t.Dim)))
	body := elements.NewDiv().ID(elements.ButtonID("problems-list")).Flex().FlexCol().Flex1().OverflowYScroll().TrackScroll(h).Py(ui.DefPx(4)).TextSize(ui.AbsPx(12)).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			dy := ev.Delta.PixelDelta(geometry.Px(20)).Y.AsF32()
			if dy != 0 {
				h.ScrollTopBy(-dy)
				ws.problemTop = max(0, ws.problemTop-dy)
				w.StopPropagation()
				w.Refresh()
			}
		})
	if len(problems) == 0 {
		body = body.Child(elements.NewDiv().Px(ui.DefPx(12)).Py(ui.DefPx(8)).Child(elements.NewText("No problems reported by open language servers.").WithTextColor(t.Dim)))
	}
	for i, p := range problems {
		color, label := t.Sky, "Info"
		switch p.diagnostic.Severity {
		case 1:
			color, label = t.Red, "Error"
		case 2:
			color, label = t.Yellow, "Warning"
		case 4:
			color, label = t.Dim, "Hint"
		}
		message := strings.ReplaceAll(p.diagnostic.Message, "\n", " ")
		if r := []rune(message); len(r) > 180 {
			message = string(r[:180]) + "…"
		}
		location := fmt.Sprintf("%s:%d:%d", p.buffer.Name, p.diagnostic.Range.Start.Line+1, p.diagnostic.Range.Start.Character+1)
		row := elements.NewDiv().ID(elements.ButtonID(fmt.Sprintf("problem-%d", i))).Flex().ItemsCenter().H(ui.Px(26)).FlexShrink(0).Px(ui.DefPx(12)).Gap(ui.DefPx(8)).CursorPointer().OverflowHidden().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
			update(func(s *Workspace) { s.openProblem(p) })
			w.Focus(ws.FocusEditor)
			w.Refresh()
		}).
			Child(elements.NewText(label).WithTextColor(color)).Child(elements.NewText(location).WithTextColor(t.Accent)).Child(elements.NewText(message).WithTextColor(t.Text))
		body = body.Child(row)
	}
	return elements.NewDiv().ID(elements.ButtonID("problems-panel")).Flex().FlexCol().H(ui.Px(ws.effectiveTermHeight())).FlexShrink(0).BgColor(t.EditorBg).BorderT_1().BorderColor(t.Surface1).
		// Shared dock resize strip: dragging the top edge re-anchors the
		// bottom dock's height (the terminal dock's edge behavior).
		Child(renderDockResize("problems-resize", t, func(y float32, w *gpui.Window) {
			update(func(s *Workspace) { s.SetTermHeight(y) })
			w.Refresh()
		})).
		Child(header).Child(body)
}
