package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/lsp"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/font"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
	"github.com/FlameInTheDark/go-gpui/style"
	gptext "github.com/FlameInTheDark/go-gpui/text"
)

func TestCompletionEditsAndUndo(t *testing.T) {
	text := "package main\n\nfunc main() { fmt.Pr }\n"
	b := editor.NewBuffer("main.go", "main.go", text)
	b.SetCursor(strings.Index(text, "Pr") + 2)
	mainEdit, _ := json.Marshal(lsp.TextEdit{Range: lsp.Range{Start: lsp.PositionAt(text, b.Cursor-2), End: lsp.PositionAt(text, b.Cursor)}, NewText: "Println"})
	item := lsp.CompletionItem{Label: "Println", TextEdit: mainEdit, AdditionalTextEdits: []lsp.TextEdit{{Range: lsp.Range{Start: lsp.Position{Line: 1}, End: lsp.Position{Line: 1}}, NewText: "import \"fmt\"\n"}}}
	if err := completionEdits(b, item); err != nil {
		t.Fatal(err)
	}
	want := "package main\nimport \"fmt\"\n\nfunc main() { fmt.Println }\n"
	if b.Text() != want {
		t.Fatalf("got %q", b.Text())
	}
	if b.Cursor != strings.Index(want, "Println")+len("Println") {
		t.Fatalf("cursor = %d", b.Cursor)
	}
	if !b.Undo() || b.Text() != text {
		t.Fatalf("completion and import must undo together: %q", b.Text())
	}
}

func TestCompletionRejectsBadEdits(t *testing.T) {
	text := "hello"
	b := editor.NewBuffer("", "", text)
	b.SetCursor(len(text))
	item := lsp.CompletionItem{Label: "help", AdditionalTextEdits: []lsp.TextEdit{{Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 1}, End: lsp.Position{Line: 0, Character: 3}}, NewText: "x"}}}
	if err := completionEdits(b, item); err == nil {
		t.Fatal("overlapping edit accepted")
	}
	if b.Text() != text {
		t.Fatal("invalid edit changed buffer")
	}
	item.AdditionalTextEdits[0].Range.Start.Line = 100
	if err := completionEdits(b, item); err == nil {
		t.Fatal("invalid line accepted")
	}
}

func TestStaleCompletionCannotBeAccepted(t *testing.T) {
	b := editor.NewBuffer("", "a.go", "foo")
	b.SetCursor(3)
	ws := &Workspace{Buffers: []*editor.Buffer{b}, completion: &completionState{buffer: b, revision: b.Revision, items: []lsp.CompletionItem{{Label: "foobar"}}}}
	b.Insert("x")
	ws.acceptCompletion(0)
	if b.Text() != "foox" || ws.completion != nil {
		t.Fatal("stale completion was accepted")
	}
}

func TestDocumentationSymbolAt(t *testing.T) {
	b := editor.NewBuffer("", "main.go", "foo.Привет(bar)\n")
	start := strings.Index(b.Text(), "Привет")
	if got := symbolAt(b, start+len("При")); got != "Привет" {
		t.Fatalf("Unicode symbol = %q", got)
	}
	if got := symbolAt(b, strings.Index(b.Text(), "(")); got != "" {
		t.Fatalf("punctuation selected %q", got)
	}
}

