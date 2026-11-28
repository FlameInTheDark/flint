package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/vulncheck"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

// panelHarness is the shared headless harness for the dock panel tests:
// the openToolHarness toolkit plus entity updates (state injection).
type panelHarness struct {
	read             func() *Workspace
	update           func(func(*Workspace))
	click            func(string)
	clickInputPrefix func(string)
	press            func(string)
	boxes            func() []gpui.Hitbox
	draw             func()
	hw               *headless.Window
}

func openPanelHarness(t *testing.T, title, root, file string) panelHarness {
	t.Helper()
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp := headless.New(geometry.SizePixels(1200, 800))
	a := gpui.NewApplication(hp)
	dispatch := make(chan func(), 64)
	handle, err := gpui.OpenWindow[Workspace](a, platform.WindowOptions{Title: title}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New(title)
		ws.Root = root
		ws.ShowTree = false
		ws.tools.rail = true
		if file != "" {
			ws.OpenFile(file)
		}
		ws.Measure(w)
		ws.SetEnv(nil, func(f func()) { dispatch <- f }, nil)
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func() *Workspace { return handle.View.Read(a.App) }
	update := func(f func(*Workspace)) {
		handle.View.Update(a.App, func(s *Workspace, c *app.Context[Workspace]) any { f(s); c.Notify(); return nil })
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
	handle.Window.Draw()
	hw := hp.LastWindow()
	drain := func() {
		for {
			select {
			case f := <-dispatch:
				f()
			default:
				return
			}
		}
	}
	return panelHarness{
		read:   read,
		update: update,
		click: func(id string) {
			t.Helper()
			for _, box := range handle.Window.RenderedHitboxes() {
				if box.Key != "n:btn-"+id {
					continue
				}
				b := box.Bounds
				if b.Size.Width <= 0 || b.Size.Height <= 0 {
					t.Fatalf("empty button %q: %+v", id, b)
				}
				p := geometry.PointPixels(b.Origin.X.AsF32()+b.Size.Width.AsF32()/2, b.Origin.Y.AsF32()+b.Size.Height.AsF32()/2)
				hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
				hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
				drain()
				handle.Window.Draw()
				return
			}
			t.Fatalf("missing button %q", id)
		},
		press: func(k string) {
			hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: k}})
			drain()
			handle.Window.Draw()
		},
		clickInputPrefix: func(prefix string) {
			t.Helper()
			for _, box := range handle.Window.RenderedHitboxes() {
				if strings.HasPrefix(box.Key, "n:"+prefix) {
					b := box.Bounds
					p := geometry.PointPixels(b.Origin.X.AsF32()+b.Size.Width.AsF32()/2, b.Origin.Y.AsF32()+b.Size.Height.AsF32()/2)
					hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
					hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
					drain()
					handle.Window.Draw()
					return
				}
			}
			t.Fatalf("missing input %q", prefix)
		},
		boxes: handle.Window.RenderedHitboxes,
		draw: func() {
			drain()
			handle.Window.Draw()
		},
		hw: hw,
	}
}

func hasBox(h panelHarness, id string) bool {
	for _, box := range h.boxes() {
		if box.Key == "n:btn-"+id {
			return true
		}
	}
	return false
}

func boxBounds(h panelHarness, id string) (geometry.Bounds[geometry.Pixels], bool) {
	for _, box := range h.boxes() {
		if box.Key == "n:btn-"+id {
			return box.Bounds, true
		}
	}
	return geometry.Bounds[geometry.Pixels]{}, false
}

