package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/lsp"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

func TestCompletionPopupLayout(t *testing.T) {
	for _, width := range []float32{1000, 320} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
			hp := headless.New(geometry.SizePixels(width, 650))
			application := gpui.NewApplication(hp)
			handle, err := gpui.OpenWindow[Workspace](application, platform.WindowOptions{Title: "completion layout"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
				ws := New("test")
				b := editor.NewBuffer("", "test.go", "x")
				b.SetCursor(1)
				ws.Buffers = []*editor.Buffer{b}
				ws.Measure(w)
				ws.completion = &completionState{buffer: b, revision: b.Revision, cursor: b.Cursor, items: []lsp.CompletionItem{
					{Label: strings.Repeat("LongSymbol", 20), Detail: strings.Repeat("long detail ", 20), Kind: 3},
					{Label: "value", Detail: "int", Kind: 6},
				}}
				return *ws
			})
			if err != nil {
				t.Fatal(err)
			}
			handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
			handle.Window.Draw()
			if out := os.Getenv("EDITOR_SHOT_DIR"); out != "" {
				png, err := hp.LastWindow().PNG()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("completion_%g.png", width)), png, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			boxes := make(map[string]gpui.Hitbox)
			for _, hb := range handle.Window.RenderedHitboxes() {
				boxes[hb.Key] = hb
			}
			get := func(key string) gpui.Hitbox {
				t.Helper()
				hb, ok := boxes["n:btn-"+key]
				if !ok {
					t.Fatalf("missing hitbox %s", key)
				}
				return hb
			}
			var previous gpui.Hitbox
			for i := 0; i < 2; i++ {
				row := get(fmt.Sprintf("completion-%d", i))
				if row.Bounds.Size.Height.AsF32() != 27 || row.Bounds.Size.Width.AsF32() > width-16 {
					t.Fatalf("unexpected row bounds: %v", row.Bounds)
				}
				if i > 0 && (row.Bounds.Origin.X != previous.Bounds.Origin.X || row.Bounds.Origin.Y.AsF32() != previous.Bounds.Origin.Y.AsF32()+27) {
					t.Fatalf("completion rows are misaligned: %v, %v", previous.Bounds, row.Bounds)
				}
				// The icon and label must actually paint in their respective columns,
				// including when an unbroken label is much wider than the popup.
				img := hp.LastWindow().Image()
				x, y := int(row.Bounds.Origin.X.AsF32()), int(row.Bounds.Origin.Y.AsF32())
				iconInk, labelInk := 0, 0
				for py := y; py < y+27; py++ {
					for px := x + 12; px < x+int(row.Bounds.Size.Width.AsF32())/2; px++ {
						p := (py*img.Width + px) * 4
						r, g, b := int(img.Pixels[p]), int(img.Pixels[p+1]), int(img.Pixels[p+2])
						if px < x+30 && r > 120 && b > g+10 && r > g+10 {
							iconInk++
						}
						if px >= x+39 && r > 150 && g > 150 && b > 150 {
							labelInk++
						}
					}
				}
				if iconInk == 0 || labelInk == 0 {
					t.Fatalf("missing icon or label ink in row %d: icon=%d label=%d", i, iconInk, labelInk)
				}
				previous = row
			}
			// The second row stays clickable even when the first label and detail are long.
			click(hp.LastWindow(), centerOf(previous))
			handle.Window.Draw()
			ws := handle.View.Read(application.App)
			if ws.completion != nil || ws.Buf().Text() != "value" {
				t.Fatalf("click did not accept second completion: text=%q", ws.Buf().Text())
			}
		})
	}
}

func TestFitCompletionText(t *testing.T) {
	measure := func(s string) float32 { return float32(len([]rune(s))) }
	for _, tc := range []struct {
		text  string
		width float32
		want  string
	}{
		{"short", 5, "short"},
		{"éclair", 4, "écl…"},
		{"a\nb\tc", 5, "a b c"},
		{"long", 0, ""},
	} {
		if got := fitCompletionText(tc.text, tc.width, measure); got != tc.want {
			t.Errorf("fitCompletionText(%q, %v) = %q, want %q", tc.text, tc.width, got, tc.want)
		}
	}
}
