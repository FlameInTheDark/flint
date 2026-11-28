// vuln.go is the Vulnerabilities dock panel: a govulncheck runner and a
// grouped result list. Groups the project's code CALLS sort first (they
// need action); import-only groups follow. Stack frames resolve to editor
// jumps when the file exists under the workspace root.
package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/vulncheck"
	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

// vulnState owns the panel: the report, the scan lifecycle, filters and
// the expanded-set (the element tree rebuilds every frame, so all
// interaction state lives in the workspace).
type vulnState struct {
	report     vulncheck.Report
	status     string // "", "loading", "done", "error"
	progress   string
	err        string
	runner     *vulncheck.Runner
	filter     string // "all" | "called" | "imported"
	search     ui.InputState
	focus      gpui.FocusHandle
	expanded   map[string]bool // OSV id -> group expanded
	stacks     map[string]bool // "osv|findingIdx" -> stack visible
	scroll     *elements.ScrollHandle
	top        float32
	left       float32       // horizontal pan offset (debugHList parity)
	startedAt  time.Time     // scan start (drives the loader's elapsed readout)
	loaderStop chan struct{} // closes the loader tick goroutine
}

func (v *vulnState) resetPanel() {
	v.expanded = map[string]bool{}
	v.stacks = map[string]bool{}
	v.scroll = elements.NewScrollHandle()
	v.top = 0
}

// vulnBusy reports whether a scan is in flight.
func (ws *Workspace) vulnBusy() bool { return ws.vuln.runner != nil && ws.vuln.runner.Running() }

// toggleVulns shows/hides the Vulnerabilities dock panel (mutually
// exclusive with the other dock panels, like the Problems toggle).
//
// The scan NEVER auto-starts on open: govulncheck runs for tens of
// seconds, and the panel must open instantly with its empty state (the
// Analyze button) instead of blocking the first paint behind a process
// spawn. The toolbar's run icon, the Analyze button and the Tools menu's
// "Check Vulnerabilities" item all start the scan explicitly.
func (ws *Workspace) toggleVulns() {
	ws.ShowVulns = !ws.ShowVulns
	if ws.ShowVulns {
		ws.ShowTerminal, ws.ShowGoOutput, ws.ShowProblems = false, false, false
		ws.ShowDebug, ws.ShowTodos, ws.ShowProfile = false, false, false
		if ws.vuln.scroll == nil {
			ws.vuln.resetPanel()
		}
	}
}

// startVulnScan runs govulncheck against the workspace root. update/w are
// nil in bare-state tests (the scan then runs synchronously headless).
// The process itself never blocks the UI thread: the scan streams progress
// from its own goroutine, coalescing UI refreshes to ~4 a second (a
// package-heavy scan emits hundreds of progress documents; refreshing per
// document starved the frame budget and read as an app freeze).
func (ws *Workspace) startVulnScan(update updateFn, w *gpui.Window) {
	if ws.Root == "" {
		ws.vuln.status, ws.vuln.err = "error", "Open a Go project to scan for vulnerabilities."
		return
	}
	if ws.vulnBusy() {
		return
	}
	if ws.vuln.scroll == nil {
		ws.vuln.resetPanel()
	}
	dir := ws.Root
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	ws.vuln.runner = vulncheck.NewRunner(cancel)
	ws.vuln.status = "loading"
	ws.vuln.err = ""
	ws.vuln.progress = "Starting govulncheck…"
	ws.vuln.startedAt = time.Now()
	stopLoader(ws.vuln.loaderStop)
	ws.vuln.loaderStop = make(chan struct{})
	stop := ws.vuln.loaderStop
	startLoaderTick(update, w, ws.dispatch, stop, func(s *Workspace) {
		// The tick only redraws: the elapsed readout derives from startedAt
		// at render time, so no state mutation is needed here.
	})
	dispatch := ws.dispatch
	apply := func(f func(*Workspace)) {
		if update != nil {
			update(f)
		} else {
			f(ws)
		}
	}
	if dispatch == nil || w == nil {
		// Headless/bare state: run inline so tests are deterministic.
		report, err := vulncheck.Scan(ctx, dir, func(msg string) {
			apply(func(s *Workspace) { s.vuln.progress = msg })
		})
		apply(func(s *Workspace) { s.finishVulnScan(report, err) })
		return
	}
	go func() {
		var report vulncheck.Report
		var err error
		// Progress coalescing: govulncheck streams one progress document per
		// scan phase (and analysis stages); landing each on the UI thread
		// costs a full redraw. Keep the latest message and refresh at most
		// every 250ms — the loader's tick keeps the elapsed readout alive
		// between coalesced flushes anyway.
		var latest string
		var lastFlush time.Time
		flush := func(force bool) {
			if !force && time.Since(lastFlush) < loaderTickInterval {
				return
			}
			lastFlush = time.Now()
			msg := latest
			dispatch(func() {
				defer func() { _ = recover() }()
				apply(func(s *Workspace) { s.vuln.progress = msg })
				w.Refresh()
			})
		}
		func() {
			defer func() { _ = recover() }()
			report, err = vulncheck.Scan(ctx, dir, func(msg string) {
				latest = msg
				flush(false)
			})
		}()
		defer func() { _ = recover() }()
		dispatch(func() {
			defer func() { _ = recover() }()
			apply(func(s *Workspace) { s.finishVulnScan(report, err) })
			w.Refresh()
		})
	}()
}

