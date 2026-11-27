package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

// TestIconSheetRender renders every embedded language logo and every
// generic file glyph — one icon per fixed 72x72 headless window, at 48px
// (geometry inspection) and 14px (the real tree/picker size) — and saves
// each frame as <key>.png under FLINT_ICON_SHEET_OUT (a temp dir when
// unset). Every render asserts real ink: an icon that paints nothing (or
// only its background) fails the test, so a blank/missing logo can never
// ship silently. This is the visual-verification harness for the icon
// rounds: every icon must be inspected in the rendered frames before
// shipping.
func TestIconSheetRender(t *testing.T) {
	keys := make([]string, 0, len(logoSvg))
	for k := range logoSvg {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	shapes := make([]string, 0, len(fileTypeShapes))
	for k := range fileTypeShapes {
		shapes = append(shapes, k)
	}
	sort.Strings(shapes)

	out := os.Getenv("FLINT_ICON_SHEET_OUT")
	if out == "" {
		out = t.TempDir()
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("output dir: %v", err)
	}

	renderOne := func(name string, build func() gpui.Element) (*headless.Window, error) {
		hp := headless.New(geometry.SizePixels(72, 72))
		appl := gpui.NewApplication(hp)
		handle, err := gpui.OpenWindow[struct{}](appl, platform.WindowOptions{}, func(w *gpui.Window, cx *app.Context[struct{}]) struct{} {
			return struct{}{}
		})
		if err != nil {
			return nil, err
		}
		handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
			return elements.NewDiv().Flex().ItemsCenter().JustifyCenter().W(Px(72)).H(Px(72)).BgColor(DefaultTheme().Crust).
				Child(build())
		})
		handle.Window.Draw()
		return hp.LastWindow(), nil
	}

	inkStats := func(img *headless.Window) (ink, colors int) {
		img2 := img.Image()
		seen := map[uint32]bool{}
		for i := 0; i < img2.Width*img2.Height; i++ {
			r, g, b, a := img2.Pixels[i*4], img2.Pixels[i*4+1], img2.Pixels[i*4+2], img2.Pixels[i*4+3]
			if a > 200 {
				seen[uint32(r)<<16|uint32(g)<<8|uint32(b)] = true
			}
			if a > 200 && (r > 60 || g > 60 || b > 60) {
				ink++
			}
		}
		return ink, len(seen)
	}

	check := func(kind, name string, build func() gpui.Element) {
		t.Run(kind+"/"+name, func(t *testing.T) {
			hw, err := renderOne(name, build)
			if err != nil {
				t.Fatalf("render window: %v", err)
			}
			if hw == nil {
				t.Fatal("no headless window")
			}
			ink, colors := inkStats(hw)
			// A real icon paints hundreds of ink pixels in a 72x72 frame;
			// a broken/blank svg paints nothing but the background. At
			// least two colors: the icon must contrast with the plate.
			if ink < 40 {
				t.Fatalf("%s %s paints almost nothing (ink=%d)", kind, name, ink)
			}
			if colors < 2 {
				t.Fatalf("%s %s paints a single color — the mark never shows", kind, name)
			}
			png, err := hw.PNG()
			if err != nil || len(png) == 0 {
				t.Fatalf("png: %v", err)
			}
			fn := filepath.Join(out, fmt.Sprintf("%s_%s.png", kind, name))
			if err := os.WriteFile(fn, png, 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		})
	}

	for _, k := range keys {
		k := k
		check("logo", k, func() gpui.Element { return logoSvgAt(k, 48) })
	}
	for _, k := range shapes {
		k := k
		check("shape", k, func() gpui.Element { return glyphSvgAt(fileIconSpec{shape: k, color: "#519ABA"}, 48) })
	}
}

// logoSvgAt renders one devicon logo at an arbitrary size (the sheet
// checker renders 48px for geometry inspection and 14px for the real
// tree/picker size).
func logoSvgAt(key string, size float32) gpui.Element {
	svg, ok := logoSvg[key]
	if !ok {
		return elements.NewDiv().W(Px(size)).H(Px(size))
	}
	return elements.NewSvgFromData([]byte(svg)).W(Px(size)).H(Px(size)).FlexShrink(0)
}

// glyphSvgAt renders a generic glyph spec at an arbitrary size.
func glyphSvgAt(spec fileIconSpec, size float32) gpui.Element {
	return fileTypeSvgSize(spec, size)
}

// logoSvgKeys exposes the embedded logo keys (tests and diagnostics).
func logoSvgKeys() []string {
	keys := make([]string, 0, len(logoSvg))
	for k := range logoSvg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
