package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

const toolRailWidth = 36

type toolLoad struct {
	cancel context.CancelFunc
	result toolResult
}

type toolPanels struct {
	rail                   bool
	left                   string // empty selects the project tree when ShowTree is set
	git, todos             *toolLoad
	gitOp                  *gitOperation
	gitRefresh             bool
	gitRefreshQueued       bool
	gitMessage             string
	outlineBuffer          *editor.Buffer
	outlineRevision        uint64
	outline                []toolEntry
	leftScroll, todoScroll *elements.ScrollHandle
}

func (p *toolPanels) resetProject() {
	for _, load := range []*toolLoad{p.git, p.todos} {
		if load != nil {
			load.cancel()
		}
	}
	p.git, p.todos, p.left = nil, nil, ""
	p.gitMessage, p.gitRefresh = "", false
	if p.gitOp != nil {
		p.gitOp.cancel()
		p.gitOp = nil
	}
}

func (ws *Workspace) railWidth() float32 {
	if ws.tools.rail {
		return toolRailWidth
	}
	return 0
}

func (ws *Workspace) paneLeft() float32 {
	x := ws.railWidth()
	if ws.tools.left != "" || (ws.ShowTree && ws.Tree != nil) {
		x += ws.effectiveTreeWidth()
	}
	return x
}

func (ws *Workspace) outline() toolResult {
	b := ws.Buf()
	if b == nil || b.Kind != editor.KindText || b.LanguageOf() != "go" {
		return toolResult{info: "Open a Go source file to see its structure."}
	}
	p := &ws.tools
	if p.outlineBuffer != b || p.outlineRevision != b.Revision {
		p.outlineBuffer, p.outlineRevision = b, b.Revision
		p.outline = goOutline(b.Text())
	}
	info := b.Name
	if len(p.outline) == 0 {
		info += " · no declarations"
	}
	return toolResult{entries: p.outline, info: info}
}

