package workspace

import (
	"runtime"

	"github.com/FlameInTheDark/flint/internal/debugger"
	"github.com/FlameInTheDark/go-gpui/action"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
)

// Window zoom actions (upstream the upstream editor's workspace zoom via the keymap): the
// View menu items mirror the same three actions. The keymap bindings are
// installed once per window; the action listeners register during paint
// (the dispatch only sees listeners of the rendered frame, so they must be
// re-registered every frame like the other element listeners).

type zoomInAction struct{ action.Unit[zoomInAction] }

func (zoomInAction) Name() string { return "editor::ZoomIn" }

type zoomOutAction struct{ action.Unit[zoomOutAction] }

func (zoomOutAction) Name() string { return "editor::ZoomOut" }

type zoomResetAction struct{ action.Unit[zoomResetAction] }

func (zoomResetAction) Name() string { return "editor::ZoomReset" }

// openSettingsAction opens the settings panel (ctrl-, like VSCode/the upstream editor).
type openSettingsAction struct {
	action.Unit[openSettingsAction]
}

func (openSettingsAction) Name() string { return "editor::OpenSettings" }

// commandPaletteAction opens the searchable action launcher.
type commandPaletteAction struct {
	action.Unit[commandPaletteAction]
}

func (commandPaletteAction) Name() string { return "editor::CommandPalette" }

type fileSearchAction struct{ action.Unit[fileSearchAction] }

func (fileSearchAction) Name() string { return "editor::FileSearch" }

type textSearchAction struct{ action.Unit[textSearchAction] }

func (textSearchAction) Name() string { return "editor::TextSearch" }

type toggleProjectAction struct {
	action.Unit[toggleProjectAction]
}

func (toggleProjectAction) Name() string { return "editor::ToggleProject" }

type runGoAction struct{ action.Unit[runGoAction] }

func (runGoAction) Name() string { return "editor::RunGo" }

type stopGoAction struct{ action.Unit[stopGoAction] }

func (stopGoAction) Name() string { return "editor::StopGo" }

type buildGoAction struct{ action.Unit[buildGoAction] }

func (buildGoAction) Name() string { return "editor::BuildGo" }

type debugGoAction struct{ action.Unit[debugGoAction] }

func (debugGoAction) Name() string { return "editor::DebugGo" }

type debugKeyAction struct{ command debugger.Action }

func (debugKeyAction) Name() string                         { return "editor::DebugControl" }
func (a debugKeyAction) BoxedClone() action.Action          { return a }
func (a debugKeyAction) PartialEq(other action.Action) bool { return a == other }

type problemsAction struct{ action.Unit[problemsAction] }

func (problemsAction) Name() string { return "editor::Problems" }

func init() {
	gpui.RegisterActionName(zoomInAction{}, "editor::ZoomIn")
	gpui.RegisterActionName(zoomOutAction{}, "editor::ZoomOut")
	gpui.RegisterActionName(zoomResetAction{}, "editor::ZoomReset")
	gpui.RegisterActionName(openSettingsAction{}, "editor::OpenSettings")
	gpui.RegisterActionName(commandPaletteAction{}, "editor::CommandPalette")
	gpui.RegisterActionName(fileSearchAction{}, "editor::FileSearch")
	gpui.RegisterActionName(textSearchAction{}, "editor::TextSearch")
	gpui.RegisterActionName(toggleProjectAction{}, "editor::ToggleProject")
	gpui.RegisterActionName(runGoAction{}, "editor::RunGo")
	gpui.RegisterActionName(stopGoAction{}, "editor::StopGo")
	gpui.RegisterActionName(buildGoAction{}, "editor::BuildGo")
	gpui.RegisterActionName(debugGoAction{}, "editor::DebugGo")
	gpui.RegisterActionName(debugKeyAction{}, "editor::DebugControl")
	gpui.RegisterActionName(problemsAction{}, "editor::Problems")
}