func TestNavigateDefinitionUsesOpenBufferAndUTF16(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "target ü.go")
	if err := os.WriteFile(path, []byte("disk contents\n"), 0644); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("// filler\n", 80) + "// 😀 Café target\n"
	buf := editor.NewBuffer(path, "target ü.go", text)
	buf.SetCursor(buf.TextLen())
	buf.Insert("// unsaved\n")
	other := editor.NewBuffer(filepath.Join(dir, "other.go"), "other.go", "package p\n")
	ws := New("definition")
	ws.WindowSize = geometry.SizePixels(800, 500)
	ws.Buffers = []*editor.Buffer{buf, other}
	ws.Active = 1
	offset := strings.Index(buf.Text(), "Café")
	pos := lsp.PositionAt(buf.Text(), offset)
	loc := lsp.Location{URI: lsp.FileURI(path), Range: lsp.Range{Start: pos, End: pos}}
	if err := ws.navigateDefinition([]lsp.Location{loc}); err != nil {
		t.Fatal(err)
	}
	if ws.Buf() != buf || buf.Cursor != offset || !buf.Dirty || len(ws.Buffers) != 2 {
		t.Fatalf("open buffer not preserved: cursor=%d dirty=%v tabs=%d", buf.Cursor, buf.Dirty, len(ws.Buffers))
	}
	line, _ := buf.OffsetToLC(offset)
	visible := (editor.Metrics{LineHeight: ws.LineHeight}).VisibleLines(ws.paneHeight())
	if line < ws.scroll() || line >= ws.scroll()+visible {
		t.Fatalf("definition line %d is not visible at scroll %d", line, ws.scroll())
	}
	ws.Active = 1
	bad := lsp.Location{URI: lsp.FileURI(path), Range: lsp.Range{Start: lsp.Position{Line: 999}, End: lsp.Position{Line: 999}}}
	if err := ws.navigateDefinition([]lsp.Location{{URI: "https://example.com/a.go"}, bad}); err == nil {
		t.Fatal("invalid definitions accepted")
	}
	if ws.Buf() != other || len(ws.Buffers) != 2 {
		t.Fatal("failed navigation changed active buffer")
	}
}

func TestLanguageOverlayKeyboardAndCtrlClick(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp := headless.New(geometry.SizePixels(1000, 650))
	application := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](application, platform.WindowOptions{Title: "language test"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("test")
		b := editor.NewBuffer("", "test.go", "Gre")
		b.SetCursor(3)
		ws.Buffers = []*editor.Buffer{b}
		ws.Cfg.Editor.FontSize = 20
		ws.Measure(w)
		w.Focus(ws.FocusEditor)
		ws.completion = &completionState{buffer: b, revision: b.Revision, cursor: b.Cursor, items: []lsp.CompletionItem{{Label: "Greet"}, {Label: "Green"}}}
		group := &languageServer{queue: make(chan languageRequest, 1), documents: make(map[*editor.Buffer]*languageDoc)}
		doc := &languageDoc{server: group, uri: "file:///test.go", text: b.Text()}
		group.documents[b] = doc
		ws.languageDocs = map[*editor.Buffer]*languageDoc{b: doc}
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
	handle.Window.Draw()
	hw := hp.LastWindow()
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "down"}})
	handle.Window.Draw()
	if got := handle.View.Read(application.App).completion.selected; got != 1 {
		t.Fatalf("selected = %d", got)
	}
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "enter"}})
	handle.Window.Draw()
	ws := handle.View.Read(application.App)
	if ws.Buf().Text() != "Green" || ws.completion != nil {
		t.Fatalf("completion not inserted: %q", ws.Buf().Text())
	}
	// Drain the post-completion synchronization, then Ctrl-click the first word.
	doc := ws.languageDocs[ws.Buf()]
	<-doc.server.queue
	x := editor.GutterWidth(ws.Buf().LineCount(), ws.CharAdvance) + ws.CharAdvance*1.2
	y := float32(ui.MenuBarHeight+ui.TabBarHeight) + ws.LineHeight/2
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x, y), Modifiers: keymap.Modifiers{Control: true}})
	select {
	case req := <-doc.server.queue:
		if req.kind != "definition" || req.offset > 2 || req.symbol != "Green" {
			t.Fatalf("unexpected definition request: %+v", req)
		}
	default:
		t.Fatal("Ctrl-click did not request a definition")
	}
	// Clicking empty space must not request a definition for the last word.
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x+ws.CharAdvance*30, y), Modifiers: keymap.Modifiers{Control: true}})
	select {
	case req := <-doc.server.queue:
		t.Fatalf("blank-space click requested a definition: %+v", req)
	default:
	}
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x, y), Modifiers: keymap.Modifiers{Control: true}})
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "q", Modifiers: keymap.Modifiers{Control: true}}})
	select {
	case req := <-doc.server.queue:
		if req.kind != "hover" || req.symbol != "Green" || req.offset != len("Green")-1 {
			t.Fatalf("unexpected Ctrl+Q request: %+v", req)
		}
	default:
		t.Fatal("Ctrl+Q did not request documentation at the caret")
	}
	handle.View.Update(application.App, func(s *Workspace, c *app.Context[Workspace]) any {
		s.documentation = &documentationState{buffer: s.Buf(), text: "func Green()\n\nA useful symbol.", x: 120, y: 100}
		c.Notify()
		return nil
	})
	handle.Window.Draw()
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "escape"}})
	if handle.View.Read(application.App).documentation != nil {
		t.Fatal("Escape did not close documentation")
	}
}

