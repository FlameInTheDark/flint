// Command bench measures the editor's real per-frame cost without a
// window: it wires the workspace headlessly, then times full Window.Draw
// frames and a simulated mouse-drag (one HandleInput per move, exactly
// like the win32 message pump delivers events).
//
//	go run ./bench
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/workspace"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
	"github.com/FlameInTheDark/go-gpui/render"
)

// bigBuffer generates an n-line synthetic Go-ish source file (mixed
// keywords, strings, comments - representative shaping/highlight load).
func bigBuffer(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		switch i % 6 {
		case 0:
			sb.WriteString("// Fib returns the n-th Fibonacci number, line ")
			sb.WriteString(strconv.Itoa(i))
			sb.WriteString(".\n")
		case 1:
			sb.WriteString("func helper")
			sb.WriteString(strconv.Itoa(i))
			sb.WriteString("(ctx Context, values []string) (Result, error) {\n")
		case 2:
			sb.WriteString("\tif len(values) == 0 {\n\t\treturn Result{}, ErrEmpty\n\t}\n")
		case 3:
			sb.WriteString("\tlabel := fmt.Sprintf(\"item %d of %d\", ")
			sb.WriteString(strconv.Itoa(i))
			sb.WriteString(", total)\n")
		case 4:
			sb.WriteString("\tfor idx, value := range values {\n\t\tout = append(out, transform(value, idx))\n\t}\n")
		default:
			sb.WriteString("}\n")
		}
	}
	return sb.String()
}

