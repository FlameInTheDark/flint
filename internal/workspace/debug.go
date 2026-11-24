package workspace

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/debugger"
	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

type debugState struct {
	session                     *debugger.Session
	name                        string
	points                      map[string][]int
	location                    uint64
	tab                         string
	stack, vars, output, breaks *elements.ScrollHandle
	follow                      bool
	openVars                    map[int]bool
}

func (ws *Workspace) showDebug() {
	ws.ShowDebug = true
	ws.ShowTerminal, ws.ShowGoOutput, ws.ShowProblems = false, false, false
	ws.ShowTodos = false
	if ws.debug.breaks == nil {
		ws.debug.resetPanel("Breakpoints")
	}
}

func (ws *Workspace) toggleDebug() {
	if ws.ShowDebug {
		ws.ShowDebug = false
	} else {
		ws.showDebug()
	}
}

func (d *debugState) resetPanel(tab string) {
	*d = debugState{
		session: d.session, name: d.name, points: d.points, tab: tab, follow: true,
		stack: elements.NewScrollHandle(), vars: elements.NewScrollHandle(),
		output: elements.NewScrollHandle(), breaks: elements.NewScrollHandle(),
	}
}

func (ws *Workspace) startDebug(action goAction, update updateFn, w *gpui.Window) {
	if s := ws.debug.session; s != nil && s.Snapshot().State != debugger.Finished {
		ws.showDebug()
		return
	}
	if err := ws.goPreflight(); err != nil {
		ws.StatusError = err.Error()
		return
	}
	dir, name, args, err := ws.launchTarget(action)
	if err != nil {
		ws.StatusError = err.Error()
		return
	}
	command, err := config.ResolveLanguageServerCommand("dlv")
	if err != nil {
		ws.StatusError = "Delve not found. Install: go install github.com/go-delve/delve/cmd/dlv@latest"
		return
	}
	mode := "debug"
	if b := ws.Buf(); action == goDebugTest || (name == "" && b != nil && strings.HasSuffix(strings.ToLower(b.Path), "_test.go")) {
		mode = "test"
		args = nil
	}
	ws.showDebug()
	ws.StatusError = ""
	s := debugger.Start(command, dir, mode, ws.debug.points, args...)
	ws.debug.session = s
	ws.debug.name = name
	ws.debug.resetPanel("Output")
	if dispatch := ws.dispatch; dispatch != nil {
		go watchDebug(s, dispatch, update, w)
	}
}

// Coalesce log bursts before dispatching; never read or mutate workspace
// fields from a debugger goroutine.
func watchDebug(s *debugger.Session, dispatch func(func()), update updateFn, w *gpui.Window) {
	var queued atomic.Bool
	post := func() {
		if !queued.CompareAndSwap(false, true) {
			return
		}
		defer func() { _ = recover() }()
		dispatch(func() {
			queued.Store(false)
			defer func() { _ = recover() }()
			update(func(ws *Workspace) {
				if ws.debug.session == s {
					ws.applyDebugSnapshot(s.Snapshot())
				}
			})
			w.Refresh()
		})
	}
	for {
		select {
		case <-s.Done():
			post()
			return
		case <-s.Changes():
			post()
			t := time.NewTimer(33 * time.Millisecond)
			select {
			case <-t.C:
			case <-s.Done():
				t.Stop()
				post()
				return
			}
		}
	}
}

func (ws *Workspace) applyDebugSnapshot(snap debugger.Snapshot) {
	d := &ws.debug
	if d.follow {
		d.output.Autoscroll()
	}
	if snap.State != debugger.Paused || snap.Selected < 0 || snap.Selected >= len(snap.Frames) || d.location == snap.Location {
		return
	}
	d.location = snap.Location
	d.tab = "Inspect"
	d.openVars = nil
	d.vars.ScrollToTop()
	if snap.Selected == 0 {
		d.stack.ScrollToTop()
	}
	f := snap.Frames[snap.Selected]
	if filepath.IsAbs(f.Source.Path) && f.Line > 0 {
		previous := ws.Buf()
		ws.openGoDiagnostic(goDiagnostic{path: f.Source.Path, line: f.Line, column: max(1, f.Column)})
		if previous != ws.Buf() && ws.Find != nil {
			ws.RefreshFind()
		}
	}
}

