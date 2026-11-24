package workspace

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

type launchEditorState struct {
	original string
	fields   [3]ui.InputState
	focus    [3]gpui.FocusHandle
	err      string
}

func (ws *Workspace) loadLaunchProfiles() {
	ws.launchRoot = ws.Root
	ws.launches, ws.launchErr = config.LoadLaunchProfiles(ws.Root)
}

func (ws *Workspace) launchTarget(action goAction) (dir, name string, args []string, err error) {
	if ws.Root != "" && (action == goRun || action == goDebug || action == goDebugTest) {
		if ws.launchRoot != ws.Root {
			ws.loadLaunchProfiles()
		}
		if ws.launchErr != nil {
			return "", "", nil, fmt.Errorf("load run configurations: %w", ws.launchErr)
		}
		if name := ws.launches.Active; name != "" {
			for _, p := range ws.launches.Profiles {
				if p.Name == name {
					dir, err := p.Directory(ws.Root)
					return dir, p.Name, p.Args, err
				}
			}
			return "", "", nil, fmt.Errorf("run configuration %q no longer exists", name)
		}
	}
	dir, err = ws.goTarget(action)
	return
}

func (ws *Workspace) editLaunch(update updateFn, w *gpui.Window) {
	var focus gpui.FocusHandle
	update(func(s *Workspace) {
		if s.Root == "" {
			s.StatusError = "Open a project folder to edit Go run configurations"
			return
		}
		s.loadLaunchProfiles()
		if s.launchErr != nil {
			s.StatusError = fmt.Sprintf("Load run configurations: %v", s.launchErr)
			return
		}
		s.StatusError = ""
		s.launchEditor = &launchEditorState{}
		for i := range s.launchEditor.focus {
			s.launchEditor.focus[i] = gpui.NewFocusHandle()
		}
		focus = s.launchEditor.focus[0]
		s.selectLaunch(s.launches.Active)
	})
	if focus.IsValid() {
		w.Focus(focus)
	}
	w.Refresh()
}

func (ws *Workspace) selectLaunch(name string) {
	e := ws.launchEditor
	e.err = ""
	for _, p := range ws.launches.Profiles {
		if p.Name == name {
			e.original = p.Name
			e.fields[0].Set(p.Name)
			e.fields[1].Set(p.PackageDir)
			args := ""
			if len(p.Args) > 0 {
				b, _ := json.Marshal(p.Args)
				args = string(b)
			}
			e.fields[2].Set(args)
			return
		}
	}
	dir := "."
	if canBreakpoint(ws.Buf()) {
		path, err := filepath.Abs(ws.Buf().Path)
		if rel, relErr := filepath.Rel(ws.Root, filepath.Dir(path)); err == nil && relErr == nil {
			dir = filepath.ToSlash(rel)
		}
	}
	e.original = ""
	e.fields[0].Set("")
	e.fields[1].Set(dir)
	e.fields[2].Set("")
}

func (ws *Workspace) persistLaunch(next config.LaunchProfiles) bool {
	if err := next.Save(ws.Root); err != nil {
		ws.launchEditor.err = err.Error()
		return false
	}
	ws.launches, ws.launchErr = next, nil
	return true
}

func (ws *Workspace) saveLaunch() bool {
	e := ws.launchEditor
	p := config.LaunchProfile{Name: strings.TrimSpace(e.fields[0].String()), PackageDir: strings.TrimSpace(e.fields[1].String())}
	if p.Name == "" || p.PackageDir == "" {
		e.err = "Enter a name and package directory."
		return false
	}
	if _, err := p.Directory(ws.Root); err != nil {
		e.err = err.Error()
		return false
	}
	args := strings.TrimSpace(e.fields[2].String())
	if args != "" {
		// JSON preserves spaces, quotes, and Windows paths without shell parsing.
		var items []json.RawMessage
		if args[0] != '[' || json.Unmarshal([]byte(args), &items) != nil {
			e.err = `Arguments must be a JSON array, e.g. ["--port", "8080"].`
			return false
		}
		for _, item := range items {
			var arg string
			if len(item) == 0 || item[0] != '"' || json.Unmarshal(item, &arg) != nil || strings.ContainsRune(arg, 0) {
				e.err = "Each argument must be a string."
				return false
			}
			p.Args = append(p.Args, arg)
		}
	}
	next := config.LaunchProfiles{Active: p.Name, Profiles: slices.Clone(ws.launches.Profiles)}
	old := -1
	for i, other := range next.Profiles {
		if other.Name == e.original {
			old = i
		} else if other.Name == p.Name {
			e.err = "A configuration with that name already exists."
			return false
		}
	}
	if old < 0 {
		next.Profiles = append(next.Profiles, p)
	} else {
		next.Profiles[old] = p
	}
	return ws.persistLaunch(next)
}

