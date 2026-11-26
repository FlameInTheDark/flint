package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/gotools"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

type goAction string

func (ws *Workspace) toggleGoOutput() {
	if ws.GoTask == nil {
		return
	}
	ws.ShowGoOutput = !ws.ShowGoOutput
	if ws.ShowGoOutput {
		ws.ShowTerminal, ws.ShowProblems, ws.ShowDebug, ws.ShowTodos = false, false, false, false
	}
}

const (
	goRun       goAction = "run"
	goStop      goAction = "stop"
	goBuild     goAction = "build"
	goTest      goAction = "test"
	goVet       goAction = "vet"
	goDebug     goAction = "debug"
	goDebugTest goAction = "debug test"
)

// goTarget picks the active Go file's package for Run/Debug and the project
// root for project-wide checks. It never interpolates a path into a shell.
func (ws *Workspace) goTarget(action goAction) (string, error) {
	root := ws.Root
	active := ""
	if b := ws.Buf(); b != nil && b.Path != "" && strings.EqualFold(filepath.Ext(b.Path), ".go") {
		abs, err := filepath.Abs(b.Path)
		if err == nil {
			active = filepath.Dir(abs)
		}
	}
	if (action == goRun || action == goDebug || action == goDebugTest) && active != "" {
		return active, nil
	}
	if root != "" {
		return root, nil
	}
	if active != "" {
		return active, nil
	}
	return "", fmt.Errorf("open a Go project or a Go file first")
}

func (ws *Workspace) goTaskRunning() bool { return ws.GoTask != nil && ws.GoTask.Running() }

func (ws *Workspace) goPreflight() error {
	if ws.goTaskRunning() {
		return fmt.Errorf("a Go task is already running; stop it first")
	}
	for _, b := range ws.Buffers {
		if b.Dirty && strings.EqualFold(filepath.Ext(b.Name), ".go") {
			return fmt.Errorf("save modified Go files before running tools: %s", b.Name)
		}
	}
	return nil
}

func goTaskSpec(action goAction, dir string) (gotools.Spec, error) {
	spec := gotools.Spec{Program: "go", Dir: dir}
	switch action {
	case goRun:
		spec.Label = "Run"
		spec.Args = []string{"run", "-mod=readonly", "."}
		spec.Interactive = true
	case goBuild:
		spec.Label = "Build"
		out, err := os.MkdirTemp("", "go-gpui-build-")
		if err != nil {
			return spec, err
		}
		spec.OutputDir = out
		spec.Args = []string{"build", "-mod=readonly", "-o", out, "./..."}
	case goTest:
		spec.Label = "Test"
		spec.Args = []string{"test", "-mod=readonly", "./..."}
	case goVet:
		spec.Label = "Go vet"
		spec.Args = []string{"vet", "-mod=readonly", "./..."}
	default:
		return spec, fmt.Errorf("unknown Go action")
	}
	return spec, nil
}

// startGoAction starts a background task or an integrated debug session. The
// callback is run from an event handler, never during a render/entity lease.
func (ws *Workspace) startGoAction(action goAction, update updateFn, w *gpui.Window) {
	update(func(s *Workspace) {
		if action == goStop {
			if s.goTaskRunning() {
				s.GoTask.Stop()
				s.StatusError = ""
			}
			return
		}
		if action == goDebug || action == goDebugTest {
			s.startDebug(action, update, w)
			w.Focus(s.FocusEditor)
			return
		}
		if err := s.goPreflight(); err != nil {
			s.StatusError = err.Error()
			if s.goTaskRunning() {
				s.ShowGoOutput = true
				s.ShowTerminal = false
				s.ShowProblems = false
				s.ShowDebug = false
				s.ShowTodos = false
			}
			return
		}
		dir, name, args, err := s.launchTarget(action)
		if err != nil {
			s.StatusError = err.Error()
			return
		}
		spec, err := goTaskSpec(action, dir)
		if err != nil {
			s.StatusError = err.Error()
			return
		}
		if name != "" {
			spec.Label += " · " + name
			spec.Args = append(spec.Args, args...)
		}
		dispatch := s.dispatch
		task := gotools.Start(spec, func(task *gotools.Task) {
			if dispatch == nil {
				return
			}
			defer func() { _ = recover() }()
			dispatch(func() {
				defer func() { _ = recover() }()
				update(func(state *Workspace) {
					if state.GoTask == task && state.goOutputFollow && state.goOutputScroll != nil {
						state.goOutputScroll.Autoscroll()
					}
				})
				w.Refresh()
			})
		})
		s.GoTask = task
		s.goInput.Set("")
		s.ShowGoOutput = true
		s.ShowTerminal = false
		s.ShowProblems = false
		s.ShowDebug = false
		s.ShowTodos = false
		s.goOutputFollow = true
		s.StatusError = ""
		if s.goOutputScroll == nil {
			s.goOutputScroll = elements.NewScrollHandle()
		}
		s.goOutputScroll.ScrollToTop()
		s.goOutputScroll.Autoscroll()
	})
	w.Refresh()
}

