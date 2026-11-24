package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/font"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
	"github.com/yuin/goldmark/ast"
)

func TestMarkdownFormattingAndCode(t *testing.T) {
	v := newMarkdownView("# Heading\n\nHello **bold** *italic* `code` [link](https://example.com).\n\n```go\nfunc main() {}\n```\n")
	if _, ok := v.root.FirstChild().(*ast.Heading); !ok {
		t.Fatal("heading not parsed")
	}
	p := v.root.FirstChild().NextSibling()
	s, runs := markdownInline(p, v.source, ui.DefaultTheme(), false)
	if s != "Hello bold italic code link." {
		t.Fatalf("inline text = %q", s)
	}
	bold, italic, code, link := false, false, false, false
	for _, r := range runs {
		bold = bold || r.Font.Weight == font.WeightBold
		italic = italic || r.Font.Style == font.StyleItalic
		code = code || r.Font.Family == "Monospace"
		link = link || r.Underline != nil
	}
	if !bold || !italic || !code || !link {
		t.Fatalf("inline styles missing: bold=%v italic=%v code=%v link=%v", bold, italic, code, link)
	}
	block, ok := p.NextSibling().(*ast.FencedCodeBlock)
	if !ok || string(block.Language(v.source)) != "go" {
		t.Fatal("fenced Go block not parsed")
	}
	colors := editor.Highlight("func main() {}", "go", editor.PaletteOfTheme(ui.DefaultTheme()))
	if len(colors) < 2 || colors[0].Color == colors[1].Color {
		t.Fatal("Go code has no syntax highlighting")
	}
}

func TestMarkdownReadmeFitsAndScrollsWithSidebar(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	// Use a fixture so this rendering and navigation test doesn't depend on
	// the project's README wording, length, or placement of its links.
	root := t.TempDir()
	readme := filepath.Join(root, "README.md")
	guide := filepath.Join(root, "docs", "tutorial.md")
	introText := "A long introduction to the example. "
	source := "# Sample project\n\n" + strings.Repeat(introText, 12) +
		"\n\n| Guide | Contents |\n| --- | --- |\n| [docs/tutorial.md](docs/tutorial.md) | Tutorial |\n\n" +
		strings.Repeat("Another paragraph to exercise vertical scrolling.\n\n", 80)
	if err := os.WriteFile(readme, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(guide), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guide, []byte("# Tutorial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, scale := range []float32{1, 1.25} {
		t.Run(fmt.Sprintf("scale-%.2f", scale), func(t *testing.T) {
			hp := headless.New(geometry.SizePixels(1000, 650))
			application := gpui.NewApplication(hp)
			handle, err := gpui.OpenWindow[Workspace](application, platform.WindowOptions{Title: "readme", ScaleFactor: scale}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
				ws := New("readme")
				ws.ShowTree = true
				ws.Tree = NewTree(root)
				ws.Buffers = []*editor.Buffer{editor.NewBuffer(readme, "README.md", source)}
				ws.Measure(w)
				ws.toggleMarkdownPreview(ws.Buf())
				w.Focus(ws.FocusEditor)
				return *ws
			})
			if err != nil {
				t.Fatal(err)
			}
			handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
			handle.Window.Draw()
			handle.Window.Draw()
			ws := handle.View.Read(application.App)
			view := ws.markdownPreview[ws.Buf()].view
			h := view.scroll
			t.Logf("viewport=%v content=%v maxTop=%v", h.ViewportSize(), h.ContentSize(), h.MaxTop())
			if h.ContentSize().Width.AsF32() > h.ViewportSize().Width.AsF32()+1 {
				t.Errorf("preview content overflows horizontally: %v > %v", h.ContentSize().Width, h.ViewportSize().Width)
			}
			if h.MaxTop() <= 0 {
				t.Fatal("README has no vertical scroll range")
			}
			var intro *gpui.AccessibilityNode
			for i := range handle.Window.RecordedAccessibility().Nodes {
				node := &handle.Window.RecordedAccessibility().Nodes[i]
				if strings.HasPrefix(node.Label, introText) {
					intro = node
					break
				}
			}
			if intro == nil {
				t.Fatal("README intro not rendered")
			}
			t.Logf("intro bounds: %v", intro.Bounds)
			if intro.Bounds.Size.Width.AsF32() > h.ViewportSize().Width.AsF32()+1 {
				t.Errorf("paragraph overflowed viewport: %v", intro.Bounds)
			}
			if intro.Bounds.Size.Height.AsF32() < 35 {
				t.Errorf("long paragraph did not wrap: %v", intro.Bounds)
			}
			hw := hp.LastWindow()
			hw.DispatchInput(platform.ScrollEvent{Position: geometry.PointPixels(500, 200), Delta: platform.ScrollDeltaPixels(0, geometry.Px(-300))})
			handle.Window.Draw()
			handle.Window.Draw()
			if h.Top().AsF32() < 200 {
				t.Errorf("wheel scroll lost after redraw: top=%v", h.Top())
			}
			before := h.Top().AsF32()
			hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "pagedown"}})
			handle.Window.Draw()
			if h.Top().AsF32() <= before {
				t.Errorf("Page Down did not scroll preview: before=%v after=%v", before, h.Top())
			}
			// README table links remain interactive after wrapping and scrolling.
			hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "home"}})
			handle.Window.Draw()
			handle.Window.Draw()
			node, _, ok := handle.Window.RecordedAccessibility().FindByLabel("docs/tutorial.md")
			if !ok {
				t.Fatal("README table link not rendered")
			}
			p := geometry.PointPixels(node.Bounds.Origin.X.AsF32()+4, node.Bounds.Origin.Y.AsF32()+8)
			hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
			hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
			if got := handle.View.Read(application.App).Buf().Path; !strings.EqualFold(got, guide) {
				t.Fatalf("table link opened %q", got)
			}
		})
	}
}

