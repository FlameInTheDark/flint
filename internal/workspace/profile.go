// profile.go is the Profiler dock panel: it loads or captures pprof
// profiles and renders them as a flame graph, a top table, annotated
// source and a callers/callees peek view — every function row jumps into
// the editor when its file resolves. Sample types come from the profile's
// own value columns (CPU time vs samples; heap inuse/alloc spaces).
package workspace

import (
        "context"
        "fmt"
        "os"
        "path/filepath"
        "strings"
        "time"

        "github.com/FlameInTheDark/flint/internal/editor"
        "github.com/FlameInTheDark/flint/internal/pprof"
        "github.com/FlameInTheDark/flint/internal/ui"
        gc "github.com/FlameInTheDark/go-gpui/color"
        "github.com/FlameInTheDark/go-gpui/elements"
        "github.com/FlameInTheDark/go-gpui/geometry"
        "github.com/FlameInTheDark/go-gpui/gpui"
        "github.com/FlameInTheDark/go-gpui/platform"
        "github.com/FlameInTheDark/go-gpui/style"
)

// flameRowH is one flame graph row's height; flameMinW prunes nodes too
// narrow to paint (their value stays in the parent's Self).
const (
        flameRowH    = 18
        flameMinPx   = 2.0
        flameMaxNode = 1600 // render cap: below this width the node is pruned
)

// profileState owns the panel. The parsed profile and the flame tree are
// rebuilt when the sample type changes (values differ per column).
type profileState struct {
        // Source bookkeeping: the loaded file path or the capture URL.
        srcPath string
        srcURL  string
        srcKind string // pprof kind name (profile/heap/...); "" for files
        seconds int
        // Data.
        prof       *pprof.Profile
        tree       *pprof.Tree
        base       *pprof.Profile // diff baseline (nil = off)
        baseSrc    string         // baseline display label
        status     string         // one-line summary (source · kind · total)
        err        string
        loading    bool
        stage      string // "" | "capture" | "parse" (drives the loader label)
        startedAt  time.Time
        loaderStop chan struct{}
        cancel     context.CancelFunc
        // View state.
        tab        string // Flame | Top | Source | Peek
        sampleType string // selected sample column ("" = profile default)
        sortBy     string // top table sort: "cum" | "flat"
        search     ui.InputState
        focus      gpui.FocusHandle
        // Flame interaction: zoom root path (breadcrumb) and selection.
        zoom     []*pprof.TreeNode
        zoomLeaf *pprof.TreeNode
        selected string // selected function name (peek/source target)
        // Scroll handles + persisted offsets per view.
        flameScroll *elements.ScrollHandle
        flameTop    float32
        topScroll   *elements.ScrollHandle
        topTop      float32
        srcScroll   *elements.ScrollHandle
        srcTop      float32
        // Render caches. The element tree rebuilds every frame, so every
        // per-frame re-aggregation (tree layout, top sort, source annotation,
        // peek walk) turned each repaint into a full re-analysis — the
        // profiler dragged the whole app below 10 fps. Each cache is keyed by
        // everything its data depends on and rebuilds only on a key change.
        flameKey   flameCacheKey
        flameRows  [][]flameRect // rects grouped by depth row (viewport culling)
        flameDepth int
        flameCount int
        topKey     topCacheKey
        topCum     []pprof.TopEntry
        topFlat    []pprof.TopEntry
        baseCumMap map[string]int64
        srcKey     srcCacheKey
        srcLines   []string
        srcValues  map[int]pprof.SourceLineValue
        nodeIdx    map[string]*pprof.TreeNode // name -> highest-value node
        peekKey    peekCacheKey
        peekData   *pprof.Peek
}

// flameCacheKey identifies a laid-out flame graph (data + zoom + size +
// filter).
type flameCacheKey struct {
        tree  *pprof.Tree
        root  *pprof.TreeNode
        width float32
        query string
}

// topCacheKey identifies the top table's aggregation (sort order applies
// over the cached entries without re-aggregating).
type topCacheKey struct {
        prof       *pprof.Profile
        sampleType string
        base       *pprof.Profile
}

// srcCacheKey identifies the Source view's annotation: profile + function
// + the exact source text (buffer length is O(1), so live edits
// invalidate the cached lines without copying the text per frame).
type srcCacheKey struct {
        prof       *pprof.Profile
        file       string
        selected   string
        sampleType string
        bufPath    string
        bufLen     int
}

// peekCacheKey identifies the peek computation.
type peekCacheKey struct {
        tree     *pprof.Tree
        selected string
}

// tabs lists the views in panel order.
var profileTabs = []string{"Flame", "Top", "Source", "Peek"}

// resetViews drops interaction state (a new profile invalidates node
// pointers).
func (p *profileState) resetViews() {
        p.zoom, p.zoomLeaf, p.selected = nil, nil, ""
        p.flameScroll = elements.NewScrollHandle()
        p.flameTop = 0
        p.topScroll = elements.NewScrollHandle()
        p.topTop = 0
        p.srcScroll = elements.NewScrollHandle()
        p.srcTop = 0
        p.search.Set("")
}

// toggleProfile shows/hides the Profiler dock panel.
func (ws *Workspace) toggleProfile() {
        ws.ShowProfile = !ws.ShowProfile
        if ws.ShowProfile {
                ws.ShowTerminal, ws.ShowGoOutput, ws.ShowProblems = false, false, false
                ws.ShowDebug, ws.ShowTodos, ws.ShowVulns = false, false, false
                if ws.prof.tree == nil && ws.prof.status == "" && !ws.prof.loading {
                        ws.prof.status = "Load a profile file or capture one from a running server."
                }
        }
}

// profBusy reports whether a capture is in flight.
func (ws *Workspace) profBusy() bool { return ws.prof.loading && ws.prof.cancel != nil }

// applyLoadedProfile lands a parsed profile on the panel. The parse and
// the tree build happened OFF the UI thread; this runs inside the
// dispatched mutation and only assigns state.
func (ws *Workspace) applyLoadedProfile(prof *pprof.Profile, tree *pprof.Tree, label string) {
        p := &ws.prof
        p.prof = prof
        p.tree = tree
        p.nodeIdx = indexTreeNodes(tree)
        p.err = ""
        p.loading = false
        p.stage = ""
        stopLoader(p.loaderStop)
        p.sampleType = ""
        p.resetViews()
        p.tab = "Flame"
        p.status = label + " · " + tree.SampleType + " · " + pprof.FormatValue(tree.Total, tree.Unit)
}

// loadProfileData parses profile bytes and lands them on the panel
// (synchronous, headless/test path — the interactive paths parse off the
// UI thread: loadProfileFile for files, startCapture for captures).
func (ws *Workspace) loadProfileData(data []byte, label string) {
        prof, err := pprof.Parse(data)
        if err != nil {
                ws.prof.loading = false
                ws.prof.err = err.Error()
                return
        }
        ws.applyLoadedProfile(prof, pprof.BuildTree(prof, ""), label)
}

// rebuild re-aggregates the tree for the current sample type.
func (ws *Workspace) rebuildTree() {
        if ws.prof.prof == nil {
                ws.prof.tree, ws.prof.nodeIdx = nil, nil
                return
        }
        ws.prof.tree = pprof.BuildTree(ws.prof.prof, ws.prof.sampleType)
        ws.prof.nodeIdx = indexTreeNodes(ws.prof.tree)
}

// indexTreeNodes maps function name -> highest-value tree node (the same
// semantics as Tree.FindNode, computed once per tree build instead of a
// full walk per rendered row).
func indexTreeNodes(t *pprof.Tree) map[string]*pprof.TreeNode {
        idx := map[string]*pprof.TreeNode{}
        var walk func(n *pprof.TreeNode)
        walk = func(n *pprof.TreeNode) {
                for _, c := range n.Children {
                        if best := idx[c.Name]; best == nil || c.Value > best.Value {
                                idx[c.Name] = c
                        }
                        walk(c)
                }
        }
        walk(t.Root)
        return idx
}

