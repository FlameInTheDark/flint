package workspace

import (
	"fmt"
	"html"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/font"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
	"github.com/FlameInTheDark/go-gpui/text"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	mdtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var markdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM))

type markdownView struct {
	source      []byte
	root        ast.Node
	scroll      *elements.ScrollHandle
	top         float32
	codeScrolls map[string]*markdownCodeScroll
	measureCode func(string) float32
}
type markdownCodeScroll struct {
	handle *elements.ScrollHandle
	left   float32
}
type markdownPreviewState struct {
	view     *markdownView
	enabled  bool
	revision uint64
}

func newMarkdownView(source string) *markdownView {
	v := &markdownView{scroll: elements.NewScrollHandle()}
	v.setSource(source)
	return v
}
func (v *markdownView) setSource(source string) {
	v.source = []byte(source)
	v.root = markdownParser.Parser().Parse(mdtext.NewReader(v.source))
	v.codeScrolls = make(map[string]*markdownCodeScroll)
}

type inlineFormat struct{ bold, italic, code, link, strike bool }
type inlineBuilder struct {
	text  strings.Builder
	runs  []text.TextRun
	theme ui.Theme
	links []markdownLink
}
type markdownLink struct {
	start, end  int
	destination string
}

func (b *inlineBuilder) append(s string, f inlineFormat) {
	if s == "" {
		return
	}
	face := font.Font{Weight: font.WeightNormal}
	if f.bold {
		face.Weight = font.WeightBold
	}
	if f.italic {
		face.Style = font.StyleItalic
	}
	if f.code {
		face.Family = "Monospace"
	}
	color := b.theme.Text
	if f.link {
		color = b.theme.Accent
	}
	run := text.TextRun{Len: len(s), Font: face, Color: color}
	if f.code {
		bg := b.theme.Surface2
		run.BackgroundColor = &bg
	}
	if f.link {
		run.Underline = &style.UnderlineStyle{Thickness: 1}
	}
	if f.strike {
		run.Strikethrough = &style.StrikethroughStyle{Thickness: 1}
	}
	b.text.WriteString(s)
	b.runs = append(b.runs, run)
}

func (b *inlineBuilder) walk(n ast.Node, source []byte, f inlineFormat) {
	switch x := n.(type) {
	case *ast.Text:
		s := string(x.Value(source))
		if !f.code && !x.IsRaw() {
			s = html.UnescapeString(string(util.UnescapePunctuations([]byte(s))))
		}
		b.append(s, f)
		if x.HardLineBreak() {
			b.append("\n", f)
		} else if x.SoftLineBreak() {
			b.append(" ", f)
		}
		return
	case *ast.String:
		b.append(string(x.Value), f)
		return
	case *ast.RawHTML:
		// HTML is not executed by the preview; keep unsupported markup visible.
		b.append(string(x.Segments.Value(source)), f)
		return
	case *ast.AutoLink:
		f.link = true
		start := b.text.Len()
		b.append(string(x.Label(source)), f)
		destination := string(x.URL(source))
		if x.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(destination, "mailto:") {
			destination = "mailto:" + destination
		}
		b.links = append(b.links, markdownLink{start, b.text.Len(), destination})
		return
	case *ast.Emphasis:
		if x.Level >= 2 {
			f.bold = true
		} else {
			f.italic = true
		}
	case *ast.CodeSpan:
		f.code = true
	case *ast.Link:
		f.link = true
		start := b.text.Len()
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			b.walk(c, source, f)
		}
		b.links = append(b.links, markdownLink{start, b.text.Len(), string(x.Destination)})
		return
	case *ast.Image: // Inline images retain meaningful alt text.
		b.append("[image: ", f)
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			b.walk(c, source, f)
		}
		b.append("]", f)
		return
	case *extast.Strikethrough:
		f.strike = true
	case *extast.TaskCheckBox:
		if x.IsChecked {
			b.append("☑ ", f)
		} else {
			b.append("☐ ", f)
		}
		return
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		b.walk(c, source, f)
	}
}

func markdownInline(n ast.Node, source []byte, theme ui.Theme, bold bool) (string, []text.TextRun) {
	b := inlineBuilder{theme: theme}
	b.walk(n, source, inlineFormat{bold: bold})
	return b.text.String(), b.runs
}