func TestDocumentationPopupOwnsPointerInput(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp := headless.New(geometry.SizePixels(800, 600))
	a := gpui.NewApplication(hp)
	h, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{}, func(w *gpui.Window, _ *app.Context[Workspace]) Workspace {
		ws := New("docs input")
		b := editor.NewBuffer("", "main.go", strings.Repeat("var example = 42\n", 250))
		ws.Buffers = []*editor.Buffer{b}
		ws.Measure(w)
		w.Focus(ws.FocusEditor)
		ws.documentation = &documentationState{
			buffer: b, x: 180, y: 90, symbol: strings.Repeat("VeryLongSymbol", 8),
			text: strings.Repeat("A paragraph of documentation.\n\n", 50),
		}
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	w := h.Window
	w.SetRootView(h.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return h.View.Read(c).Render(&h, c, w) })
	w.Draw()
	w.Draw()
	ws := h.View.Read(a.App)
	view := ws.documentation.view
	if view.scroll.MaxTop() <= 0 {
		t.Fatal("documentation does not have a scroll range")
	}
	center := func(id string) geometry.Point[geometry.Pixels] {
		t.Helper()
		for _, hb := range w.RenderedHitboxes() {
			if hb.Key == "n:btn-"+id {
				return geometry.PointPixels(hb.Bounds.Origin.X.AsF32()+hb.Bounds.Size.Width.AsF32()/2, hb.Bounds.Origin.Y.AsF32()+hb.Bounds.Size.Height.AsF32()/2)
			}
		}
		t.Fatalf("missing hitbox %s", id)
		return geometry.Point[geometry.Pixels]{}
	}
	hw := hp.LastWindow()
	wheel := func(p geometry.Point[geometry.Pixels], dy float32) {
		hw.DispatchInput(platform.ScrollEvent{Position: p, Delta: platform.ScrollDeltaPixels(0, geometry.Px(dy))})
		w.Draw()
	}
	wheel(center("documentation-close"), -150) // header has no scroll handler
	if ws.scroll() != 0 || view.top != 0 {
		t.Fatal("header wheel leaked into either scroll pane")
	}
	wheel(center("documentation-scroll"), -150)
	if ws.scroll() != 0 || view.top < 100 {
		t.Fatalf("docs scroll = %v, editor scroll = %v", view.top, ws.scroll())
	}
	wheel(center("documentation-scroll"), -10000)
	wheel(center("documentation-scroll"), -150)
	if ws.scroll() != 0 || view.top != view.scroll.MaxTop().AsF32() {
		t.Fatal("wheel at the end of the documentation leaked into the editor")
	}
	wheel(geometry.PointPixels(110, 170), -150)
	if ws.scroll() <= 0 {
		t.Fatal("wheel outside the popup did not reach the editor")
	}
	cursor := ws.Buf().Cursor
	p := center("documentation-close")
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
	w.Draw()
	if ws.documentation != nil || ws.Buf().Cursor != cursor {
		t.Fatal("close was intercepted by the editor underneath")
	}
}