// loadProfileFile opens a profile from disk (the picker/native dialog
// path). The disk read and the parse run off the UI thread when the async
// hooks are wired; bare state (tests without a window) parses inline.
func (ws *Workspace) loadProfileFile(path string) {
        dispatch, apply := ws.dispatch, ws.apply
        if dispatch == nil || apply == nil {
                data, err := pprof.FetchFile(path)
                if err != nil {
                        ws.prof.err = err.Error()
                        return
                }
                ws.prof.srcPath, ws.prof.srcURL, ws.prof.srcKind = path, "", ""
                ws.loadProfileData(data, filepath.Base(path))
                return
        }
        p := &ws.prof
        p.loading = true
        p.stage = "parse"
        p.startedAt = time.Now()
        stopLoader(p.loaderStop)
        p.loaderStop = make(chan struct{})
        startLoaderTick(apply, nil, dispatch, p.loaderStop, func(s *Workspace) {})
        go func() {
                data, err := pprof.FetchFile(path)
                if err == nil {
                        var prof *pprof.Profile
                        prof, err = pprof.Parse(data)
                        if err == nil {
                                tree := pprof.BuildTree(prof, "")
                                dispatch(func() {
                                        defer func() { _ = recover() }()
                                        apply(func(s *Workspace) {
                                                s.prof.srcPath, s.prof.srcURL, s.prof.srcKind = path, "", ""
                                                s.applyLoadedProfile(prof, tree, filepath.Base(path))
                                        })
                                })
                                return
                        }
                }
                dispatch(func() {
                        defer func() { _ = recover() }()
                        apply(func(s *Workspace) {
                                s.prof.loading = false
                                s.prof.stage = ""
                                stopLoader(s.prof.loaderStop)
                                s.prof.err = err.Error()
                        })
                })
        }()
}

// profileSummary renders the profile's kind + total for the status line
// (tests and headless paths; the interactive loader uses the already
// built tree to avoid a second aggregation).
func profileSummary(prof *pprof.Profile) string {
        tree := pprof.BuildTree(prof, "")
        return tree.SampleType + " · " + pprof.FormatValue(tree.Total, tree.Unit)
}

// startCapture fetches a live profile from a running server. It follows
// the Go task pattern: the fetch runs on a goroutine and lands through the
// dispatch hook.
func (ws *Workspace) startCapture(update updateFn, w *gpui.Window, host string) {
        if ws.profBusy() {
                return
        }
        kind := pprof.KindByName(ws.prof.srcKind)
        seconds := ws.prof.seconds
        if seconds <= 0 {
                seconds = 10
        }
        fetchURL := pprof.NormalizeSource(host, kind, seconds)
        if fetchURL == "" {
                ws.prof.err = "Enter the server address to capture from."
                return
        }
        if ws.prof.tree != nil {
                ws.prof.resetViews()
        }
        ws.prof.loading = true
        ws.prof.stage = "capture"
        ws.prof.startedAt = time.Now()
        stopLoader(ws.prof.loaderStop)
        ws.prof.loaderStop = make(chan struct{})
        startLoaderTick(update, w, ws.dispatch, ws.prof.loaderStop, func(s *Workspace) {})
        ws.prof.err = ""
        ws.prof.srcURL, ws.prof.srcPath = host, ""
        ctx, cancel := context.WithTimeout(context.Background(), pprof.CaptureTimeout(seconds))
        ws.prof.cancel = cancel
        dispatch := ws.dispatch
        if dispatch == nil || w == nil {
                data, err := pprof.FetchURL(ctx, fetchURL)
                if cancel != nil {
                        cancel()
                }
                ws.prof.cancel = nil
                if err != nil {
                        ws.prof.loading = false
                        ws.prof.err = err.Error()
                        return
                }
                ws.prof.srcKind = kind.Name
                ws.prof.srcURL, ws.prof.srcPath = host, ""
                ws.loadProfileData(data, captureLabel(host, kind, seconds))
                return
        }
        go func() {
                var data []byte
                var err error
                func() {
                        defer func() { _ = recover() }()
                        data, err = pprof.FetchURL(ctx, fetchURL)
                }()
                defer func() { _ = recover() }()
                if err != nil {
                        dispatch(func() {
                                defer func() { _ = recover() }()
                                update(func(s *Workspace) {
                                        s.prof.cancel = nil
                                        s.prof.loading = false
                                        s.prof.stage = ""
                                        stopLoader(s.prof.loaderStop)
                                        s.prof.err = err.Error()
                                })
                                w.Refresh()
                        })
                        return
                }
                // Parse off the UI thread: the decode plus tree build costs
                // hundreds of ms on real captures and froze the app at exactly
                // "the moment of getting data".
                prof, parseErr := pprof.Parse(data)
                var tree *pprof.Tree
                if parseErr == nil {
                        tree = pprof.BuildTree(prof, "")
                }
                label := captureLabel(host, kind, seconds)
                dispatch(func() {
                        defer func() { _ = recover() }()
                        update(func(s *Workspace) {
                                s.prof.cancel = nil
                                if parseErr != nil {
                                        s.prof.loading = false
                                        s.prof.stage = ""
                                        stopLoader(s.prof.loaderStop)
                                        s.prof.err = parseErr.Error()
                                        return
                                }
                                s.prof.srcKind = kind.Name
                                s.prof.srcURL, s.prof.srcPath = host, ""
                                s.applyLoadedProfile(prof, tree, label)
                        })
                        w.Refresh()
                })
        }()
}

// stopCapture cancels an in-flight capture.
func (ws *Workspace) stopCapture() {
        if c := ws.prof.cancel; c != nil {
                c()
        }
        ws.prof.loading = false
        ws.prof.stage = ""
        stopLoader(ws.prof.loaderStop)
}

// setBaseline stores the current profile as the diff base for the Top view.
func (ws *Workspace) setBaseline() {
        if ws.prof.prof == nil {
                return
        }
        ws.prof.base = ws.prof.prof
        ws.prof.baseSrc = ws.prof.status
}

// clearBaseline drops the diff base.
func (ws *Workspace) clearBaseline() {
        ws.prof.base, ws.prof.baseSrc = nil, ""
}

// selectProfileNode selects a flame/top node and reveals its Source view
// data (Peek recomputes on render).
func (ws *Workspace) selectProfileNode(name string, update updateFn, w *gpui.Window) {
        update(func(s *Workspace) {
                s.prof.selected = name
        })
        w.Refresh()
}

// jumpProfileNode opens the selected function's first source line in the
// editor (the flame graph's "jump to code" action).
func (ws *Workspace) jumpProfileNode(node *pprof.TreeNode) {
        if node == nil || node.File == "" || node.Line <= 0 {
                return
        }
        path := node.File
        if !filepath.IsAbs(path) {
                if ws.Root != "" {
                        path = filepath.Join(ws.Root, path)
                }
        }
        if !fileExists(path) {
                // Source may live in GOROOT or a module cache: show it read-only
                // through the buffer loader's own error path.
                ws.StatusError = "Source not found: " + node.File
                return
        }
        ws.openGoDiagnostic(goDiagnostic{path: path, line: int(node.Line), column: 1})
}

// profileSampleTypes lists the profile's value columns for the selector.
func (p *profileState) sampleTypes() []string {
        if p.prof == nil {
                return nil
        }
        out := make([]string, 0, len(p.prof.SampleTypes))
        for _, st := range p.prof.SampleTypes {
                out = append(out, st.Type)
        }
        return out
}

// currentSampleType resolves the effective sample column.
func (p *profileState) currentSampleType() string {
        if p.sampleType == "" && p.tree != nil {
                return p.tree.SampleType
        }
        return p.sampleType
}