// markdownInlineElement keeps link hit-testing aligned with the same wrapped
// text width used for layout, including links in table cells and list items.
func markdownInlineElement(n ast.Node, source []byte, theme ui.Theme, bold bool, width float32, openLink func(string)) *elements.Div {
	builder := inlineBuilder{theme: theme}
	builder.walk(n, source, inlineFormat{bold: bold})
	s := builder.text.String()
	paragraph := elements.NewDiv().Flex().FlexCol().W(ui.Px(width)).MinW_0().Child(elements.NewText(s).WithWidth(geometry.Px(width)).WithRuns(builder.runs))
	if len(builder.links) == 0 || openLink == nil {
		return paragraph
	}
	links, runs := builder.links, builder.runs
	return paragraph.OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
		bounds, ok := w.ListenerHitbox()
		if !ok {
			return
		}
		sys := w.TextSystem()
		if sys == nil {
			return
		}
		var fonts []text.FontRun
		for _, r := range runs {
			id, _ := sys.FontID(r.Font)
			fonts = append(fonts, text.FontRun{Len: r.Len, FontID: id})
		}
		ts := style.DefaultTextStyle()
		shaped := sys.LayoutLine(s, ts.FontSize.ToPixels(w.RemSize()), fonts)
		wrapWidth := bounds.Bounds.Size.Width
		wrapped := text.WrappedLineLayout{Unwrapped: shaped, WrapWidth: &wrapWidth}
		wrapped.WrapBoundaries = sys.WrapBoundaries(shaped, s, wrapWidth, 0)
		pos := geometry.PointPixels(ev.Up.Position.X.AsF32()-bounds.Bounds.Origin.X.AsF32(), ev.Up.Position.Y.AsF32()-bounds.Bounds.Origin.Y.AsF32())
		idx, _, ok := wrapped.IndexForPosition(pos, geometry.Px(ts.LineHeightInPixels(w.RemSize())))
		if !ok {
			return
		}
		for _, link := range links {
			if idx >= link.start && idx < link.end {
				openLink(link.destination)
				return
			}
		}
	})
}

func markdownCodeLanguage(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "golang":
		return "go"
	case "js", "jsx":
		return "javascript"
	case "ts", "tsx":
		return "typescript"
	case "py":
		return "python"
	case "rs":
		return "rust"
	case "sh", "bash", "zsh":
		return "shell"
	case "yml":
		return "yaml"
	case "c++":
		return "cpp"
	}
	return s
}

