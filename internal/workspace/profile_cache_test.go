package workspace

import (
        "os"
        "path/filepath"
        "strings"
        "sync"
        "testing"
        "time"

        gc "github.com/FlameInTheDark/go-gpui/color"

        "github.com/FlameInTheDark/flint/internal/pprof"
        "github.com/FlameInTheDark/flint/internal/ui"
)

// deepSyntheticProfile builds a single deep recursion chain (one child per
// level), tall enough that the flame layout far exceeds the pane's row
// budget and the viewport culling has something to cut.
func deepSyntheticProfile(t *testing.T, mainPath string, depth int) *pprof.Profile {
        t.Helper()
        p := &pprof.Profile{
                SampleTypes:   []pprof.ValueType{{Type: "samples", Unit: "count"}},
                DefaultType:   0,
                StringTable:   []string{"", "samples", "count"},
                DurationNanos: 100_000_000,
        }
        locs := make([]pprof.Location, 0, depth)
        for i := 1; i <= depth; i++ {
                p.Functions = append(p.Functions, pprof.Function{ID: uint64(i), Name: "main.rec", Filename: mainPath, StartLine: int64(i)})
                p.StringTable = append(p.StringTable, "main.rec")
                locs = append(locs, pprof.Location{ID: uint64(i), Lines: []pprof.Line{{FunctionID: uint64(i), Line: int64(i)}}})
        }
        // One sample walking the whole chain (leaf first, like the runtime).
        var ids []uint64
        for i := depth; i >= 1; i-- {
                ids = append(ids, uint64(i))
        }
        p.Locations = locs
        p.Samples = []pprof.Sample{{Locations: ids, Values: []int64{10}}}
        return p
}

// TestFlameCacheReuseAndCulling pins the flame graph's per-frame work: the
// layout cache survives unchanged frames (same backing slice), and the
// rendered cell count is bounded by the viewport instead of the profile
// depth (the pre-cache build created every cell on every repaint, dragging
// the whole app's fps down with big profiles).
func TestFlameCacheReuseAndCulling(t *testing.T) {
        proot, mainPath := writeProfileProject(t)
        h := openPanelHarness(t, "profiler", proot, mainPath)
        injectProfile(h, deepSyntheticProfile(t, mainPath, 120))

        h.draw() // settle: handles get their viewport sizes
        p := h.read().prof
        rowsBefore := p.flameRows
        count := p.flameCount
        if count < 100 {
                t.Fatalf("fixture too shallow for a culling test (rects=%d)", count)
        }

        h.draw()
        p = h.read().prof
        if len(p.flameRows) == 0 || &p.flameRows[0] != &rowsBefore[0] {
                t.Fatal("an unchanged frame rebuilt the flame layout cache")
        }

        // Count painted cells: strictly fewer than the layout's rect count
        // (the pane fits a handful of rows; the profile is 120 deep).
        cells := 0
        for _, box := range h.boxes() {
                if len(box.Key) > 11 && box.Key[:11] == "n:btn-flame" && box.Key != "n:btn-flame-scroll" {
                        // cell keys are flame-<idx>; exclude the scroll container and
                        // any chrome (crumbs render only when zoomed).
                        id := box.Key[len("n:btn-"):]
                        if len(id) > 6 && id[:6] == "flame-" {
                                digits := true
                                for _, r := range id[6:] {
                                        if r < '0' || r > '9' {
                                                digits = false
                                                break
                                        }
                                }
                                if digits {
                                        cells++
                                }
                        }
                }
        }
        if cells == 0 || cells >= count {
                t.Fatalf("viewport culling inactive: painted %d of %d rects", cells, count)
        }
}