type goDiagnostic struct {
	path         string
	line, column int
}

var goDiagnosticPattern = regexp.MustCompile(`^(.+?):([0-9]+)(?::([0-9]+))?:`)

func parseGoDiagnostic(line, dir string) (goDiagnostic, bool) {
	m := goDiagnosticPattern.FindStringSubmatch(strings.TrimSpace(line))
	if len(m) == 0 {
		return goDiagnostic{}, false
	}
	lineNo, e := strconv.Atoi(m[2])
	if e != nil || lineNo < 1 {
		return goDiagnostic{}, false
	}
	col := 1
	if m[3] != "" {
		col, e = strconv.Atoi(m[3])
		if e != nil || col < 1 {
			return goDiagnostic{}, false
		}
	}
	path := filepath.FromSlash(m[1])
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return goDiagnostic{path: path, line: lineNo, column: col}, true
}

func (ws *Workspace) openGoDiagnostic(d goDiagnostic) {
	ws.OpenFile(d.path)
	b := ws.Buf()
	if b == nil || !sameFilePath(b.Path, d.path) || b.Kind != editor.KindText {
		return
	}
	ws.moveToSourceLine(d.line, d.column)
}

func (ws *Workspace) moveToSourceLine(lineNo, column int) {
	b := ws.Buf()
	if b == nil || b.Kind != editor.KindText {
		return
	}
	line := min(max(lineNo-1, 0), b.LineCount()-1)
	b.SetCursor(b.LCToOffset(line, max(0, column-1)))
	visible := (editor.Metrics{LineHeight: ws.LineHeight}).VisibleLines(ws.paneHeight())
	if line < ws.scroll() || line >= ws.scroll()+visible {
		ws.setScroll(line - visible/3)
	}
	ws.scrollCaretIntoViewX()
	ws.NoteCaretActivity()
}