// currentUnit resolves the effective unit.
func (p *profileState) currentUnit() string {
        if p.tree == nil {
                return ""
        }
        return p.tree.Unit
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// renderProfile assembles the Profiler dock panel: toolbar row, tab row
// (underline tabs), view body and status footer.
func renderProfile(ws *Workspace, t ui.Theme, update updateFn, w *gpui.Window) gpui.Element {
        p := &ws.prof
        if !p.focus.IsValid() {
                p.focus = gpui.NewFocusHandle()
        }
        // Toolbar: source actions, profile kind, sample type, search.
        bar := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(32)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(6)).
                BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1)
        bar.Child(elements.NewDiv().FlexShrink(0).Child(elements.NewText("Profiler").WithTextColor(t.Text)))
        if p.loading {
                stageLabel := "capturing " + fmt.Sprint(max(1, p.seconds)) + "s from " + p.srcURL
                if p.stage == "parse" {
                        stageLabel = "parsing the profile"
                } else if capKind := pprof.KindByName(p.srcKind); p.stage == "capture" && !capKind.Timed {
                        // Heap/allocs/goroutine snapshots are single GETs, not
                        // timed windows — don't promise "capturing 10s".
                        stageLabel = "fetching " + strings.ToLower(capKind.Title) + " snapshot from " + p.srcURL
                }
                bar.Child(elements.NewDiv().FlexShrink(0).MinW_0().OverflowHidden().WhitespaceNowrap().
                        Child(elements.NewText("· " + elapsedSeconds(p.startedAt) + " · " + stageLabel + "…").WithTextColor(t.Dim)))
        }
        bar.Child(elements.NewDiv().FlexGrow(1).MinW_0())
        tool := func(id, icon, tip string, col gc.Hsla, run func(*gpui.Window)) {
                bar.Child(elements.NewDiv().ID(elements.ButtonID("prof-" + id)).Flex().ItemsCenter().JustifyCenter().W(ui.Px(23)).H(ui.Px(23)).FlexShrink(0).
                        Rounded(ui.AbsPx(4)).CursorPointer().TooltipBuilder(ui.Tooltip(tip)).
                        Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
                        OnClick(func(_ gpui.ClickEvent, win *gpui.Window) { run(win); win.Refresh() }).
                        Child(ui.ToolbarIcon(icon, col)))
        }
        if p.loading {
                tool("stop", "stop", "Stop capture", t.Red, func(*gpui.Window) { update(func(s *Workspace) { s.stopCapture() }) })
        } else {
                capKind := pprof.KindByName(p.srcKind)
                tool("capture", "run", "Capture a profile from a running server ("+strings.ToLower(capKind.Title)+" — prompts for the address)", t.Green, func(win *gpui.Window) {
                        ws.openCapturePrompt(update, win)
                })
        }
        tool("load", "upload", "Open a profile file", t.Dim, func(*gpui.Window) { ws.openProfilePicker() })
        // Capture-kind selector: which /debug/pprof endpoint a live capture
        // fetches. Heap and Allocs carry the memory data (live RAM in use /
        // total allocated bytes) — the CPU profile has none, which made the
        // panel's RAM numbers impossible to find before.
        if !p.loading {
                kind := pprof.KindByName(p.srcKind)
                bar.Child(elements.NewDiv().ID(elements.ButtonID("prof-capture-kind")).Flex().ItemsCenter().Gap(ui.DefPx(4)).H(ui.Px(24)).Px(ui.DefPx(7)).FlexShrink(0).
                        Rounded(ui.AbsPx(4)).CursorPointer().TooltipBuilder(ui.Tooltip("Capture kind · Heap shows live memory (RAM), Allocs the total allocated")).
                        Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
                        OnClick(func(ev gpui.ClickEvent, win *gpui.Window) {
                                items := make([]ui.MenuItem, 0, len(pprof.Kinds))
                                for _, k := range pprof.Kinds {
                                        k := k
                                        items = append(items, ui.MenuItem{
                                                Label:    k.Title,
                                                Shortcut: kindHint(k),
                                                HasCheck: true,
                                                Checked:  k.Name == kind.Name,
                                                Action: func() {
                                                        update(func(s *Workspace) { s.prof.srcKind = k.Name })
                                                },
                                        })
                                }
                                openProfileMenuAt(ws, update, win, items, ev.Up.Position)
                        }).
                        Child(elements.NewText(kind.Title).WithTextColor(t.Sky)).Child(ui.Icon("chevron-down", t.Dim, 12)))
        }
        // Sample type selector (a popup menu like the tree's context menu).
        if types := p.sampleTypes(); len(types) > 1 {
                label := p.currentSampleType()
                bar.Child(elements.NewDiv().ID(elements.ButtonID("prof-sampletype")).Flex().ItemsCenter().Gap(ui.DefPx(4)).H(ui.Px(24)).Px(ui.DefPx(7)).FlexShrink(0).
                        Rounded(ui.AbsPx(4)).CursorPointer().TooltipBuilder(ui.Tooltip("Sample type")).
                        Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
                        OnClick(func(ev gpui.ClickEvent, win *gpui.Window) {
                                items := make([]ui.MenuItem, 0, len(types))
                                for _, ty := range types {
                                        ty := ty
                                        items = append(items, ui.MenuItem{Label: ty, Action: func() {
                                                update(func(s *Workspace) {
                                                        s.prof.sampleType = ty
                                                        s.prof.resetViews()
                                                        s.rebuildTree()
                                                })
                                        }})
                                }
                                openProfileMenuAt(ws, update, win, items, ev.Up.Position)
                        }).
                        Child(elements.NewText(label).WithTextColor(t.Sky)).Child(ui.Icon("chevron-down", t.Dim, 12)))
        }
        // Search filter (highlights matching flame nodes; filters Top).
        search := ui.RenderInput(ui.InputConfig{
                State: &p.search, Theme: t, Placeholder: "Filter…", Focus: p.focus,
                Update:   func(f func(*ui.InputState)) { update(func(s *Workspace) { f(&s.prof.search) }) },
                OnEscape: func(win *gpui.Window) { win.Focus(ws.FocusEditor); win.Refresh() },
        })
        bar.Child(elements.NewDiv().ID(elements.ButtonID("prof-search-box")).W(ui.Px(140)).FlexShrink(0).H(ui.Px(24)).Child(search))
        if p.base != nil {
                bar.Child(vulnChip("prof-baseline-chip", "diff vs baseline", t.Mauve))
                tool("clear-base", "eraser", "Clear the diff baseline", t.Dim, func(*gpui.Window) { update(func(s *Workspace) { s.clearBaseline() }) })
        } else if p.prof != nil {
                tool("set-base", "copy", "Store the current profile as the diff baseline", t.Dim, func(*gpui.Window) { update(func(s *Workspace) { s.setBaseline() }) })
        }
        tool("close", "close", "Hide Profiler", t.Dim, func(win *gpui.Window) {
                update(func(s *Workspace) { s.ShowProfile = false })
                win.Focus(ws.FocusEditor)
                win.Refresh()
        })

        // Tabs.
        tabs := elements.NewDiv().ID(ui.StructID("profile-tabs")).Flex().ItemsCenter().FlexShrink(0).Px(ui.DefPx(6)).Gap(ui.DefPx(2)).BorderB_1().BorderColor(t.Surface1)
        for _, tab := range profileTabs {
                active := tab == p.tab || (p.tab == "" && tab == "Flame")
                el := elements.NewDiv().ID(elements.ButtonID("profile-tab-" + tab)).Flex().ItemsCenter().H(ui.Px(28)).FlexShrink(0).
                        Px(ui.DefPx(10)).TextSize(ui.AbsPx(12)).WhitespaceNowrap().CursorPointer().
                        Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
                        OnMouseDown(func(ev platform.MouseDownEvent, win *gpui.Window) {
                                if ev.Button != platform.ButtonLeft {
                                        return
                                }
                                update(func(s *Workspace) { s.prof.tab = tab })
                                win.Refresh()
                        })
                if active {
                        el.BgColor(t.EditorBg).BorderB_2().BorderColor(t.Accent)
                }
                col := t.Dim
                if active {
                        col = t.Text
                }
                tabs.Child(el.Child(elements.NewText(tab).WithTextColor(col)))
        }
        // Right side of the tab row: the profile summary.
        tabs.Child(elements.NewDiv().FlexGrow(1))
        if p.status != "" {
                tabs.Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().Pr(ui.DefPx(8)).
                        Child(elements.NewText(p.status).WithTextColor(t.Dim)))
        }

        var body gpui.Element
        if p.prof == nil {
                if p.loading {
                        body = renderProfileLoader(t, p)
                } else {
                        body = renderProfileEmpty(ws, t, update)
                }
        } else {
                switch p.tab {
                case "Top":
                        body = renderProfileTop(ws, t, update)
                case "Source":
                        body = renderProfileSource(ws, t, update, w)
                case "Peek":
                        body = renderProfilePeek(ws, t, update, w)
                default:
                        body = renderFlame(ws, t, update, w)
                }
        }
        if p.err != "" {
                errRow := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(24)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(8)).
                        BgColor(t.Red.Alpha(0.12)).
                        Child(ui.Icon("triangle-alert", t.Red, 13)).
                        Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().Child(elements.NewText(p.err).WithTextColor(t.Red)))
                body = elements.NewDiv().Flex().FlexCol().Flex1().MinH_0().Child(errRow).Child(body)
        }
        return elements.NewDiv().ID(elements.ButtonID("profile-panel")).Flex().FlexCol().H(ui.Px(ws.effectiveTermHeight())).FlexShrink(0).
                BgColor(t.EditorBg).BorderT_1().BorderColor(t.Surface1).
                Child(renderDockResize("profile-resize", t, func(y float32, win *gpui.Window) {
                        update(func(s *Workspace) { s.SetTermHeight(y) })
                        win.Refresh()
                })).
                Child(bar).Child(tabs).Child(body)
}

