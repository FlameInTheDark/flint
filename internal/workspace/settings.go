package workspace

import (
	"strconv"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/terminal"
	"github.com/FlameInTheDark/flint/internal/ui"
)

// The settings panel: a modal with a section sidebar (Appearance /
// Editor / Terminal / General), upstream-style rows (label + description on
// the left, the control on the right) and live apply — every change
// updates the running workspace and persists to
// ~/.go-gpui/editor/settings.json.

// Settings section keys (the sidebar's selection).
const (
	settingsTabAppearance = "appearance"
	settingsTabEditor     = "editor"
	settingsTabTerminal   = "terminal"
	settingsTabGeneral    = "general"
)

var settingsSections = []struct {
	key   string
	label string
}{
	{settingsTabAppearance, "Appearance"},
	{settingsTabEditor, "Editor"},
	{settingsTabTerminal, "Terminal"},
	{settingsTabGeneral, "General"},
}

// OpenSettings opens the modal (File menu, Ctrl+,). Text inputs seed
// from the current config.
func (ws *Workspace) OpenSettings() {
	ws.SettingsOpen = true
	if ws.SettingsTab == "" {
		ws.SettingsTab = settingsTabAppearance
	}
	if !ws.shellFocus.IsValid() {
		ws.shellFocus = gpui.NewFocusHandle()
	}
	if !ws.argsFocus.IsValid() {
		ws.argsFocus = gpui.NewFocusHandle()
	}
	if ws.shellInput == nil {
		ws.shellInput = &ui.InputState{}
		ws.argsInput = &ui.InputState{}
	}
	ws.shellInput.Set(ws.Cfg.Terminal.Shell)
	ws.argsInput.Set(ws.Cfg.Terminal.ShellArgs)
}

// CloseSettings persists pending text edits and hides the modal.
func (ws *Workspace) CloseSettings() {
	ws.syncSettingsTextFields()
	ws.SettingsOpen = false
	_ = ws.Cfg.Save()
}

// syncSettingsTextFields copies the text inputs back into the config.
func (ws *Workspace) syncSettingsTextFields() {
	if ws.shellInput != nil {
		ws.Cfg.Terminal.Shell = ws.shellInput.String()
	}
	if ws.argsInput != nil {
		ws.Cfg.Terminal.ShellArgs = ws.argsInput.String()
	}
	ws.applyTerminalSettings()
}

// applyTerminalSettings pushes the terminal section onto every open
// terminal (without a settings-file write; keystroke-level).
func (ws *Workspace) applyTerminalSettings() {
	ws.eachTerminal(func(t *terminal.Terminal) {
		t.SetShell(ws.Cfg.Terminal.Shell, ws.Cfg.Terminal.ShellArgs)
		t.SetMaxLines(ws.Cfg.Terminal.Scrollback)
	})
}

// renderSettings builds [backdrop, card] for the settings modal. The
// card header (title + close) comes from RenderModal; the body fills the
// rest (Flex1 — a bare FlexCol collapses inside the fixed-height card).
func renderSettings(ws *Workspace, t ui.Theme, update updateFn, focusEditor func(*gpui.Window), winW, winH float32) []gpui.Element {
	body := func() gpui.Element {
		card := elements.NewDiv().Flex().FlexCol().Flex1().OverflowHidden()

		content := elements.NewDiv().Flex().Flex1().OverflowHidden().
			Child(settingsSidebar(ws, t, update)).
			Child(settingsBody(ws, t, update))

		footer := elements.NewDiv().Flex().ItemsCenter().FlexShrink(0).
			H(ui.Px(44)).
			Px(ui.DefPx(16)).Gap(ui.DefPx(10)).
			BorderT_1().BorderColor(t.Surface1).
			TextSize(ui.AbsPx(11)).LineHeight(ui.DefPx(14)).
			Child(elements.NewText("Changes apply live and save to ~/.go-gpui/editor/settings.json").WithTextColor(t.Overlay)).
			Child(elements.NewDiv().FlexGrow(1)).
			Child(ui.Button("settings-reset", "Reset", t, false, func(w *gpui.Window) {
				update(func(s *Workspace) {
					def := config.Default()
					s.ApplyConfig(def)
					if s.shellInput != nil {
						s.shellInput.Set(def.Terminal.Shell)
						s.argsInput.Set(def.Terminal.ShellArgs)
					}
				})
				w.Refresh()
			})).
			Child(ui.Button("settings-done", "Done", t, true, func(w *gpui.Window) {
				update(func(s *Workspace) { s.CloseSettings() })
				focusEditor(w)
				w.Refresh()
			}))

		return card.Child(content).Child(footer)
	}
	return ui.RenderModal(ui.ModalConfig{
		Theme:      t,
		Title:      "Settings",
		Width:      780,
		Height:     520,
		WindowSize: geometry.SizePixels(winW, winH),
		Focus:      ws.FocusModal,
		Body:       body,
		OnClose: func(w *gpui.Window) {
			update(func(s *Workspace) { s.CloseSettings() })
			focusEditor(w)
		},
	})
}