// vulnFixture is a compact govulncheck stream (called finding + import-only
// finding). The called trace's entry frame (serve/serve.go:19) must exist
// under the test root for the jump assertions.
const vulnFixture = `{
  "config": {"protocol_version": "v1.0.0", "scanner_name": "govulncheck", "scanner_version": "v1.1.0", "scan_level": "symbol", "scan_mode": "source"}
}
{
  "SBOM": {"modules": [{"path": "example.test/app"}, {"path": "golang.org/x/crypto", "version": "v0.17.0"}]}
}
{
  "osv": {
    "id": "GO-2024-3321",
    "aliases": ["CVE-2024-45337"],
    "summary": "Misuse of ServerConfig.PublicKeyCallback may cause authorization bypass in golang.org/x/crypto",
    "details": "Servers that register a PublicKeyCallback callback are vulnerable to authorization bypass.",
    "affected": [{"package": {"name": "golang.org/x/crypto"}}],
    "database_specific": {"url": "https://pkg.go.dev/vuln/GO-2024-3321"}
  }
}
{
  "osv": {
    "id": "GO-2025-4006",
    "summary": "Crypto/x509: verify panics on odd-sized RSA modulus",
    "affected": [{"package": {"name": "stdlib"}}],
    "database_specific": {"url": "https://pkg.go.dev/vuln/GO-2025-4006"}
  }
}
{
  "finding": {
    "osv": "GO-2024-3321",
    "fixed_version": "v0.31.0",
    "trace": [
      {"module": "golang.org/x/crypto", "version": "v0.17.0", "package": "golang.org/x/crypto/ssh", "function": "serverAuthenticate", "receiver": "*connection", "position": {"filename": "ssh/server.go", "line": 434, "column": 26}},
      {"module": "example.test/app", "package": "example.test/app/serve", "function": "Serve", "position": {"filename": "serve/serve.go", "line": 19, "column": 27}}
    ]
  }
}
{
  "finding": {
    "osv": "GO-2024-3321",
    "fixed_version": "v0.31.0",
    "trace": [{"module": "golang.org/x/crypto", "version": "v0.17.0"}]
  }
}
{
  "finding": {
    "osv": "GO-2025-4006",
    "fixed_version": "v1.24.8",
    "trace": [{"module": "stdlib", "version": "go1.23.4"}]
  }
}
`

func writeVulnProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/app\n\ngo 1.23\n"), 0644); err != nil {
		t.Fatal(err)
	}
	serve := filepath.Join(root, "serve")
	if err := os.MkdirAll(serve, 0755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("package serve\n\n// Serve starts the (vulnerable) server.\nfunc Serve() {\n")
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&b, "\t_ = %d // padding line\n", i)
	}
	b.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(serve, "serve.go"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func injectVulnReport(h panelHarness) {
	h.update(func(ws *Workspace) {
		report, err := vulncheck.Parse(strings.NewReader(vulnFixture))
		if err != nil {
			tfail(err)
		}
		ws.vuln.report = report
		ws.vuln.status = "done"
		ws.vuln.resetPanel()
		ws.ShowVulns = true
	})
	h.draw()
}

// tfail lets nested closures fail the current test without importing
// testing into the update closure signature.
var tfail = func(err error) {}

func TestVulnPanelOpensIdleWithAnalyzeButton(t *testing.T) {
	root := writeVulnProject(t)
	h := openPanelHarness(t, "vulns", root, "")
	// Keep the scan resolution hermetic: an empty PATH makes govulncheck
	// lookup fail instantly, so the async run lands as an error state
	// instead of depending on a real binary.
	t.Setenv("PATH", t.TempDir())

	// First open: the panel appears instantly with its empty state. It
	// used to auto-start govulnscan synchronously on the UI thread and
	// froze the whole app until the scan finished.
	h.update(func(ws *Workspace) { ws.toggleVulns() })
	h.draw()
	ws := h.read()
	if ws.vuln.status != "" {
		t.Fatalf("opening the panel auto-started a scan (status %q)", ws.vuln.status)
	}
	if ws.vulnBusy() {
		t.Fatal("opening the panel armed a scan runner")
	}
	if !hasBox(h, "vuln-analyze") {
		t.Fatal("empty state is missing the Analyze button")
	}
	if hasBox(h, "vuln-stop") {
		t.Fatal("idle panel shows the stop button")
	}

	// Pressing Analyze starts the scan through the async path: the state
	// flips to loading with the runner armed, then resolves (here: the
	// PATH-less "govulncheck not found" error) and offers Retry.
	h.click("vuln-analyze")
	h.draw()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ws = h.read()
		if ws.vuln.status == "error" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Analyze did not resolve an error (status %q)", ws.vuln.status)
		}
		time.Sleep(10 * time.Millisecond)
		h.draw()
	}
	if !strings.Contains(h.read().vuln.err, "govulncheck not found") {
		t.Fatalf("unexpected error text %q", h.read().vuln.err)
	}
	if !hasBox(h, "vuln-retry") {
		t.Fatal("error state is missing the Retry button")
	}
}