// renderMarkdownBlocks builds a themed, read-only view of CommonMark and GFM.
// Parsing is cached in markdownView; syntax colors use the editor palette.
func renderMarkdownBlocks(root ast.Node, source []byte, theme ui.Theme, preview bool, basePath string, openLink func(string), width float32, view *markdownView) *elements.Div {
	width = max(float32(1), width)
	content := elements.NewDiv().Flex().FlexCol().FlexShrink(0).W(ui.Px(width)).MinW_0().Gap(ui.DefPx(8))
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		var el gpui.Element
		switch n := node.(type) {
		case *ast.Heading:
			s, runs := markdownInline(n, source, theme, true)
			size := float32(21)
			if preview {
				size = 28
			}
			if n.Level > 1 {
				size -= float32(min(n.Level-1, 4)) * 2
			}
			el = elements.NewDiv().Flex().FlexCol().W(ui.Px(width)).MinW_0().Pt(ui.DefPx(6)).TextSize(ui.AbsPx(size)).Child(elements.NewText(s).WithWidth(geometry.Px(width)).WithRuns(runs))
		case *ast.Paragraph, *ast.TextBlock:
			if image, ok := node.FirstChild().(*ast.Image); ok && image.NextSibling() == nil {
				if img := markdownImage(image, source, basePath, width); img != nil {
					el = img
					break
				}
			}
			el = markdownInlineElement(node, source, theme, false, width, openLink)
		case *ast.FencedCodeBlock:
			el = renderMarkdownCode(string(n.Lines().Value(source)), markdownCodeLanguage(string(n.Language(source))), theme, width, view, fmt.Sprintf("fenced-%d", n.Pos()))
		case *ast.CodeBlock:
			el = renderMarkdownCode(string(n.Lines().Value(source)), "", theme, width, view, fmt.Sprintf("code-%d", n.Pos()))
		case *ast.HTMLBlock:
			el = renderMarkdownCode(string(n.Text(source)), "html", theme, width, view, fmt.Sprintf("html-%d", n.Pos()))
		case *ast.Blockquote:
			el = elements.NewDiv().Flex().FlexCol().W(ui.Px(width)).MinW_0().BorderL_2().BorderColor(theme.Accent.Alpha(0.6)).Pl(ui.DefPx(12)).Child(renderMarkdownBlocks(node, source, theme, preview, basePath, openLink, width-14, view))
		case *ast.List:
			list := elements.NewDiv().Flex().FlexCol().W(ui.Px(width)).MinW_0().Gap(ui.DefPx(4))
			index := n.Start
			for item := n.FirstChild(); item != nil; item = item.NextSibling() {
				marker := "•"
				if n.IsOrdered() {
					marker = fmt.Sprintf("%d.", index)
					index++
				}
				list = list.Child(elements.NewDiv().Flex().W(ui.Px(width)).MinW_0().Gap(ui.DefPx(8)).Child(elements.NewDiv().W(ui.Px(22)).FlexShrink(0).Child(elements.NewText(marker).WithTextColor(theme.Dim))).Child(elements.NewDiv().Flex().FlexCol().Flex1().MinW_0().Child(renderMarkdownBlocks(item, source, theme, preview, basePath, openLink, width-30, view))))
			}
			el = list
		case *ast.ThematicBreak:
			el = elements.NewDiv().H(ui.Px(1)).BgColor(theme.Surface2)
		case *extast.Table:
			table := elements.NewDiv().Flex().FlexCol().W(ui.Px(width)).MinW_0().Border_1().BorderColor(theme.Surface2).Rounded(ui.AbsPx(4)).OverflowHidden()
			for row := node.FirstChild(); row != nil; row = row.NextSibling() {
				r := elements.NewDiv().Flex().W(ui.Px(width - 2)).MinW_0().BorderB_1().BorderColor(theme.Surface1)
				_, header := row.(*extast.TableHeader)
				if header {
					r = r.BgColor(theme.Surface1)
				}
				cellWidth := max(float32(1), (width-2)/float32(max(row.ChildCount(), 1))-16)
				for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
					r = r.Child(elements.NewDiv().Flex().FlexCol().Flex1().MinW_0().Px(ui.DefPx(8)).Py(ui.DefPx(5)).Child(markdownInlineElement(cell, source, theme, header, cellWidth, openLink)))
				}
				table = table.Child(r)
			}
			el = table
		default:
			if node.HasChildren() {
				el = renderMarkdownBlocks(node, source, theme, preview, basePath, openLink, width, view)
			}
		}
		if el != nil {
			content = content.Child(el)
		}
	}
	return content
}

