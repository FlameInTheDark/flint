package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/FlameInTheDark/flint/internal/pprof"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/platform"
)

// syntheticProfile builds a small CPU profile whose frames live in the
// test module (busy -> middle -> leaf under main), so flame rows, top
// rows, source annotations and editor jumps all resolve against the temp
// workspace. Values are 13 samples of 10ms distributed over the chain.
func syntheticProfile(t *testing.T, mainPath string) *pprof.Profile {
	t.Helper()
	mainDir := filepath.Dir(mainPath)
	const (
		fnBusy    = 1
		fnMiddle  = 2
		fnLeaf    = 3
		fnRuntime = 4
	)
	p := &pprof.Profile{
		SampleTypes:   []pprof.ValueType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}},
		DefaultType:   1,
		PeriodType:    pprof.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:        10_000_000,
		DurationNanos: 130_000_000,
		StringTable:   []string{"", "samples", "count", "cpu", "nanoseconds"},
		Functions: []pprof.Function{
			{ID: fnBusy, Name: "main.busy", Filename: mainPath, StartLine: 15},
			{ID: fnMiddle, Name: "main.middle", Filename: mainPath, StartLine: 13},
			{ID: fnLeaf, Name: "main.leaf", Filename: mainPath, StartLine: 5},
			{ID: fnRuntime, Name: "runtime.main", Filename: "/go/src/runtime/proc.go", StartLine: 250},
		},
	}
	loc := func(id, fnID uint64, line int64, next []uint64) pprof.Location {
		return pprof.Location{ID: id, Lines: []pprof.Line{{FunctionID: fnID, Line: line}}}
	}
	_ = mainDir
	// Location ids 1..4; lines match the harness main.go source (the
	// crc32 call at line 8, middle's body at 13, busy's call at 17).
	p.Locations = []pprof.Location{
		loc(1, fnLeaf, 8, nil),
		loc(2, fnMiddle, 13, nil),
		loc(3, fnBusy, 17, nil),
		loc(4, fnRuntime, 267, nil),
	}
	sample := func(ids []uint64, samples int64, cpu int64) pprof.Sample {
		return pprof.Sample{Locations: ids, Values: []int64{samples, cpu}}
	}
	p.Samples = []pprof.Sample{
		// leaf -> middle -> busy -> runtime.main (7 samples, all in leaf's loop)
		sample([]uint64{1, 2, 3, 4}, 7, 70_000_000),
		// leaf -> middle -> busy but landing on busy's call line (3 samples)
		sample([]uint64{2, 3, 4}, 3, 30_000_000),
		// busy directly called from runtime (3 samples)
		sample([]uint64{3, 4}, 3, 30_000_000),
	}
	return p
}