func (ws *Workspace) breakpointPath(path string) string {
	for k := range ws.debug.points {
		if sameFilePath(k, path) {
			return k
		}
	}
	abs, _ := filepath.Abs(path)
	return abs
}

func canBreakpoint(b *editor.Buffer) bool {
	return b != nil && b.Kind == editor.KindText && b.Path != "" && strings.EqualFold(filepath.Ext(b.Path), ".go")
}

func (ws *Workspace) toggleBreakpoint(b *editor.Buffer, line int) {
	if !canBreakpoint(b) || line < 1 || line > b.LineCount() {
		return
	}
	if b.Dirty {
		ws.StatusError = "Save this Go file before changing breakpoints"
		return
	}
	ws.StatusError = ""
	d := &ws.debug
	if d.points == nil {
		d.points = make(map[string][]int)
	}
	path := ws.breakpointPath(b.Path)
	lines := d.points[path]
	i, found := slices.BinarySearch(lines, line)
	if found {
		lines = slices.Delete(lines, i, i+1)
	} else {
		lines = slices.Insert(lines, i, line)
	}
	ws.setDebugBreakpoints(path, lines)
}

func (ws *Workspace) setDebugBreakpoints(path string, lines []int) {
	d := &ws.debug
	if len(lines) == 0 {
		delete(d.points, path)
	} else {
		d.points[path] = lines
	}
	if d.session != nil {
		d.session.SetBreakpoints(path, lines)
	}
}

func (ws *Workspace) toggleCaretBreakpoint() {
	if b := ws.Buf(); canBreakpoint(b) {
		line, _ := b.OffsetToLC(b.Cursor)
		ws.toggleBreakpoint(b, line+1)
	}
}

func (ws *Workspace) debugMarkers(b *editor.Buffer, snap debugger.Snapshot) (map[int]editor.BreakpointMarker, int) {
	var marks map[int]editor.BreakpointMarker
	execLine := 0
	if canBreakpoint(b) && len(ws.debug.points) != 0 {
		path := ws.breakpointPath(b.Path)
		if lines := ws.debug.points[path]; len(lines) != 0 {
			marks = make(map[int]editor.BreakpointMarker, len(lines))
			for _, n := range lines {
				bp, resolved := snap.Breakpoints[path][n]
				marks[n] = editor.BreakpointMarker{Rejected: snap.State != debugger.Finished && resolved && !bp.Verified}
			}
		}
	}
	if snap.State == debugger.Paused && len(snap.Frames) > 0 {
		if f := snap.Frames[0]; sameFilePath(b.Path, f.Source.Path) && !b.Dirty {
			execLine = f.Line
		}
	}
	return marks, execLine
}

func (ws *Workspace) debugControl(action debugger.Action) {
	if s := ws.debug.session; s != nil {
		snap := s.Snapshot()
		if (action == debugger.Pause && snap.State == debugger.Running) || (action != debugger.Pause && snap.State == debugger.Paused) {
			ws.showDebug()
			s.Request(action, 0, snap.StopID)
		}
	}
}

func debugButton(t ui.Theme, id, label string, col gc.Hsla, click func(*gpui.Window)) *elements.Div {
	d := elements.NewDiv().ID(elements.ButtonID(id)).Flex().ItemsCenter().H(ui.Px(24)).FlexShrink(0).
		Px(ui.DefPx(6)).Rounded(ui.AbsPx(4)).WhitespaceNowrap()
	if click != nil {
		d.CursorPointer().Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { click(w); w.Refresh() })
	}
	return d.Child(elements.NewText(label).WithTextColor(col))
}

func debugList(id string, h *elements.ScrollHandle, onScroll func()) *elements.Div {
	return elements.NewDiv().ID(elements.ButtonID(id)).Flex().FlexCol().Flex1().MinH_0().MinW_0().
		OverflowYScroll().TrackScroll(h).Px(ui.DefPx(8)).Py(ui.DefPx(4)).TextSize(ui.AbsPx(12)).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			dy := ev.Delta.PixelDelta(geometry.Px(20)).Y.AsF32()
			if dy == 0 {
				return
			}
			if onScroll != nil {
				onScroll()
			}
			h.ScrollTopBy(-dy)
			w.StopPropagation()
			w.Refresh()
		})
}