func TestMarkdownLongCodeScrollsHorizontally(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	source := "# Code\n\n```go\n" + strings.Repeat("longIdentifier", 80) + "\n```\n\n```go\nshort()\n```\n\n" + strings.Repeat("Paragraph for vertical scrolling.\n\n", 40)
	hp := headless.New(geometry.SizePixels(800, 600))
	application := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](application, platform.WindowOptions{Title: "code scroll"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("code scroll")
		ws.Buffers = []*editor.Buffer{editor.NewBuffer("", "long.md", source)}
		ws.Measure(w)
		ws.toggleMarkdownPreview(ws.Buf())
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
	handle.Window.Draw()
	handle.Window.Draw()
	ws := handle.View.Read(application.App)
	view := ws.markdownPreview[ws.Buf()].view
	if len(view.codeScrolls) != 2 {
		t.Fatalf("expected separate code scroll states, got %d", len(view.codeScrolls))
	}
	var code *markdownCodeScroll
	for _, s := range view.codeScrolls {
		if s.handle.MaxLeft() > 0 {
			code = s
		}
	}
	if code == nil {
		t.Fatal("long code has no horizontal scroll range")
	}
	var box gpui.Hitbox
	for _, hb := range handle.Window.RenderedHitboxes() {
		if strings.Contains(hb.Key, "markdown-code-fenced-") {
			box = hb
			break
		}
	}
	if box.Bounds.Size.Width == 0 {
		t.Fatal("code block hitbox not found")
	}
	hw := hp.LastWindow()
	hw.DispatchInput(platform.ScrollEvent{Position: geometry.PointPixels(box.Bounds.Origin.X.AsF32()+40, box.Bounds.Origin.Y.AsF32()+20), Delta: platform.ScrollDeltaPixels(0, geometry.Px(-150)), Modifiers: keymap.Modifiers{Shift: true}})
	handle.Window.Draw()
	handle.Window.Draw()
	if code.handle.Left().AsF32() < 100 {
		t.Fatalf("Shift+wheel did not scroll code after redraw: %v", code.handle.Left())
	}
	if view.scroll.Top() != 0 {
		t.Fatalf("horizontal code scroll also moved document: %v", view.scroll.Top())
	}
	var track gpui.Hitbox
	for _, hb := range handle.Window.RenderedHitboxes() {
		if strings.Contains(hb.Key, "markdown-code-scrollbar-fenced-") {
			track = hb
			break
		}
	}
	if track.Bounds.Size.Width == 0 {
		t.Fatal("long code scrollbar is not visible")
	}
	x := track.Bounds.Origin.X.AsF32() + track.Bounds.Size.Width.AsF32() - 2
	y := track.Bounds.Origin.Y.AsF32() + track.Bounds.Size.Height.AsF32()/2
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x, y)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x, y)})
	handle.Window.Draw()
	handle.Window.Draw()
	if code.handle.Left().AsF32() < code.handle.MaxLeft().AsF32()*0.9 {
		t.Fatalf("code scrollbar did not seek to end: %v / %v", code.handle.Left(), code.handle.MaxLeft())
	}
}