func writeProfileProject(t *testing.T) (root, mainPath string) {
	t.Helper()
	root = t.TempDir()
	mainPath = filepath.Join(root, "main.go")
	src := `package main

import "hash/crc32"

func leaf(n int) uint32 {
	total := uint32(0)
	for i := 0; i < n; i++ {
		total = crc32.ChecksumIEEE([]byte{byte(i)})
	}
	return total
}

func middle(n int) uint32 { return leaf(n) + leaf(n/2) }

func busy() {
	for i := 0; i < 100; i++ {
		_ = middle(50)
	}
}

func main() {
	busy()
}
`
	if err := os.WriteFile(mainPath, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/prof\n\ngo 1.23\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root, mainPath
}

func injectProfile(h panelHarness, p *pprof.Profile) {
	h.update(func(ws *Workspace) {
		ws.prof.prof = p
		ws.prof.srcPath = "test.cpu"
		ws.prof.status = "test.cpu · " + profileSummary(p)
		ws.prof.resetViews()
		ws.ShowProfile = true
		ws.rebuildTree()
	})
	h.draw()
}

func TestProfilePanelFlameZoomAndTabs(t *testing.T) {
	root, mainPath := writeProfileProject(t)
	h := openPanelHarness(t, "profiler", root, mainPath)
	injectProfile(h, syntheticProfile(t, mainPath))

	// Flame view is the default: the canvas + cells render with sane
	// geometry (every cell inside the pane, positive widths).
	flameCells := 0
	for _, b := range h.boxes() {
		if key, ok := flameCellKey(b.Key); ok {
			flameCells++
			if b.Bounds.Size.Width.AsF32() <= 0 || b.Bounds.Size.Height.AsF32() != 18 {
				t.Fatalf("flame cell %s geometry: %+v", key, b.Bounds)
			}
		}
	}
	if flameCells < 4 {
		t.Fatalf("too few flame cells: %d", flameCells)
	}
	if !hasBox(h, "profile-tab-Flame") || !hasBox(h, "profile-tab-Top") || !hasBox(h, "profile-tab-Source") || !hasBox(h, "profile-tab-Peek") {
		t.Fatal("view tabs missing")
	}
	if !hasBox(h, "prof-sampletype") {
		t.Fatal("sample type selector missing (CPU profiles have samples+cpu columns)")
	}

	// Click a flame cell (not the total row): the graph zooms and the
	// breadcrumb + reset appear.
	zoomed := ""
	for _, b := range h.boxes() {
		if _, ok := flameCellKey(b.Key); !ok {
			continue
		}
		center := geometry.PointPixels(b.Bounds.Origin.X.AsF32()+b.Bounds.Size.Width.AsF32()/2, b.Bounds.Origin.Y.AsF32()+9)
		h.hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: center})
		h.hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: center})
		h.draw()
		if leaf := h.read().prof.zoomLeaf; leaf != nil {
			zoomed = leaf.Name
			break
		}
	}
	if zoomed == "" || zoomed == "root" {
		t.Fatalf("clicking a flame cell did not zoom (leaf=%q)", zoomed)
	}
	if !hasBox(h, "flame-zoom-reset") {
		t.Fatal("zoom breadcrumb reset missing")
	}
	h.click("flame-zoom-reset")
	if h.read().prof.zoomLeaf != nil {
		t.Fatal("reset zoom did not clear the breadcrumb")
	}

	// Top view: rows for the profile's functions, click selects.
	h.click("profile-tab-Top")
	if !hasBox(h, "top-main.busy") || !hasBox(h, "top-main.leaf") {
		t.Fatal("top rows missing")
	}
	h.click("top-main.busy")
	if h.read().prof.selected != "main.busy" {
		t.Fatalf("top click selection = %q", h.read().prof.selected)
	}

	// Source view: select main.leaf, then click its hot crc32 line. The
	// row must sit clear of the status bar overlay, so the list scrolls a
	// few rows first.
	h.click("top-main.leaf")
	h.click("profile-tab-Source")
	// The row list is viewport-culled (only the visible lines exist per
	// frame), so scroll the hot line into view before asserting it.
	h.update(func(ws *Workspace) { ws.prof.srcTop = 80 })
	h.draw()
	h.draw() // the scroll request settles in the layout after the first frame
	if !hasBox(h, "prof-srcline-8") {
		t.Fatal("hot source line (crc32 call in leaf) missing")
	}
	if b, ok := boxBounds(h, "prof-srcline-8"); !ok || b.Origin.Y.AsF32() > 770 {
		t.Fatalf("hot line row not scrolled into view: %+v", b)
	}
	h.click("prof-srcline-8")
	h.draw()
	b := h.read().Buf()
	if b == nil || b.Path != mainPath {
		t.Fatalf("source line jump opened %v", b)
	}
	if line, _ := b.OffsetToLC(b.Cursor); line != 7 { // source line 8, 0-based 7
		t.Fatalf("source jump landed on line %d, want 7", line)
	}

	// Peek view: callers/callees of the default selection.
	h.click("profile-tab-Peek")
	// The selection is still main.leaf: its caller is main.middle.
	if !hasBox(h, "peek-callers-main.middle") {
		t.Fatal("peek caller entries missing")
	}
	// Peek rows jump: clicking the caller selects it (and tries to jump).
	h.click("peek-callers-main.middle")
	if h.read().prof.selected != "main.middle" {
		t.Fatalf("peek click selection = %q", h.read().prof.selected)
	}
}

// flameCellKey extracts the numeric cell index from a flame hitbox key
// (excludes non-cell elements like flame-scroll).
func flameCellKey(key string) (string, bool) {
	const prefix = "n:btn-flame-"
	if len(key) > len(prefix) && key[:len(prefix)] == prefix {
		id := key[len(prefix):]
		for _, r := range id {
			if r < '0' || r > '9' {
				return "", false
			}
		}
		return id, true
	}
	return "", false
}