// renderProfileEmpty is the no-profile state with both entry paths spelled
// out (the panel's first-run help).
func renderProfileEmpty(ws *Workspace, t ui.Theme, update updateFn) gpui.Element {
        body := elements.NewDiv().Flex().FlexCol().Flex1().MinH_0().ItemsCenter().JustifyCenter().Gap(ui.DefPx(6))
        body.Child(elements.NewDiv().Child(elements.NewText("No profile loaded").WithTextColor(t.Dim)))
        body.Child(elements.NewDiv().Child(elements.NewText("Capture a profile from a running server (adds /debug/pprof/<kind>), or open a profile file.").WithTextColor(t.Overlay)))
        body.Child(elements.NewDiv().Child(elements.NewText("Tip: go test -bench . -cpuprofile cpu.out, then open cpu.out here.").WithTextColor(t.Overlay)))
        return elements.NewDiv().Flex().FlexCol().Flex1().MinH_0().Child(body)
}

// renderProfileLoader is the loading state's body: the capture/parse runs
// off the UI thread, so the panel (and the whole app) stays interactive
// while the data is on its way — the loader shows the stage and elapsed
// time, Stop stays in the toolbar.
func renderProfileLoader(t ui.Theme, p *profileState) gpui.Element {
        body := elements.NewDiv().Flex().FlexCol().Flex1().MinH_0().ItemsCenter().JustifyCenter().Gap(ui.DefPx(8))
        body.Child(elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(8)).
                Child(ui.Icon("loader-circle", t.Accent, 16)).
                Child(elements.NewText("Loading the profile…").WithTextColor(t.Text)))
        stageLabel := "Capturing the profile"
        if p.stage == "parse" {
                stageLabel = "Parsing the profile"
        }
        body.Child(elements.NewDiv().Child(elements.NewText(stageLabel + " · " + elapsedSeconds(p.startedAt) + " elapsed").WithTextColor(t.Dim)))
        body.Child(elements.NewDiv().Child(elements.NewText("The capture runs in the background — use the toolbar's stop button to cancel.").WithTextColor(t.Overlay)))
        return elements.NewDiv().Flex().FlexCol().Flex1().MinH_0().Child(body)
}

// openProfilePicker opens the file picker in profile mode (native dialog
// first, the in-app modal as fallback).
func (ws *Workspace) openProfilePicker() {
        if ws.promptPath != nil {
                if path, ok := ws.promptPath(platform.FileDialogOptions{Files: true, Prompt: "Open Profile"}); ok && path != "" {
                        ws.applyMutation(func(s *Workspace) { s.loadProfileFile(path) })
                }
                return
        }
        ws.applyMutation(func(s *Workspace) {
                dir := s.Root
                if dir == "" {
                        if b := s.Buf(); b != nil && b.Path != "" {
                                dir = filepath.Dir(b.Path)
                        } else {
                                dir, _ = os.Getwd()
                        }
                }
                s.Picker = NewPicker(PickerLoadProfile, dir)
        })
}

// kindHint is the capture-kind menu's right-hand hint (what each endpoint
// measures; the memory ones spell out where the RAM numbers live).
func kindHint(k pprof.ProfileKind) string {
        if k.Timed {
                return "CPU time over a window"
        }
        switch k.Name {
        case "heap":
                return "live memory (RAM)"
        case "allocs":
                return "total allocated memory"
        case "goroutine":
                return "stacks of all goroutines"
        case "block":
                return "time blocked on sync/IO"
        case "mutex":
                return "time waiting on locks"
        case "threadcreate":
                return "OS threads created"
        }
        return k.Name
}

// captureLabel describes a landed profile in the status line (timed kinds
// show the collection window; snapshots don't have one).
func captureLabel(host string, kind pprof.ProfileKind, seconds int) string {
        if kind.Timed {
                return fmt.Sprintf("%s · %s (captured %ds)", host, kind.Title, seconds)
        }
        return fmt.Sprintf("%s · %s snapshot", host, kind.Title)
}

// openCapturePrompt asks for the server address (the PromptState modal).
func (ws *Workspace) openCapturePrompt(update updateFn, w *gpui.Window) {
        capKind := pprof.KindByName(ws.prof.srcKind)
        update(func(s *Workspace) {
                if !s.FocusModal.IsValid() {
                        s.FocusModal = gpui.NewFocusHandle()
                }
                s.OpenPrompt("Capture "+capKind.Title+" profile", s.prof.srcURL, "Capture", func(s *Workspace, text string) error {
                        host := strings.TrimSpace(text)
                        if host == "" {
                                return fmt.Errorf("enter the server address, e.g. localhost:8080")
                        }
                        if s.prof.seconds <= 0 {
                                s.prof.seconds = 10
                        }
                        s.startCapture(update, w, host)
                        return nil
                })
        })
        w.Focus(ws.FocusModal)
        w.Refresh()
}

// openProfileMenuAt opens the shared context popup at a window position
// (the tree's context-menu path).
func openProfileMenuAt(ws *Workspace, update updateFn, w *gpui.Window, items []ui.MenuItem, pos geometry.Point[geometry.Pixels]) {
        update(func(s *Workspace) {
                s.Menu.Open = -1
                s.Popup = &ui.PopupState{Items: items, X: float32(pos.X.AsF32()), Y: float32(pos.Y.AsF32()), Hover: -1}
        })
        w.Focus(ws.FocusPopup)
        w.Refresh()
}

// ---------------------------------------------------------------------------
// Flame graph
// ---------------------------------------------------------------------------

// flameRect is one painted flame cell, fully precomputed: the layout
// cache bakes the geometry, the heat ratio, the label and the tooltip so
// a repaint only positions elements (it never walks the tree, formats
// values or allocates strings).
type flameRect struct {
        idx  int // stable identity for the element ID ("flame-<idx>")
        node *pprof.TreeNode
        x, w float32
        depth   int
        heat    float32 // self share of the hottest frame (drives the color ramp)
        matched bool
        txt     string  // label shown inside the cell ("" = too narrow)
        // Precomputed tooltip rows (baked once per layout; a repaint never
        // formats values or allocates tooltip strings).
        tipTitle string
        tipBody  []string
        isRoot   bool // the synthetic total row
}

// layoutFlame flattens the (zoomed) subtree into per-depth rows of
// absolute x/w rects. The root fills the full width; children split their
// parent's width proportionally to value. Nodes under the pixel floor are
// skipped (their value stays in the parent's Self), and the global render
// cap bounds the cache size on pathological profiles.
func layoutFlame(root *pprof.TreeNode, width float32, unit string, query string, sampleN int) ([][]flameRect, int) {
        if root == nil || width <= 0 || root.Value <= 0 {
                return nil, 0
        }
        // Heat normalizes SELF against the view's hottest frame, not the
        // total: in a dominant-stack profile (one call path holding ~all
        // samples) every ancestor's share of the total is ~equal and the
        // leaves' self time maps to a fraction of it — the whole graph read
        // as one flat wall of amber. Against the max, the hottest frame is
        // fully red and the ramp stays informative on any profile shape.
        maxSelf := int64(0)
        var scanMax func(n *pprof.TreeNode)
        scanMax = func(n *pprof.TreeNode) {
                if n.Self > maxSelf {
                        maxSelf = n.Self
                }
                for _, c := range n.Children {
                        scanMax(c)
                }
        }
        scanMax(root)
        rows := [][]flameRect{}
        count := 0
        var rec func(n *pprof.TreeNode, x, w float32, depth int)
        rec = func(n *pprof.TreeNode, x, w float32, depth int) {
                if count >= flameMaxNode {
                        return
                }
                for len(rows) <= depth {
                        rows = append(rows, nil)
                }
                r := flameRect{idx: count, node: n, x: x, w: w, depth: depth, isRoot: n.Name == "root" && depth == 0}
                r.heat = 0
                if maxSelf > 0 {
                        r.heat = float32(n.Self) / float32(maxSelf)
                }
                r.matched = query != "" && strings.Contains(strings.ToLower(n.Name), query)
                if r.isRoot {
                        r.tipTitle = "total"
                        r.tipBody = []string{
                                pprof.FormatValue(n.Value, unit) + " · " + fmt.Sprint(sampleN) + " samples",
                        }
                        r.txt = "total " + pprof.FormatValue(n.Value, unit)
                } else {
                        r.tipTitle = n.Name
                        body := []string{
                                pprof.FormatValue(n.Value, unit) + " · " + pprof.Pct(n.Value, root.Value) + " of view",
                        }
                        if n.Self > 0 {
                                body = append(body, "self "+pprof.FormatValue(n.Self, unit)+" · "+pprof.Pct(n.Self, root.Value)+" of view")
                        }
                        if n.File != "" {
                                body = append(body, fmt.Sprintf("%s:%d", n.File, n.Line))
                        }
                        r.tipBody = body
                        // In-cell labels stay name-only for narrow cells; the
                        // cumulative percent only earns space on wide frames (a
                        // linear stack repeats the same number on every row).
                        if w > 160 {
                                r.txt = pprof.ShortFunc(n.Name) + " · " + pprof.Pct(n.Value, root.Value)
                        } else if w > 24 {
                                r.txt = pprof.ShortFunc(n.Name)
                        }
                }
                rows[depth] = append(rows[depth], r)
                count++
                if n.Value <= 0 || len(n.Children) == 0 {
                        return
                }
                cx := x
                for _, c := range n.Children {
                        cw := w * float32(c.Value) / float32(n.Value)
                        if cw >= flameMinPx {
                                rec(c, cx, cw, depth+1)
                        }
                        cx += cw
                }
        }
        rec(root, 0, width, 0)
        return rows, count
}