func renderLaunchEditor(ws *Workspace, t ui.Theme, update updateFn, size geometry.Size[geometry.Pixels]) []gpui.Element {
	e := ws.launchEditor
	close := func(w *gpui.Window) {
		update(func(s *Workspace) { s.launchEditor = nil })
		w.Focus(ws.FocusEditor)
		w.Refresh()
	}
	save := func(w *gpui.Window) {
		saved := false
		update(func(s *Workspace) { saved = s.saveLaunch() })
		if saved {
			close(w)
		} else {
			w.Refresh()
		}
	}
	body := func() gpui.Element {
		side := elements.NewDiv().Flex().FlexCol().W(ui.Px(165)).FlexShrink(0).OverflowYScroll().BorderR_1().BorderColor(t.Surface1).P(ui.DefPx(8)).Gap(ui.DefPx(4))
		side.Child(ui.Button("launch-new", "+ New", t, false, func(w *gpui.Window) {
			update(func(s *Workspace) { s.selectLaunch("") })
			w.Focus(e.focus[0])
			w.Refresh()
		}))
		for i, p := range ws.launches.Profiles {
			label := p.Name
			if p.Name == ws.launches.Active {
				label = "• " + label
			}
			row := elements.NewDiv().ID(elements.ButtonID(fmt.Sprintf("launch-profile-%d", i))).Flex().ItemsCenter().H(ui.Px(26)).FlexShrink(0).Px(ui.DefPx(8)).Rounded(ui.AbsPx(4)).
				TextEllipsis().WhitespaceNowrap().OverflowHidden().CursorPointer().Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
				OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
					update(func(s *Workspace) { s.selectLaunch(p.Name) })
					w.Focus(e.focus[0])
					w.Refresh()
				}).Child(elements.NewText(label).WithTextColor(t.Text))
			if p.Name == e.original {
				row.BgColor(t.Surface2)
			}
			side.Child(row)
		}
		form := elements.NewDiv().Flex().FlexCol().Flex1().MinW_0().MinH_0().OverflowYScroll().P(ui.DefPx(12)).Gap(ui.DefPx(5))
		for i, label := range []string{"Name", "Go package directory (relative to project or absolute)", "Program arguments (optional JSON array)"} {
			form.Child(elements.NewText(label).WithTextColor(t.Text)).Child(elements.NewDiv().Flex().ItemsCenter().H(ui.Px(26)).FlexShrink(0).
				OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
					if ev.Button == platform.ButtonLeft {
						w.Focus(e.focus[i])
						w.Refresh()
					}
				}).Child(ui.RenderInput(ui.InputConfig{
				ID: fmt.Sprintf("launch-field-%d", i), State: &e.fields[i], Theme: t, Focus: e.focus[i],
				Placeholder: []string{"My Go app", "cmd/my-app", `["--port", "8080"]`}[i],
				Update: func(f func(*ui.InputState)) {
					update(func(s *Workspace) { f(&s.launchEditor.fields[i]); s.launchEditor.err = "" })
				},
				OnEscape: close, OnEnter: save,
			})))
		}
		if e.err != "" {
			form.Child(elements.NewText(e.err).WithTextColor(t.Red))
		}
		footer := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(44)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(6)).BorderT_1().BorderColor(t.Surface1)
		footer.Child(ui.Button("launch-default", "Use Active Editor", t, false, func(w *gpui.Window) {
			ok := false
			update(func(s *Workspace) { next := s.launches; next.Active = ""; ok = s.persistLaunch(next) })
			if ok {
				close(w)
			} else {
				w.Refresh()
			}
		}))
		footer.Child(elements.NewDiv().FlexGrow(1))
		if e.original != "" {
			footer.Child(ui.Button("launch-delete", "Delete", t, false, func(w *gpui.Window) {
				update(func(s *Workspace) {
					next := s.launches
					next.Profiles = slices.DeleteFunc(slices.Clone(next.Profiles), func(p config.LaunchProfile) bool { return p.Name == e.original })
					if next.Active == e.original {
						next.Active = ""
					}
					if s.persistLaunch(next) {
						s.selectLaunch(next.Active)
					}
				})
				w.Refresh()
			}))
		}
		footer.Child(ui.Button("launch-save", "Save & Use", t, true, save))
		return elements.NewDiv().Flex().FlexCol().Flex1().MinH_0().Child(elements.NewDiv().Flex().Flex1().MinH_0().Child(side).Child(form)).Child(footer)
	}
	return ui.RenderModal(ui.ModalConfig{Title: "Go Run Configurations", Theme: t, Width: min(680, size.Width.AsF32()-12), Height: min(330, size.Height.AsF32()-12), WindowSize: size, Focus: ws.FocusModal, Body: body, OnClose: close})
}
