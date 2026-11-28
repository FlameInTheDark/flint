package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/pprof"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/vulncheck"
)

// writeFile creates the render dir and saves one PNG.
func writeFile(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), data, 0o644)
}

// TestVisualVulnProfileScenes renders the Vulnerabilities and Profiler
// panels in every key state for the manual visual audit (screenshots land
// in /tmp/render).
func TestVisualVulnProfileScenes(t *testing.T) {
	dir := "/tmp/render"

	// ------------------------------------------------------------------
	// Vulnerabilities panel: report injected, one group expanded.
	// ------------------------------------------------------------------
	root := writeVulnProject(t)
	h := openPanelHarness(t, "vulns", root, filepath.Join(root, "serve", "serve.go"))
	h.update(func(ws *Workspace) {
		report, err := vulncheck.Parse(strings.NewReader(vulnFixture))
		if err != nil {
			t.Fatal(err)
		}
		ws.vuln.report = report
		ws.vuln.status = "done"
		ws.vuln.resetPanel()
		ws.ShowVulns = true
	})
	h.draw()
	shotT(t, h, dir, "vuln_report.png")

	// Empty state: the Analyze entry point (the first thing a user sees).
	he := openPanelHarness(t, "vulns", root, filepath.Join(root, "serve", "serve.go"))
	he.update(func(ws *Workspace) { ws.ShowVulns = true })
	he.draw()
	shotT(t, he, dir, "vuln_empty.png")

	h.click("vuln-group-GO-2024-3321")
	h.click("vuln-stack-GO-2024-3321|0")
	shotT(t, h, dir, "vuln_expanded.png")

	// Scanning state.
	h.update(func(ws *Workspace) {
		ws.vuln.status = "loading"
		ws.vuln.progress = "Fetching vulnerabilities from the database..."
	})
	h.draw()
	shotT(t, h, dir, "vuln_scanning.png")

	// Light theme parity.
	h.update(func(ws *Workspace) {
		ws.Cfg.Appearance.Theme = "Tokyo Night Light"
		ws.theme = ui.ThemeByName("Tokyo Night Light")
		ws.vuln.status = "done"
		ws.vuln.progress = ""
		ws.vuln.resetPanel()
		ws.vuln.expanded["GO-2024-3321"] = true
		ws.vuln.stacks["GO-2024-3321|0"] = true
	})
	h.draw()
	shotT(t, h, dir, "vuln_light.png")

	// ------------------------------------------------------------------
	// Profiler panel: flame, zoom, top, source, peek, empty.
	// ------------------------------------------------------------------
	proot, mainPath := writeProfileProject(t)
	hp := openPanelHarness(t, "profiler", proot, mainPath)
	injectProfile(hp, syntheticProfile(t, mainPath))
	shotT(t, hp, dir, "profile_flame.png")

	// Zoom on the busiest sub-tree (click the widest real cell twice).
	hp.click("flame-1")
	hp.draw()
	shotT(t, hp, dir, "profile_flame_zoom.png")
	hp.click("flame-zoom-reset")

	hp.click("profile-tab-Top")
	shotT(t, hp, dir, "profile_top.png")

	hp.click("top-main.leaf")
	hp.click("profile-tab-Source")
	hp.update(func(ws *Workspace) { ws.prof.srcTop = 44 })
	hp.draw()
	shotT(t, hp, dir, "profile_source.png")

	hp.click("profile-tab-Peek")
	shotT(t, hp, dir, "profile_peek.png")

	// Heap-style profile: sample type selector + diff baseline chip.
	hp.click("profile-tab-Flame")
	hp.update(func(ws *Workspace) {
		p := syntheticProfile(t, mainPath)
		p.SampleTypes = append(p.SampleTypes, pprof.ValueType{Type: "inuse_space", Unit: "bytes"})
		for i := range p.Samples {
			p.Samples[i].Values = append(p.Samples[i].Values, 2048)
		}
		ws.prof.prof = p
		ws.prof.resetViews()
		ws.rebuildTree()
		ws.setBaseline()
	})
	hp.draw()
	shotT(t, hp, dir, "profile_heap_diff.png")

	// Empty state.
	hp2 := openPanelHarness(t, "profiler", proot, mainPath)
	hp2.update(func(ws *Workspace) { ws.ShowProfile = true })
	hp2.draw()
	shotT(t, hp2, dir, "profile_empty.png")

	// Light theme for the profiler flame.
	hp.update(func(ws *Workspace) {
		ws.Cfg.Appearance.Theme = "Tokyo Night Light"
		ws.theme = ui.ThemeByName("Tokyo Night Light")
	})
	hp.draw()
	shotT(t, hp, dir, "profile_flame_light.png")
}

func shotT(t *testing.T, h panelHarness, dir, name string) {
	t.Helper()
	png, err := h.hw.PNG()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFile(dir, name, png); err != nil {
		t.Fatal(err)
	}
}