// startToolLoad only accesses snapshots of workspace inputs off the UI thread.
func (ws *Workspace) startToolLoad(kind string, update updateFn, w *gpui.Window) {
	var work func(context.Context) toolResult
	var dest **toolLoad
	if kind == "git" {
		if ws.tools.gitOp != nil {
			return
		}
		ws.tools.gitRefresh = false
		dest = &ws.tools.git
		dir := ws.Root
		if b := ws.Buf(); dir == "" && b != nil && b.Path != "" {
			dir = filepath.Dir(b.Path)
		}
		if old := ws.tools.git; dir == "" && old != nil {
			dir = old.result.repo
		}
		work = func(ctx context.Context) toolResult { return gitStatus(ctx, dir) }
	} else {
		dest = &ws.tools.todos
		root := ws.Root
		opened := make(map[string]string)
		for _, b := range ws.Buffers {
			if path, err := filepath.Abs(b.Path); canBreakpoint(b) && err == nil {
				opened[path] = b.Text()
			}
		}
		work = func(ctx context.Context) toolResult { return scanTodos(ctx, root, opened) }
	}
	if old := *dest; old != nil {
		old.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	load := &toolLoad{cancel: cancel, result: toolResult{info: "Loading…"}}
	*dest = load
	if dispatch := ws.dispatch; dispatch != nil {
		go func() {
			result := work(ctx)
			cancel()
			defer func() { _ = recover() }()
			dispatch(func() {
				defer func() { _ = recover() }()
				update(func(s *Workspace) {
					if (kind == "git" && s.tools.git == load) || (kind == "todo" && s.tools.todos == load) {
						load.result = result
					}
				})
				w.Refresh()
			})
		}()
	} else {
		load.result = work(ctx)
		cancel()
	}
}

func (ws *Workspace) selectLeftTool(kind string, update updateFn, w *gpui.Window) {
	if ws.tools.left == kind {
		ws.tools.left = ""
	} else {
		ws.tools.left = kind
		ws.tools.leftScroll = elements.NewScrollHandle()
		if kind == "git" {
			ws.startToolLoad(kind, update, w)
		}
	}
	ws.ShowTree = false
}

func (ws *Workspace) toggleTodos(update updateFn, w *gpui.Window) {
	ws.ShowTodos = !ws.ShowTodos
	if ws.ShowTodos {
		ws.ShowDebug, ws.ShowTerminal, ws.ShowProblems, ws.ShowGoOutput = false, false, false, false
		if ws.tools.todoScroll == nil {
			ws.tools.todoScroll = elements.NewScrollHandle()
		}
		ws.startToolLoad("todo", update, w)
	}
}

func renderToolRail(ws *Workspace, t ui.Theme, update updateFn, project func(*gpui.Window)) gpui.Element {
	const buttonSize, gap, edge, divider = 30, 2, 4, 1
	height := max(0, ws.WindowSize.Height.AsF32()-ws.chromeTop()-ui.StatusHeight)
	rail := elements.NewDiv().Flex().FlexCol().W(ui.Px(toolRailWidth)).H(ui.Px(height)).FlexShrink(0).MinH_0().OverflowYScroll().Py(ui.DefPx(edge)).BgColor(t.Crust).BorderR_1().BorderColor(t.Surface1)
	icon := func(id, label string, active bool, run func(*gpui.Window)) *elements.Div {
		color := t.Dim
		b := elements.NewDiv().ID(elements.ButtonID("tool-" + id)).Flex().FlexShrink(0).ItemsCenter().JustifyCenter().W(ui.Px(buttonSize)).H(ui.Px(buttonSize)).Rounded(ui.AbsPx(5)).Tooltip(label).CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { run(w); w.Refresh() })
		if active {
			b.BgColor(t.Surface2)
			color = t.Accent
		}
		return b.Child(ui.ToolbarIcon(id, color))
	}
	left := func(kind string) func(*gpui.Window) {
		return func(w *gpui.Window) { update(func(s *Workspace) { s.selectLeftTool(kind, update, w) }) }
	}
	bottom := func(f func(*Workspace, *gpui.Window)) func(*gpui.Window) {
		return func(w *gpui.Window) { update(func(s *Workspace) { f(s, w) }); w.Focus(ws.FocusEditor) }
	}
	sideTools := elements.NewDiv().Flex().FlexCol().ItemsCenter().FlexShrink(0).Gap(ui.DefPx(gap)).
		Child(icon("project", "Project explorer (Ctrl+B)", ws.ShowTree && ws.Tree != nil && ws.tools.left == "", project)).
		Child(icon("structure", "Structure · Go declarations", ws.tools.left == "structure", left("structure"))).
		Child(icon("git", "Git version control · working tree status", ws.tools.left == "git", left("git")))
	dockTools := elements.NewDiv().Flex().FlexColReverse().ItemsCenter().FlexShrink(0).Gap(ui.DefPx(gap)).
		BorderT_1().BorderColor(t.Surface1).Pt(ui.DefPx(edge)).
		Child(icon("terminal", "Toggle terminal (Ctrl+`)", ws.ShowTerminal, func(w *gpui.Window) { update(func(s *Workspace) { s.ToggleTerminal(w) }) })).
		Child(icon("problems", "Problems (Alt+6)", ws.ShowProblems, bottom(func(s *Workspace, _ *gpui.Window) { s.toggleProblems() }))).
		Child(icon("debug", "Toggle Go Debug", ws.ShowDebug, bottom(func(s *Workspace, _ *gpui.Window) { s.toggleDebug() }))).
		Child(icon("todo", "TODOs · Go source comments", ws.ShowTodos, bottom(func(s *Workspace, w *gpui.Window) { s.toggleTodos(update, w) })))
	dockCount := 4
	if ws.GoTask != nil {
		dockTools.Child(icon("output", "Go task output", ws.ShowGoOutput, bottom(func(s *Workspace, _ *gpui.Window) { s.toggleGoOutput() })))
		dockCount++
	}
	groupHeight := func(n int) int { return n*buttonSize + (n-1)*gap }
	// Scroll containers measure their children on the scroll axis. Reserve
	// the space explicitly so the lower group is anchored even in that pass.
	occupied := groupHeight(3) + groupHeight(dockCount) + 3*edge + divider
	spacer := max(8, height-float32(occupied))
	return rail.Child(sideTools).Child(elements.NewDiv().H(ui.Px(spacer)).FlexShrink(0)).Child(dockTools)
}

