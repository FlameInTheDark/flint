package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"

	"github.com/FlameInTheDark/flint/internal/ui"
)

// TreeNode is one entry of the project tree.
type TreeNode struct {
	Name     string
	Path     string
	IsDir    bool
	Expanded bool
	Loaded   bool
	Children []*TreeNode
	Parent   *TreeNode
}

// ignoredEntries are never listed (like the upstream editor's default file ignores).
var ignoredEntries = map[string]bool{
	".git": true, "node_modules": true, ".hg": true, ".svn": true,
	"target": true, "obj": true,
}

// NewTree loads the root node of a directory.
func NewTree(root string) *TreeNode {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	n := &TreeNode{
		Name:  filepath.Base(abs),
		Path:  abs,
		IsDir: true,
	}
	n.EnsureLoaded()
	return n
}

// EnsureLoaded lists children once (sorted: dirs first, then names).
func (n *TreeNode) EnsureLoaded() {
	if n.Loaded || !n.IsDir {
		return
	}
	n.Loaded = true
	entries, err := os.ReadDir(n.Path)
	if err != nil {
		return
	}
	var dirs, files []*TreeNode
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if ignoredEntries[name] {
			continue
		}
		child := &TreeNode{
			Name:   name,
			Path:   filepath.Join(n.Path, name),
			IsDir:  e.IsDir(),
			Parent: n,
		}
		if e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(child.Path); err == nil {
				child.IsDir = info.IsDir()
			}
		}
		if child.IsDir {
			dirs = append(dirs, child)
		} else {
			files = append(files, child)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	n.Children = append(append([]*TreeNode{}, dirs...), files...)
}

// Reload re-lists a directory node (after fs operations), preserving the
// expanded state of children by name.
func (n *TreeNode) Reload() {
	if !n.IsDir {
		return
	}
	expanded := map[string]bool{}
	for _, c := range n.Children {
		if c.Expanded {
			expanded[c.Name] = true
		}
	}
	n.Loaded = false
	n.EnsureLoaded()
	for _, c := range n.Children {
		if expanded[c.Name] {
			c.Expanded = true
			c.EnsureLoaded()
		}
	}
}

// Find locates a node by absolute path.
func (n *TreeNode) Find(path string) *TreeNode {
	if n.Path == path {
		return n
	}
	for _, c := range n.Children {
		if c.Path == path {
			return c
		}
		if c.IsDir && strings.HasPrefix(path, c.Path+string(os.PathSeparator)) {
			if found := c.Find(path); found != nil {
				return found
			}
		}
	}
	return nil
}

// treeRowHeight matches the row height used for scrollbar geometry.
const treeRowHeight = float32(24)

// treeRow is one rendered tree row.
type treeRow struct {
	node  *TreeNode
	depth int
}

// flattenVisible walks the tree collecting rendered rows.
func flattenVisible(n *TreeNode, depth int, out *[]treeRow) {
	for _, c := range n.Children {
		*out = append(*out, treeRow{node: c, depth: depth})
		if c.IsDir && c.Expanded {
			flattenVisible(c, depth+1, out)
		}
	}
}

// treeFileColor picks an icon color per file extension.
func treeFileColor(name string, t ui.Theme) gc.Hsla {
	switch {
	case strings.HasSuffix(name, ".go"):
		return t.Sky
	case strings.HasSuffix(name, ".md"):
		return t.Peach
	case strings.HasSuffix(name, ".mod"), strings.HasSuffix(name, ".sum"):
		return t.Mauve
	case strings.HasSuffix(name, ".png"), strings.HasSuffix(name, ".jpg"),
		strings.HasSuffix(name, ".svg"), strings.HasSuffix(name, ".ico"):
		return t.Yellow
	case strings.HasSuffix(name, ".ts"), strings.HasSuffix(name, ".js"),
		strings.HasSuffix(name, ".rs"), strings.HasSuffix(name, ".py"):
		return t.Green
	}
	return t.Dim
}

// TreeCallbacks wires tree interactions to the workspace.
type TreeCallbacks struct {
	// Toggle expands/collapses a folder node.
	Toggle func(node *TreeNode)
	// OpenFile opens a file in a tab.
	OpenFile func(path string)
	// Context opens a context menu for a node at a window position.
	Context func(node *TreeNode, pos geometry.Point[geometry.Pixels])
	// Scroll is the panel's persistent scroll handle (owned by the
	// workspace; per-render handles lose cross-frame requests because the
	// element tree rebuilds every frame).
	Scroll *elements.ScrollHandle
	// Measure returns a string's rendered width in the panel's font
	// (label widths set the rows' definite widths for horizontal scroll).
	Measure func(string) float32
	// OnResizeWidth fires while the user drags the panel's inner edge; the
	// argument is the pointer's window x (the edge follows the pointer).
	OnResizeWidth func(x float32)
	Refresh       func()
	CollapseAll   func()
}