// finishVulnScan lands a completed (or failed) scan on the UI thread.
func (ws *Workspace) finishVulnScan(report vulncheck.Report, err error) {
	if ws.vuln.runner != nil {
		ws.vuln.runner.Stop()
		ws.vuln.runner = nil
	}
	stopLoader(ws.vuln.loaderStop)
	ws.vuln.progress = ""
	if err != nil {
		if ctxErr(err) {
			return // cancelled: keep the previous report on display
		}
		ws.vuln.status, ws.vuln.err = "error", err.Error()
		return
	}
	ws.vuln.report = report
	ws.vuln.status = "done"
	ws.vuln.err = ""
	ws.vuln.expanded = map[string]bool{}
	ws.vuln.stacks = map[string]bool{}
}

func ctxErr(err error) bool { return err == context.Canceled || err == context.DeadlineExceeded }

// stopVulnScan cancels the in-flight scan.
func (ws *Workspace) stopVulnScan() {
	if r := ws.vuln.runner; r != nil {
		r.Stop()
	}
}

// vulnCounts summarizes the report for chips and filters.
func (v *vulnState) vulnCounts() (called, imported int) {
	return len(v.report.Called), len(v.report.Imported)
}

// vulnJumpFrame picks the call-stack frame a row should jump to: the last
// frame whose file resolves inside the workspace (the project's own code).
func (ws *Workspace) vulnJumpFrame(f vulncheck.Finding) (vulncheck.Frame, bool) {
	for i := len(f.Trace) - 1; i >= 0; i-- {
		fr := f.Trace[i]
		if fr.HasPosition() && ws.vulnResolvePath(fr) != "" {
			return fr, true
		}
	}
	return vulncheck.Frame{}, false
}

