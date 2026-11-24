package workspace

import (
	"strings"

	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"

	"github.com/FlameInTheDark/flint/internal/ui"
)

// CommandPaletteState holds the input and selected row for the action
// launcher. A separate state object keeps it small, predictable and easy to
// test without coupling it to rendering.
type CommandPaletteState struct {
	Filter ui.InputState
	Sel    int
}

type commandID uint8

const (
	commandNewFile commandID = iota
	commandOpenFile
	commandOpenFolder
	commandSave
	commandSaveAll
	commandCloseEditor
	commandFind
	commandGoToLine
	commandToggleTerminal
	commandToggleTree
	commandToggleMinimap
	commandSettings
	commandFindFile
	commandGoRun
	commandGoBuild
	commandGoTest
	commandGoVet
	commandGoDebug
	commandProblems
	commandGoDebugTest
	commandDebugBreakpoint
	commandToggleDebug
	commandEditLaunch
	commandToggleStructure
	commandToggleGit
	commandToggleTodos
	commandFindProjectText
	commandFindProjectStructure
)

type commandEntry struct {
	ID       commandID
	Label    string
	Detail   string
	Shortcut string
}

var commandEntries = []commandEntry{
	{commandNewFile, "File: New File", "Create an untitled editor", "Ctrl+N"},
	{commandOpenFile, "File: Open File…", "Open a file from disk", "Ctrl+O"},
	{commandOpenFolder, "File: Open Folder…", "Open a folder as the workspace", "Ctrl+Shift+O"},
	{commandSave, "File: Save", "Save the active editor", "Ctrl+S"},
	{commandSaveAll, "File: Save All", "Save every modified editor", "Ctrl+Alt+S"},
	{commandCloseEditor, "View: Close Editor", "Close the active tab", "Ctrl+W"},
	{commandFind, "Edit: Find", "Find text in the active editor", "Ctrl+F"},
	{commandGoToLine, "Go: Go to Line…", "Jump to a line number", "Ctrl+G"},
	{commandToggleTerminal, "View: Toggle Terminal", "Show or hide the integrated terminal", "Ctrl+`"},
	{commandToggleTree, "View: Toggle Explorer", "Show or hide the project explorer", ""},
	{commandToggleMinimap, "View: Toggle Minimap", "Show or hide the code overview", ""},
	{commandSettings, "Preferences: Open Settings", "Configure appearance and editor behavior", "Ctrl+,"},
	{commandFindFile, "Go: Search Project Files", "Find and open a file by name", "Ctrl+P"},
	{commandFindProjectText, "Edit: Find in Files", "Search for text across the project", "Ctrl+Shift+F"},
	{commandFindProjectStructure, "Go: Search Project Structure", "Jump to a Go declaration by name", ""},
	{commandGoRun, "Go: Run Package", "Run the active Go package", "Shift+F10"},
	{commandGoBuild, "Go: Build Project", "Compile Go packages into a temporary output directory", "Ctrl+F9"},
	{commandGoTest, "Go: Test Project", "Run Go tests", ""},
	{commandGoVet, "Go: Inspect with Go vet", "Run Go vet and open diagnostics", ""},
	{commandGoDebug, "Go: Start Debugging", "Debug the active package with Delve", "Shift+F9"},
	{commandGoDebugTest, "Go: Debug Package Tests", "Run the active package's tests under Delve", ""},
	{commandDebugBreakpoint, "Debug: Toggle Breakpoint", "Add or remove a breakpoint at the caret", "F9"},
	{commandToggleDebug, "View: Toggle Go Debug", "Show or hide the debugger panel", ""},
	{commandEditLaunch, "Go: Edit Run Configurations…", "Set project entry points and program arguments", ""},
	{commandToggleStructure, "View: Toggle Structure", "Show Go declarations in the active file", ""},
	{commandToggleGit, "View: Toggle Git", "Show working tree and staged changes", ""},
	{commandToggleTodos, "View: Toggle TODOs", "Find TODO and FIXME comments in Go source", ""},
	{commandProblems, "View: Toggle Problems", "Show language-server diagnostics", "Alt+6"},
}

func (p *CommandPaletteState) visible() []commandEntry {
	query := strings.ToLower(strings.TrimSpace(p.Filter.String()))
	if query == "" {
		return commandEntries
	}
	words := strings.Fields(query)
	var out []commandEntry
	for _, entry := range commandEntries {
		haystack := strings.ToLower(entry.Label + " " + entry.Detail)
		matched := true
		for _, word := range words {
			if !strings.Contains(haystack, word) {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, entry)
		}
	}
	return out
}