// settingsSidebar renders the section list.
func settingsSidebar(ws *Workspace, t ui.Theme, update updateFn) *elements.Div {
	bar := elements.NewDiv().Flex().FlexCol().FlexShrink(0).
		W(ui.Px(168)).
		Py(ui.DefPx(10)).Px(ui.DefPx(8)).Gap(ui.DefPx(2)).
		BgColor(t.Surface).BorderR_1().BorderColor(t.Surface1)
	for _, sec := range settingsSections {
		active := ws.SettingsTab == sec.key
		item := elements.NewDiv().
			ID(elements.ButtonID("settings-nav-" + sec.key)).
			Flex().ItemsCenter().
			H(ui.Px(30)).Px(ui.DefPx(10)).Rounded(ui.AbsPx(4)).
			CursorPointer().
			TextSize(ui.AbsPx(13)).LineHeight(ui.DefPx(16)).
			OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
				update(func(s *Workspace) { s.SettingsTab = sec.key })
				w.Refresh()
			})
		if active {
			item = item.BgColor(t.Surface2).
				Child(elements.NewDiv().
					W(ui.Px(3)).H(ui.Px(14)).Rounded(ui.AbsPx(2)).BgColor(t.Accent)).
				Child(elements.NewDiv().W(ui.Px(6))).
				Child(elements.NewText(sec.label).WithTextColor(t.Text))
		} else {
			item = item.Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
				Child(elements.NewText(sec.label).WithTextColor(t.Dim))
		}
		bar = bar.Child(item)
	}
	return bar
}

// settingsBody renders the selected section's rows.
func settingsBody(ws *Workspace, t ui.Theme, update updateFn) *elements.Div {
	switch ws.SettingsTab {
	case settingsTabEditor:
		return editorSettingsRows(ws, t, update)
	case settingsTabTerminal:
		return terminalSettingsRows(ws, t, update)
	case settingsTabGeneral:
		return generalSettingsRows(ws, t, update)
	default:
		return appearanceSettingsRows(ws, t, update)
	}
}