// vulnResolvePath maps a trace frame onto a workspace file (govulncheck
// reports positions relative to the module root; stdlib and dependency
// frames do not exist under the workspace root and are not clickable).
func (ws *Workspace) vulnResolvePath(fr vulncheck.Frame) string {
	if !fr.HasPosition() || ws.Root == "" {
		return ""
	}
	name := filepath.FromSlash(fr.Position.Filename)
	if filepath.IsAbs(name) {
		if fileExists(name) {
			return name
		}
		return ""
	}
	p := filepath.Join(ws.Root, name)
	if fileExists(p) {
		return p
	}
	return ""
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// openVulnFrame reveals a stack frame in the editor.
func (ws *Workspace) openVulnFrame(fr vulncheck.Frame) {
	if p := ws.vulnResolvePath(fr); p != "" {
		ws.openGoDiagnostic(goDiagnostic{path: p, line: fr.Position.Line, column: fr.Position.Column})
	}
}

// vulnGroups applies the filter chips and the search box to the report.
func (v *vulnState) vulnGroups() (called, imported []vulncheck.Group) {
	query := strings.ToLower(strings.TrimSpace(v.search.String()))
	match := func(g vulncheck.Group) bool {
		if query == "" {
			return true
		}
		hay := strings.ToLower(g.OSV.ID + " " + g.OSV.Summary + " " + strings.Join(g.OSV.Aliases, " ") + " " + g.Module)
		for _, f := range g.Called {
			for _, fr := range f.Trace {
				hay += " " + strings.ToLower(fr.Package+" "+fr.Symbol())
			}
		}
		return strings.Contains(hay, query)
	}
	if v.filter != "imported" {
		for _, g := range v.report.Called {
			if match(g) {
				called = append(called, g)
			}
		}
	}
	if v.filter != "called" {
		for _, g := range v.report.Imported {
			if match(g) {
				imported = append(imported, g)
			}
		}
	}
	return called, imported
}

// vulnSortedStack returns a finding's trace ordered entry point first
// (govulncheck's text order): the readable top-down call path.
func vulnSortedStack(f vulncheck.Finding) []vulncheck.Frame {
	out := make([]vulncheck.Frame, len(f.Trace))
	for i, fr := range f.Trace {
		out[len(f.Trace)-1-i] = fr
	}
	return out
}

// vulnFixCommand renders the upgrade command for an affected module.
func vulnFixCommand(g vulncheck.Group) string {
	if g.Module == "" || g.Fixed == "" {
		return ""
	}
	if g.Module == "stdlib" {
		return "go upgrade  # Go " + strings.TrimPrefix(g.Fixed, "v")
	}
	return "go get " + g.Module + "@" + g.Fixed
}

// vulnCopyText is the clipboard fallback when no openURL hook exists.
func (ws *Workspace) vulnOpenURL(url string) {
	if url == "" {
		return
	}
	if ws.openURL != nil {
		if err := ws.openURL(url); err != nil {
			ws.StatusError = err.Error()
		}
		return
	}
	if ws.clipboard != nil {
		ws.clipboard(platform.ClipboardItem{Text: url})
		ws.StatusError = "Advisory URL copied: " + url
	}
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// vulnChipsRow builds the shared header controls (filter chips, search,
// run/stop). Kept on the header row to leave the list maximum height.
func renderVulns(ws *Workspace, t ui.Theme, update updateFn, w *gpui.Window) gpui.Element {
	v := &ws.vuln
	persistScroll(v.scroll, &v.top)
	if !v.focus.IsValid() {
		v.focus = gpui.NewFocusHandle()
	}
	header := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(32)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(8)).
		BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1)
	header.Child(elements.NewDiv().FlexShrink(0).Child(elements.NewText("Vulnerabilities").WithTextColor(t.Text)))
	called, imported := v.vulnCounts()
	if v.status == "done" {
		chips := elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(5)).FlexShrink(0)
		if called > 0 {
			chips.Child(vulnChipFilter(ws, update, "vuln-count-called", fmt.Sprintf("%d called", called), t.Red, "called", "Show only vulnerabilities your code calls"))
		}
		if imported > 0 {
			chips.Child(vulnChipFilter(ws, update, "vuln-count-imported", fmt.Sprintf("%d imported", imported), t.Yellow, "imported", "Show only imported-but-not-called modules"))
		}
		if called+imported == 0 {
			chips.Child(vulnChipFilter(ws, update, "vuln-count-clear", "clear", t.Green, "all", "No known vulnerabilities: nothing to filter"))
		}
		header.Child(chips)
	}
	if v.status == "loading" {
		header.Child(elements.NewDiv().FlexShrink(0).MinW_0().OverflowHidden().WhitespaceNowrap().
			Child(elements.NewText("· " + elapsedSeconds(v.startedAt) + " · " + v.progress).WithTextColor(t.Dim)))
	}
	header.Child(elements.NewDiv().FlexGrow(1).MinW_0())
	// Filter chips: All / Called / Imported (visible once results exist).
	if v.status == "done" {
		for _, f := range []struct{ id, label string }{{"all", "All"}, {"called", "Called"}, {"imported", "Imported"}} {
			active := v.filter == f.id || (v.filter == "" && f.id == "all")
			chip := elements.NewDiv().ID(elements.ButtonID("vuln-filter-" + f.id)).Flex().ItemsCenter().H(ui.Px(22)).Px(ui.DefPx(8)).FlexShrink(0).
				Rounded(ui.AbsPx(4)).TextSize(ui.AbsPx(11)).WhitespaceNowrap().CursorPointer().TooltipBuilder(ui.Tooltip("Show " + f.label + " vulnerabilities")).
				Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
				OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
					update(func(s *Workspace) { s.vuln.filter = f.id })
					win.Refresh()
				})
			if active {
				chip.BgColor(t.Accent.Alpha(0.25))
			}
			col := t.Dim
			if active {
				col = t.Text
			}
			header.Child(chip.Child(elements.NewText(f.label).WithTextColor(col)))
		}
		// Search box filters by id, CVE alias, module, package or symbol.
		input := ui.RenderInput(ui.InputConfig{
			State: &v.search, Theme: t, Placeholder: "Filter…", Focus: v.focus,
			Update:   func(f func(*ui.InputState)) { update(func(s *Workspace) { f(&s.vuln.search) }) },
			OnEscape: func(win *gpui.Window) { win.Focus(ws.FocusEditor); win.Refresh() },
		})
		header.Child(elements.NewDiv().ID(elements.ButtonID("vuln-search-box")).W(ui.Px(150)).FlexShrink(0).H(ui.Px(24)).Child(input))
	}
	// Run / Stop: a scan is started with the play icon and replaced by the
	// stop square while running (the Go task toolbar's pattern).
	if v.status == "loading" {
		header.Child(elements.NewDiv().ID(elements.ButtonID("vuln-stop")).Flex().ItemsCenter().JustifyCenter().W(ui.Px(23)).H(ui.Px(23)).FlexShrink(0).
			Rounded(ui.AbsPx(4)).CursorPointer().TooltipBuilder(ui.Tooltip("Stop scan")).
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
			OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
				update(func(s *Workspace) { s.stopVulnScan() })
				win.Refresh()
			}).Child(ui.ToolbarIcon("stop", t.Red)))
	} else {
		header.Child(elements.NewDiv().ID(elements.ButtonID("vuln-run")).Flex().ItemsCenter().JustifyCenter().W(ui.Px(23)).H(ui.Px(23)).FlexShrink(0).
			Rounded(ui.AbsPx(4)).CursorPointer().TooltipBuilder(ui.Tooltip("Run govulncheck")).
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
			OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
				update(func(s *Workspace) { s.startVulnScan(update, win) })
				win.Refresh()
			}).Child(ui.ToolbarIcon("run", t.Green)))
	}
	header.Child(elements.NewDiv().ID(elements.ButtonID("vuln-close")).Flex().ItemsCenter().JustifyCenter().W(ui.Px(23)).H(ui.Px(23)).FlexShrink(0).
		Rounded(ui.AbsPx(4)).CursorPointer().TooltipBuilder(ui.Tooltip("Hide Vulnerabilities")).
		Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
		OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
			update(func(s *Workspace) { s.ShowVulns = false })
			win.Focus(ws.FocusEditor)
			win.Refresh()
		}).Child(ui.ToolbarIcon("close", t.Dim)))

	// Body list: horizontal pan like the debug variables view — long
	// package paths and stack symbols widen the content column and
	// wheel/shift-wheel pans across them instead of clipping inside rows.
	list := debugHList("vuln-list", v.scroll, nil, &v.top, &v.left, w)
	switch v.status {
	case "loading":
		list.Child(renderVulnLoader(t, v))
	case "error":
		list.Child(debugRow(v.err, t.Red))
		if strings.HasPrefix(v.err, "govulncheck not found") {
			list.Child(debugRow(vulncheck.InstallHint, t.Dim))
		}
		list.Child(renderVulnAnalyzeButton(t, update, "vuln-retry", "Retry scan"))
	case "":
		list.Child(renderVulnEmpty(t, update, ws.effectiveTermHeight() > 150))
	case "done":
		called, imported := v.vulnGroups()
		if len(called)+len(imported) == 0 {
			list.Child(elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(7)).H(ui.Px(24)).Px(ui.DefPx(4)).
				Child(ui.Icon("check", t.Green, 14)).Child(debugRow("No known vulnerabilities match the current filter.", t.Green)))
		} else {
			idx := 0
			idx = renderVulnSection(ws, t, list, called, "Called by your code", "the vulnerable symbols are reachable from this project", idx, update, w)
			renderVulnSection(ws, t, list, imported, "Imported, symbol not called", "the module ships the vulnerability but this project never calls it", idx, update, w)
		}
	}
	return elements.NewDiv().ID(elements.ButtonID("vuln-panel")).Flex().FlexCol().H(ui.Px(ws.effectiveTermHeight())).FlexShrink(0).
		BgColor(t.EditorBg).BorderT_1().BorderColor(t.Surface1).
		Child(renderDockResize("vuln-resize", t, func(y float32, win *gpui.Window) {
			update(func(s *Workspace) { s.SetTermHeight(y) })
			win.Refresh()
		})).
		Child(header).Child(list.plain())
}