func debugRow(label string, col gc.Hsla) *elements.Div {
	return elements.NewDiv().H(ui.Px(20)).FlexShrink(0).Flex().ItemsCenter().WhitespaceNowrap().OverflowHidden().
		Child(ui.MonoText(label, col))
}

func renderDebug(ws *Workspace, snap debugger.Snapshot, t ui.Theme, update updateFn) gpui.Element {
	d := &ws.debug
	s := d.session
	label := "Go Debug · " + string(snap.State)
	if d.name != "" {
		label += " · " + d.name
	}
	if s == nil {
		label = "Go Debug"
	}
	controls := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(32)).FlexShrink(0).Px(ui.DefPx(6)).Gap(ui.DefPx(2)).BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).
		Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().Child(elements.NewText(label).WithTextColor(t.Text))).Child(elements.NewDiv().FlexGrow(1))
	addControl := func(id, label string, col gc.Hsla, fn func(*gpui.Window)) {
		controls.Child(debugButton(t, "debug-"+id, label, col, fn))
	}
	action := func(a debugger.Action) func(*gpui.Window) {
		return func(*gpui.Window) { s.Request(a, 0, snap.StopID) }
	}
	switch snap.State {
	case debugger.Paused:
		addControl("continue", "Continue", t.Green, action(debugger.Continue))
		addControl("next", "Step Over", t.Text, action(debugger.Next))
		addControl("in", "Step In", t.Text, action(debugger.StepIn))
		addControl("out", "Step Out", t.Text, action(debugger.StepOut))
	case debugger.Running:
		addControl("pause", "Pause", t.Text, action(debugger.Pause))
	}
	if s == nil || snap.State == debugger.Finished {
		addControl("start", "Start", t.Green, func(w *gpui.Window) { ws.startGoAction(goDebug, update, w) })
	} else if snap.State != debugger.Stopping {
		addControl("stop", "Stop", t.Red, func(*gpui.Window) { s.Stop() })
	}
	controls.Child(debugButton(t, "debug-hide", "×", t.Dim, func(*gpui.Window) { update(func(ws *Workspace) { ws.ShowDebug = false }) }).Tooltip("Hide debugger panel"))
	tabs := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(26)).FlexShrink(0).Px(ui.DefPx(6)).Gap(ui.DefPx(4)).BorderB_1().BorderColor(t.Surface1)
	for _, tab := range []string{"Inspect", "Output", "Breakpoints"} {
		col := t.Dim
		if tab == d.tab {
			col = t.Accent
		}
		tabs.Child(debugButton(t, "debug-tab-"+tab, tab, col, func(*gpui.Window) { update(func(ws *Workspace) { ws.debug.tab = tab }) }))
	}
	if snap.Detail != "" {
		tabs.Child(elements.NewDiv().MinW_0().OverflowHidden().WhitespaceNowrap().Child(elements.NewText(snap.Detail).WithTextColor(t.Dim)))
	}
	var body gpui.Element
	switch d.tab {
	case "Inspect":
		stack := debugList("debug-stack", d.stack, nil).BorderR_1().BorderColor(t.Surface1).Child(debugRow("CALL STACK", t.Accent))
		vars := debugList("debug-variables", d.vars, nil).Child(debugRow("VARIABLES", t.Accent))
		if snap.State != debugger.Paused {
			stack.Child(debugRow("Pause to inspect the program", t.Dim))
		} else {
			stack.Child(debugRow(fmt.Sprintf("Goroutine %d", snap.Thread), t.Dim))
			for i, frame := range snap.Frames {
				col := t.Text
				if i == snap.Selected {
					col = t.Accent
				}
				label := frame.Name
				if frame.Source.Path != "" {
					label = fmt.Sprintf("%s:%d · %s", filepath.Base(frame.Source.Path), frame.Line, label)
				}
				stack.Child(debugRow(label, col).ID(elements.ButtonID(fmt.Sprintf("debug-frame-%d", i))).CursorPointer().
					OnClick(func(_ gpui.ClickEvent, _ *gpui.Window) { s.Request(debugger.SelectFrame, i, snap.StopID) }))
			}
			count := 500 // cap render work for large nested values
			for _, scope := range snap.Scopes {
				ref := scope.VariablesReference
				_, loaded := snap.Variables[ref]
				if !scope.Expensive && loaded {
					vars.Child(debugRow(scope.Name, t.Mauve))
					appendDebugVars(vars, ws, snap, t, update, ref, 1, &count)
				} else {
					debugExpandable(vars, ws, snap, t, update, scope.Name, ref, 0, &count)
				}
			}
		}
		body = elements.NewDiv().Flex().Flex1().MinH_0().Child(stack).Child(vars)
	case "Breakpoints":
		list := debugList("debug-breakpoints", d.breaks, nil)
		paths := make([]string, 0, len(d.points))
		for path := range d.points {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		if len(paths) == 0 {
			list.Child(debugRow("Click the Go editor gutter or press F9 to add a breakpoint.", t.Dim))
		}
		for _, path := range paths {
			for _, line := range d.points[path] {
				label := fmt.Sprintf("%s:%d", path, line)
				if bp, ok := snap.Breakpoints[path][line]; ok && snap.State != debugger.Finished {
					if !bp.Verified {
						label += " · " + bp.Message
					} else if bp.Line > 0 && bp.Line != line {
						label += fmt.Sprintf(" → %d", bp.Line)
					}
				}
				row := elements.NewDiv().Flex().ItemsCenter().FlexShrink(0).Gap(ui.DefPx(4))
				row.Child(debugButton(t, fmt.Sprintf("debug-remove-%s:%d", path, line), "×", t.Red, func(*gpui.Window) {
					update(func(ws *Workspace) {
						ws.setDebugBreakpoints(path, slices.DeleteFunc(ws.debug.points[path], func(n int) bool { return n == line }))
					})
				}).Tooltip("Remove breakpoint"))
				row.Child(debugRow(label, t.Text).MinW_0().CursorPointer().OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
					update(func(ws *Workspace) { ws.openGoDiagnostic(goDiagnostic{path: path, line: line, column: 1}) })
					w.Focus(ws.FocusEditor)
					w.Refresh()
				}))
				list.Child(row)
			}
		}
		body = list
	default:
		list := debugList("debug-output", d.output, func() {
			update(func(ws *Workspace) { ws.debug.follow = false })
			d.output.StopAutoscroll()
		})
		lines := strings.Split(strings.ReplaceAll(snap.Output, "\r\n", "\n"), "\n")
		for _, line := range lines[max(0, len(lines)-500):] {
			list.Child(debugRow(line, t.Text))
		}
		body = list
	}
	return elements.NewDiv().ID(elements.ButtonID("debug-panel")).Flex().FlexCol().H(ui.Px(ws.effectiveTermHeight())).FlexShrink(0).
		BgColor(t.EditorBg).BorderT_1().BorderColor(t.Surface1).Child(controls).Child(tabs).Child(body)
}

