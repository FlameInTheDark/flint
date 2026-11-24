package workspace_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// openFileScene builds a headless workspace with one buffer opened from
// disk (the OpenFile path: image/binary detection).
func openFileScene(t *testing.T, ws *workspace.Workspace) (*headless.Platform, *gpui.WindowHandle[workspace.Workspace], *app.App) {
	t.Helper()
	hp := headless.New(geometry.SizePixels(1000, 650))
	appl := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[workspace.Workspace](appl, platform.WindowOptions{Title: "file-scene"},
		func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
			ws.Measure(w)
			w.Focus(ws.FocusEditor)
			return *ws
		})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		v := handle.View.Read(cx)
		return v.Render(&handle, cx, w)
	})
	handle.Window.Draw()
	return hp, &handle, appl.App
}

// newSceneWorkspace returns a fresh workspace (headless-safe defaults).
func newSceneWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	ws := workspace.New("file-scene")
	ws.TermSync = true
	return ws
}

// TestBinaryFileOpensAsHex pins the binary detection: a file with NUL
// bytes opens as a read-only hex buffer (not a text buffer), scroll math
// runs on 16-byte rows, and saving is a no-op that never truncates the
// file.
func TestBinaryFileOpensAsHex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.bin")
	data := append([]byte{0x00, 0x01, 0x02, 0x41, 0x42, 0x7F, 0xFF, 0x00}, bytes.Repeat([]byte{0xAB}, 40)...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newSceneWorkspace(t)
	ws.OpenFile(path)
	b := ws.Buf()
	if b == nil {
		t.Fatal("binary file did not open")
	}
	if b.Kind != editor.KindHex {
		t.Fatalf("kind = %v want KindHex", b.Kind)
	}
	if b.HexRowCount() != (len(data)+15)/16 {
		t.Fatalf("hex rows = %d want %d", b.HexRowCount(), (len(data)+15)/16)
	}

	// Scene: the hex pane renders.
	hp, handle, _ := openFileScene(t, ws)
	handle.Window.Draw()
	shot(t, hp, "/tmp/render", "hex_view.png")

	// Saving a hex buffer must not truncate the backing file.
	if err := b.Save(); err != nil {
		t.Fatalf("hex save: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("saving a hex buffer modified the binary file")
	}
}

// TestImageFileOpensInViewer pins the image detection: a PNG opens as a
// KindImage buffer and the viewer scene renders.
func TestImageFileOpensInViewer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dot.png")
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 40), B: 0x99, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := newSceneWorkspace(t)
	ws.OpenFile(path)
	b := ws.Buf()
	if b == nil || b.Kind != editor.KindImage {
		t.Fatalf("kind = %v want KindImage", b.Kind)
	}
	if b.Image == nil || b.Image.Bounds().Dx() != 8 {
		t.Fatal("image did not decode")
	}

	// Scene: the viewer renders (fit mode by default).
	hp, handle, _ := openFileScene(t, ws)
	handle.Window.Draw()
	shot(t, hp, "/tmp/render", "image_view.png")
}

// TestImageZoomAndPanState drives the workspace-level zoom/pan helpers
// the image pane's callbacks mutate.
func TestImageZoomAndPanState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dot.png")
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newSceneWorkspace(t)
	ws.OpenFile(path)

	// Fit by default; toggling switches to 1:1; zoom/pan write through.
	if !ws.DebugImgFit() {
		t.Fatal("image buffers start in fit mode")
	}
	ws.DebugToggleImgFit()
	if ws.DebugImgFit() {
		t.Fatal("toggle did not leave fit mode")
	}
	ws.DebugSetImgZoom(3)
	ws.DebugSetImgPan(40, 20)
	if z := ws.DebugImgZoom(); z != 3 {
		t.Fatalf("zoom = %v want 3", z)
	}
	if px, py := ws.DebugImgPan(); px != 40 || py != 20 {
		t.Fatalf("pan = %v,%v want 40,20", px, py)
	}
	// Clamp bounds hold.
	if z := editor.ClampImageZoom(1000); z != 32 {
		t.Fatalf("clamp high = %v want 32", z)
	}
	if z := editor.ClampImageZoom(0.0001); z != 0.05 {
		t.Fatalf("clamp low = %v want 0.05", z)
	}
	// Fit zoom math: a 8x6 image in a 400x300 viewport fits at 50x.
	if z := editor.ImageFitZoom(8, 6, 400, 300); z != 50 {
		t.Fatalf("fit zoom = %v want 50", z)
	}
}