func main() {
	big := flag.Int("big", 0, "use a synthetic N-line buffer (tests large-document scaling)")
	frames := flag.Int("frames", 40, "number of steady-state frames to time and, if requested, profile")
	profile := flag.String("cpuprofile", "", "write a steady-state CPU profile to this file")
	pngFile := flag.String("png", "", "write the caret check frame to this PNG file")
	flag.Parse()
	if *frames < 1 {
		log.Fatal("-frames must be at least 1")
	}

	hp := headless.New(geometry.SizePixels(1100, 700))
	a := gpui.NewApplication(newNoPresentPlatform(hp))

	handle, err := gpui.OpenWindow[workspace.Workspace](a, platform.WindowOptions{
		Title: "bench",
	}, func(w *gpui.Window, cx *app.Context[workspace.Workspace]) workspace.Workspace {
		ws := workspace.New("bench")
		if *big > 0 {
			ws.Buffers = []*editor.Buffer{editor.NewBuffer("", "big.go", bigBuffer(*big))}
		} else {
			ws.Buffers = workspace.BenchBuffers()
		}
		ws.SetScrolls(0, 0, 0)
		ws.Measure(w)
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		panic(err)
	}
	w := handle.Window
	w.SetRootView(handle.View.ID(), func(w *gpui.Window, cx *app.App) gpui.Element {
		var el gpui.Element
		handle.View.Update(cx, func(v *workspace.Workspace, vcx *app.Context[workspace.Workspace]) any {
			el = v.Render(&handle, cx, w)
			return nil
		})
		return el
	})
	w.Draw()

	// Warm up: fonts, glyph atlas, shaping caches.
	for i := 0; i < 10; i++ {
		w.Draw()
	}

	// 1. Steady-state full frame cost.
	t0 := time.Now()
	for i := 0; i < *frames; i++ {
		w.Draw()
	}
	avg := time.Since(t0) / time.Duration(*frames)
	fmt.Printf("steady-state frame build (no raster or present): %v/frame\n", avg)

	// CPU profile of the steady-state frames.
	if *profile != "" {
		f, err := os.Create(*profile)
		if err != nil {
			log.Fatal(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			log.Fatal(err)
		}
		for i := 0; i < *frames; i++ {
			w.Draw()
		}
		pprof.StopCPUProfile()
		if err := f.Close(); err != nil {
			log.Fatal(err)
		}
		fmt.Println("profile written to", *profile)
	}

	// 2. Simulated drag: mouse down + 100 moves (HandleInput draws internally,
	// exactly like the win32 wndProc path).
	total := time.Duration(0)
	max := time.Duration(0)
	start := time.Now()
	w.HandleInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(300, 200)})
	for i := 0; i < 100; i++ {
		t1 := time.Now()
		w.HandleInput(platform.MouseMoveEvent{
			Position:      geometry.PointPixels(300+float32(i), 200+float32(i)/4),
			PressedButton: btnLeft(),
		})
		d := time.Since(t1)
		total += d
		if d > max {
			max = d
		}
	}
	wall := time.Since(start)
	fmt.Printf("drag input: 100 moves in %v (wall), avg %v/move, max %v\n", wall, total/100, max)

	// 3. Hover moves over plain content (no listeners fire).
	total = 0
	start = time.Now()
	for i := 0; i < 60; i++ {
		t1 := time.Now()
		w.HandleInput(platform.MouseMoveEvent{Position: geometry.PointPixels(50+float32(i), 300)})
		total += time.Since(t1)
	}
	wall = time.Since(start)
	fmt.Printf("hover input: 60 moves in %v (wall), avg %v/move\n", wall, total/60)

	// 4. Caret visibility check: click into line 2, draw, scan the image for
	// accent-colored pixels in the pane area.
	w.HandleInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(120, 120)})
	w.HandleInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: geometry.PointPixels(120, 120)})
	w.Draw()
	// The no-present wrapper skips rasterization; render one frame into the
	// headless window for the pixel checks below.
	hw := hp.LastWindow()
	hw.Draw(w.Scene())
	img := hw.Image()
	ar, ag, ab := workspace.BenchAccentRGB()
	type px struct{ x, y int }
	var found []px
	for y := 28; y < 700; y++ {
		for x := 60; x < 1100; x++ {
			i := (y*img.Width + x) * 4
			r, g, b := img.Pixels[i], img.Pixels[i+1], img.Pixels[i+2]
			dr, dg, db := int(r)-int(ar*255), int(g)-int(ag*255), int(b)-int(ab*255)
			if dr*dr+dg*dg+db*db < 900 {
				found = append(found, px{x, y})
			}
		}
	}
	fmt.Printf("caret check: %d accent-colored pixels in pane", len(found))
	if len(found) > 0 {
		xs, ys := map[int]bool{}, map[int]bool{}
		for _, p := range found {
			xs[p.x] = true
			ys[p.y] = true
		}
		fmt.Printf(" (x range %d..%d, y range %d..%d)", minOf(xs), maxOf(xs), minOf(ys), maxOf(ys))
	}
	fmt.Println()
	// Dump the frame for visual inspection.
	if *pngFile != "" {
		pngBytes, err := hw.PNG()
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*pngFile, pngBytes, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("frame saved to", *pngFile)
	}
}

// noPresentPlatform measures frame building only: the platform Draw is a
// no-op instead of the headless CPU rasterization. It also skips GPU-side
// preparation and upload, which are not part of this benchmark.
type noPresentPlatform struct {
	platform.Platform
}

func newNoPresentPlatform(p platform.Platform) platform.Platform {
	return &noPresentPlatform{Platform: p}
}

func (p *noPresentPlatform) OpenWindow(opts platform.WindowOptions, cb platform.WindowCallbacks) (platform.PlatformWindow, error) {
	w, err := p.Platform.OpenWindow(opts, cb)
	if err != nil {
		return nil, err
	}
	return &noPresentWindow{PlatformWindow: w}, nil
}

type noPresentWindow struct{ platform.PlatformWindow }

func (w *noPresentWindow) Draw(scene *render.Scene) {}

// pngWindow recovers the headless window for the visual dump.
func pngWindow(hp *headless.Platform) *headless.Window { return hp.LastWindow() }

func minOf(m map[int]bool) int {
	min := 1 << 30
	for v := range m {
		if v < min {
			min = v
		}
	}
	return min
}

func maxOf(m map[int]bool) int {
	max := -(1 << 30)
	for v := range m {
		if v > max {
			max = v
		}
	}
	return max
}

func btnLeft() *platform.MouseButton {
	b := platform.ButtonLeft
	return &b
}