func renderMarkdownCode(code, lang string, theme ui.Theme, width float32, view *markdownView, key string) gpui.Element {
	code = strings.TrimSuffix(code, "\n")
	scroll := view.codeScrolls[key]
	if scroll == nil {
		scroll = &markdownCodeScroll{handle: elements.NewScrollHandle()}
		view.codeScrolls[key] = scroll
	}
	h := scroll.handle
	if h.ViewportSize().Width > 0 {
		scroll.left = min(scroll.left, h.MaxLeft().AsF32())
	}
	h.SetLeft(geometry.Px(scroll.left))
	box := elements.NewDiv().ID(elements.ButtonID("markdown-code-" + key)).Flex().FlexCol().FlexShrink(0).W(ui.Px(width)).MinW_0().P(ui.DefPx(10)).BgColor(theme.Crust).Border_1().BorderColor(theme.Surface1).Rounded(ui.AbsPx(5)).OverflowXScroll().TrackScroll(h).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			delta := ev.Delta.PixelDelta(geometry.Px(24))
			dx := delta.X.AsF32()
			if ev.Modifiers.Shift && dx == 0 {
				dx = delta.Y.AsF32()
			}
			if dx != 0 && h.MaxLeft() > 0 {
				scroll.left = max(float32(0), min(h.MaxLeft().AsF32(), scroll.left-dx))
				h.SetLeft(geometry.Px(scroll.left))
				w.StopPropagation()
				w.Refresh()
			}
		})
	if lang != "" {
		box = box.Child(elements.NewDiv().TextSize(ui.AbsPx(11)).Pb(ui.DefPx(5)).Child(elements.NewText(lang).WithTextColor(theme.Dim)))
	}
	for _, line := range strings.Split(code, "\n") {
		display := editor.DisplayLine(line)
		if display == "" {
			display = " "
		}
		runs := editor.HighlightCached(display, lang, editor.PaletteOfTheme(theme))
		lineWidth := float32(editor.DisplayCols(line))*8 + 4
		if view.measureCode != nil {
			lineWidth = view.measureCode(display) + 4
		}
		box = box.Child(elements.NewDiv().W(ui.Px(max(float32(1), lineWidth))).FlexShrink(0).WhitespaceNowrap().Child(elements.NewText(display).WithRuns(runs)))
	}
	outer := elements.NewDiv().Flex().FlexCol().W(ui.Px(width)).MinW_0().Child(box)
	maxLeft, viewport, total := h.MaxLeft().AsF32(), h.ViewportSize().Width.AsF32(), h.ContentSize().Width.AsF32()
	if maxLeft > 0 && viewport > 0 {
		thumb := min(width, max(float32(24), width*viewport/total))
		travel := width - thumb
		left := float32(0)
		if travel > 0 {
			left = scroll.left / maxLeft * travel
		}
		seek := func(x float32) {
			if travel > 0 {
				scroll.left = max(float32(0), min(float32(1), (x-thumb/2)/travel)) * maxLeft
				h.SetLeft(geometry.Px(scroll.left))
			}
		}
		track := elements.NewDiv().ID(elements.ButtonID("markdown-code-scrollbar-" + key)).Relative().W(ui.Px(width)).H(ui.Px(9)).
			OnMouseDownCapture(func(ev platform.MouseDownEvent, w *gpui.Window) {
				if ev.Button != platform.ButtonLeft {
					return
				}
				if hb, ok := w.ListenerHitbox(); ok {
					seek(ev.Position.X.AsF32() - hb.Bounds.Origin.X.AsF32())
					w.CapturePointer(hb.ID)
				}
				w.StopPropagation()
				w.Refresh()
			}).
			OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
				if !ev.Dragging() {
					return
				}
				if hb, ok := w.ListenerHitbox(); ok {
					seek(ev.Position.X.AsF32() - hb.Bounds.Origin.X.AsF32())
				}
				w.StopPropagation()
				w.Refresh()
			}).
			OnMouseUp(func(_ platform.MouseUpEvent, w *gpui.Window) { w.ReleasePointer(); w.Refresh() }).
			Child(elements.NewDiv().Absolute().Left(ui.Px(left)).Top(ui.Px(2)).W(ui.Px(thumb)).H(ui.Px(5)).Rounded(ui.AbsPx(3)).BgColor(theme.Surface2))
		outer = outer.Child(track)
	}
	return outer
}

func markdownImage(n *ast.Image, source []byte, basePath string, width float32) gpui.Element {
	dest := string(n.Destination)
	u, err := url.Parse(dest)
	if err != nil {
		return nil
	}
	var img *elements.Img
	switch u.Scheme {
	case "https", "http", "data":
		img = elements.NewImgFromURI(dest)
	case "":
		if basePath == "" {
			return nil
		}
		path := filepath.FromSlash(u.Path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(basePath), path)
		}
		img = elements.NewImgFromPath(path)
	default:
		return nil
	}
	return elements.NewDiv().H(ui.Px(220)).Child(img.W(ui.Px(min(float32(360), width))).H(ui.Px(210)))
}

func markdownLinkHandler(ws *Workspace, basePath string, update updateFn, w *gpui.Window) func(string) {
	return func(destination string) {
		u, err := url.Parse(destination)
		if err != nil {
			return
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "mailto":
			if ws.openURL != nil {
				if err := ws.openURL(destination); err != nil {
					update(func(s *Workspace) { s.StatusError = err.Error() })
				}
			}
		case "":
			if u.Path == "" || basePath == "" {
				return
			}
			path := filepath.FromSlash(u.Path)
			if !filepath.IsAbs(path) {
				path = filepath.Join(filepath.Dir(basePath), path)
			}
			update(func(s *Workspace) {
				if err := s.OpenPath(path); err != nil {
					s.StatusError = err.Error()
				}
			})
			w.Focus(ws.FocusEditor)
			w.Refresh()
		}
	}
}