// flameHeat maps a frame's normalized SELF heat (share of the view's
// hottest frame) onto an amber→red ramp (the hotspot finder: wide-but-
// shallow frames stay cold, busy leaves glow). The hue interpolates the
// SHORT way around the color wheel (H is
// normalized 0..1 in go-gpui; raw linear interpolation from amber to red
// would cross green).
func flameHeat(t ui.Theme, heat float32, matched bool) gc.Hsla {
        base := gc.RGB(0xE0AF68).Hsla() // amber
        hot := gc.RGB(0xE06C75).Hsla()  // soft red
        k := heat
        if k > 1 {
                k = 1
        }
        if k < 0 {
                k = 0
        }
        // Ease the ramp: most frames are cold; only real hotspots go red.
        k = k * k
        dh := hot.H - base.H
        if dh > 0.5 {
                dh -= 1
        } else if dh < -0.5 {
                dh += 1
        }
        c := gc.Hsla{
                H: base.H + dh*k,
                S: base.S + (hot.S-base.S)*k,
                L: base.L + (hot.L-base.L)*k,
                A: 0.92,
        }
        if matched {
                c = t.Accent.Alpha(0.35)
        }
        return c
}

// flameHotText is the fixed near-black label color for bright flame fills.
// Fixed (not theme-derived): the heat ramp stays amber/orange in every
// theme, and a light theme's own text color is mid-dark enough to contrast
// poorly with it.
var flameHotText = gc.RGB(0x1A1B22).Hsla()

// flameLabelColor picks a flame cell's label color a human can actually
// read: the cell's fill is composited over the panel background (the ramp
// paints at alpha 0.92) and its effective luminance decides — bright
// fills (amber, orange, red) take the near-black label, dark fills
// (theme-tinted matches on dark surfaces) keep the theme text.
func flameLabelColor(panelBg, fill, fallback gc.Hsla) gc.Hsla {
        lum := func(c gc.Hsla) float32 {
                rgb := c.ToRgb()
                return 0.2126*rgb.R + 0.7152*rgb.G + 0.0722*rgb.B
        }
        a := fill.A
        if a > 1 {
                a = 1
        }
        if lum(fill)*a+lum(panelBg)*(1-a) >= 0.35 {
                return flameHotText
        }
        return fallback
}

// renderFlame paints the icicle flame graph: absolutely positioned cells
// under a breadcrumb/legend header, vertically scrollable, click to zoom,
// hover to inspect, double-purpose click on the selected cell for the jump.
func renderFlame(ws *Workspace, t ui.Theme, update updateFn, w *gpui.Window) gpui.Element {
        p := &ws.prof
        tree := p.tree
        if tree == nil {
                return renderProfileEmpty(ws, t, update)
        }
        width := ws.paneWidth() - 16
        if width < 200 {
                width = 200
        }
        root := tree.Root
        if p.zoomLeaf != nil {
                root = p.zoomLeaf
        }
        query := strings.ToLower(strings.TrimSpace(p.search.String()))

        // Flame cache: the layout (with its baked labels and tooltips)
        // rebuilds only when the tree, zoom root, width or filter changes;
        // every other frame just positions cached rects.
        key := flameCacheKey{tree: tree, root: root, width: width, query: query}
        if p.flameKey != key {
                p.flameRows, p.flameCount = layoutFlame(root, width, p.currentUnit(), query, tree.SampleN)
                p.flameDepth = len(p.flameRows)
                p.flameKey = key
        }
        depth := p.flameDepth

        legend := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(26)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(8)).
                BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).TextSize(ui.AbsPx(11))
        if len(p.zoom) > 0 {
                legend.Child(elements.NewDiv().ID(elements.ButtonID("flame-zoom-reset")).Flex().ItemsCenter().Gap(ui.DefPx(4)).H(ui.Px(20)).Px(ui.DefPx(6)).
                        Rounded(ui.AbsPx(4)).CursorPointer().TooltipBuilder(ui.Tooltip("Reset zoom (Esc)")).
                        Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
                        OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
                                update(func(s *Workspace) { s.prof.zoom, s.prof.zoomLeaf = nil, nil })
                                win.Refresh()
                        }).Child(ui.Icon("chevron-down", t.Dim, 12)).Child(elements.NewText("Reset zoom").WithTextColor(t.Text)))
                // Breadcrumb: every ancestor on the zoom path.
                for i, n := range p.zoom {
                        bc := elements.NewDiv().ID(elements.ButtonID(fmt.Sprintf("flame-crumb-%d", i))).FlexShrink(0).MinW_0().OverflowHidden().WhitespaceNowrap().
                                Child(elements.NewText(pprof.ShortFunc(n.Name)).WithTextColor(t.Dim))
                        legend.Child(bc)
                        legend.Child(elements.NewDiv().FlexShrink(0).Child(elements.NewText("/").WithTextColor(t.Overlay)))
                }
        }
        legend.Child(elements.NewDiv().FlexGrow(1))
        // Heat legend: the self-time ramp the cells use (cold amber → hot red).
        legend.Child(elements.NewDiv().FlexShrink(0).Flex().ItemsCenter().Gap(ui.DefPx(5)).
                Child(elements.NewText("self time").WithTextColor(t.Overlay)).
                Child(heatRamp(t)))
        legend.Child(elements.NewDiv().FlexShrink(0).Child(elements.NewText(
                fmt.Sprintf("total %s · %s · %d samples", pprof.FormatValue(root.Value, p.currentUnit()), p.currentSampleType(), tree.SampleN)).WithTextColor(t.Dim)))

        // The canvas: one relative container sized depth rows tall; cells
        // absolutely positioned. The scroll container wraps it for deep stacks.
        canvas := elements.NewDiv().Relative().FlexShrink(0).
                W(ui.Px(width)).H(ui.Px(float32(depth * (flameRowH + 1))))
        // Viewport culling: cells are created only for depth rows intersecting
        // the scroll viewport. Top() is one frame stale (the same timing as the
        // scrollbar thumb); the ±1 row margins absorb it, and with no measured
        // viewport yet (first frame, headless) every row renders.
        visTop := p.flameScroll.Top().AsF32()
        visH := p.flameScroll.ViewportSize().Height.AsF32()
        if visH <= 0 {
                visH = float32(depth) * (flameRowH + 1)
        }
        rowPitch := float32(flameRowH + 1)
        firstRow := int(visTop/rowPitch) - 1
        lastRow := int((visTop+visH)/rowPitch) + 1
        if firstRow < 0 {
                firstRow = 0
        }
        if lastRow > depth-1 {
                lastRow = depth - 1
        }
        for d := firstRow; d <= lastRow; d++ {
                for _, nl := range p.flameRows[d] {
                        n := nl.node
                        matched := nl.matched
                        col := flameHeat(t, nl.heat, matched)
                        // Label color follows the fill's on-screen brightness:
                        // the heat ramp is bright at BOTH ends (amber ≈ 64%
                        // lightness), so the near-white theme text used to wash
                        // out completely on yellow/orange cells.
                        textCol := flameLabelColor(t.EditorBg, col, t.Text)
                        var cell *elements.Div
                        cellID := fmt.Sprintf("flame-%d", nl.idx)
                        if nl.isRoot {
                                // Root row: total bar (also the zoom reset target).
                                cell = elements.NewDiv().ID(elements.ButtonID(cellID)).Absolute().Left(ui.Px(nl.x)).Top(ui.Px(float32(nl.depth * (flameRowH + 1)))).
                                        W(ui.Px(maxF(nl.w, 1))).H(ui.Px(flameRowH)).BgColor(t.Surface1).Rounded(ui.AbsPx(2)).
                                        ItemsCenter().Pl(ui.DefPx(4)).OverflowHidden().CursorPointer().
                                        TooltipBuilder(ui.TooltipRich(nl.tipTitle, nl.tipBody, "")).
                                        Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().TextSize(ui.AbsPx(10)).
                                                Child(elements.NewText(nl.txt).WithTextColor(t.Dim)))
                        } else {
                                cell = elements.NewDiv().ID(elements.ButtonID(cellID)).Absolute().Left(ui.Px(nl.x)).Top(ui.Px(float32(nl.depth * (flameRowH + 1)))).
                                        W(ui.Px(maxF(nl.w, 1))).H(ui.Px(flameRowH)).BgColor(col).Rounded(ui.AbsPx(2)).
                                        ItemsCenter().Pl(ui.DefPx(4)).OverflowHidden().
                                        TooltipBuilder(ui.TooltipRich(nl.tipTitle, nl.tipBody, "Click: zoom · Right-click: source, peek")).
                                        CursorPointer().
                                        Hover(func(s *style.StyleRefinement) { s.BgColor(col.Alpha(1.0)) })
                                if nl.w >= 8 {
                                        // A 1px dark seam between siblings: adjacent frames of the
                                        // same color read as separate bars instead of one long strip.
                                        cell = cell.BorderR_1().BorderColor(t.EditorBg.Alpha(0.45))
                                }
                                if nl.txt != "" {
                                        cell.Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().TextSize(ui.AbsPx(10)).
                                                Child(elements.NewText(nl.txt).WithTextColor(textCol)))
                                }
                                node := n
                                cell.OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
                                        update(func(s *Workspace) { s.zoomFlame(node) })
                                        win.Refresh()
                                })
                                cell.OnRightDown(func(ev platform.MouseDownEvent, win *gpui.Window) {
                                        menu := []ui.MenuItem{
                                                {Label: "Jump to Source", Action: func() {
                                                        update(func(s *Workspace) {
                                                                s.prof.selected = node.Name
                                                                s.jumpProfileNode(node)
                                                        })
                                                }},
                                                {Label: "Peek Callers/Callees", Action: func() {
                                                        update(func(s *Workspace) { s.prof.selected, s.prof.tab = node.Name, "Peek" })
                                                }},
                                                {Label: "Show in Top", Action: func() {
                                                        update(func(s *Workspace) { s.prof.selected, s.prof.tab = node.Name, "Top" })
                                                }},
                                                {Label: "Zoom Here", Action: func() {
                                                        update(func(s *Workspace) { s.zoomFlame(node) })
                                                }},
                                        }
                                        openProfileMenuAt(ws, update, win, menu, ev.Position)
                                })
                        }
                        canvas.Child(cell)
                }
        }
        list := elements.NewDiv().ID(elements.ButtonID("flame-scroll")).Flex().FlexCol().Flex1().MinH_0().MinW_0().
                OverflowYScroll().TrackScroll(p.flameScroll).Px(ui.DefPx(8)).Py(ui.DefPx(4)).
                OnScroll(func(ev platform.ScrollEvent, win *gpui.Window) {
                        dy := ev.Delta.PixelDelta(geometry.Px(20)).Y.AsF32()
                        if dy != 0 {
                                p.flameScroll.ScrollTopBy(-dy)
                                p.flameTop = max(0, p.flameTop-dy)
                                win.StopPropagation()
                                win.Refresh()
                        }
                })
        list.Child(canvas)
        if p.flameCount == 0 {
                list.Child(debugRow("No samples for this profile and sample type.", t.Dim))
        }
        hint := elements.NewDiv().Flex().ItemsCenter().FlexShrink(0).H(ui.Px(22)).Px(ui.DefPx(10)).Gap(ui.DefPx(6)).
                BgColor(t.Surface).BorderT_1().BorderColor(t.Surface1).TextSize(ui.AbsPx(11)).
                Child(elements.NewText("Click a frame to zoom · right-click for actions (jump to source, peek)").WithTextColor(t.Overlay))
        return elements.NewDiv().ID(ui.StructID("profile-flame")).Flex().FlexCol().Flex1().MinH_0().Child(legend).Child(list).Child(hint)
}