// appearanceSettingsRows: the theme picker with palette swatches.
func appearanceSettingsRows(ws *Workspace, t ui.Theme, update updateFn) *elements.Div {
	body := settingsRowsContainer()
	body = body.Child(sectionLabel(t, "THEME"))
	for _, name := range ui.ThemeNames() {
		active := ws.Cfg.Appearance.Theme == name
		row := elements.NewDiv().
			ID(elements.ButtonID("settings-theme-" + name)).
			Flex().ItemsCenter().Gap(ui.DefPx(10)).
			H(ui.Px(32)).Px(ui.DefPx(10)).Rounded(ui.AbsPx(4)).
			CursorPointer().
			TextSize(ui.AbsPx(13)).LineHeight(ui.DefPx(16)).
			OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
				update(func(s *Workspace) {
					cfg := s.Cfg
					cfg.Appearance.Theme = name
					s.ApplyConfig(cfg)
				})
				w.Refresh()
			})
		if active {
			row = row.BgColor(t.Surface2)
		} else {
			row = row.Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) })
		}
		check := elements.NewDiv().Flex().ItemsCenter().JustifyCenter().W(ui.Px(16))
		if active {
			check = check.Child(ui.MonoText("✓", t.Accent))
		}
		pal := ui.ThemeByName(name)
		row = row.Child(check).
			// A swatch strip previews the palette.
			Child(elements.NewDiv().Flex().Gap(ui.DefPx(2)).
				Child(swatch(pal.EditorBg)).Child(swatch(pal.Surface2)).
				Child(swatch(pal.Accent)).Child(swatch(pal.Green)).
				Child(swatch(pal.Red))).
			Child(elements.NewDiv().W(ui.Px(6))).
			Child(elements.NewText(name).WithTextColor(t.Text))
		body = body.Child(row)
	}
	return body
}

// swatch is a 10px color chip previewing a theme.
func swatch(c gc.Hsla) gpui.Element {
	return elements.NewDiv().
		W(ui.Px(10)).H(ui.Px(10)).Rounded(ui.AbsPx(3)).
		BgColor(c)
}

// editorSettingsRows: font size, tab size, minimap, line numbers.
func editorSettingsRows(ws *Workspace, t ui.Theme, update updateFn) *elements.Div {
	body := settingsRowsContainer()

	fontPx := ws.Cfg.Editor.FontSize
	if fontPx == 0 {
		fontPx = 16 // the default's effective value
	}
	body = body.Child(row(t, "Font Size", "Size of the code and terminal glyphs, in px",
		stepper(t, "settings-font", fontPx, 10, 32, 1, update, func(s *Workspace, v int) {
			cfg := s.Cfg
			cfg.Editor.FontSize = v
			s.ApplyConfig(cfg)
		})))

	body = body.Child(row(t, "Tab Size", "Width of a tab stop, in display columns",
		stepper(t, "settings-tab", ws.Cfg.Editor.TabSize, 2, 8, 1, update, func(s *Workspace, v int) {
			cfg := s.Cfg
			cfg.Editor.TabSize = v
			s.ApplyConfig(cfg)
		})))

	body = body.Child(row(t, "Minimap", "Show the code overview rail right of the editor",
		toggle(t, "settings-minimap", ws.Cfg.Editor.ShowMinimap, update, func(s *Workspace, on bool) {
			cfg := s.Cfg
			cfg.Editor.ShowMinimap = on
			s.ApplyConfig(cfg)
		})))

	body = body.Child(row(t, "Line Numbers", "Show the gutter with line numbers",
		toggle(t, "settings-linenumbers", ws.Cfg.General.ShowLineNumbers, update, func(s *Workspace, on bool) {
			cfg := s.Cfg
			cfg.General.ShowLineNumbers = on
			s.ApplyConfig(cfg)
		})))
	return body
}

// terminalSettingsRows: shell, args, scrollback.
func terminalSettingsRows(ws *Workspace, t ui.Theme, update updateFn) *elements.Div {
	body := settingsRowsContainer()

	body = body.Child(row(t, "Shell", "Executable that runs command lines (empty = platform default)",
		textInputRow(ws, t, update, ws.shellInput, ws.shellFocus, "e.g. pwsh, bash, cmd",
			func(s *Workspace) {
				if s.shellInput != nil {
					s.Cfg.Terminal.Shell = s.shellInput.String()
					s.applyTerminalSettings()
				}
			})))

	body = body.Child(row(t, "Shell Args", "Arguments before the command line (e.g. -c or /c)",
		textInputRow(ws, t, update, ws.argsInput, ws.argsFocus, "e.g. -c or /c",
			func(s *Workspace) {
				if s.argsInput != nil {
					s.Cfg.Terminal.ShellArgs = s.argsInput.String()
					s.applyTerminalSettings()
				}
			})))

	body = body.Child(row(t, "Scrollback", "Maximum lines kept in the terminal history",
		stepper(t, "settings-scrollback", ws.Cfg.Terminal.Scrollback, 1000, 100000, 1000, update,
			func(s *Workspace, v int) {
				cfg := s.Cfg
				cfg.Terminal.Scrollback = v
				s.ApplyConfig(cfg)
			})))
	return body
}