func TestProfilePanelSampleTypesAndBaseline(t *testing.T) {
	_, mainPath := writeProfileProject(t)
	h := openPanelHarness(t, "profiler", "", "")
	p := syntheticProfile(t, mainPath)
	// Second value column: heap-style profile with two spaces.
	p.SampleTypes = append(p.SampleTypes, pprof.ValueType{Type: "inuse_space", Unit: "bytes"})
	for i := range p.Samples {
		p.Samples[i].Values = append(p.Samples[i].Values, 2048)
	}
	injectProfile(h, p)

	if !hasBox(h, "prof-sampletype") {
		t.Fatal("sample type selector missing for a multi-column profile")
	}
	h.click("prof-sampletype")
	if popup := h.read().Popup; popup == nil || len(popup.Items) != 3 {
		t.Fatalf("sample type popup = %+v", popup)
	}
	// Pick the second sample type through the popup item.
	item := h.read().Popup.Items[2]
	item.Action()
	h.draw()
	if h.read().prof.sampleType != "inuse_space" {
		t.Fatalf("sample type after pick = %q", h.read().prof.sampleType)
	}

	// Diff baseline: set from the current profile; the Top view gains the
	// delta column and the chip appears; clear removes both.
	h.click("profile-tab-Top")
	h.click("prof-set-base")
	if h.read().prof.base == nil {
		t.Fatal("baseline not stored")
	}
	if !hasBox(h, "prof-clear-base") {
		t.Fatal("clear baseline button missing")
	}
	h.click("prof-clear-base")
	if h.read().prof.base != nil || hasBox(h, "prof-baseline-chip") {
		t.Fatal("baseline not cleared")
	}
}

func TestProfilePanelCapturePromptAndErrors(t *testing.T) {
	root, _ := writeProfileProject(t)
	h := openPanelHarness(t, "profiler", root, "")
	h.update(func(ws *Workspace) { ws.ShowProfile = true })
	h.draw()

	// Empty state explains both entry paths.
	if h.read().prof.prof != nil {
		t.Fatal("unexpected profile before load")
	}

	// Capture prompt validates the address; a dead server surfaces the
	// error row (connection refused is fast and offline-safe).
	h.click("prof-capture")
	if h.read().Prompt == nil {
		t.Fatal("capture prompt did not open")
	}
	h.update(func(ws *Workspace) { ws.Prompt.Value.Set("") })
	h.press("enter")
	if h.read().Prompt == nil || h.read().Prompt.Err == "" {
		t.Fatalf("empty address accepted: %+v", h.read().Prompt)
	}
	h.update(func(ws *Workspace) {
		ws.Prompt.Value.Set("127.0.0.1:1")
		ws.prof.seconds = 1
	})
	h.press("enter")
	h.draw()
	// The fetch fails (nothing listens on port 1): the error row appears.
	if h.read().prof.prof == nil && h.read().prof.err == "" {
		t.Fatalf("capture error not surfaced: loading=%v err=%q", h.read().prof.loading, h.read().prof.err)
	}

	// Load opens the profile picker (mode LoadProfile).
	h.click("prof-load")
	if p := h.read().Picker; p == nil || p.Mode != PickerLoadProfile {
		t.Fatalf("profile picker = %+v", p)
	}
	_ = fmt.Sprint
}

func TestProfilePanelLoadFileFromPicker(t *testing.T) {
	root, mainPath := writeProfileProject(t)
	h := openPanelHarness(t, "profiler", root, "")
	// A real profile file on disk (fixture bytes from the pprof package).
	profBytes, err := os.ReadFile(filepath.Join("..", "pprof", "testdata", "cpu.out"))
	if err != nil {
		t.Skip("pprof fixture not available")
	}
	path := filepath.Join(root, "captured.cpu.out")
	if err := os.WriteFile(path, profBytes, 0644); err != nil {
		t.Fatal(err)
	}
	h.update(func(ws *Workspace) { ws.ShowProfile = true; ws.loadProfileFile(path) })
	h.draw()
	if h.read().prof.prof == nil {
		t.Fatalf("profile not loaded: %q", h.read().prof.err)
	}
	if h.read().prof.tree == nil || h.read().prof.tree.Total == 0 {
		t.Fatal("flame tree not built")
	}
	// The flame canvas renders for the real fixture too.
	cells := 0
	for _, b := range h.boxes() {
		if _, ok := flameCellKey(b.Key); ok {
			cells++
		}
	}
	if cells < 4 {
		t.Fatalf("flame cells for real profile = %d", cells)
	}
	_ = mainPath
	_ = root
}