// RenderTree renders the project panel (the upstream editor's left dock): header, rows
// with chevrons, folder/file icons, indent guides and an active-file
// highlight; the panel width is user-resizable by dragging its inner edge,
// and the list scrolls horizontally when rows are wider than the panel.
func RenderTree(root *TreeNode, t ui.Theme, activePath string, width float32, cb TreeCallbacks) gpui.Element {
	header := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(ui.TreeHeaderH)).
		FlexShrink(0). // the header keeps its height while the list scrolls
		Px(ui.DefPx(10)).Gap(ui.DefPx(6)).BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).
		Child(elements.NewDiv().TextSize(ui.AbsPx(12)).Child(elements.NewText("Project").WithTextColor(t.Text)))
	name := root.Name
	if r := []rune(name); len(r) > 14 {
		name = string(r[:13]) + "…"
	}
	header = header.Child(ui.FolderIcon(t.Sky, false)).
		Child(elements.NewDiv().Flex1().OverflowHidden().WhitespaceNowrap().TextSize(ui.AbsPx(12)).Child(elements.NewText(name).WithTextColor(t.Dim)))
	button := func(id, icon, tip string, action func()) gpui.Element {
		return elements.NewDiv().ID(elements.ButtonID("tree-" + id)).W(ui.Px(22)).H(ui.Px(22)).FlexShrink(0).Flex().ItemsCenter().JustifyCenter().Rounded(ui.AbsPx(4)).CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).Tooltip(tip).
			OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { action(); w.Refresh() }).Child(ui.ToolbarIcon(icon, t.Dim))
	}
	if cb.Refresh != nil {
		header = header.Child(button("refresh", "refresh", "Refresh Project", cb.Refresh))
	}
	if cb.CollapseAll != nil {
		header = header.Child(button("collapse", "collapse", "Collapse All", cb.CollapseAll))
	}

	var rows []treeRow
	flattenVisible(root, 0, &rows)

	// Scrollbar geometry from the handle's mirrored state (one frame of
	// lag, the same timing as the editor's scrollbars).
	handle := cb.Scroll
	if handle == nil {
		handle = elements.NewScrollHandle()
	}
	content := handle.ContentSize().Height.AsF32()
	viewport := handle.ViewportSize().Height.AsF32()
	maxTop := content - viewport
	if maxTop < 0 {
		maxTop = 0
	}
	trackH := viewport - 2*ui.TreeScrollbarInset
	thumbH := trackH
	if content > 0 && viewport > 0 && content > viewport {
		thumbH = viewport / content * trackH
		if thumbH < 24 {
			thumbH = 24
		}
		if thumbH > trackH {
			thumbH = trackH
		}
	}
	travel := trackH - thumbH
	top := handle.Top().AsF32()
	thumbTop := float32(0)
	if maxTop > 0 && travel > 0 {
		thumbTop = (top / maxTop) * travel
	}

	// Horizontal geometry from the same handle (one frame of lag); the
	// thumb itself is computed where the horizontal bar renders.
	hContent := handle.ContentSize().Width.AsF32()
	hViewport := handle.ViewportSize().Width.AsF32()
	hMaxLeft := hContent - hViewport
	if hMaxLeft < 0 {
		hMaxLeft = 0
	}
	left := handle.Left().AsF32()

	// Row widths: the label's measured width plus the fixed row chrome.
	// A definite width >= the list's width keeps the hover highlight
	// spanning the whole panel, and widths beyond it give the scroll
	// container its horizontal extent (content wider than the panel).
	// The style Width is the border box (layout pinned in
	// TestDefiniteWidthBoxSizing).
	listInner := width - 1 // the panel's right border
	rowWidth := func(r treeRow) float32 {
		labelW := float32(0)
		if cb.Measure != nil {
			labelW = cb.Measure(r.node.Name)
		}
		w := float32(8+r.depth*14) + 12 + 5 + 14 + 7 + labelW + 10
		if w < listInner {
			w = listInner
		}
		return w
	}

	list := elements.NewDiv().ID(elements.ButtonID("tree-list")).
		Flex().FlexCol().Flex1().OverflowYScroll().Pt(ui.DefPx(4)).
		// Flex1 (basis 0) keeps the list's flex basis at the available height: a
		// content-sized basis leaked every visible tree row into the workspace
		// row's flex basis, inflating it past the window and collapsing the
		// shrinkable chrome on tall projects.
		TrackScroll(handle).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			// The default container scroll would handle both axes, but
			// shift+vertical-wheel must pan horizontally too (and the
			// native X delta mapping is platform-dependent): route both
			// axes here through the handle. ScrollTopBy/ScrollLeftBy
			// accumulate same-frame events (Top() is one frame stale).
			px := ev.Delta.PixelDelta(geometry.Px(treeRowHeight))
			dx, dy := px.X.AsF32(), px.Y.AsF32()
			if ev.Modifiers.Shift && dx == 0 && dy != 0 {
				// Wheel down (dy < 0) pans right, like the native
				// horizontal wheel convention (dx > 0 = later content).
				dx, dy = -dy, 0
			}
			moved := false
			if dy != 0 {
				handle.ScrollTopBy(-dy)
				moved = true
			}
			if dx != 0 {
				handle.ScrollLeftBy(dx)
				moved = true
			}
			if moved {
				w.Refresh()
			}
		})
	for i := range rows {
		r := rows[i]
		node := r.node
		active := sameFilePath(node.Path, activePath)
		w := rowWidth(r)

		row := elements.NewDiv().
			ID(elements.ButtonID("tree-" + node.Path)).
			Flex().ItemsCenter().H(ui.Px(treeRowHeight)).TextSize(ui.AbsPx(13)).
			W(ui.Px(w)). // definite: full-width highlight + horizontal extent
			Pl(ui.DefPx(float32(8 + r.depth*14))).
			Pr(ui.DefPx(10)).
			WhitespaceNowrap(). // long names extend the row; the list scrolls, never wraps
			CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) })
		if active {
			row = row.BgColor(t.Accent.Alpha(0.18))
		}

		row = row.OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
			if node.IsDir {
				cb.Toggle(node)
			} else {
				cb.OpenFile(node.Path)
			}
			w.Refresh()
		})
		if cb.Context != nil {
			row = row.OnRightDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
				cb.Context(node, ev.Position)
				w.Refresh()
			})
		}

		// Chevron for directories (fixed-width for files).
		if node.IsDir {
			row = row.Child(ui.ChevronIcon(t.Dim, node.Expanded))
		} else {
			row = row.Child(elements.NewDiv().W(ui.Px(12)))
		}
		row = row.Child(elements.NewDiv().W(ui.Px(5)))
		if node.IsDir {
			row = row.Child(ui.FolderIcon(t.Sky, node.Expanded))
		} else {
			row = row.Child(ui.FileIcon(treeFileColor(node.Name, t)))
		}
		row = row.Child(elements.NewDiv().W(ui.Px(7)))

		// The accent rail carries the selected-state signal; retain normal
		// text contrast so the active filename stays legible in all themes.
		labelColor := t.Text
		row = row.Child(elements.NewText(node.Name).WithTextColor(labelColor))

		// Active-file left bar. Absolute insets anchor at the row's
		// padding box, so cancel the row's Pl indent to keep the bar
		// hugging the row's left edge.
		if active {
			row = row.Child(elements.NewDiv().
				Absolute().Left(ui.Px(-float32(8 + r.depth*14))).Top(ui.Px(0)).Bottom(ui.Px(0)).
				W(ui.Px(2)).BgColor(t.Accent))
		}
		list = list.Child(row)
	}

	panel := elements.NewDiv().Flex().FlexCol().Relative().W(ui.Px(width)).FlexShrink(0).
		BgColor(t.Surface).BorderR_1().BorderColor(t.Surface1).
		Child(header).
		Child(list)

	// The vertical scrollbar overlays the panel's right edge (inset by the
	// resize grip) only when the rows overflow the viewport.
	if content > viewport && viewport > 0 {
		seek := func(localY float32) {
			if travel <= 0 {
				return
			}
			ratio := (localY - thumbH/2) / travel
			if ratio < 0 {
				ratio = 0
			}
			if ratio > 1 {
				ratio = 1
			}
			handle.SetTop(geometry.Px(ratio * maxTop))
		}
		scrollbar := elements.NewDiv().
			ID(elements.ButtonID("tree-vscroll")).
			Absolute().Right(ui.Px(ui.TreeResizeGrip)).Top(ui.Px(ui.TreeHeaderH + ui.TreeScrollbarInset)).Bottom(ui.Px(ui.TreeScrollbarInset)).
			W(ui.Px(ui.TreeScrollbarW)).
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1.Alpha(0.5)) }).
			// Capture-phase press: the track overlays the rows (deeper,
			// propagation-stopping), so the track claims the drag first.
			OnMouseDownCapture(func(ev platform.MouseDownEvent, w *gpui.Window) {
				if ev.Button != platform.ButtonLeft {
					return
				}
				if hb, ok := w.ListenerHitbox(); ok {
					seek(ev.Position.Y.AsF32() - float32(hb.Bounds.Origin.Y))
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
					seek(ev.Position.Y.AsF32() - float32(hb.Bounds.Origin.Y))
				}
				w.StopPropagation()
				w.Refresh()
			}).
			OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
				w.ReleasePointer()
				w.Refresh()
			}).
			Child(elements.NewDiv().
				Absolute().Left(ui.Px(1.5)).Top(ui.Px(thumbTop)).
				W(ui.Px(ui.TreeScrollbarW - 3)).H(ui.Px(thumbH)).
				Rounded(ui.AbsPx(3)).
				BgColor(t.Surface2))
		panel = panel.Child(scrollbar)
	}

	// Horizontal scrollbar along the panel's bottom edge when the rows are
	// wider than the panel (drag it like the vertical one).
	if hMaxLeft > 0 && hViewport > 0 {
		trackW := hViewport - 2*ui.TreeScrollbarInset - ui.TreeResizeGrip
		if trackW > 40 {
			hThumb := hViewport / hContent * trackW
			if hThumb < 24 {
				hThumb = 24
			}
			if hThumb > trackW {
				hThumb = trackW
			}
			hTravel := trackW - hThumb
			hLeft := float32(0)
			if hMaxLeft > 0 && hTravel > 0 {
				hLeft = (left / hMaxLeft) * hTravel
			}
			seek := func(localX float32) {
				if hTravel <= 0 {
					return
				}
				ratio := (localX - hThumb/2) / hTravel
				if ratio < 0 {
					ratio = 0
				}
				if ratio > 1 {
					ratio = 1
				}
				handle.SetLeft(geometry.Px(ratio * hMaxLeft))
			}
			hbar := elements.NewDiv().
				ID(elements.ButtonID("tree-hscroll")).
				Absolute().Left(ui.Px(0)).Bottom(ui.Px(ui.TreeScrollbarInset)).
				W(ui.Px(trackW)).H(ui.Px(ui.TreeScrollbarW)).
				Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1.Alpha(0.5)) }).
				// Capture-phase press (the track overlays the rows).
				OnMouseDownCapture(func(ev platform.MouseDownEvent, w *gpui.Window) {
					if ev.Button != platform.ButtonLeft {
						return
					}
					if hb, ok := w.ListenerHitbox(); ok {
						seek(ev.Position.X.AsF32() - float32(hb.Bounds.Origin.X))
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
						seek(ev.Position.X.AsF32() - float32(hb.Bounds.Origin.X))
					}
					w.StopPropagation()
					w.Refresh()
				}).
				OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
					w.ReleasePointer()
					w.Refresh()
				}).
				Child(elements.NewDiv().
					Absolute().Left(ui.Px(hLeft)).Top(ui.Px(1.5)).
					W(ui.Px(hThumb)).H(ui.Px(ui.TreeScrollbarW - 3)).
					Rounded(ui.AbsPx(3)).
					BgColor(t.Surface2))
			panel = panel.Child(hbar)
		}
	}

	// The resize grip rides the panel's inner (right) edge: dragging it
	// resizes the panel (the upstream editor's dock resize handle). Added last so it
	// hit-tests above the scrollbars and rows.
	grip := elements.NewDiv().
		ID(elements.ButtonID("tree-resize-grip")).
		Absolute().Right(ui.Px(0)).Top(ui.Px(0)).Bottom(ui.Px(0)).
		W(ui.Px(ui.TreeResizeGrip)).
		CursorColResize().
		Hover(func(s *style.StyleRefinement) { s.BgColor(t.Accent.Alpha(0.35)) }).
		// Capture-phase press: the grip overlays the rows (which are
		// deeper and stop propagation on their own presses), so the grip
		// must claim the gesture in capture before the rows see it.
		OnMouseDownCapture(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button != platform.ButtonLeft {
				return
			}
			if hb, ok := w.ListenerHitbox(); ok {
				w.CapturePointer(hb.ID)
			}
			w.StopPropagation()
		}).
		OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
			if !ev.Dragging() {
				return
			}
			if cb.OnResizeWidth != nil {
				cb.OnResizeWidth(ev.Position.X.AsF32())
			}
			w.StopPropagation()
		}).
		OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
			w.ReleasePointer()
			w.Refresh()
		})
	panel = panel.Child(grip)

	return panel
}

// collapseTree closes nested project folders without deleting cached entries.
func collapseTree(n *TreeNode) {
	for _, child := range n.Children {
		child.Expanded = false
		collapseTree(child)
	}
}