// renderGoOutput is a compact Run/Build/Test/Go vet tool window. Diagnostic
// rows open their source location; users can scroll back or stop the process.
func renderGoOutput(ws *Workspace, t ui.Theme, update updateFn, w *gpui.Window) gpui.Element {
	snap := ws.GoTask.Snapshot()
	h := ws.goOutputScroll
	if !ws.goOutputFollow {
		persistScroll(h, &ws.goOutputTop)
	}
	header := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(32)).FlexShrink(0).Px(ui.DefPx(12)).Gap(ui.DefPx(8)).BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).
		Child(elements.NewText(snap.Spec.Label).WithTextColor(t.Text)).Child(elements.NewText("· " + snap.Status).WithTextColor(t.Dim)).Child(elements.NewDiv().FlexGrow(1))
	if snap.Running {
		header = header.Child(elements.NewDiv().ID(elements.ButtonID("go-task-stop")).CursorPointer().Px(ui.DefPx(8)).Rounded(ui.AbsPx(4)).Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { ws.startGoAction(goStop, update, w) }).Child(elements.NewText("Stop").WithTextColor(t.Red)))
	}
	header = header.Child(elements.NewDiv().ID(elements.ButtonID("go-output-close")).TooltipBuilder(ui.Tooltip("Hide Go output")).CursorPointer().Px(ui.DefPx(8)).Rounded(ui.AbsPx(4)).Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
		update(func(s *Workspace) { s.ShowGoOutput = false })
		w.Focus(ws.FocusEditor)
		w.Refresh()
	}).Child(elements.NewText("×").WithTextColor(t.Dim)))
	body := elements.NewDiv().ID(elements.ButtonID("go-output-list")).Flex().FlexCol().Flex1().MinW_0().OverflowYScroll().TrackScroll(h).Px(ui.DefPx(12)).Py(ui.DefPx(6)).TextSize(ui.AbsPx(12)).
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			dy := ev.Delta.PixelDelta(geometry.Px(20)).Y.AsF32()
			if dy != 0 {
				update(func(s *Workspace) { s.goOutputFollow = false })
				h.StopAutoscroll()
				h.ScrollTopBy(-dy)
				ws.goOutputTop = max(0, ws.goOutputTop-dy)
				w.StopPropagation()
				w.Refresh()
			}
		})
	lines := strings.Split(strings.ReplaceAll(snap.Output, "\r\n", "\n"), "\n")
	if len(lines) > 1000 {
		lines = lines[len(lines)-1000:]
	}
	for i, line := range lines {
		if line == "" {
			line = " "
		}
		textColor := t.Text
		row := elements.NewDiv().WhitespaceNowrap().FlexShrink(0)
		if d, ok := parseGoDiagnostic(line, snap.Spec.Dir); ok {
			textColor = t.Accent
			row = row.ID(elements.ButtonID(fmt.Sprintf("go-diagnostic-%d", i))).CursorPointer().OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
				update(func(s *Workspace) { s.openGoDiagnostic(d) })
				w.Focus(ws.FocusEditor)
				w.Refresh()
			})
		}
		body = body.Child(row.Child(ui.MonoText(line, textColor)))
	}
	panel := elements.NewDiv().ID(elements.ButtonID("go-output-panel")).Flex().FlexCol().H(ui.Px(ws.effectiveTermHeight())).FlexShrink(0).BgColor(t.EditorBg).BorderT_1().BorderColor(t.Surface1).
		// Shared dock resize strip: dragging the top edge re-anchors
		// the bottom dock's height (the terminal dock's edge behavior).
		Child(renderDockResize("go-output-resize", t, func(y float32, w *gpui.Window) {
			update(func(s *Workspace) { s.SetTermHeight(y) })
			w.Refresh()
		})).
		Child(header).Child(body)
	if snap.Spec.Interactive && snap.Running {
		input := ui.RenderInput(ui.InputConfig{
			State: &ws.goInput, Theme: t, Placeholder: "Program input…", Focus: ws.FocusGoInput,
			Update: func(f func(*ui.InputState)) { update(func(s *Workspace) { f(&s.goInput) }) },
			OnEnter: func(win *gpui.Window) {
				var line string
				update(func(s *Workspace) {
					line = s.goInput.String()
					s.goInput.Set("")
					s.goOutputFollow = true
					s.goOutputScroll.Autoscroll()
				})
				task, dispatch := ws.GoTask, ws.dispatch
				if task != nil {
					go func() {
						if err := task.SendInput(line); err != nil && dispatch != nil {
							defer func() { _ = recover() }()
							dispatch(func() {
								defer func() { _ = recover() }()
								update(func(s *Workspace) {
									if s.GoTask == task {
										s.StatusError = err.Error()
									}
								})
								win.Refresh()
							})
						}
					}()
				}
				win.Refresh()
			},
			OnEscape: func(win *gpui.Window) { win.Focus(ws.FocusEditor); win.Refresh() },
		})
		panel = panel.Child(elements.NewDiv().Flex().ItemsCenter().FlexShrink(0).H(ui.Px(34)).Px(ui.DefPx(8)).BorderT_1().BorderColor(t.Surface1).Child(input))
	}
	return panel
}