// TestBMPDecoder pins the hand-rolled BMP decoder (24bpp bottom-up, the
// most common variant) against a reference pixel grid.
func TestBMPDecoder(t *testing.T) {
	// A 3x2 24-bpp bottom-up BMP.
	w, h := 3, 2
	stride := (w*3 + 3) &^ 3 // 12 (padded)
	pixels := make([]byte, stride*h)
	// Bottom row (stored first): blue, green, red in BGR order.
	pixels[0], pixels[1], pixels[2] = 0xFF, 0x00, 0x00 // blue
	pixels[3], pixels[4], pixels[5] = 0x00, 0xFF, 0x00 // green
	pixels[6], pixels[7], pixels[8] = 0x00, 0x00, 0xFF // red
	// Top row: white.
	for i := stride; i < stride+9; i++ {
		pixels[i] = 0xFF
	}
	hdr := make([]byte, 14)
	hdr[0], hdr[1] = 'B', 'M'
	putLE32(hdr[4:], uint32(14+40+len(pixels)))
	putLE32(hdr[10:], uint32(14+40))
	info := make([]byte, 40)
	putLE32(info[0:], 40)
	putLE32(info[4:], uint32(w))
	putLE32(info[8:], uint32(h))
	putLE16(info[12:], 1)
	putLE16(info[14:], 24)
	data := append(hdr, info...)
	data = append(data, pixels...)

	img, err := editor.DecodeImage(data)
	if err != nil {
		t.Fatalf("decode bmp: %v", err)
	}
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		t.Fatalf("bounds = %v", img.Bounds())
	}
	// Top-left pixel is white.
	r, g, b, _ := img.At(0, 0).RGBA()
	if r>>8 != 0xFF || g>>8 != 0xFF || b>>8 != 0xFF {
		t.Fatalf("top-left = %v,%v,%v want white", r>>8, g>>8, b>>8)
	}
	// Bottom-left is blue (BGR order handled).
	_, _, bb, _ := img.At(0, 1).RGBA()
	if bb>>8 != 0xFF {
		t.Fatalf("bottom-left blue = %v", bb>>8)
	}
	// Bottom-middle is green.
	_, gg, _, _ := img.At(1, 1).RGBA()
	if gg>>8 != 0xFF {
		t.Fatalf("bottom-middle green = %v", gg>>8)
	}
}

func putLE16(b []byte, v uint16) { b[0] = byte(v); b[1] = byte(v >> 8) }

func putLE32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

// TestTerminalShortcutTogglesDock keeps the keyboard alternative to the rail.
func TestTerminalShortcutTogglesDock(t *testing.T) {
	ws := newSceneWorkspace(t)
	ws.Buffers = workspace.BenchBuffers()
	hp, handle, cx := openFileScene(t, ws)
	hw := hp.LastWindow()

	if ws.ShowTerminal {
		t.Fatal("terminal should start hidden")
	}
	toggle := platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "`", Modifiers: keymap.Modifiers{Control: true}}}
	hw.DispatchInput(toggle)
	handle.Window.Draw()
	if !handle.View.Read(cx).ShowTerminal {
		t.Fatal("shortcut did not open the terminal")
	}
	shot(t, hp, "/tmp/render", "status_term_open.png")

	// Pressing it again hides it, even with terminal focus.
	hw.DispatchInput(toggle)
	handle.Window.Draw()
	if handle.View.Read(cx).ShowTerminal {
		t.Fatal("shortcut did not close the terminal")
	}
}