// zoomKeymap builds the window keymap with the zoom chords (the upstream editor's defaults:
// ctrl-=/ctrl+- zoom, ctrl-0 reset; ctrl+shift+= is the + chord on US
// layouts, reported with the unshifted "=" key name).
func zoomKeymap() *keymap.Keymap {
	os := keymap.OSLinux
	if runtime.GOOS == "windows" {
		os = keymap.OSWindows
	}
	km := keymap.New(os)
	for _, b := range []struct {
		keys string
		act  action.Action
	}{
		{"ctrl-=", zoomInAction{}},
		{"ctrl-shift-=", zoomInAction{}},
		{"ctrl-+", zoomInAction{}},
		{"ctrl--", zoomOutAction{}},
		{"ctrl-0", zoomResetAction{}},
		{"ctrl-,", openSettingsAction{}},
		{"ctrl-shift-p", commandPaletteAction{}},
		{"ctrl-p", fileSearchAction{}},
		{"ctrl-shift-f", textSearchAction{}},
		{"ctrl-b", toggleProjectAction{}},
		{"shift-f10", runGoAction{}},
		{"ctrl-f2", stopGoAction{}},
		{"ctrl-f9", buildGoAction{}},
		{"shift-f9", debugGoAction{}},
		{"f9", debugKeyAction{"breakpoint"}},
		{"f5", debugKeyAction{debugger.Continue}},
		{"f10", debugKeyAction{debugger.Next}},
		{"f7", debugKeyAction{debugger.StepIn}},
		{"shift-f8", debugKeyAction{debugger.StepOut}},
		{"shift-f5", debugKeyAction{"stop"}},
		{"alt-6", problemsAction{}},
	} {
		binding, err := keymap.NewBinding(b.keys, b.act, "", os)
		if err != nil {
			continue // a bad chord must never break the editor startup
		}
		km.AddBindings(binding)
	}
	return km
}

// InstallZoomKeys attaches the zoom keymap to the window (once, at window
// setup; the keymap outlives frames).
func InstallZoomKeys(w *gpui.Window) {
	w.SetKeymap(zoomKeymap())
}

// onZoomActions registers the paint-time action listeners that translate
// matched zoom bindings into Window zoom calls. Called from Render (paint),
// because the dispatch pipeline only consults the rendered frame's
// listeners.
func onZoomActions(w *gpui.Window) {
	gpui.OnAction(w, func(a zoomInAction, phase gpui.DispatchPhase, win *gpui.Window) {
		if phase == gpui.BubblePhase {
			win.ZoomIn()
		}
	})
	gpui.OnAction(w, func(a zoomOutAction, phase gpui.DispatchPhase, win *gpui.Window) {
		if phase == gpui.BubblePhase {
			win.ZoomOut()
		}
	})
	gpui.OnAction(w, func(a zoomResetAction, phase gpui.DispatchPhase, win *gpui.Window) {
		if phase == gpui.BubblePhase {
			win.ZoomReset()
		}
	})
}

// onSettingsAction registers the paint-time listener for the settings
// chord; the mutation runs through the workspace's update scope.
func onSettingsAction(w *gpui.Window, open func()) {
	gpui.OnAction(w, func(a openSettingsAction, phase gpui.DispatchPhase, win *gpui.Window) {
		if phase == gpui.BubblePhase {
			open()
		}
	})
}

func onCommandPaletteAction(w *gpui.Window, open func()) {
	gpui.OnAction(w, func(a commandPaletteAction, phase gpui.DispatchPhase, win *gpui.Window) {
		if phase == gpui.BubblePhase {
			open()
		}
	})
}

func onFileSearchAction(w *gpui.Window, open func(searchMode)) {
	gpui.OnAction(w, func(_ fileSearchAction, phase gpui.DispatchPhase, _ *gpui.Window) {
		if phase == gpui.BubblePhase {
			open(searchFiles)
		}
	})
	gpui.OnAction(w, func(_ textSearchAction, phase gpui.DispatchPhase, _ *gpui.Window) {
		if phase == gpui.BubblePhase {
			open(searchText)
		}
	})
}

func onProjectAndGoActions(w *gpui.Window, toggle func(), run func(goAction), problems func()) {
	gpui.OnAction(w, func(_ toggleProjectAction, phase gpui.DispatchPhase, _ *gpui.Window) {
		if phase == gpui.BubblePhase {
			toggle()
		}
	})
	gpui.OnAction(w, func(_ runGoAction, phase gpui.DispatchPhase, _ *gpui.Window) {
		if phase == gpui.BubblePhase {
			run(goRun)
		}
	})
	gpui.OnAction(w, func(_ stopGoAction, phase gpui.DispatchPhase, _ *gpui.Window) {
		if phase == gpui.BubblePhase {
			run(goStop)
		}
	})
	gpui.OnAction(w, func(_ buildGoAction, phase gpui.DispatchPhase, _ *gpui.Window) {
		if phase == gpui.BubblePhase {
			run(goBuild)
		}
	})
	gpui.OnAction(w, func(_ debugGoAction, phase gpui.DispatchPhase, _ *gpui.Window) {
		if phase == gpui.BubblePhase {
			run(goDebug)
		}
	})
	gpui.OnAction(w, func(_ problemsAction, phase gpui.DispatchPhase, _ *gpui.Window) {
		if phase == gpui.BubblePhase {
			problems()
		}
	})
}