// renderVulnLoader is the scanning state's body: a centered loader block
// with the elapsed readout and the latest progress message (the Stop
// action stays in the toolbar — the scan runs on its own goroutine, so
// the panel keeps responding while govulncheck works).
func renderVulnLoader(t ui.Theme, v *vulnState) *elements.Div {
	block := elements.NewDiv().Flex().FlexCol().Flex1().ItemsCenter().JustifyCenter().Gap(ui.DefPx(8)).FlexShrink(0)
	block.Child(elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(8)).
		Child(ui.Icon("loader-circle", t.Accent, 16)).
		Child(elements.NewText("Scanning with govulncheck…").WithTextColor(t.Text)))
	msg := v.progress
	if msg == "" {
		msg = "Analyzing packages for known vulnerabilities"
	}
	block.Child(elements.NewDiv().MaxW(ui.Px(560)).OverflowHidden().WhitespaceNowrap().
		Child(elements.NewText(msg).WithTextColor(t.Dim)))
	block.Child(elements.NewDiv().Child(ui.MonoText(elapsedSeconds(v.startedAt)+" elapsed · the scan runs in the background", t.Overlay)))
	return block
}

// renderVulnEmpty is the no-data state: a centered explainer with the
// Analyze action. First open shows this instantly — the scan only starts
// when the user asks for it (govulncheck takes tens of seconds and used
// to freeze the panel's first paint when it auto-ran). In a short dock
// the explainer line drops out (showDesc) so the title and the button
// stay visible.
func renderVulnEmpty(t ui.Theme, update updateFn, showDesc bool) *elements.Div {
	block := elements.NewDiv().Flex().FlexCol().Flex1().ItemsCenter().JustifyCenter().Gap(ui.DefPx(12)).FlexShrink(0)
	block.Child(elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(9)).
		Child(ui.Icon("shield-alert", t.Accent, 18)).
		Child(elements.NewText("No vulnerability data yet").WithTextColor(t.Text)))
	if showDesc {
		block.Child(elements.NewDiv().MaxW(ui.Px(560)).OverflowHidden().WhitespaceNowrap().TextSize(ui.AbsPx(12)).
			Child(elements.NewText("Run govulncheck to scan the project's dependencies for known vulnerabilities.").WithTextColor(t.Dim)))
	}
	block.Child(renderVulnAnalyzeButton(t, update, "vuln-analyze", "Analyze with govulncheck"))
	return block
}