func TestVulnPanelGroupsExpandAndJump(t *testing.T) {
	tfail = func(err error) { t.Fatal(err) }
	t.Cleanup(func() { tfail = func(error) {} })
	root := writeVulnProject(t)
	h := openPanelHarness(t, "vulns", root, filepath.Join(root, "serve", "serve.go"))
	injectVulnReport(h)

	// The header shows both count chips and the two group rows.
	if !hasBox(h, "vuln-count-called") || !hasBox(h, "vuln-count-imported") {
		t.Fatal("count chips missing")
	}
	if !hasBox(h, "vuln-group-GO-2024-3321") || !hasBox(h, "vuln-group-GO-2025-4006") {
		t.Fatal("group rows missing")
	}
	// Groups start collapsed: no stack rows yet.
	if hasBox(h, "vuln-stack-GO-2024-3321|0") {
		t.Fatal("stack visible before expansion")
	}

	h.click("vuln-group-GO-2024-3321")
	if !hasBox(h, "vuln-stack-GO-2024-3321|0") {
		t.Fatal("expanding the group did not reveal the call stack")
	}
	// Action buttons: advisory + fix.
	if !hasBox(h, "vuln-advisory-GO-2024-3321") || !hasBox(h, "vuln-fix-GO-2024-3321") {
		t.Fatal("group action buttons missing")
	}

	// Open the stack and jump through the entry frame.
	h.click("vuln-stack-GO-2024-3321|0")
	frameID := "vuln-frame-GO-2024-3321|0|0" // entry row (Serve), first stack row
	if !hasBox(h, frameID) {
		t.Fatal("stack frame rows missing")
	}
	h.click(frameID)
	h.draw()
	b := h.read().Buf()
	if b == nil || b.Path != filepath.Join(root, "serve", "serve.go") {
		t.Fatalf("stack frame jump opened %v", b)
	}
	line, _ := b.OffsetToLC(b.Cursor)
	if line != 18 { // 0-based line 18 = serve.go line 19
		t.Fatalf("jump landed on line %d, want 18", line)
	}

	// Filter chips: Called hides the imported group; Imported hides the
	// called one.
	h.click("vuln-filter-called")
	if hasBox(h, "vuln-group-GO-2025-4006") {
		t.Fatal("Called filter kept the imported group")
	}
	if !hasBox(h, "vuln-group-GO-2024-3321") {
		t.Fatal("Called filter dropped the called group")
	}
	h.click("vuln-filter-imported")
	if hasBox(h, "vuln-group-GO-2024-3321") {
		t.Fatal("Imported filter kept the called group")
	}
	if !hasBox(h, "vuln-group-GO-2025-4006") {
		t.Fatal("Imported filter dropped the imported group")
	}

	// The close button hides the panel.
	h.click("vuln-filter-all")
	h.click("vuln-close")
	if h.read().ShowVulns {
		t.Fatal("close button did not hide the panel")
	}
}