// generalSettingsRows: panel visibility.
func generalSettingsRows(ws *Workspace, t ui.Theme, update updateFn) *elements.Div {
	body := settingsRowsContainer()
	body = body.Child(row(t, "Project Panel", "Show the file tree on the left",
		toggle(t, "settings-tree", ws.Cfg.General.ShowTree, update, func(s *Workspace, on bool) {
			cfg := s.Cfg
			cfg.General.ShowTree = on
			s.ApplyConfig(cfg)
		})))
	body = body.Child(row(t, "Terminal Panel", "Show the terminal dock at the bottom",
		toggle(t, "settings-terminal", ws.Cfg.General.ShowTerminal, update, func(s *Workspace, on bool) {
			cfg := s.Cfg
			cfg.General.ShowTerminal = on
			if on && s.Term == nil {
				s.NewTerminal()
			}
			s.ApplyConfig(cfg)
		})))
	return body
}

// ---------------------------------------------------------------------------
// Row and control builders
// ---------------------------------------------------------------------------

func settingsRowsContainer() *elements.Div {
	return elements.NewDiv().Flex().FlexCol().Flex1().OverflowHidden().
		Py(ui.DefPx(14)).Px(ui.DefPx(18)).Gap(ui.DefPx(2))
}

// sectionLabel is a small group label.
func sectionLabel(t ui.Theme, s string) *elements.Div {
	return elements.NewDiv().
		Px(ui.DefPx(10)).Py(ui.DefPx(6)).
		TextSize(ui.AbsPx(11)).LineHeight(ui.DefPx(14)).
		Child(elements.NewText(s).WithTextColor(t.Overlay))
}

// row is one setting: label + description left, control right.
func row(t ui.Theme, label, desc string, control gpui.Element) *elements.Div {
	return elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(14)).
		MinH(ui.Px(46)).Py(ui.DefPx(6)).Px(ui.DefPx(10)).
		BorderB_1().BorderColor(t.Surface1).
		Child(elements.NewDiv().Flex().FlexCol().FlexGrow(1).Gap(ui.DefPx(2)).
			Child(elements.NewText(label).WithTextColor(t.Text)).
			Child(elements.NewDiv().TextSize(ui.AbsPx(11)).LineHeight(ui.DefPx(14)).
				Child(elements.NewText(desc).WithTextColor(t.Dim)))).
		Child(control)
}

// toggle renders a switch pill. The click handler applies the flipped
// value (the element tree rebuilds every frame, so `on` is current).
func toggle(t ui.Theme, id string, on bool, update updateFn, apply func(*Workspace, bool)) gpui.Element {
	bg := t.Surface2
	knobCol := t.Dim
	if on {
		bg = t.Accent
		knobCol = gc.RGB(0xffffff).Hsla()
	}
	pill := elements.NewDiv().
		ID(elements.ButtonID(id)).
		Flex().ItemsCenter().
		W(ui.Px(36)).H(ui.Px(20)).Rounded(ui.AbsPx(10)).
		Px(ui.DefPx(2)).
		BgColor(bg).
		CursorPointer()
	if on {
		pill = pill.JustifyEnd()
	} else {
		pill = pill.JustifyStart()
	}
	pill = pill.OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
		update(func(s *Workspace) { apply(s, !on) })
		w.Refresh()
	})
	return pill.Child(elements.NewDiv().
		W(ui.Px(16)).H(ui.Px(16)).Rounded(ui.AbsPx(8)).
		BgColor(knobCol))
}