// renderVulnAnalyzeButton is the pill action that starts (or retries) a
// govulncheck scan: the empty state's Analyze and the error state's Retry
// share one builder (and one async runner — the process spawns off the UI
// thread and the panel flips to its loader on the next frame).
//
// The pill reads as a button: a visible border around a green-tinted fill
// (the previous bare accent wash at 14% alpha read as a stray dark strip
// floating behind the text), a play glyph in the action color and a real
// hover state that brightens both.
func renderVulnAnalyzeButton(t ui.Theme, update updateFn, id, label string) *elements.Div {
	return elements.NewDiv().ID(elements.ButtonID(id)).Flex().ItemsCenter().JustifyCenter().Gap(ui.DefPx(8)).H(ui.Px(30)).Px(ui.DefPx(14)).FlexShrink(0).
		Rounded(ui.AbsPx(6)).BgColor(t.Green.Alpha(0.12)).Border_1().BorderColor(t.Green.Alpha(0.45)).CursorPointer().
		TooltipBuilder(ui.Tooltip("Run govulncheck on the workspace root")).
		Hover(func(s *style.StyleRefinement) { s.BgColor(t.Green.Alpha(0.24)); s.SetBorderColor(t.Green.Alpha(0.75)) }).
		OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
			update(func(s *Workspace) { s.startVulnScan(update, win) })
			win.Refresh()
		}).
		Child(ui.Icon("play", t.Green, 14)).
		Child(elements.NewText(label).WithTextColor(t.Text))
}