func TestVulnPanelStatesAndSearch(t *testing.T) {
	root := writeVulnProject(t)
	h := openPanelHarness(t, "vulns", root, "")

	// Loading state: the stop button replaces run.
	h.update(func(ws *Workspace) {
		ws.vuln.status = "loading"
		ws.vuln.progress = "Fetching vulnerabilities from the database..."
		ws.vuln.resetPanel()
		ws.ShowVulns = true
	})
	h.draw()
	if hasBox(h, "vuln-run") || !hasBox(h, "vuln-stop") {
		t.Fatal("loading state did not switch run/stop controls")
	}

	// Error state renders the message row.
	h.update(func(ws *Workspace) {
		ws.vuln.status = "error"
		ws.vuln.err = vulncheck.InstallHint
	})
	h.draw()
	if !hasBox(h, "vuln-run") || hasBox(h, "vuln-stop") {
		t.Fatal("error state did not restore the run button")
	}

	// Clean report: green "clear" chip, no group rows.
	h.update(func(ws *Workspace) {
		ws.vuln.status = "done"
		ws.vuln.err = ""
		ws.vuln.report = vulncheck.Report{}
	})
	h.draw()
	if !hasBox(h, "vuln-count-clear") || hasBox(h, "vuln-count-called") {
		t.Fatal("clean report state wrong")
	}

	// Search narrows the visible groups (type into the filter box).
	h.update(func(ws *Workspace) {
		inject := func() {}
		_ = inject
		ws.vuln.status = "done"
		ws.vuln.report = mustVulnReport()
		ws.vuln.resetPanel()
	})
	h.draw()
	if !hasBox(h, "vuln-group-GO-2024-3321") || !hasBox(h, "vuln-group-GO-2025-4006") {
		t.Fatal("groups missing before search")
	}
	h.clickInputPrefix("btn-input-") // focus the filter input (RenderInput's own field)
	// Type part of the CVE alias (CVE-2024-45337): only GO-2024-3321 matches.
	for _, ch := range "45337" {
		h.hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: string(ch), KeyChar: string(ch)}})
		h.draw()
	}
	if !hasBox(h, "vuln-group-GO-2024-3321") {
		t.Fatal("search by CVE alias dropped the matching group")
	}
	if hasBox(h, "vuln-group-GO-2025-4006") {
		t.Fatal("search by CVE alias kept the non-matching group")
	}
}

func mustVulnReport() vulncheck.Report {
	report, err := vulncheck.Parse(strings.NewReader(vulnFixture))
	if err != nil {
		panic(err)
	}
	return report
}

func TestVulnJumpResolvesWorkspacePathsOnly(t *testing.T) {
	root := writeVulnProject(t)
	ws := New("bare")
	ws.Root = root
	called := mustVulnReport().Called[0]
	fr, ok := ws.vulnJumpFrame(called.Called[0])
	if !ok || filepath.Base(fr.Position.Filename) != "serve.go" {
		t.Fatalf("jump frame = %+v ok=%v", fr, ok)
	}
	// Dependency frames never resolve (ssh/server.go is not under root).
	if ws.vulnResolvePath(called.Called[0].Trace[0]) != "" {
		t.Fatal("dependency frame resolved into the workspace")
	}
	// Fix command rendering.
	if got := vulnFixCommand(called); got != "go get golang.org/x/crypto@v0.31.0" {
		t.Fatalf("fix command = %q", got)
	}
	stdlib := mustVulnReport().Imported[0]
	if got := vulnFixCommand(stdlib); got != "go upgrade  # Go 1.24.8" {
		t.Fatalf("stdlib fix command = %q", got)
	}
	// The editor integration entry point lands on the resolved frame.
	ws.OpenFile(filepath.Join(root, "serve", "serve.go"))
	ws.openVulnFrame(fr)
	if b := ws.Buf(); b == nil || b.Path != filepath.Join(root, "serve", "serve.go") {
		t.Fatalf("openVulnFrame buffer = %v", b)
	}
	if line, _ := ws.Buf().OffsetToLC(ws.Buf().Cursor); line != 18 {
		t.Fatalf("openVulnFrame line = %d", line)
	}
	_ = editor.KindText
}