func maxF(a, b float32) float32 {
        if a > b {
                return a
        }
        return b
}

// heatRamp paints the flame color ramp as eight discrete cells (the
// legend's key to the self-time coloring).
func heatRamp(t ui.Theme) gpui.Element {
        row := elements.NewDiv().Flex().ItemsCenter().FlexShrink(0).OverflowHidden().Rounded(ui.AbsPx(2))
        for i := 0; i < 8; i++ {
                row.Child(elements.NewDiv().W(ui.Px(7)).H(ui.Px(9)).BgColor(flameHeat(t, float32(i)/7, false)))
        }
        return row
}

// zoomFlame re-roots the flame graph at a node (pushing the breadcrumb).
func (ws *Workspace) zoomFlame(node *pprof.TreeNode) {
        if node == nil || node.Name == "root" {
                return
        }
        p := &ws.prof
        if p.zoomLeaf == node {
                return
        }
        // Rebuild the ancestor path from the current zoom root.
        base := p.tree.Root
        for _, n := range p.zoom {
                base = n
        }
        path := findFlamePath(base, node)
        if path == nil {
                return
        }
        p.zoom = append(p.zoom, path[:len(path)-1]...)
        p.zoomLeaf = node
        p.flameTop = 0
}

// findFlamePath finds the ancestor chain root->node (inclusive).
func findFlamePath(root, target *pprof.TreeNode) []*pprof.TreeNode {
        if root == target {
                return []*pprof.TreeNode{root}
        }
        for _, c := range root.Children {
                if sub := findFlamePath(c, target); sub != nil {
                        return append([]*pprof.TreeNode{root}, sub...)
                }
        }
        return nil
}

// ---------------------------------------------------------------------------
// Top table
// ---------------------------------------------------------------------------