// Exercise the workspace worker, UI dispatch, snapshots and real server together.
func TestWorkspaceWithGopls(t *testing.T) {
	if _, err := config.ResolveLanguageServerCommand("gopls"); err != nil {
		t.Skip("gopls not installed:", err)
	}
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	text := "package main\n\n// Greet says hello.\nfunc Greet() { println(\"INTERNAL_SOURCE_SENTINEL\") }\n\n// Farewell says goodbye.\nfunc Farewell() {}\nfunc main() { Gre; Farewell() }\n"
	otherPath := filepath.Join(dir, "other.go")
	other := "package main\nfunc use() { Shar; Helper() }\n"
	helperPath := filepath.Join(dir, "helper.go")
	helper := "package main\n\n// Helper is defined in another file.\nfunc Helper() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/demo\n\ngo 1.23\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte(other), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helperPath, []byte(helper), 0644); err != nil {
		t.Fatal(err)
	}
	hp := headless.New(geometry.SizePixels(1000, 650))
	application := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](application, platform.WindowOptions{Title: "gopls test"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("test")
		ws.Measure(w)
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
	dispatch := make(chan func(), 32)
	update := func(f func(*Workspace)) {
		handle.View.Update(application.App, func(s *Workspace, c *app.Context[Workspace]) any { f(s); c.Notify(); return nil })
	}
	update(func(s *Workspace) {
		s.SetEnv(nil, func(f func()) { dispatch <- f }, nil)
		s.SetDialogs(update, nil, nil)
		s.OpenFile(path)
		b := s.Buf()
		b.SetCursor(strings.LastIndex(text, "Gre") + 3)
		s.requestLanguage(b, "completion", b.Cursor, 0, 0)
	})
	defer update(func(s *Workspace) { s.closeLanguages() })
	deadline := time.After(30 * time.Second)
	wait := func(ready func(*Workspace) bool) {
		t.Helper()
		for !ready(handle.View.Read(application.App)) {
			select {
			case f := <-dispatch:
				f()
			case <-deadline:
				t.Fatal("timed out waiting for gopls")
			}
		}
	}
	wait(func(s *Workspace) bool { return s.completion != nil })
	ws := handle.View.Read(application.App)
	found := false
	for _, item := range ws.completion.items {
		if strings.HasPrefix(item.Label, "Greet") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("workspace did not receive Greet completion")
	}
	update(func(s *Workspace) {
		b := s.Buf()
		s.completion = nil
		s.requestLanguage(b, "hover", strings.Index(text, "func Greet")+len("func ")+2, 120, 100)
	})
	wait(func(s *Workspace) bool { return s.documentation != nil })
	if !strings.Contains(handle.View.Read(application.App).documentation.text, "Greet says hello") {
		t.Fatal("workspace did not receive documentation")
	}
	// Ctrl-click the second function's call through the real code pane.
	handle.Window.Draw()
	ws = handle.View.Read(application.App)
	line, col := ws.Buf().OffsetToLC(strings.LastIndex(text, "Farewell") + 2)
	lineText := ws.Buf().Line(line)
	sys := handle.Window.TextSystem()
	fid, _ := sys.FontID(font.Font{Family: "Monospace", Weight: font.WeightNormal})
	layout := sys.LayoutLine(lineText, style.DefaultTextStyle().FontSize.ToPixels(handle.Window.RemSize()), []gptext.FontRun{{Len: len(lineText), FontID: fid}})
	x := editor.GutterWidth(ws.Buf().LineCount(), ws.CharAdvance) + layout.XForIndex(col).AsF32() + 1
	y := float32(ui.MenuBarHeight+ui.TabBarHeight) + (float32(line)+0.5)*ws.LineHeight
	update(func(s *Workspace) { s.documentation = nil })
	handle.Window.Draw()
	hw := hp.LastWindow()
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x, y), Modifiers: keymap.Modifiers{Control: true}})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x, y), Modifiers: keymap.Modifiers{Control: true}})
	wantFarewell := strings.Index(text, "func Farewell") + len("func ")
	wait(func(s *Workspace) bool { return s.Buf().Cursor == wantFarewell })
	if len(handle.View.Read(application.App).Buffers) != 1 {
		t.Fatal("same-file definition opened a duplicate tab")
	}
	// Ctrl+Q requests the selected definition's documentation at the caret.
	handle.Window.Draw()
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "q", Modifiers: keymap.Modifiers{Control: true}}})
	wait(func(s *Workspace) bool { return s.documentation != nil })
	doc := handle.View.Read(application.App).documentation
	if doc.symbol != "Farewell" || !strings.Contains(doc.text, "Farewell says goodbye") || strings.Contains(doc.text, "INTERNAL_SOURCE_SENTINEL") || strings.Contains(doc.text, "Greet says hello") {
		t.Fatalf("wrong symbol documentation (%s): %s", doc.symbol, doc.text)
	}
	original := handle.View.Read(application.App).Buf()
	update(func(s *Workspace) {
		b := s.Buf()
		b.SetCursor(b.TextLen())
		s.Edit(func(b *editor.Buffer) { b.Insert("\n// SharedUnsaved is not on disk.\nfunc SharedUnsaved() {}\n") })
		s.OpenFile(otherPath)
		b = s.Buf()
		b.SetCursor(strings.Index(other, "Shar") + 4)
		s.requestLanguage(b, "completion", b.Cursor, 0, 0)
	})
	if len(handle.View.Read(application.App).languageServers) != 1 {
		t.Fatal("files in the same root did not share a server")
	}
	wait(func(s *Workspace) bool { return s.completion != nil })
	found = false
	for _, item := range handle.View.Read(application.App).completion.items {
		if strings.HasPrefix(item.Label, "SharedUnsaved") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("completion did not see unsaved symbol in another tab")
	}
	// A definition in an already-open dirty tab must reuse the in-memory buffer.
	update(func(s *Workspace) {
		b := s.Buf()
		start := strings.Index(b.Text(), "Shar")
		b.Anchor = start
		b.Cursor = start + len("Shar")
		s.Edit(func(b *editor.Buffer) { b.Insert("SharedUnsaved") })
		s.requestLanguage(b, "definition", start+3, 0, 0)
	})
	wantShared := strings.Index(original.Text(), "func SharedUnsaved") + len("func ")
	wait(func(s *Workspace) bool { return s.Buf() == original && s.Buf().Cursor == wantShared })
	if !original.Dirty || len(handle.View.Read(application.App).Buffers) != 2 {
		t.Fatal("navigation did not preserve the existing unsaved tab")
	}
	// Navigate from another.go to an unopened file through Ctrl-click.
	update(func(s *Workspace) { s.OpenFile(otherPath) })
	handle.Window.Draw()
	ws = handle.View.Read(application.App)
	otherText := ws.Buf().Text()
	line, col = ws.Buf().OffsetToLC(strings.Index(otherText, "Helper") + 2)
	lineText = ws.Buf().Line(line)
	layout = sys.LayoutLine(lineText, style.DefaultTextStyle().FontSize.ToPixels(handle.Window.RemSize()), []gptext.FontRun{{Len: len(lineText), FontID: fid}})
	x = editor.GutterWidth(ws.Buf().LineCount(), ws.CharAdvance) + layout.XForIndex(col).AsF32() + 1
	y = float32(ui.MenuBarHeight+ui.TabBarHeight) + (float32(line)-ws.scrollF()+0.5)*ws.LineHeight
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x, y), Modifiers: keymap.Modifiers{Control: true}})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(x, y), Modifiers: keymap.Modifiers{Control: true}})
	wantHelper := strings.Index(helper, "func Helper") + len("func ")
	wait(func(s *Workspace) bool {
		return strings.EqualFold(s.Buf().Path, helperPath) && s.Buf().Cursor == wantHelper
	})
	if len(handle.View.Read(application.App).Buffers) != 3 {
		t.Fatal("definition in unopened file did not open a tab")
	}
}