func appendDebugVars(list *elements.Div, ws *Workspace, snap debugger.Snapshot, t ui.Theme, update updateFn, ref, depth int, count *int) {
	if depth > 10 {
		return
	}
	for _, v := range snap.Variables[ref] {
		if *count <= 0 {
			return
		}
		*count--
		label := v.Name + " = " + strings.ReplaceAll(v.Value, "\n", "\\n")
		if v.Type != "" {
			label += "  (" + v.Type + ")"
		}
		debugExpandable(list, ws, snap, t, update, label, v.VariablesReference, depth, count)
	}
}

func debugExpandable(list *elements.Div, ws *Workspace, snap debugger.Snapshot, t ui.Theme, update updateFn, label string, ref, depth int, count *int) {
	open := ws.debug.openVars[ref]
	prefix := "  "
	if ref > 0 {
		prefix = "▸ "
	}
	if ref > 0 && open {
		prefix = "▾ "
	}
	row := debugRow(prefix+label, t.Text).Pl(ui.DefPx(float32(depth * 12)))
	if ref > 0 {
		s := ws.debug.session
		row.CursorPointer().OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
			update(func(ws *Workspace) {
				if ws.debug.openVars == nil {
					ws.debug.openVars = make(map[int]bool)
				}
				ws.debug.openVars[ref] = !open
			})
			if !open {
				s.Request(debugger.Expand, ref, snap.StopID)
			}
			w.Refresh()
		})
	}
	list.Child(row)
	if ref > 0 && open {
		appendDebugVars(list, ws, snap, t, update, ref, depth+1, count)
	}
}