// markdownScroll keeps offsets across frame rebuilds and exposes a draggable
// scrollbar when the content is taller than its viewport.
func markdownScroll(view *markdownView, content gpui.Element, theme ui.Theme, id string, focus gpui.FocusHandle, onSave func()) gpui.Element {
	h := view.scroll
	if h.ViewportSize().Height > 0 {
		view.top = min(view.top, h.MaxTop().AsF32())
	}
	h.SetTop(geometry.Px(view.top))
	list := elements.NewDiv().ID(elements.ButtonID(id + "-scroll")).Flex().FlexCol().Flex1().MinW_0().MinH_0().OverflowYScroll().TrackScroll(h).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			dy := ev.Delta.PixelDelta(geometry.Px(24)).Y.AsF32()
			if dy != 0 {
				view.top = max(float32(0), min(h.MaxTop().AsF32(), view.top-dy))
				h.SetTop(geometry.Px(view.top))
				w.StopPropagation()
				w.Refresh()
			}
		}).
		Child(content)
	container := elements.NewDiv().Flex().Flex1().MinW_0().MinH_0().Relative().OverflowHidden().Child(list)
	if focus.IsValid() {
		container = container.TrackFocus(focus).OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
			mods := ev.Keystroke.Modifiers
			if (mods.Control || mods.Platform) && !mods.Alt && ev.Keystroke.Key == "s" && onSave != nil {
				onSave()
				w.StopPropagation()
				w.Refresh()
				return
			}
			if mods.Control || mods.Platform || mods.Alt {
				return
			}
			step := float32(0)
			switch ev.Keystroke.Key {
			case "up":
				step = -24
			case "down":
				step = 24
			case "pageup":
				step = -h.ViewportSize().Height.AsF32() * 0.9
			case "pagedown", "space":
				step = h.ViewportSize().Height.AsF32() * 0.9
			case "home":
				view.top = 0
			case "end":
				view.top = h.MaxTop().AsF32()
			default:
				return
			}
			if ev.Keystroke.Key == "space" && ev.Keystroke.Modifiers.Shift {
				step = -step
			}
			view.top = max(float32(0), min(h.MaxTop().AsF32(), view.top+step))
			h.SetTop(geometry.Px(view.top))
			w.StopPropagation()
			w.Refresh()
		})
	}
	viewport, total := h.ViewportSize().Height.AsF32(), h.ContentSize().Height.AsF32()
	maxTop := h.MaxTop().AsF32()
	if maxTop > 0 && viewport > 0 {
		thumb := max(float32(22), viewport*viewport/total)
		thumb = min(thumb, viewport)
		travel := viewport - thumb
		top := float32(0)
		if travel > 0 {
			top = h.Top().AsF32() / maxTop * travel
		}
		seek := func(ev platform.MouseDownEvent, w *gpui.Window) {
			if hb, ok := w.ListenerHitbox(); ok && travel > 0 {
				ratio := (ev.Position.Y.AsF32() - hb.Bounds.Origin.Y.AsF32() - thumb/2) / travel
				view.top = max(float32(0), min(float32(1), ratio)) * maxTop
				h.SetTop(geometry.Px(view.top))
				w.CapturePointer(hb.ID)
			}
			w.StopPropagation()
			w.Refresh()
		}
		track := elements.NewDiv().ID(elements.ButtonID(id + "-scrollbar")).Absolute().Right(ui.Px(0)).Top(ui.Px(0)).W(ui.Px(10)).H(ui.Px(viewport)).
			OnMouseDownCapture(func(ev platform.MouseDownEvent, w *gpui.Window) {
				if ev.Button == platform.ButtonLeft {
					seek(ev, w)
				}
			}).
			OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
				if !ev.Dragging() {
					return
				}
				if hb, ok := w.ListenerHitbox(); ok && travel > 0 {
					ratio := (ev.Position.Y.AsF32() - hb.Bounds.Origin.Y.AsF32() - thumb/2) / travel
					view.top = max(float32(0), min(float32(1), ratio)) * maxTop
					h.SetTop(geometry.Px(view.top))
				}
				w.StopPropagation()
				w.Refresh()
			}).
			OnMouseUp(func(_ platform.MouseUpEvent, w *gpui.Window) { w.ReleasePointer(); w.Refresh() }).
			Child(elements.NewDiv().Absolute().Left(ui.Px(2)).Top(ui.Px(top)).W(ui.Px(6)).H(ui.Px(thumb)).Rounded(ui.AbsPx(3)).BgColor(theme.Surface2))
		container = container.Child(track)
	}
	return container
}