// vulnChip is a passive count badge (no filter action; profiler chrome).
func vulnChip(id, label string, c gc.Hsla) *elements.Div {
	return elements.NewDiv().ID(elements.ButtonID(id)).Flex().ItemsCenter().H(ui.Px(18)).Px(ui.DefPx(7)).FlexShrink(0).
		Rounded(ui.AbsPx(9)).BgColor(c.Alpha(0.16)).TextSize(ui.AbsPx(11)).
		Child(elements.NewText(label).WithTextColor(c))
}

// vulnChipFilter is a small colored count badge that doubles as a filter
// shortcut (clicking it applies the matching section filter).
func vulnChipFilter(ws *Workspace, update updateFn, id, label string, c gc.Hsla, filter, tip string) *elements.Div {
	return elements.NewDiv().ID(elements.ButtonID(id)).Flex().ItemsCenter().H(ui.Px(18)).Px(ui.DefPx(7)).FlexShrink(0).
		Rounded(ui.AbsPx(9)).BgColor(c.Alpha(0.16)).TextSize(ui.AbsPx(11)).CursorPointer().
		TooltipBuilder(ui.Tooltip(tip)).
		Hover(func(s *style.StyleRefinement) { s.BgColor(c.Alpha(0.28)) }).
		OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
			update(func(s *Workspace) { s.vuln.filter = filter })
			w.Refresh()
		}).
		Child(elements.NewText(label).WithTextColor(c))
}

// renderVulnSection renders one section header plus its (expandable) OSV
// groups; returns the next running finding index.
func renderVulnSection(ws *Workspace, t ui.Theme, list *debugListEl, groups []vulncheck.Group, title, note string, startIdx int, update updateFn, w *gpui.Window) int {
	v := &ws.vuln
	sectionColor := t.Yellow
	if title == "Called by your code" {
		sectionColor = t.Red
	}
	list.Child(elements.NewDiv().Flex().ItemsCenter().H(ui.Px(24)).FlexShrink(0).Px(ui.DefPx(4)).Gap(ui.DefPx(6)).
		Child(elements.NewText(title).WithTextColor(sectionColor)).
		Child(ui.MonoText(fmt.Sprint(len(groups)), sectionColor)).
		Child(elements.NewDiv().FlexGrow(1)).
		Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().
			Child(elements.NewText(note).WithTextColor(t.Overlay))))
	for _, g := range groups {
		id := g.OSV.ID
		expanded := v.expanded[id]
		dot := t.Yellow
		if sectionColor == t.Red {
			dot = t.Red
		}
		row := elements.NewDiv().ID(elements.ButtonID("vuln-group-" + id)).Flex().ItemsCenter().H(ui.Px(26)).FlexShrink(0).Px(ui.DefPx(4)).Gap(ui.DefPx(7)).
			CursorPointer().OverflowHidden().WhitespaceNowrap().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
			OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
				update(func(s *Workspace) { s.vuln.expanded[id] = !s.vuln.expanded[id] })
				win.Refresh()
			}).
			Child(ui.ChevronIcon(t.Dim, expanded)).
			Child(elements.NewDiv().W(ui.Px(8)).H(ui.Px(8)).FlexShrink(0).Rounded(ui.AbsPx(4)).BgColor(dot)).
			Child(ui.MonoText(id, t.Accent))
		summary := g.OSV.Summary
		if summary == "" {
			summary = g.OSV.ID
		}
		row.Child(elements.NewDiv().Flex1().MinW_0().OverflowHidden().WhitespaceNowrap().
			Child(elements.NewText(summary).WithTextColor(t.Text)))
		if g.Fixed != "" {
			row.Child(elements.NewDiv().FlexShrink(0).TextSize(ui.AbsPx(11)).
				Child(elements.NewText("fixed " + g.Fixed).WithTextColor(t.Green)))
		}
		list.Child(row)
		if !expanded {
			continue
		}
		list.Child(renderVulnGroupBody(ws, t, g, update, w))
	}
	return startIdx
}