// renderProfileTop renders the flat/cum table with inline bars, click to
// select + jump, and (with a baseline) delta columns.
func renderProfileTop(ws *Workspace, t ui.Theme, update updateFn) gpui.Element {
        p := &ws.prof
        persistScroll(p.topScroll, &p.topTop)
        if p.sortBy == "" {
                p.sortBy = "cum"
        }
        // Top cache: the aggregation runs once per (profile, sample type,
        // baseline) — both sort orders come out of the cache, so switching
        // the sort column (and every repaint) never re-walks the samples.
        tk := topCacheKey{prof: p.prof, sampleType: p.sampleType, base: p.base}
        if p.topKey != tk {
                p.topCum = pprof.Top(p.prof, p.sampleType, "cum")
                p.topFlat = pprof.Top(p.prof, p.sampleType, "flat")
                p.baseCumMap = nil
                if p.base != nil {
                        p.baseCumMap = map[string]int64{}
                        for _, e := range pprof.Top(p.base, p.sampleType, "cum") {
                                p.baseCumMap[e.Name] = e.Cum
                        }
                }
                p.topKey = tk
        }
        entries := p.topCum
        if p.sortBy == "flat" {
                entries = p.topFlat
        }
        query := strings.ToLower(strings.TrimSpace(p.search.String()))
        header := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(26)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(10)).
                BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).TextSize(ui.AbsPx(11))
        sortBtn := func(id, label, column string) *elements.Div {
                el := elements.NewDiv().ID(elements.ButtonID("top-sort-" + id)).FlexShrink(0).Px(ui.DefPx(5)).Rounded(ui.AbsPx(3)).CursorPointer().
                        Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
                        OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
                                update(func(s *Workspace) { s.prof.sortBy = column })
                                win.Refresh()
                        })
                col := t.Dim
                if p.sortBy == column {
                        col = t.Text
                        el = el.BgColor(t.Surface2)
                }
                return el.Child(elements.NewText(label).WithTextColor(col))
        }
        header.Child(sortBtn("flat", "flat", "flat"))
        header.Child(sortBtn("cum", "cum", "cum"))
        header.Child(elements.NewDiv().FlexShrink(0).Pl(ui.DefPx(8)).Child(elements.NewText("function").WithTextColor(t.Dim)))
        header.Child(elements.NewDiv().FlexGrow(1))
        if p.base != nil {
                header.Child(elements.NewDiv().FlexShrink(0).Child(elements.NewText("Δ vs baseline").WithTextColor(t.Mauve)))
        }
        list := elements.NewDiv().ID(elements.ButtonID("profile-top-list")).Flex().FlexCol().Flex1().MinH_0().MinW_0().
                OverflowYScroll().TrackScroll(p.topScroll).Px(ui.DefPx(8)).Py(ui.DefPx(4)).TextSize(ui.AbsPx(12)).
                OnScroll(func(ev platform.ScrollEvent, win *gpui.Window) {
                        dy := ev.Delta.PixelDelta(geometry.Px(20)).Y.AsF32()
                        if dy != 0 {
                                p.topScroll.ScrollTopBy(-dy)
                                p.topTop = max(0, p.topTop-dy)
                                win.StopPropagation()
                                win.Refresh()
                        }
                })
        total := p.tree.Total
        if total == 0 {
                list.Child(debugRow("No samples in this profile.", t.Dim))
                return elements.NewDiv().Flex().FlexCol().Flex1().MinH_0().Child(header).Child(list)
        }
        shown := 0
        for _, e := range entries {
                if query != "" && !strings.Contains(strings.ToLower(e.Name), query) {
                        continue
                }
                if shown >= 500 {
                        list.Child(debugRow("… filter to see more functions", t.Dim))
                        break
                }
                shown++
                e := e
                barW := float32(0)
                if p.sortBy == "flat" {
                        barW = 120 * float32(e.Flat) / float32(total)
                } else {
                        barW = 120 * float32(e.Cum) / float32(total)
                }
                row := elements.NewDiv().ID(elements.ButtonID("top-" + e.Name)).Flex().ItemsCenter().H(ui.Px(24)).FlexShrink(0).Gap(ui.DefPx(8)).
                        CursorPointer().OverflowHidden().WhitespaceNowrap().
                        Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) })
                if p.selected == e.Name {
                        row.BgColor(t.Accent.Alpha(0.18))
                }
                mainCol := ui.MonoText(pprof.FormatValue(e.Flat, p.currentUnit()), t.Text)
                cumCol := ui.MonoText(pprof.FormatValue(e.Cum, p.currentUnit()), t.Dim)
                nameCol := elements.NewDiv().Flex1().MinW_0().OverflowHidden().Child(ui.MonoText(e.Name, t.Text))
                barCol := flameHeat(t, float32(e.Flat)/float32(total), false)
                bar := elements.NewDiv().W(ui.Px(120)).H(ui.Px(8)).FlexShrink(0).Rounded(ui.AbsPx(3)).BgColor(t.Surface1).
                        Child(elements.NewDiv().W(ui.Px(maxF(barW, 1))).H(ui.Px(8)).Rounded(ui.AbsPx(3)).BgColor(barCol))
                row.Child(mainCol).Child(cumCol).Child(nameCol).Child(bar)
                if p.base != nil {
                        deltaCol := elements.NewDiv().W(ui.Px(90)).FlexShrink(0)
                        if b, ok := p.baseCumMap[e.Name]; ok {
                                diff := e.Cum - b
                                col := t.Dim
                                label := pprof.FormatValue(diff, p.currentUnit())
                                if diff > 0 {
                                        col = t.Red
                                } else if diff < 0 {
                                        col = t.Green
                                }
                                deltaCol.Child(ui.MonoText(label, col))
                        } else {
                                deltaCol.Child(ui.MonoText("new", t.Yellow))
                        }
                        row.Child(deltaCol)
                }
                tipBody := []string{}
                if e.File != "" {
                        tipBody = append(tipBody, e.File+":"+fmt.Sprint(e.Line))
                }
                tipBody = append(tipBody, fmt.Sprintf("flat %s · cum %s · %s of total",
                        pprof.FormatValue(e.Flat, p.currentUnit()), pprof.FormatValue(e.Cum, p.currentUnit()), pprof.Pct(e.Cum, total)))
                row.TooltipBuilder(ui.TooltipRich(e.Name, tipBody, "Click: select · Right-click: jump/peek"))
                row.OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
                        update(func(s *Workspace) { s.prof.selected = e.Name })
                        win.Refresh()
                })
                row.OnRightDown(func(ev platform.MouseDownEvent, win *gpui.Window) {
                        menu := []ui.MenuItem{
                                {Label: "Jump to Source", Action: func() {
                                        update(func(s *Workspace) {
                                                s.prof.selected = e.Name
                                                if n := s.prof.nodeIdx[e.Name]; n != nil {
                                                        s.jumpProfileNode(n)
                                                }
                                        })
                                }},
                                {Label: "Peek Callers/Callees", Action: func() {
                                        update(func(s *Workspace) { s.prof.selected, s.prof.tab = e.Name, "Peek" })
                                }},
                        }
                        openProfileMenuAt(ws, update, win, menu, ev.Position)
                })
                list.Child(row)
        }
        if shown == 0 {
                list.Child(debugRow("No functions match the filter.", t.Dim))
        }
        return elements.NewDiv().Flex().FlexCol().Flex1().MinH_0().Child(header).Child(list)
}

// ---------------------------------------------------------------------------
// Source view (annotated per-line values)
// ---------------------------------------------------------------------------