func renderLeftTool(ws *Workspace, t ui.Theme, update updateFn) gpui.Element {
	kind := ws.tools.left
	var panel *elements.Div
	if kind == "git" {
		panel = renderGitPanel(ws, t, update)
	} else {
		panel = renderToolList(ws, t, update, kind, "Structure", ws.outline(), ws.tools.leftScroll, nil, func(*gpui.Window) { update(func(s *Workspace) { s.tools.left = "" }) })
	}
	panel.Relative().FlexShrink(0).W(ui.Px(ws.effectiveTreeWidth())).BorderR_1().BorderColor(t.Surface1)
	panel.Child(elements.NewDiv().ID(elements.ButtonID("tool-left-resize")).Absolute().Right(ui.Px(0)).Top(ui.Px(0)).Bottom(ui.Px(0)).W(ui.Px(4)).CursorColResize().
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button == platform.ButtonLeft {
				if hb, ok := w.ListenerHitbox(); ok {
					w.CapturePointer(hb.ID)
					w.StopPropagation()
				}
			}
		}).OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
		if ev.Dragging() {
			update(func(s *Workspace) { s.SetTreeWidth(ev.Position.X.AsF32()) })
			w.Refresh()
			w.StopPropagation()
		}
	}).OnMouseUp(func(_ platform.MouseUpEvent, w *gpui.Window) { w.ReleasePointer() }))
	return panel
}

func renderTodos(ws *Workspace, t ui.Theme, update updateFn) gpui.Element {
	result := toolResult{info: "Scan Go sources for TODO and FIXME comments."}
	if load := ws.tools.todos; load != nil {
		result = load.result
	}
	return renderToolList(ws, t, update, "todo", "TODOs", result, ws.tools.todoScroll,
		func(w *gpui.Window) { update(func(s *Workspace) { s.startToolLoad("todo", update, w) }) },
		func(*gpui.Window) { update(func(s *Workspace) { s.ShowTodos = false }) }).H(ui.Px(ws.effectiveTermHeight())).FlexShrink(0).BorderT_1().BorderColor(t.Surface1)
}

func renderToolList(ws *Workspace, t ui.Theme, update updateFn, kind, title string, result toolResult, scroll *elements.ScrollHandle, refresh, close func(*gpui.Window)) *elements.Div {
	button := func(icon, label string, run func(*gpui.Window)) *elements.Div {
		return elements.NewDiv().ID(elements.ButtonID(kind + "-" + icon)).Flex().FlexShrink(0).ItemsCenter().JustifyCenter().W(ui.Px(23)).H(ui.Px(23)).Rounded(ui.AbsPx(4)).CursorPointer().Tooltip(label).
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).Child(ui.ToolbarIcon(icon, t.Dim)).OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { run(w); w.Refresh() })
	}
	header := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(32)).FlexShrink(0).Gap(ui.DefPx(3)).Px(ui.DefPx(8)).BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).
		Child(elements.NewDiv().Flex1().MinW_0().OverflowHidden().WhitespaceNowrap().Child(elements.NewText(title).WithTextColor(t.Text)))
	if refresh != nil {
		header.Child(button("refresh", "Refresh "+title, refresh))
	}
	header.Child(button("close", "Hide "+title, close))
	list := debugList(kind+"-list", scroll, nil)
	list.Child(debugRow(result.info, t.Dim))
	for i, entry := range result.entries {
		label := entry.label
		if kind == "todo" {
			path := entry.where.path
			if p, err := filepath.Rel(ws.Root, path); ws.Root != "" && err == nil {
				path = p
			}
			label = fmt.Sprintf("%s:%d · %s", path, entry.where.line, label)
		}
		row := elements.NewDiv().ID(elements.ButtonID(fmt.Sprintf("%s-item-%d", kind, i))).Flex().FlexShrink(0).ItemsCenter().Gap(ui.DefPx(6)).H(ui.Px(23)).Px(ui.DefPx(4)).OverflowHidden().WhitespaceNowrap().
			Child(ui.MonoText(entry.badge, t.Accent)).Child(elements.NewText(label).WithTextColor(t.Text))
		if !entry.deleted {
			row.CursorPointer().Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
				update(func(s *Workspace) {
					if kind == "structure" {
						s.moveToSourceLine(entry.where.line, entry.where.column)
					} else {
						s.openGoDiagnostic(entry.where)
					}
				})
				w.Focus(ws.FocusEditor)
				w.Refresh()
			})
		}
		list.Child(row)
	}
	return elements.NewDiv().Flex().FlexCol().MinH_0().BgColor(t.EditorBg).Child(header).Child(list)
}
