// Image viewer: the presentation of decoded image buffers — zoom (wheel,
// anchored at the cursor; ctrl+0 / double-click reset; a fit pill), and
// drag-to-pan when zoomed in. The image paints through the window's
// atlas-cached PaintImage; zoom and pan are pure geometry recomputed per
// frame. Drag deltas and zoom state live on the workspace (element trees
// are rebuilt every frame, so listeners cannot keep gesture state).

package editor

import (
	"fmt"
	"image"

	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"

	"github.com/FlameInTheDark/flint/internal/ui"
)

// ImageConfig configures RenderImagePane.
type ImageConfig struct {
	Buf   *Buffer
	Theme ui.Theme
	// Viewport sizes the fit computation and the pan clamp.
	ViewportW float32
	ViewportH float32
	// Zoom is the scale factor (1 = 100%); Fit derives it from the
	// viewport instead (the workspace stores both per buffer).
	Zoom float32
	// PanX/PanY is the image offset in pane pixels.
	PanX, PanY float32
	Fit        bool
	// OnZoomAt applies a zoom change with the pan the pane computed for
	// the anchor (cursor) point.
	OnZoomAt func(zoom, panX, panY float32)
	// OnDragStart reports a press at a window position (gesture start).
	OnDragStart func(x, y float32)
	// OnDragMove reports a move to a window position while dragging; the
	// workspace diffs against its stored gesture position.
	OnDragMove func(x, y float32)
	// OnPan applies a direct pixel delta (ctrl+wheel).
	OnPan func(dx, dy float32)
	// OnToggleFit flips fit/1:1 (double-click, reset, the pill).
	OnToggleFit func()
	// Focus is the editor focus handle (keyboard plumbing).
	Focus gpui.FocusHandle
}

// ImageZoomClamp bounds the zoom range.
const (
	imageZoomMin = 0.05
	imageZoomMax = 32.0
)

// imageTileSize is the largest source chunk painted through PaintImage in
// one piece: the sprite atlas page is 1024px and tiles reserve a 1px
// transparent padding ring, so a chunk above 1022px parks as an EMPTY tile
// and paints nothing (the "open an image, just an empty nothing" bug — any
// screenshot bigger than the page used to render blank). The viewer paints
// the image as a grid of chunks that each fit a page; adjacent screen
// rectangles share the exact float edges, so no seams appear between them.
const imageTileSize = 1020

// imageMipK picks the upload resolution for the current zoom: the smallest
// power-of-two k with at least half a device pixel per uploaded pixel
// (beyond that the quality loss is invisible), capped at 16. Zoomed-out
// views of huge images upload one small mip instead of dozens of full-res
// tiles that would blow the atlas page budget.
func imageMipK(zoom, devScale float32, imgW, imgH int) int {
	if imgW <= imageTileSize && imgH <= imageTileSize {
		return 1
	}
	s := zoom * devScale
	k := 1
	for s < 0.5 && k < 16 {
		k *= 2
		s *= 2
	}
	return k
}

// lastTile returns the origin of the rightmost/bottommost chunk covering
// [min, max) at the chunk stride (the tiling loops walk it downward).
func lastTile(min, max int) int {
	if max-min <= 1 {
		return min
	}
	return min + (max-1-min)/imageTileSize*imageTileSize
}

// ClampImageZoom clamps a zoom factor into the viewer's range.
func ClampImageZoom(z float32) float32 {
	if z < imageZoomMin {
		return imageZoomMin
	}
	if z > imageZoomMax {
		return imageZoomMax
	}
	return z
}

// ImageFitZoom computes the fit-to-window scale for an image in a
// viewport (bounded at 64x so pathological tiny sources stay sane).
func ImageFitZoom(imgW, imgH, viewW, viewH float32) float32 {
	if imgW <= 0 || imgH <= 0 || viewW <= 0 || viewH <= 0 {
		return 1
	}
	z := viewW / imgW
	if h := viewH / imgH; h < z {
		z = h
	}
	if z > 64 {
		z = 64
	}
	return z
}