// stepper renders [- value +]. The buttons apply clamped deltas against
// the CURRENT config (fresh closures each frame).
func stepper(t ui.Theme, id string, value, min, max, step int, update updateFn, apply func(*Workspace, int)) gpui.Element {
	label := strconv.Itoa(value)
	clamp := func(v int) int {
		if v < min {
			v = min
		}
		if v > max {
			v = max
		}
		return v
	}
	return elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(6)).
		Child(stepButton(id+"-dec", "−", t, func(w *gpui.Window) {
			update(func(s *Workspace) { apply(s, clamp(value-step)) })
			w.Refresh()
		})).
		Child(elements.NewDiv().Flex().ItemsCenter().JustifyCenter().
			W(ui.Px(64)).H(ui.Px(24)).Rounded(ui.AbsPx(4)).
			BgColor(t.EditorBg).Border_1().BorderColor(t.Surface2).
			TextSize(ui.AbsPx(12)).LineHeight(ui.DefPx(16)).
			Child(ui.MonoText(label, t.Text))).
		Child(stepButton(id+"-inc", "+", t, func(w *gpui.Window) {
			update(func(s *Workspace) { apply(s, clamp(value+step)) })
			w.Refresh()
		}))
}

// stepButton is a 22px square [-]/[+] button.
func stepButton(id, label string, t ui.Theme, onClick func(w *gpui.Window)) gpui.Element {
	tip := "Increase value"
	if label == "−" {
		tip = "Decrease value"
	}
	return elements.NewDiv().
		ID(elements.ButtonID(id)).
		Tooltip(tip).
		Flex().ItemsCenter().JustifyCenter().
		W(ui.Px(22)).H(ui.Px(22)).Rounded(ui.AbsPx(4)).
		BgColor(t.Surface1).
		CursorPointer().
		TextSize(ui.AbsPx(13)).LineHeight(ui.DefPx(16)).
		Child(ui.MonoText(label, t.Text)).
		Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
		OnClick(func(ev gpui.ClickEvent, w *gpui.Window) { onClick(w) })
}

// textInputRow wraps a RenderInput with click-to-focus and live apply.
func textInputRow(ws *Workspace, t ui.Theme, update updateFn, st *ui.InputState, focus gpui.FocusHandle, placeholder string, onChange func(*Workspace)) gpui.Element {
	input := ui.RenderInput(ui.InputConfig{
		State:       st,
		Theme:       t,
		Placeholder: placeholder,
		Focus:       focus,
		Update: func(f func(*ui.InputState)) {
			update(func(s *Workspace) {
				if st == s.shellInput || st == s.argsInput {
					f(st)
					onChange(s)
				}
			})
		},
		OnEnter: func(w *gpui.Window) {
			update(func(s *Workspace) { s.CloseSettings() })
			w.Refresh()
		},
		OnEscape: func(w *gpui.Window) {
			update(func(s *Workspace) { s.SettingsOpen = false })
			w.Refresh()
		},
	})
	// Click focuses the field (RenderInput itself has no mouse handler).
	return elements.NewDiv().Flex().FlexGrow(0).JustifyEnd().
		W(ui.Px(240)).
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			if ev.Button == platform.ButtonLeft {
				w.Focus(focus)
			}
		}).
		Child(input)
}

// closeButton is the modal title bar's × button.
func closeButton(id string, t ui.Theme, onClick func(w *gpui.Window)) gpui.Element {
	return elements.NewDiv().
		ID(elements.ButtonID(id)).
		Tooltip("Close (Esc)").
		Flex().ItemsCenter().JustifyCenter().
		W(ui.Px(24)).H(ui.Px(24)).Rounded(ui.AbsPx(4)).
		CursorPointer().
		TextSize(ui.AbsPx(13)).LineHeight(ui.DefPx(16)).
		Child(elements.NewText("×").WithTextColor(t.Dim)).
		Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
		OnClick(func(ev gpui.ClickEvent, w *gpui.Window) { onClick(w) })
}