// TestProfileLoaderStateAndAsyncParse pins the loader + off-thread file
// load: a loading panel shows the stop control, and a file load with the
// async hooks wired parses on a goroutine and lands through dispatch
// (the pre-fix paths parsed inside the UI mutation and froze the app).
func TestProfileLoaderStateAndAsyncParse(t *testing.T) {
        proot, mainPath := writeProfileProject(t)
        h := openPanelHarness(t, "profiler", proot, mainPath)

        // Loading state (capture stage, no data yet): stop button + loader.
        h.update(func(ws *Workspace) {
                ws.prof.loading = true
                ws.prof.stage = "capture"
                ws.prof.startedAt = time.Now()
                ws.ShowProfile = true
        })
        h.draw()
        if !hasBox(h, "prof-stop") {
                t.Fatal("the loading toolbar must expose the stop control")
        }

        // Async file load: dispatch channel + direct apply, both wired, so
        // loadProfileFile takes the goroutine path. The parse (gzip+protobuf)
        // runs off the calling thread and the result lands through dispatch.
        data, err := os.ReadFile("../pprof/testdata/cpu.out")
        if err != nil {
                t.Fatal(err)
        }
        src := filepath.Join(t.TempDir(), "cpu.out")
        if err := os.WriteFile(src, data, 0o644); err != nil {
                t.Fatal(err)
        }
        dispatch := make(chan func(), 64)
        var pumpDone sync.WaitGroup
        pumpDone.Add(1)
        stop := make(chan struct{})
        go func() { // pump the dispatch queue onto this test's goroutine
                defer pumpDone.Done()
                for {
                        select {
                        case f := <-dispatch:
                                f()
                        case <-stop:
                                return
                        }
                }
        }()
        defer func() { close(stop); pumpDone.Wait() }()

        ws := New("prof-async")
        ws.Root = proot
        ws.SetEnv(nil, func(f func()) { dispatch <- f }, nil)
        ws.apply = func(f func(*Workspace)) { f(ws) }
        ws.loadProfileFile(src)

        deadline := time.Now().Add(5 * time.Second)
        for time.Now().Before(deadline) {
                time.Sleep(2 * time.Millisecond)
                if !ws.prof.loading && ws.prof.tree != nil {
                        break
                }
        }
        if ws.prof.loading || ws.prof.prof == nil || ws.prof.tree == nil {
                t.Fatal("the async file load never landed")
        }
        if ws.prof.nodeIdx == nil {
                t.Fatal("the node index missing after load")
        }
        if ws.prof.stage != "" {
                t.Fatal("loader stage not cleared after load")
        }
        if !strings.Contains(ws.prof.status, "cpu.out") {
                t.Fatalf("status = %q, want the cpu.out label", ws.prof.status)
        }
}

// TestFlameHeatNormalizationAndTooltip pins the flame graph's visual
// contract: self-time heat is normalized against the view's hottest frame
// (the old total-share ramp left a dominant-stack profile as one flat
// wall of amber), and the per-cell tooltip is baked as separate rows (the
// text system paints "\n" as a missing glyph, which used to render the
// tooltip as one window-wide strip).
func TestFlameHeatNormalizationAndTooltip(t *testing.T) {
        leaf := &pprof.TreeNode{Name: "main.leaf", Value: 80, Self: 80, File: "main.go", Line: 9}
        mid := &pprof.TreeNode{Name: "main.mid", Value: 90, Self: 10, Children: []*pprof.TreeNode{leaf}}
        side := &pprof.TreeNode{Name: "main.side", Value: 10}
        root := &pprof.TreeNode{Name: "root", Value: 100, Children: []*pprof.TreeNode{mid, side}}

        rows, count := layoutFlame(root, 800, "nanoseconds", "", 3)
        if count != 4 || len(rows) != 3 {
                t.Fatalf("layout = %d rects, %d rows; want 4 rects, 3 rows", count, len(rows))
        }
        heat := map[string]float32{}
        for _, row := range rows {
                for _, r := range row {
                        heat[r.node.Name] = r.heat
                        if !r.isRoot && len(r.tipBody) == 0 {
                                t.Fatalf("%s: tooltip body not baked", r.node.Name)
                        }
                }
        }
        if heat["main.leaf"] != 1 {
                t.Fatalf("hottest frame heat = %v, want 1", heat["main.leaf"])
        }
        if heat["main.mid"] != float32(10)/float32(80) {
                t.Fatalf("mid heat = %v, want self/max", heat["main.mid"])
        }
        if heat["main.side"] != 0 {
                t.Fatalf("zero-self frame heat = %v, want 0", heat["main.side"])
        }

        // Tooltip rows: cum line, self line, source line — no raw newlines.
        var leafTip []string
        for _, row := range rows[2] {
                if row.node == leaf {
                        leafTip = row.tipBody
                }
        }
        if len(leafTip) != 3 ||
                !strings.Contains(leafTip[0], "80ns") || !strings.Contains(leafTip[0], "80.0% of view") ||
                leafTip[1] != "self 80ns · 80.0% of view" ||
                leafTip[2] != "main.go:9" {
                t.Fatalf("leaf tooltip body = %v", leafTip)
        }
        for _, line := range leafTip {
                if strings.Contains(line, "\n") {
                        t.Fatalf("tooltip row carries a raw newline: %q", line)
                }
        }
}

// TestFlameLabelContrast pins the readable-label contract: the heat ramp
// (amber -> red) is bright at BOTH ends, so its cell labels take the fixed
// near-black color — the near-white theme text used to be unreadable on
// yellow/orange cells. Theme-tinted matches (dark fills on the dark panel)
// keep the theme text.
func TestFlameLabelContrast(t *testing.T) {
        theme := ui.DefaultTheme()
        for _, fill := range []gc.Hsla{flameHeat(theme, 0, false), flameHeat(theme, 1, false)} {
                if got := flameLabelColor(theme.EditorBg, fill, theme.Text); got != flameHotText {
                        t.Errorf("heat fill label picked the light theme text on a bright fill (lum=%.2f)", fill.L)
                }
        }
        if got := flameLabelColor(theme.EditorBg, flameHeat(theme, 0, true), theme.Text); got != theme.Text {
                t.Errorf("matched (dark) fill picked the near-black label, want the theme text")
        }
}