// RenderCommandPalette renders the compact action launcher. It uses a modal
// shell for the familiar dimmed-background behavior. The query field is
// deliberately wrapped in a fixed-height row: RenderInput is normally a
// flex-growing control, which would otherwise consume the palette's list.
func RenderCommandPalette(p *CommandPaletteState, t ui.Theme, focus gpui.FocusHandle,
	update func(func(*CommandPaletteState)), run func(commandID, *gpui.Window), close func(*gpui.Window), winW, winH float32) []gpui.Element {
	visible := p.visible()
	selected := p.Sel
	if selected < 0 || selected >= len(visible) {
		selected = 0
	}
	keyHandler := func(ev platform.KeyDownEvent, w *gpui.Window) {
		switch ev.Keystroke.Key {
		case "down":
			if len(visible) > 0 {
				update(func(s *CommandPaletteState) { s.Sel = (s.Sel + 1) % len(visible) })
			}
		case "up":
			if len(visible) > 0 {
				update(func(s *CommandPaletteState) { s.Sel = (s.Sel - 1 + len(visible)) % len(visible) })
			}
		case "enter":
			if len(visible) > 0 {
				run(visible[selected].ID, w)
			}
		case "escape":
			close(w)
		default:
			update(func(s *CommandPaletteState) {
				before := s.Filter.String()
				if s.Filter.Key(ev, w) {
					w.StopPropagation()
				}
				if before != s.Filter.String() {
					s.Sel = 0
				}
			})
		}
		w.Refresh()
	}

	body := func() gpui.Element {
		input := ui.RenderInput(ui.InputConfig{State: &p.Filter, Theme: t, Placeholder: "Type a command…",
			Update: func(f func(*ui.InputState)) { update(func(s *CommandPaletteState) { f(&s.Filter) }) }})
		search := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(50)).FlexShrink(0).
			Px(ui.DefPx(12)).Gap(ui.DefPx(10)).BorderB_1().BorderColor(t.Surface1).
			Child(elements.NewDiv().W(ui.Px(14)).TextSize(ui.AbsPx(18)).Child(elements.NewText(">").WithTextColor(t.Accent))).
			Child(elements.NewDiv().FlexGrow(1).FlexShrink(0).H(ui.Px(26)).Child(input))
		list := elements.NewDiv().Flex().FlexCol().FlexGrow(1).P(ui.DefPx(6)).Gap(ui.DefPx(2)).OverflowYScroll()
		if len(visible) == 0 {
			list = list.Child(elements.NewDiv().P(ui.DefPx(12)).Child(elements.NewText("No matching commands").WithTextColor(t.Dim)))
		}
		for i, entry := range visible {
			idx, item := i, entry
			row := elements.NewDiv().ID(elements.ButtonID("command-" + item.Label)).
				Flex().ItemsCenter().H(ui.Px(40)).Px(ui.DefPx(10)).Rounded(ui.AbsPx(4)).CursorPointer().
				Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) })
			if idx == selected {
				row = row.BgColor(t.Accent.Alpha(0.22)).BorderL_2().BorderColor(t.Accent)
			}
			labels := elements.NewDiv().Flex().FlexCol().FlexGrow(1).
				Child(elements.NewText(item.Label).WithTextColor(t.Text)).
				Child(elements.NewDiv().TextSize(ui.AbsPx(12)).Child(elements.NewText(item.Detail).WithTextColor(t.Dim)))
			row = row.Child(labels)
			if item.Shortcut != "" {
				row = row.Child(elements.NewDiv().TextSize(ui.AbsPx(12)).Child(elements.NewText(item.Shortcut).WithTextColor(t.Overlay)))
			}
			row = row.OnClick(func(ev gpui.ClickEvent, w *gpui.Window) { run(item.ID, w) }).
				OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
					if !ev.Dragging() && selected != idx {
						update(func(s *CommandPaletteState) { s.Sel = idx })
						w.Refresh()
					}
				})
			list = list.Child(row)
		}
		return elements.NewDiv().Flex().FlexCol().FlexGrow(1).
			TrackFocus(focus).OnKeyDown(keyHandler).
			Child(search).Child(list).
			Child(elements.NewDiv().H(ui.Px(30)).FlexShrink(0).Flex().ItemsCenter().Px(ui.DefPx(12)).BorderT_1().BorderColor(t.Surface1).
				TextSize(ui.AbsPx(12)).Child(elements.NewText("↑↓ navigate   Enter run   Esc dismiss").WithTextColor(t.Overlay)))
	}
	return ui.RenderModal(ui.ModalConfig{Theme: t, Title: "Command Palette", Width: 680, Height: 440,
		WindowSize: geometry.SizePixels(winW, winH), Focus: focus, Body: body, OnClose: close})
}