// renderVulnGroupBody renders one expanded OSV group: aliases, details,
// the advisory/fix actions and every call stack (called groups) or the
// import-only note.
func renderVulnGroupBody(ws *Workspace, t ui.Theme, g vulncheck.Group, update updateFn, w *gpui.Window) *elements.Div {
	v := &ws.vuln
	indent := func(el *elements.Div) *elements.Div {
		return el.Px(ui.DefPx(4)).Pl(ui.DefPx(30))
	}
	body := elements.NewDiv().Flex().FlexCol().FlexShrink(0).MinW_0()
	// Aliases + module line.
	meta := g.Module
	if meta == "" {
		meta = "unknown module"
	}
	metaRow := debugRow(meta+" · "+strings.Join(g.OSV.Aliases, ", "), t.Dim)
	body.Child(indent(metaRow))
	if d := oneLine(g.OSV.Details); d != "" {
		body.Child(indent(debugRow(d, t.Overlay)).TooltipBuilder(ui.Tooltip(g.OSV.Details)))
	}
	// Action row: open the advisory and copy the upgrade command.
	actions := elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(6)).H(ui.Px(24)).FlexShrink(0)
	actionBtn := func(id, icon, tip string, run func(*gpui.Window)) {
		actions.Child(elements.NewDiv().ID(elements.ButtonID("vuln-" + id + "-" + g.OSV.ID)).Flex().ItemsCenter().JustifyCenter().W(ui.Px(22)).H(ui.Px(22)).FlexShrink(0).
			Rounded(ui.AbsPx(4)).CursorPointer().TooltipBuilder(ui.Tooltip(tip)).
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
			OnClick(func(_ gpui.ClickEvent, win *gpui.Window) { run(win); win.Refresh() }).
			Child(ui.Icon(icon, t.Dim, 14)))
	}
	if url := g.URL(); url != "" {
		actionBtn("advisory", "external-link", "Open the vulnerability advisory", func(*gpui.Window) { ws.vulnOpenURL(url) })
	}
	if cmd := vulnFixCommand(g); cmd != "" {
		actionBtn("fix", "copy", "Copy the upgrade command ("+cmd+")", func(*gpui.Window) {
			if ws.clipboard != nil {
				ws.clipboard(platform.ClipboardItem{Text: cmd})
			}
		})
	}
	body.Child(indent(actions))
	// Called stacks: one block per finding trace.
	for fi, f := range g.Called {
		stackID := fmt.Sprintf("%s|%d", g.OSV.ID, fi)
		jumpFrame, jumpable := ws.vulnJumpFrame(f)
		head := elements.NewDiv().ID(elements.ButtonID("vuln-stack-" + stackID)).Flex().ItemsCenter().H(ui.Px(22)).FlexShrink(0).Gap(ui.DefPx(6)).
			CursorPointer().OverflowHidden().WhitespaceNowrap().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
			OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
				update(func(s *Workspace) { s.vuln.stacks[stackID] = !s.vuln.stacks[stackID] })
				win.Refresh()
			})
		open := v.stacks[stackID]
		head.Child(ui.ChevronIcon(t.Dim, open))
		if jumpable {
			head.Child(ui.MonoText(fmt.Sprintf("entry %s · %s:%d", jumpFrame.Symbol(), filepath.Base(jumpFrame.Position.Filename), jumpFrame.Position.Line), t.Text))
		} else {
			head.Child(ui.MonoText(fmt.Sprintf("call path (%d frames)", len(f.Trace)), t.Text))
		}
		body.Child(indent(head))
		if !open {
			continue
		}
		stack := vulnSortedStack(f)
		for si, fr := range stack {
			last := si == len(stack)-1
			line := ""
			path := ""
			if fr.HasPosition() {
				path = filepath.Base(fr.Position.Filename)
				line = fmt.Sprintf("%s:%d", path, fr.Position.Line)
			}
			color := t.Dim
			if last {
				color = t.Red
			}
			row := debugRow(fr.Symbol()+"  "+line, color)
			if last || fr.HasPosition() && ws.vulnResolvePath(fr) != "" {
				row = row.ID(elements.ButtonID(fmt.Sprintf("vuln-frame-%s|%d|%d", g.OSV.ID, fi, si))).CursorPointer().
					Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
					OnClick(func(_ gpui.ClickEvent, win *gpui.Window) {
						update(func(s *Workspace) { s.openVulnFrame(fr) })
						win.Focus(ws.FocusEditor)
						win.Refresh()
					})
			}
			body.Child(indent(row))
		}
	}
	// Import-only note.
	if g.Imported {
		body.Child(indent(debugRow("Module imported; the vulnerable symbol is not called by this project.", t.Dim)))
	}
	return body
}

// oneLine collapses a multi-line detail string for a single row.
func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if r := []rune(s); len(r) > 160 {
		s = string(r[:160]) + "…"
	}
	return s
}