// renderProfileSource shows the selected function's source with per-line
// flat/cum columns (the `pprof -list` view); clicking a hot line jumps to
// the editor. The buffer's live text is preferred (unsaved edits stay
// honest), disk read as fallback.
func renderProfileSource(ws *Workspace, t ui.Theme, update updateFn, w *gpui.Window) gpui.Element {
        p := &ws.prof
        persistScroll(p.srcScroll, &p.srcTop)
        if p.prof == nil {
                return renderProfileEmpty(ws, t, update)
        }
        if p.selected == "" {
                // Default to the highest-cum user function.
                if top := pprof.Top(p.prof, p.sampleType, "cum"); len(top) > 0 {
                        p.selected = top[0].Name
                }
        }
        if p.selected == "" {
                return elements.NewDiv().Flex().Flex1().ItemsCenter().Child(debugRow("Select a function in the flame graph or the top table.", t.Dim))
        }
        // Locate the function's file.
        var file string
        if n := p.nodeIdx[p.selected]; n != nil {
                file = n.File
        }
        if file == "" {
                for _, e := range pprof.Top(p.prof, p.sampleType, "cum") {
                        if e.Name == p.selected {
                                file = e.File
                                break
                        }
                }
        }
        path := file
        if path != "" && !filepath.IsAbs(path) && ws.Root != "" {
                path = filepath.Join(ws.Root, path)
        }
        // Source cache: the annotation walk and the source text resolve once
        // per (profile, function, source text). The buffer's O(1) TextLen is
        // part of the key, so live edits invalidate the cached lines without
        // copying the whole buffer every frame.
        bufLen := -1
        var openBuf *editor.Buffer
        for _, b := range ws.Buffers {
                if path != "" && sameFilePath(b.Path, path) {
                        openBuf = b
                        bufLen = b.TextLen()
                        break
                }
        }
        sk := srcCacheKey{prof: p.prof, file: file, selected: p.selected, sampleType: p.sampleType, bufPath: path, bufLen: bufLen}
        if p.srcKey != sk {
                p.srcValues = pprof.AnnotateLines(p.prof, file, p.selected, p.sampleType)
                p.srcLines = nil
                if path != "" {
                        var src []byte
                        if openBuf != nil {
                                src = []byte(openBuf.Text())
                        } else if data, err := os.ReadFile(path); err == nil {
                                src = data
                        }
                        if src != nil {
                                p.srcLines = strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
                        }
                }
                p.srcKey = sk
        }
        srcLabel := ""
        if path != "" {
                srcLabel = file
        }
        header := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(26)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(8)).
                BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).TextSize(ui.AbsPx(11))
        header.Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().Child(ui.MonoText(p.selected, t.Text)))
        header.Child(elements.NewDiv().FlexGrow(1))
        header.Child(elements.NewDiv().FlexShrink(0).Child(elements.NewText(srcLabel).WithTextColor(t.Dim)))
        list := elements.NewDiv().ID(elements.ButtonID("profile-source-list")).Flex().FlexCol().Flex1().MinH_0().MinW_0().
                OverflowYScroll().TrackScroll(p.srcScroll).Px(ui.DefPx(8)).Py(ui.DefPx(4)).
                OnScroll(func(ev platform.ScrollEvent, win *gpui.Window) {
                        dy := ev.Delta.PixelDelta(geometry.Px(20)).Y.AsF32()
                        if dy != 0 {
                                p.srcScroll.ScrollTopBy(-dy)
                                p.srcTop = max(0, p.srcTop-dy)
                                win.StopPropagation()
                                win.Refresh()
                        }
                })
        if p.srcLines == nil {
                list.Child(debugRow("Source not available: "+file, t.Dim))
                list.Child(debugRow("Open the file in the editor (or keep it in the workspace) to see per-line values.", t.Overlay))
        } else {
                unit := p.currentUnit()
                // Viewport culling: one 18px row per source line — only the lines
                // intersecting the scroll viewport render (a 2000-line function
                // used to create ~2000 rows per frame).
                visTop := p.srcScroll.Top().AsF32()
                visH := p.srcScroll.ViewportSize().Height.AsF32()
                if visH <= 0 {
                        visH = float32(len(p.srcLines)) * 19
                }
                const srcRowPitch = float32(19) // 18px row + 1px stack spacing
                first := int(visTop/srcRowPitch) - 1
                last := int((visTop+visH)/srcRowPitch) + 1
                if first < 0 {
                        first = 0
                }
                if last > len(p.srcLines)-1 {
                        last = len(p.srcLines) - 1
                }
                for i := first; i <= last; i++ {
                        text := p.srcLines[i]
                        lineNo := i + 1
                        v, hot := p.srcValues[lineNo]
                        row := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(18)).FlexShrink(0).Gap(ui.DefPx(8)).OverflowHidden().WhitespaceNowrap()
                        flatCol := elements.NewDiv().W(ui.Px(64)).FlexShrink(0).TextSize(ui.AbsPx(11))
                        if v.Flat > 0 {
                                flatCol.Child(ui.MonoText(pprof.FormatValue(v.Flat, unit), t.Red))
                        } else {
                                flatCol.Child(ui.MonoText("·", t.Overlay))
                        }
                        cumCol := elements.NewDiv().W(ui.Px(64)).FlexShrink(0).TextSize(ui.AbsPx(11))
                        if v.Cum > 0 && v.Cum != v.Flat {
                                cumCol.Child(ui.MonoText(pprof.FormatValue(v.Cum, unit), t.Yellow))
                        } else if v.Cum > 0 {
                                cumCol.Child(ui.MonoText(pprof.FormatValue(v.Cum, unit), t.Red))
                        } else {
                                cumCol.Child(ui.MonoText("·", t.Overlay))
                        }
                        noCol := elements.NewDiv().W(ui.Px(34)).FlexShrink(0).TextSize(ui.AbsPx(11))
                        noCol.Child(ui.MonoText(fmt.Sprint(lineNo), t.Overlay))
                        codeCol := ui.MonoText(text, t.Dim)
                        row.Child(flatCol).Child(cumCol).Child(noCol).Child(codeCol)
                        if hot {
                                row.BgColor(t.Yellow.Alpha(0.10))
                                row = row.ID(elements.ButtonID(fmt.Sprintf("prof-srcline-%d", lineNo))).CursorPointer().
                                        Hover(func(s *style.StyleRefinement) { s.BgColor(t.Yellow.Alpha(0.2)) }).
                                        OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
                                                update(func(s *Workspace) { s.jumpProfileLine(path, lineNo) })
                                                win.Focus(ws.FocusEditor)
                                                win.Refresh()
                                        })
                        }
                        list.Child(row)
                }
        }
        hint := elements.NewDiv().Flex().ItemsCenter().FlexShrink(0).H(ui.Px(22)).Px(ui.DefPx(10)).
                BgColor(t.Surface).BorderT_1().BorderColor(t.Surface1).TextSize(ui.AbsPx(11)).
                Child(elements.NewText("flat = time in the line itself · cum = time through it · click a hot line to open it in the editor").WithTextColor(t.Overlay))
        return elements.NewDiv().Flex().FlexCol().Flex1().MinH_0().Child(header).Child(list).Child(hint)
}

// jumpProfileLine opens a source line in the editor (the Source view's
// click action).
func (ws *Workspace) jumpProfileLine(path string, line int) {
        if !fileExists(path) {
                ws.StatusError = "Source not found: " + path
                return
        }
        ws.openGoDiagnostic(goDiagnostic{path: path, line: line, column: 1})
}

// ---------------------------------------------------------------------------
// Peek view (callers / callees)
// ---------------------------------------------------------------------------

// renderProfilePeek renders the callers/callees of the selected function
// (the pprof peek view); entries click-jump like the top table.
func renderProfilePeek(ws *Workspace, t ui.Theme, update updateFn, w *gpui.Window) gpui.Element {
        p := &ws.prof
        if p.tree == nil {
                return renderProfileEmpty(ws, t, update)
        }
        if p.selected == "" {
                if top := pprof.Top(p.prof, p.sampleType, "cum"); len(top) > 0 {
                        p.selected = top[0].Name
                }
        }
        if p.selected == "" {
                return elements.NewDiv().Flex().Flex1().ItemsCenter().Child(debugRow("Select a function in the flame graph or the top table.", t.Dim))
        }
        // Peek cache: the full-tree callers/callees walk runs once per
        // (tree, selected function) — not per frame.
        pkKey := peekCacheKey{tree: p.tree, selected: p.selected}
        if p.peekKey != pkKey {
                p.peekData = p.tree.PeekAt(p.selected)
                p.peekKey = pkKey
        }
        pk := p.peekData
        segment := func(id, title string, entries []pprof.TopEntry, empty string) *elements.Div {
                seg := elements.NewDiv().ID(ui.StructID("peek-" + id)).Flex().FlexCol().Flex1().MinW_0().MinH_0().BorderR_1().BorderColor(t.Surface1)
                head := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(24)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(8)).
                        BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).TextSize(ui.AbsPx(11)).
                        Child(elements.NewText(title).WithTextColor(t.Dim))
                head.Child(elements.NewDiv().FlexGrow(1))
                head.Child(ui.MonoText(fmt.Sprint(len(entries)), t.Dim))
                seg.Child(head)
                scroll := elements.NewDiv().Flex().FlexCol().Flex1().MinH_0().MinW_0().OverflowYScroll().Px(ui.DefPx(8)).Py(ui.DefPx(4))
                if len(entries) == 0 {
                        scroll.Child(debugRow(empty, t.Overlay))
                }
                for _, e := range entries {
                        e := e
                        node := p.nodeIdx[e.Name]
                        label := fmt.Sprintf("%s · %s", pprof.ShortFunc(e.Name), pprof.FormatValue(e.Flat, p.currentUnit()))
                        row := elements.NewDiv().ID(elements.ButtonID("peek-" + id + "-" + e.Name)).Flex().ItemsCenter().H(ui.Px(22)).FlexShrink(0).Gap(ui.DefPx(6)).
                                CursorPointer().OverflowHidden().WhitespaceNowrap().Px(ui.DefPx(2)).
                                Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
                                TooltipBuilder(ui.TooltipRich(e.Name, peekTipBody(e), "Click: open in the editor")).
                                OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
                                        update(func(s *Workspace) {
                                                s.prof.selected = e.Name
                                                s.jumpProfileNode(node)
                                        })
                                        win.Focus(ws.FocusEditor)
                                        win.Refresh()
                                }).
                                Child(ui.MonoText(label, t.Text))
                        scroll.Child(row)
                }
                return seg.Child(scroll)
        }
        head := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(24)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(8)).
                BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).
                Child(ui.MonoText("Peek · "+p.selected, t.Text)).Child(elements.NewDiv().FlexGrow(1))
        return elements.NewDiv().ID(ui.StructID("profile-peek")).Flex().FlexCol().Flex1().MinH_0().
                Child(head).
                Child(elements.NewDiv().Flex().Flex1().MinH_0().
                        Child(segment("callers", "Callers", pk.Callers, "No callers recorded (entry point).")).
                        Child(segment("callees", "Callees", pk.Callees, "No callees (leaf frame).")))
}

// peekTipBody assembles the peek row tooltip's body (source position).
func peekTipBody(e pprof.TopEntry) []string {
        if e.File == "" {
                return nil
        }
        return []string{e.File + ":" + fmt.Sprint(e.Line)}
}

// editor guard: the panel reads buffer text for unsaved source.
var _ = editor.KindText