func TestMarkdownPreviewAndDocumentationScroll(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	var content strings.Builder
	content.WriteString("# Long document\n\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&content, "Paragraph %d with **important** content.\n\n", i)
	}
	hp := headless.New(geometry.SizePixels(1000, 650))
	application := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](application, platform.WindowOptions{Title: "markdown"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("markdown")
		ws.Buffers = []*editor.Buffer{editor.NewBuffer("", "README.md", content.String())}
		ws.Measure(w)
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
	handle.Window.Draw()
	hw := hp.LastWindow()
	// The button sits at the far right of the tab strip.
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(955, 45)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(955, 45)})
	handle.Window.Draw()
	ws := handle.View.Read(application.App)
	state := ws.markdownPreview[ws.Buf()]
	if state == nil || !state.enabled || state.view == nil {
		t.Fatal("markdown preview button did not open preview")
	}
	if state.view.scroll.MaxTop() <= 0 {
		t.Fatalf("preview not scrollable: content=%v viewport=%v", state.view.scroll.ContentSize(), state.view.scroll.ViewportSize())
	}
	hw.DispatchInput(platform.ScrollEvent{Position: geometry.PointPixels(500, 200), Delta: platform.ScrollDeltaPixels(0, geometry.Px(-120))})
	handle.Window.Draw()
	if state.view.scroll.Top() <= 0 {
		t.Fatal("preview wheel did not scroll")
	}
	// Return to source, then open a long documentation popup at a known position.
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(955, 45)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(955, 45)})
	if state.enabled {
		t.Fatal("preview button did not return to source")
	}
	handle.Window.Draw()
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "x", KeyChar: "x"}})
	if !strings.HasPrefix(handle.View.Read(application.App).Buf().Text(), "x# Long document") {
		t.Fatal("source did not regain keyboard focus")
	}
	handle.View.Update(application.App, func(s *Workspace, c *app.Context[Workspace]) any {
		s.documentation = &documentationState{buffer: s.Buf(), text: content.String(), x: 100, y: 100}
		c.Notify()
		return nil
	})
	handle.Window.Draw()
	doc := handle.View.Read(application.App).documentation
	if doc.view == nil || doc.view.scroll.MaxTop() <= 0 {
		t.Fatal("documentation is not scrollable")
	}
	if doc.view.scroll.ViewportSize().Width.AsF32() > 520 {
		t.Fatalf("documentation content overflowed popup: %v", doc.view.scroll.ViewportSize())
	}
	hw.DispatchInput(platform.ScrollEvent{Position: geometry.PointPixels(200, 200), Delta: platform.ScrollDeltaPixels(0, geometry.Px(-120))})
	handle.Window.Draw()
	if doc.view.scroll.Top() <= 0 {
		t.Fatalf("documentation wheel did not scroll: content=%v viewport=%v", doc.view.scroll.ContentSize(), doc.view.scroll.ViewportSize())
	}
	// Dragging the scrollbar reaches content far below the first page.
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(615, 430)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(615, 430)})
	handle.Window.Draw()
	if doc.view.scroll.Top() < 500 {
		t.Fatalf("documentation scrollbar did not seek: %v", doc.view.scroll.Top())
	}
	var opened string
	handle.View.Update(application.App, func(s *Workspace, c *app.Context[Workspace]) any {
		s.openURL = func(url string) error { opened = url; return nil }
		s.documentation = &documentationState{buffer: s.Buf(), text: "[Example](https://example.com)", x: 100, y: 100}
		c.Notify()
		return nil
	})
	handle.Window.Draw()
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(120, 170)})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(120, 170)})
	if opened != "https://example.com" {
		t.Fatalf("documentation link opened %q", opened)
	}
}
