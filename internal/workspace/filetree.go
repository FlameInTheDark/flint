package workspace

import (
        "os"
        "path/filepath"
        "sort"
        "strings"

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
// Dot entries ARE listed now (.github, .golangci.yml, ...): only .git stays
// hidden, everything else in the project is visible and interactable.
var ignoredEntries = map[string]bool{
        ".git": true, "node_modules": true, "target": true, "obj": true,
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

// treeVirtBuffer is how many extra rows the virtualized tree renders above
// and below the viewport. The scroll handle's mirrored geometry is one
// frame stale, so the buffer hides that lag during fast wheel scrolls and
// scrollbar drags (16 rows = 384px, far past any realistic per-frame delta).
const treeVirtBuffer = 16

// treeVirtFallbackRows is how many rows to render before the first layout
// reports the real viewport height (the scroll handle's geometry is empty
// until then).
const treeVirtFallbackRows = 40

// TreeCallbacks wires tree interactions to the workspace.
type TreeCallbacks struct {
        // Git carries the project tree's working-tree coloring snapshot (an
        // empty Repo means no repository or the status refresh has not landed
        // yet; rows then keep the default colors).
        Git TreeGitView
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
        // Drag drag-and-drop state accessors (workspace-owned gesture state;
        // nil callbacks disable dragging in bare-state tests).
        Drag    func() *treeDragState
        SetDrag func(*treeDragState)
        // Drop performs the move when a drag is released on a target.
        Drop        func(src, dstDir *TreeNode)
        Refresh     func()
        CollapseAll func()
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
                        Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).TooltipBuilder(ui.Tooltip(tip)).
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

        // Virtualization: build only the rows inside (or near) the viewport.
        // Building every row of an expanded folder cost ~65ms per frame at
        // 400 rows and ~160ms at 1000 (each row is a dozen styled elements
        // plus two SVG icons that re-parse on every paint), which made
        // expanding a folder with many files freeze the whole panel. Rows
        // are fixed-height, so the visible window is pure arithmetic on the
        // scroll handle's mirrored geometry, and spacer rows keep the scroll
        // extent identical to a fully built list: the scrollbar, wheel
        // deltas and drop hit-testing stay pixel-for-pixel what they were.
        // The horizontal extent follows the rendered rows (label widths come
        // from the per-name measure cache), not off-screen ones. `top` is the
        // handle's mirrored scroll position from the block above.
        listViewport := handle.ViewportSize().Height.AsF32()
        if listViewport <= 0 {
                listViewport = treeVirtFallbackRows * treeRowHeight
        }
        listPadTop := float32(4) // the list's Pt(4) padding above the first row
        first := int((top-listPadTop)/treeRowHeight) - treeVirtBuffer
        if first < 0 {
                first = 0
        }
        last := int((top-listPadTop+listViewport)/treeRowHeight) + 1 + treeVirtBuffer
        if last > len(rows) {
                last = len(rows)
        }

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
                if !r.node.IsDir {
                        if badge := gitRowState(cb.Git, r.node).badge(); badge != "" && cb.Measure != nil {
                                w += 6 + cb.Measure(badge) // the one-letter status badge after the name
                        }
                }
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
        if cb.Context != nil {
                // Right-click on the empty area below the rows: the project
                // root's menu (new file / new folder in the workspace root).
                // Rows handle their own context menus in the bubble phase, so
                // the background handler only fires when no row hitbox sits
                // under the pointer.
                list = list.OnRightDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
                        if treeDropTargetAt(w, ev.Position, root) != nil {
                                return
                        }
                        cb.Context(root, ev.Position)
                        w.Refresh()
                })
        }
        // The spacer above the window stands in for the rows rendered before
        // `first`; with the bottom spacer it keeps every row's y position
        // exactly where a fully built list would put it.
        if first > 0 {
                list = list.Child(elements.NewDiv().FlexShrink(0).H(ui.Px(float32(first) * treeRowHeight)))
        }
        for i := first; i < last; i++ {
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
                // Drag feedback: the carried row dims, the drop target tints green
                // when the move is legal and red when it is not. (Bare-state tests
                // wire no drag callbacks: no state, no feedback.)
                if cb.Drag != nil {
                        if d := cb.Drag(); d != nil && d.armed {
                                switch {
                                case d.source == node:
                                        row = row.BgColor(t.Surface1)
                                case d.target == node && d.valid:
                                        row = row.BgColor(t.Green.Alpha(0.18))
                                case d.target == node:
                                        row = row.BgColor(t.Red.Alpha(0.15))
                                }
                        }
                }

                // Click vs drag: a press starts a potential drag (the pointer is
                // captured like the scrollbars' drags), moving past the arm threshold
                // turns it into a drop gesture with the row under the pointer as
                // target, releasing below the threshold plays back as a click. The
                // target is hit-tested through the rendered row hitboxes, so the
                // captured source row still resolves whichever row sits under the
                // pointer.
                row = row.OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
                        if ev.Button != platform.ButtonLeft || cb.SetDrag == nil {
                                return
                        }
                        cb.SetDrag(&treeDragState{
                                node: node, source: node,
                                pressX: ev.Position.X.AsF32(), pressY: ev.Position.Y.AsF32(),
                        })
                        if hb, ok := w.ListenerHitbox(); ok {
                                w.CapturePointer(hb.ID)
                        }
                })
                row = row.OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
                        if cb.Drag == nil {
                                return
                        }
                        d := cb.Drag()
                        if d == nil || d.node != node || !ev.Dragging() {
                                return
                        }
                        dx := ev.Position.X.AsF32() - d.pressX
                        dy := ev.Position.Y.AsF32() - d.pressY
                        if !d.armed && dx*dx+dy*dy < treeDragArmPx*treeDragArmPx {
                                return
                        }
                        target := treeDropTargetAt(w, ev.Position, root)
                        var dstDir *TreeNode
                        valid := false
                        if ev.Position.X.AsF32() <= width {
                                // Only positions inside the tree panel count: releasing over
                                // the editor must cancel, never move to root.
                                dstDir = treeDropDir(target, root)
                                valid = treeDropValid(d.source, dstDir)
                        }
                        if !d.armed || d.target != target || d.valid != valid {
                                d.armed = true
                                d.target = target
                                d.valid = valid
                                cb.SetDrag(d)
                                w.Refresh()
                        }
                })
                row = row.OnMouseUp(func(ev platform.MouseUpEvent, w *gpui.Window) {
                        if ev.Button != platform.ButtonLeft {
                                return
                        }
                        if cb.Drag == nil || cb.SetDrag == nil {
                                // Bare callbacks (no drag plumbing): plain click semantics.
                                if node.IsDir {
                                        cb.Toggle(node)
                                } else {
                                        cb.OpenFile(node.Path)
                                }
                                w.Refresh()
                                return
                        }
                        d := cb.Drag()
                        if d == nil || d.node != node {
                                return
                        }
                        w.ReleasePointer()
                        cb.SetDrag(nil)
                        if d.armed {
                                if d.valid {
                                        cb.Drop(d.source, treeDropDir(d.target, root))
                                }
                        } else if node.IsDir {
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
                        row = row.Child(ui.FileTypeIcon(node.Name, t))
                }
                row = row.Child(elements.NewDiv().W(ui.Px(7)))

                // Git status coloring: the name takes the status color (folders
                // inherit their worst descendant's), files carry the one-letter
                // status badge after the name — the project explorer convention
                // (M modified, U new, A staged-added, D deleted, C conflict;
                // ignored files only dim, they get no letter).
                st := gitRowState(cb.Git, node)
                labelColor := t.Text
                if st != gitClean {
                        labelColor = gitStateColor(t, st)
                }
                row = row.Child(elements.NewText(node.Name).WithTextColor(labelColor))
                if !node.IsDir {
                        if badge := st.badge(); badge != "" {
                                row = row.Child(elements.NewDiv().W(ui.Px(6))).
                                        Child(elements.NewText(badge).WithTextColor(gitStateColor(t, st)))
                        }
                }

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
        // The spacer below the window keeps the scroll extent at the full
        // tree height, so the scrollbar and its drag math never notice the
        // virtualization.
        if rest := len(rows) - last; rest > 0 {
                list = list.Child(elements.NewDiv().FlexShrink(0).H(ui.Px(float32(rest) * treeRowHeight)))
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

// ---------------------------------------------------------------------------
// Drag and drop
// ---------------------------------------------------------------------------

// treeDragState is one in-flight tree drag (workspace-owned: the element
// tree rebuilds every frame, so gesture state lives beside the rest).
// A press starts it; moving beyond the arm threshold turns it into a
// drag; releasing without arming plays back as a click.
type treeDragState struct {
        node   *TreeNode // the row the gesture started on
        source *TreeNode // the tree node to move (same as node today)
        target *TreeNode // the row currently under the pointer (nil = none)
        armed  bool      // moved far enough to count as a drag, not a click
        valid  bool      // the current target accepts the drop
        pressX float32
        pressY float32
}

// treeDragArmPx is the pointer travel that turns a press into a drag
// (below it, a release is a click).
const treeDragArmPx = 5

// treeDropTargetAt hit-tests the rendered row hitboxes for the drop
// target: rows carry "n:btn-tree-<abs path>" keys, so the pointer's
// position resolves to a node even while the pointer is captured by the
// source row. The header buttons and scroll chrome keys don't parse as
// paths (root.Find misses them) and are skipped naturally.
func treeDropTargetAt(w *gpui.Window, pos geometry.Point[geometry.Pixels], root *TreeNode) *TreeNode {
        if w == nil || root == nil {
                return nil
        }
        const prefix = "n:btn-tree-"
        for _, box := range w.RenderedHitboxes() {
                key := box.Key
                if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
                        continue
                }
                if !geometry.BoundsContains(box.Bounds, pos) {
                        continue
                }
                if n := root.Find(key[len(prefix):]); n != nil {
                        return n
                }
        }
        return nil
}

// treeDropDir resolves the directory a drop lands in: onto a folder =
// move INTO it, onto a file = into the file's parent,
// onto the tree background/header = the project root.
func treeDropDir(target *TreeNode, root *TreeNode) *TreeNode {
        if target == nil {
                return root
        }
        if target.IsDir {
                return target
        }
        if target.Parent != nil {
                return target.Parent
        }
        return root
}

// treeDropValid reports whether src may move into dstDir: not into
// itself, not into its own subtree, not onto its current parent (a
// no-op), and no entry with the same name may exist at the destination.
func treeDropValid(src, dstDir *TreeNode) bool {
        if src == nil || dstDir == nil || !dstDir.IsDir {
                return false
        }
        if src.Path == dstDir.Path {
                return false
        }
        if src.Parent != nil && src.Parent.Path == dstDir.Path {
                return false // already there
        }
        for p := dstDir; p != nil; p = p.Parent {
                if p.Path == src.Path {
                        return false // into own subtree
                }
        }
        if _, err := os.Stat(filepath.Join(dstDir.Path, src.Name)); err == nil {
                return false // destination name taken
        }
        return true
}

// moveTreeNode performs the drop: os.Rename (same volume), reloads both
// affected directories, expands the destination and re-points open
// buffers whose paths lived under the moved subtree.
func (ws *Workspace) moveTreeNode(src *TreeNode, dstDir *TreeNode) {
        if src == nil || dstDir == nil || !dstDir.IsDir || ws.Tree == nil {
                return
        }
        if !treeDropValid(src, dstDir) {
                return
        }
        dst := filepath.Join(dstDir.Path, src.Name)
        if err := os.Rename(src.Path, dst); err != nil {
                ws.StatusError = "Move failed: " + err.Error()
                return
        }
        if parent := src.Parent; parent != nil {
                parent.Reload()
        } else {
                ws.Tree.Reload()
        }
        dstDir.Reload()
        dstDir.Expanded = true
        dstDir.EnsureLoaded()
        // The move changes git status for both directories.
        ws.markTreeGitDirty()
        // Keep open buffers pointing at the files' new locations (the tab
        // keeps its content; saving writes to the new path).
        oldPrefix := src.Path + string(os.PathSeparator)
        for _, b := range ws.Buffers {
                if b.Path == src.Path {
                        b.Path = dst
                } else if strings.HasPrefix(b.Path, oldPrefix) {
                        b.Path = filepath.Join(dst, strings.TrimPrefix(b.Path, oldPrefix))
                }
        }
        if ws.treeTextW != nil {
                ws.treeTextW = nil // label widths may change: re-measure
        }
}