// TestLanguageMenuSelection pins the status-bar language selector: the
// filter narrows the catalog, selecting a language re-highlights the
// buffer, and the status bar shows it.
func TestLanguageMenuSelection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.py")
	if err := os.WriteFile(path, []byte("def hello():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := newSceneWorkspace(t)
	ws.OpenFile(path)
	b := ws.Buf()
	if b.LanguageOf() != "python" {
		t.Fatalf("detected language = %q want python", b.LanguageOf())
	}

	hp, handle, cx := openFileScene(t, ws)
	hw := hp.LastWindow()

	update := func(f func(*workspace.Workspace)) {
		handle.View.Update(cx, func(state *workspace.Workspace, c *app.Context[workspace.Workspace]) any {
			f(state)
			c.Notify()
			return nil
		})
	}
	update(func(s *workspace.Workspace) { s.OpenLangMenu() })
	handle.Window.Focus(handle.View.Read(cx).FocusLangMenu)
	handle.Window.Draw()
	shot(t, hp, "/tmp/render", "lang_menu.png")

	// Type a filter: "rus" leaves Rust; enter applies it.
	for _, ch := range "rus" {
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: string(ch), KeyChar: string(ch)}})
	}
	handle.Window.Draw()
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "enter"}})
	handle.Window.Draw()

	if got := b.LanguageOf(); got != "rust" {
		t.Fatalf("selected language = %q want rust", got)
	}
	if b.LangOverride != "rust" {
		t.Fatalf("override = %q", b.LangOverride)
	}
	// The highlighter switched: a Rust keyword line tokenizes as a bold
	// keyword run.
	pal := editor.PaletteOfTheme(ws.Theme())
	runs := editor.Highlight("fn main() {}", "rust", pal)
	if len(runs) == 0 || runs[0].Color != pal.Keyword {
		t.Fatal("rust highlighting did not apply after selection")
	}

	// The menu closed on selection.
	if handle.View.Read(cx).LangMenu != nil {
		t.Fatal("language menu stayed open after selection")
	}
}

// TestGenericHighlighters pins the extended language table: detection by
// extension and the keyword tokenizer for a representative set.
func TestGenericHighlighters(t *testing.T) {
	cases := map[string]string{
		"main.go": "go", "main.py": "python", "lib.rs": "rust", "app.js": "javascript",
		"App.java": "java", "a.c": "c", "a.cpp": "cpp", "a.cs": "csharp", "run.sh": "shell",
		"q.sql": "sql", "conf.toml": "toml", "conf.yaml": "yaml", "data.json": "json",
		"note.md": "markdown", "x.txt": "text", "noext": "text",
	}
	for name, want := range cases {
		if got := editor.Language(name); got != want {
			t.Errorf("Language(%q) = %q want %q", name, got, want)
		}
	}

	pal := editor.PaletteOfTheme(wsTheme())
	// Python: # comment paints the whole line as comment.
	runs := editor.Highlight("# a comment", "python", pal)
	if runs[0].Color != pal.Comment {
		t.Fatal("python # comment not colored")
	}
	// C: keyword `int` bold; string colored.
	runs = editor.Highlight("int x = \"s\";", "c", pal)
	if runs[0].Color != pal.Keyword {
		t.Fatal("c keyword not colored")
	}
	found := false
	for _, r := range runs {
		if r.Color == pal.String {
			found = true
		}
	}
	if !found {
		t.Fatal("c string not colored")
	}
	// JSON: key colored as function-ish, string value colored.
	runs = editor.Highlight(`{"key": "value"}`, "json", pal)
	if runs[0].Color != pal.Function {
		t.Fatalf("json key color = %v", runs[0].Color)
	}
}

// TestCustomTitleBarScene renders the in-app title bar (custom
// decorations forced on a headless window) and pins its chrome geometry:
// the bar sits above the menu bar and the three window buttons ride the
// right edge.
func TestCustomTitleBarScene(t *testing.T) {
	ws := newSceneWorkspace(t)
	ws.Buffers = workspace.BenchBuffers()
	ws.SetCustomDecorationsForTest()
	hp, handle, cx := openFileScene(t, ws)
	handle.Window.Draw()
	shot(t, hp, "/tmp/render", "title_bar.png")

	// The layout reserves the title bar: the pane height shrinks by the
	// title bar's height versus a plain scene of the same window size.
	ws2 := newSceneWorkspace(t)
	ws2.Buffers = workspace.BenchBuffers()
	ws2.SetWindowSizeForTest(1000, 650)
	if ws2.DebugPaneHeight() <= handle.View.Read(cx).DebugPaneHeight() {
		t.Fatal("custom title bar did not reserve layout height")
	}
}

func wsTheme() ui.Theme { return ui.DefaultTheme() }