func maxF(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// RenderImagePane renders the zoomable image view.
func RenderImagePane(cfg ImageConfig) gpui.Element {
	t := cfg.Theme
	b := cfg.Buf

	ib := b.Image.Bounds()
	imgW, imgH := float32(ib.Dx()), float32(ib.Dy())
	viewW, viewH := cfg.ViewportW, cfg.ViewportH

	zoom := cfg.Zoom
	if cfg.Fit {
		zoom = ImageFitZoom(imgW, imgH, viewW, viewH)
	}
	drawW, drawH := imgW*zoom, imgH*zoom

	// Pan clamp: a smaller-than-viewport image floats freely; a bigger one
	// keeps at least a quarter of the pane reachable on each axis.
	maxPanX := maxF(drawW-viewW, 0) + viewW/4
	maxPanY := maxF(drawH-viewH, 0) + viewH/4
	panX, panY := cfg.PanX, cfg.PanY
	if panX < -maxPanX {
		panX = -maxPanX
	}
	if panX > maxPanX {
		panX = maxPanX
	}
	if panY < -maxPanY {
		panY = -maxPanY
	}
	if panY > maxPanY {
		panY = maxPanY
	}
	if cfg.Fit {
		panX, panY = 0, 0
	}

	// zoomAt maps the pane-local anchor under the cursor to the pan that
	// keeps it under the cursor after the zoom.
	zoomAt := func(newZoom, ax, ay float32) (float32, float32) {
		newZoom = ClampImageZoom(newZoom)
		// left/top of the drawn image in pane coordinates.
		left := viewW/2 - drawW/2 + panX
		top := viewH/2 - drawH/2 + panY
		imgX := (ax - left) / zoom
		imgY := (ay - top) / zoom
		newW, newH := imgW*newZoom, imgH*newZoom
		panX := ax - imgX*newZoom - (viewW/2 - newW/2)
		panY := ay - imgY*newZoom - (viewH/2 - newH/2)
		return panX, panY
	}

	canvas := elements.NewCanvas(
		func(bounds geometry.Bounds[geometry.Pixels], w *gpui.Window) {},
		func(bounds geometry.Bounds[geometry.Pixels], w *gpui.Window) {
			cx := bounds.Origin.X.AsF32() + bounds.Size.Width.AsF32()/2
			cy := bounds.Origin.Y.AsF32() + bounds.Size.Height.AsF32()/2
			// Screen rectangle of the full image (pane coordinates).
			left := cx - drawW/2 + panX
			top := cy - drawH/2 + panY
			// The image paints as a grid of atlas-page-sized chunks.
			// A source bigger than the page cannot occupy one tile:
			// the atlas parks it as an empty 1x1 placeholder and the
			// viewer shows nothing. Zoomed-out views upload the mip
			// level (a cached downscaled copy) instead of every
			// full-res tile, bounding the atlas cost.
			src := b.imageTileSource(imageMipK(zoom, w.ScaleFactor(), ib.Dx(), ib.Dy()))
			srcB := src.Bounds()
			// Source image pixels per uploaded pixel (>= 1).
			kx := float32(imgW) / float32(srcB.Dx())
			ky := float32(imgH) / float32(srcB.Dy())
			bx0, bx1 := bounds.Origin.X.AsF32(), bounds.Origin.X.AsF32()+bounds.Size.Width.AsF32()
			by0, by1 := bounds.Origin.Y.AsF32(), bounds.Origin.Y.AsF32()+bounds.Size.Height.AsF32()
			corners := geometry.ZeroCornersAbsolute().ToPixels(w.RemSize())
			// All normalized decodes are sub-imagerable; anything else
			// (never produced by OpenBuffer) paints as one piece.
			tiled, canTile := src.(subImager)
			if !canTile {
				w.PaintImage(geometry.BoundsPixels(left, top, drawW, drawH), corners, src, false)
				return
			}
			// Chunks paint left-to-right, top-to-bottom, and each one
			// extends a pixel past its content (clamped at the image
			// edge): the renderer's bilinear sampler blends a sprite's
			// right/bottom edge into the atlas tile's transparent
			// padding ring — a ~1px dark rim — while the left/top edge
			// clamps clean. The later-painted (on-top) chunk's clean
			// left/top edge therefore covers the earlier chunk's rim,
			// and chunk boundaries show no seam.
			for ty := srcB.Min.Y; ty < srcB.Max.Y; ty += imageTileSize {
				for tx := srcB.Min.X; tx < srcB.Max.X; tx += imageTileSize {
					x1, y1 := min(tx+imageTileSize, srcB.Max.X), min(ty+imageTileSize, srcB.Max.Y)
					// Cull chunks outside the pane (a huge image
					// panned/zoomed around uploads only what is
					// visible). The test uses the extended rect so
					// a just-off-view chunk still covers its
					// neighbor's edge rim.
					ex0, ey0 := max(tx-1, srcB.Min.X), max(ty-1, srcB.Min.Y)
					ex1, ey1 := min(x1+1, srcB.Max.X), min(y1+1, srcB.Max.Y)
					rx0 := left + float32(ex0-srcB.Min.X)*kx*zoom
					ry0 := top + float32(ey0-srcB.Min.Y)*ky*zoom
					rx1 := left + float32(ex1-srcB.Min.X)*kx*zoom
					ry1 := top + float32(ey1-srcB.Min.Y)*ky*zoom
					if rx1 <= bx0 || rx0 >= bx1 || ry1 <= by0 || ry0 >= by1 {
						continue
					}
					tile := tiled.SubImage(image.Rect(ex0, ey0, ex1, ey1))
					w.PaintImage(geometry.BoundsPixels(rx0, ry0, rx1-rx0, ry1-ry0), corners, tile, false)
				}
			}
		})
	// Fill the pane: the canvas's default style is an auto-sized flex
	// child (zero size inside the pane's row); flex-grow + basis 0
	// stretches it over the whole viewport.
	canvas.StyleRef().Flex1()

	// Info pill: dimensions + zoom + fit/reset action.
	pct := int(zoom*100 + 0.5)
	pill := elements.NewDiv().
		ID(elements.ButtonID("image-info")).
		Absolute().Left(ui.Px(10)).Bottom(ui.Px(10)).
		Flex().ItemsCenter().Gap(ui.DefPx(8)).
		Px(ui.DefPx(8)).Py(ui.DefPx(4)).
		Rounded(ui.AbsPx(6)).
		BgColor(t.Surface).Border_1().BorderColor(t.Surface1).
		TextSize(ui.AbsPx(11)).
		Child(elements.NewText(fmt.Sprintf("%s  ·  %d%%", ImageInfo(b), pct)).WithTextColor(t.Dim)).
		Child(elements.NewDiv().
			ID(elements.ButtonID("image-fit")).
			Flex().ItemsCenter().Px(ui.DefPx(5)).Rounded(ui.AbsPx(4)).
			CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
			Child(elements.NewText("reset").WithTextColor(t.Overlay)).
			OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
				if cfg.OnToggleFit != nil {
					cfg.OnToggleFit()
				}
				w.Refresh()
			}))

	pane := elements.NewDiv().
		ID(elements.ButtonID("image-pane")).
		Flex().Flex1().Relative().OverflowHidden().
		BgColor(t.EditorBg).
		TrackFocus(cfg.Focus).
		// Drag-to-pan: captured, so panning keeps tracking when the
		// cursor leaves the pane.
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				w.CapturePointer(hb.ID)
			}
			if cfg.OnDragStart != nil {
				cfg.OnDragStart(ev.Position.X.AsF32(), ev.Position.Y.AsF32())
			}
			w.Refresh()
		}).
		OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
			if !ev.Dragging() || cfg.Fit {
				return
			}
			if cfg.OnDragMove != nil {
				cfg.OnDragMove(ev.Position.X.AsF32(), ev.Position.Y.AsF32())
			}
			w.Refresh()
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
		}).
		OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
			// Double-click toggles fit/1:1 (like every image viewer).
			if ev.Down.ClickCount == 2 && cfg.OnToggleFit != nil {
				cfg.OnToggleFit()
				w.Refresh()
			}
		}).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			if ev.Modifiers.Control || ev.Modifiers.Platform {
				// Ctrl+wheel pans vertically (the editor convention for
				// horizontal text pan does not apply to an image).
				if cfg.OnPan != nil {
					cfg.OnPan(0, -ev.Delta.PixelDelta(geometry.Px(60)).Y.AsF32())
				}
				w.Refresh()
				return
			}
			// Plain wheel zooms at the cursor.
			if cfg.OnZoomAt != nil {
				factor := 1.25
				if dy := ev.Delta.PixelDelta(geometry.Px(1)).Y.AsF32(); dy > 0 {
					factor = 1 / 1.25
				}
				ax, ay := viewW/2, viewH/2
				if hb, ok := w.ListenerHitbox(); ok {
					ax = ev.Position.X.AsF32() - hb.Bounds.Origin.X.AsF32()
					ay = ev.Position.Y.AsF32() - hb.Bounds.Origin.Y.AsF32()
				}
				px, py := zoomAt(zoom*float32(factor), ax, ay)
				cfg.OnZoomAt(ClampImageZoom(zoom*float32(factor)), px, py)
			}
			w.Refresh()
		}).
		OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
			k := ev.Keystroke
			if k.Modifiers.Control || k.Modifiers.Platform {
				if k.Key == "0" && cfg.OnToggleFit != nil {
					cfg.OnToggleFit()
				}
				w.Refresh()
				return
			}
			w.Refresh()
		}).
		Child(canvas).
		Child(pill)
	return pane
}
