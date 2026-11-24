package workspace

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/debugger"
	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
)

// updateFn runs a workspace mutation (entity update + redraw upstream).
type updateFn = func(func(*Workspace))

// MenuDefs returns the menu bar definition (File / Edit / View). Item
// lists are rebuilt when a menu opens; the builders close over the live
// workspace pointer, so disabled/checked state is always current.
func MenuDefs(update updateFn, w *gpui.Window, quit func(), ws *Workspace) []ui.Menu {
	act := func(f func(*Workspace)) func() {
		return func() { update(f) }
	}

	return []ui.Menu{
		{Title: "File", Items: func() []ui.MenuItem {
			return []ui.MenuItem{
				{Label: "New File", Shortcut: "Ctrl+N", Action: act(func(ws *Workspace) { ws.NewFile() })},
				// Open File/Folder run the platform's native dialog; openPicker
				// itself keeps the modal dialog outside any entity update and
				// applies the result through the workspace's apply hook.
				{Label: "Open File…", Shortcut: "Ctrl+O", Action: func() {
					ws.openPicker(PickerOpenFile)
				}},
				{Label: "Open Folder…", Shortcut: "Ctrl+Shift+O", Action: func() {
					ws.openPicker(PickerOpenFolder)
				}},
				{Separator: true},
				{Label: "Save", Shortcut: "Ctrl+S", Disabled: ws.Buf() == nil || ws.Buf().Path == "",
					Action: act(func(ws *Workspace) { ws.Save() })},
				{Label: "Save All", Shortcut: "Ctrl+Alt+S", Action: act(func(ws *Workspace) { ws.SaveAll() })},
				{Separator: true},
				{Label: "Close Editor", Shortcut: "Ctrl+W", Action: act(func(ws *Workspace) { ws.CloseEditor() })},
				{Separator: true},
				{Label: "Settings", Shortcut: "Ctrl+,", Action: act(func(ws *Workspace) { ws.OpenSettings() })},
				{Label: "Quit", Action: func() { quit() }},
			}
		}},
		{Title: "Edit", Items: func() []ui.MenuItem {
			b := ws.Buf()
			hasSel := b != nil && b.HasSelection()
			clip := ""
			if pw := w.PlatformWindow(); pw != nil {
				if item, ok := pw.ReadClipboard(); ok {
					clip = item.Text
				}
			}
			return []ui.MenuItem{
				{Label: "Undo", Shortcut: "Ctrl+Z", Disabled: b == nil || !b.CanUndo(),
					Action: act(func(ws *Workspace) { ws.Edit(func(b *editor.Buffer) { b.Undo() }) })},
				{Label: "Redo", Shortcut: "Ctrl+Shift+Z", Disabled: b == nil || !b.CanRedo(),
					Action: act(func(ws *Workspace) { ws.Edit(func(b *editor.Buffer) { b.Redo() }) })},
				{Separator: true},
				{Label: "Cut", Shortcut: "Ctrl+X", Disabled: !hasSel,
					Action: act(func(ws *Workspace) { ws.Cut(w) })},
				{Label: "Copy", Shortcut: "Ctrl+C", Disabled: !hasSel,
					Action: act(func(ws *Workspace) { ws.Copy(w) })},
				{Label: "Paste", Shortcut: "Ctrl+V", Disabled: clip == "",
					Action: act(func(ws *Workspace) { ws.Paste(w) })},
				{Label: "Select All", Shortcut: "Ctrl+A", Disabled: b == nil,
					Action: act(func(ws *Workspace) { ws.Edit(func(b *editor.Buffer) { b.SelectAll() }) })},
				{Separator: true},
				{Label: "Find in File", Shortcut: "Ctrl+F", Action: act(func(ws *Workspace) { ws.OpenFind() })},
				{Label: "Quick Documentation", Shortcut: "Ctrl+Q", Disabled: b == nil || b.Kind != editor.KindText, Action: act(func(ws *Workspace) { ws.showCaretDocumentation() })},
				{Label: "Go to Line…", Shortcut: "Ctrl+G", Action: act(func(ws *Workspace) {
					ws.OpenPrompt("Go to Line", "", "Go", func(ws *Workspace, text string) error {
						if !ws.GoToLine(text) {
							return errInvalidLine{text}
						}
						return nil
					})
				})},
			}
		}},
		{Title: "View", Items: func() []ui.MenuItem {
			return []ui.MenuItem{
				{Label: "Toggle Project Panel", Shortcut: "Ctrl+B", HasCheck: true, Checked: ws.ShowTree,
					Action: act(func(ws *Workspace) { ws.ToggleTree() })},
				{Label: "Toggle Terminal", Shortcut: "Ctrl+`", HasCheck: true, Checked: ws.ShowTerminal,
					Action: act(func(ws *Workspace) { ws.ToggleTerminal(w) })},
				{Label: "Toggle Problems", Shortcut: "Alt+6", HasCheck: true, Checked: ws.ShowProblems,
					Action: act(func(ws *Workspace) { ws.toggleProblems() })},
				{Label: "Toggle Line Numbers", HasCheck: true, Checked: ws.ShowLineNumbers,
					Action: act(func(ws *Workspace) { ws.ToggleLineNumbers() })},
				{Label: "Toggle FPS Overlay", HasCheck: true, Checked: ws.ShowFPS,
					Action: act(func(ws *Workspace) { ws.ShowFPS = !ws.ShowFPS })},
				{Separator: true},
				// Window zoom (upstream Window::zoom; scales the whole UI).
				{Label: "Zoom In", Shortcut: "Ctrl+=", Action: func() { w.ZoomIn() }},
				{Label: "Zoom Out", Shortcut: "Ctrl+-", Action: func() { w.ZoomOut() }},
				{Label: "Reset Zoom", Shortcut: "Ctrl+0", Action: func() { w.ZoomReset() }},
				{Separator: true},
				// Fullscreen and the debug overlay (frame statistics).
				{Label: "Toggle Fullscreen", Shortcut: "F11", Action: func() {
					if pw := w.PlatformWindow(); pw != nil {
						pw.ToggleFullscreen()
					}
				}},
				{Label: "Debug Overlay", HasCheck: true, Checked: w.DebugOverlayEnabled(),
					Action: func() { w.SetDebugOverlay(!w.DebugOverlayEnabled()) }},
				{Label: "Toggle Go Debug", HasCheck: true, Checked: ws.ShowDebug,
					Action: act(func(ws *Workspace) { ws.toggleDebug() })},
				{Label: "Toggle Go Output", HasCheck: true, Checked: ws.ShowGoOutput, Disabled: ws.GoTask == nil,
					Action: act(func(s *Workspace) { s.toggleGoOutput() })},
			}
		}},
		{Title: "Run", Items: func() []ui.MenuItem {
			var state debugger.State
			if s := ws.debug.session; s != nil {
				state = s.Snapshot().State
			}
			control := func(command debugger.Action) func() { return act(func(s *Workspace) { s.debugControl(command) }) }
			return []ui.MenuItem{
				{Label: "Run Go Package", Shortcut: "Shift+F10", Action: func() { ws.startGoAction(goRun, update, w) }},
				{Label: "Edit Run Configurations…", Disabled: ws.Root == "", Action: func() { ws.editLaunch(update, w) }},
				{Label: "Build Go Project", Shortcut: "Ctrl+F9", Action: func() { ws.startGoAction(goBuild, update, w) }},
				{Label: "Test Go Project", Action: func() { ws.startGoAction(goTest, update, w) }},
				{Label: "Inspect with Go vet", Action: func() { ws.startGoAction(goVet, update, w) }},
				{Separator: true},
				{Label: "Start Go Debugging", Shortcut: "Shift+F9", Action: func() { ws.startGoAction(goDebug, update, w) }},
				{Label: "Debug Package Tests", Action: func() { ws.startGoAction(goDebugTest, update, w) }},
				{Label: "Toggle Breakpoint", Shortcut: "F9", Disabled: !canBreakpoint(ws.Buf()), Action: act(func(s *Workspace) { s.toggleCaretBreakpoint() })},
				{Label: "Continue", Shortcut: "F5", Disabled: state != debugger.Paused, Action: control(debugger.Continue)},
				{Label: "Pause", Disabled: state != debugger.Running, Action: control(debugger.Pause)},
				{Label: "Step Over", Shortcut: "F10", Disabled: state != debugger.Paused, Action: control(debugger.Next)},
				{Label: "Step In", Shortcut: "F7", Disabled: state != debugger.Paused, Action: control(debugger.StepIn)},
				{Label: "Step Out", Shortcut: "Shift+F8", Disabled: state != debugger.Paused, Action: control(debugger.StepOut)},
				{Label: "Stop Debugging", Shortcut: "Shift+F5", Disabled: state == "" || state == debugger.Finished || state == debugger.Stopping, Action: func() {
					if s := ws.debug.session; s != nil {
						s.Stop()
					}
				}},
				{Label: "Stop Go Task", Disabled: ws.GoTask == nil || !ws.GoTask.Snapshot().Running, Action: func() {
					if ws.GoTask != nil {
						ws.GoTask.Stop()
					}
				}},
			}
		}},
		{Title: "Help", Items: func() []ui.MenuItem {
			return []ui.MenuItem{
				{Label: "About", Action: func() {
					w.Prompt(gpui.PromptLevelInfo, "Flint",
						"A upstream-style code editor built on the pure-Go gpui port.",
						"Prompts, zoom, menus, terminal and the debug overlay are live.",
						[]gpui.PromptAction{{Label: "OK"}})
				}},
			}
		}},
	}
}

// errInvalidLine reports a bad line spec.
type errInvalidLine struct{ spec string }

func (e errInvalidLine) Error() string { return "invalid line number: " + e.spec }

// openPicker opens the file/folder picker in a sensible directory.
func (ws *Workspace) openPicker(mode PickerMode) {
	// Native OS dialog first (upstream prompt_for_paths: the regular
	// Windows file/folder chooser via IFileDialog). The dialog runs on the
	// caller's stack OUTSIDE any entity update - it is modal and pumps its
	// own message loop, so input handlers (and their updates) must stay
	// free to run while it is open; only the result mutates the entity,
	// through the stored apply hook. Cancelled dialogs keep the workspace
	// untouched. The in-app modal picker below is the fallback for
	// platforms without native dialog support.
	if ws.promptPaths != nil {
		if mode == PickerOpenFile {
			paths, ok := ws.promptPaths(platform.FileDialogOptions{
				Files:    true,
				Multiple: true,
				Prompt:   "Open File",
			})
			if !ok {
				return
			}
			ws.applyMutation(func(s *Workspace) {
				for _, p := range paths {
					s.OpenFile(p)
				}
			})
			return
		}
		dirs, ok := ws.promptPaths(platform.FileDialogOptions{
			Directories: true,
			Prompt:      "Open Folder",
		})
		if !ok || len(dirs) == 0 {
			return
		}
		ws.applyMutation(func(s *Workspace) {
			if err := s.OpenFolder(dirs[0]); err != nil {
				s.SetPromptError(err.Error())
			}
		})
		return
	}
	ws.applyMutation(func(s *Workspace) {
		dir := s.Root
		if dir == "" {
			if b := s.Buf(); b != nil && b.Path != "" {
				dir = filepath.Dir(b.Path)
			} else {
				dir, _ = os.Getwd()
			}
		}
		s.Picker = NewPicker(mode, dir)
	})
}

// applyMutation runs f through the entity update when one was wired
// (mutation + notify), falling back to a direct call in bare state tests.
func (ws *Workspace) applyMutation(f func(*Workspace)) {
	if ws.apply != nil {
		ws.apply(f)
		return
	}
	f(ws)
}

// EditorContextItems builds the code editor's context menu (right-click).
func EditorContextItems(update updateFn, w *gpui.Window, ws *Workspace) []ui.MenuItem {
	act := func(f func(*Workspace)) func() {
		return func() { update(f) }
	}
	b := ws.Buf()
	hasSel := b != nil && b.HasSelection()
	clip := ""
	if pw := w.PlatformWindow(); pw != nil {
		if item, ok := pw.ReadClipboard(); ok {
			clip = item.Text
		}
	}
	return []ui.MenuItem{
		{Label: "Cut", Shortcut: "Ctrl+X", Disabled: !hasSel, Action: act(func(ws *Workspace) { ws.Cut(w) })},
		{Label: "Copy", Shortcut: "Ctrl+C", Disabled: !hasSel, Action: act(func(ws *Workspace) { ws.Copy(w) })},
		{Label: "Paste", Shortcut: "Ctrl+V", Disabled: clip == "", Action: act(func(ws *Workspace) { ws.Paste(w) })},
		{Separator: true},
		{Label: "Select All", Shortcut: "Ctrl+A", Action: act(func(ws *Workspace) { ws.Edit(func(b *editor.Buffer) { b.SelectAll() }) })},
		{Separator: true},
		{Label: "Find in File", Shortcut: "Ctrl+F", Action: act(func(ws *Workspace) { ws.OpenFind() })},
		{Label: "Go to Line…", Shortcut: "Ctrl+G", Action: act(func(ws *Workspace) {
			ws.OpenPrompt("Go to Line", "", "Go", func(ws *Workspace, text string) error {
				if !ws.GoToLine(text) {
					return errInvalidLine{text}
				}
				return nil
			})
		})},
	}
}

// TreeContextItems builds the project tree's context menu for a node.
func TreeContextItems(update updateFn, ws *Workspace, node *TreeNode) []ui.MenuItem {
	act := func(f func(*Workspace)) func() {
		return func() { update(f) }
	}
	dir := node.Path
	if !node.IsDir {
		dir = filepath.Dir(node.Path)
	}
	items := []ui.MenuItem{
		{Label: "New File…", Action: act(func(ws *Workspace) {
			ws.OpenPrompt("New File in "+filepath.Base(dir), "", "Create", func(ws *Workspace, name string) error {
				return ws.createIn(dir, name, false)
			})
		})},
		{Label: "New Folder…", Action: act(func(ws *Workspace) {
			ws.OpenPrompt("New Folder in "+filepath.Base(dir), "", "Create", func(ws *Workspace, name string) error {
				return ws.createIn(dir, name, true)
			})
		})},
		{Separator: true},
		{Label: "Rename…", Disabled: node.Parent == nil, Action: act(func(ws *Workspace) {
			ws.OpenPrompt("Rename "+node.Name, node.Name, "Rename", func(ws *Workspace, name string) error {
				return ws.renameNode(node, name)
			})
		})},
		{Label: "Delete", Disabled: node.Parent == nil, Action: act(func(ws *Workspace) {
			what := "file"
			if node.IsDir {
				what = "folder and all its contents"
			}
			ws.Confirm("Delete "+what+" \""+node.Name+"\"?", func(ws *Workspace) error {
				return ws.deleteNode(node)
			})
		})},
		{Separator: true},
		{Label: "Copy Path", Action: act(func(ws *Workspace) { ws.copyText(node.Path) })},
		{Label: "Copy Relative Path", Disabled: ws.Root == "", Action: act(func(ws *Workspace) {
			ws.copyText(relPath(ws.Root, node.Path))
		})},
	}
	return items
}

// TabContextItems builds a tab's context menu.
func TabContextItems(update updateFn, path string) []ui.MenuItem {
	act := func(f func(*Workspace)) func() {
		return func() { update(f) }
	}
	return []ui.MenuItem{
		{Label: "Close", Shortcut: "Ctrl+W", Action: act(func(ws *Workspace) {
			for i, b := range ws.Buffers {
				if b.Path == path {
					ws.Active = i
					ws.CloseEditor()
					break
				}
			}
		})},
		{Label: "Close Others", Action: act(func(ws *Workspace) {
			for _, b := range ws.Buffers {
				if b.Path == path {
					for _, other := range ws.Buffers {
						if other != b {
							ws.closeLanguage(other)
							delete(ws.markdownPreview, other)
							ws.closeDiff(other)
						}
					}
					ws.completion = nil
					ws.documentation = nil
					ws.Buffers = []*editor.Buffer{b}
					ws.scrolls = []float32{0}
					ws.scrollAnims = []float32{0}
					ws.scrollAnimRuns = []bool{false}
					ws.Active = 0
					break
				}
			}
		})},
		{Label: "Close All", Action: act(func(ws *Workspace) {
			ws.closeLanguages()
			for b := range ws.diffs {
				ws.closeDiff(b)
			}
			ws.markdownPreview = nil
			ws.completion = nil
			ws.documentation = nil
			ws.Buffers = nil
			ws.scrolls = nil
			ws.scrollAnims = nil
			ws.scrollAnimRuns = nil
			ws.scrollXs = nil
			ws.Active = 0
		})},
		{Separator: true},
		{Label: "Copy Path", Disabled: path == "", Action: act(func(ws *Workspace) {
			ws.copyText(path)
		})},
	}
}

// createIn creates a file or folder inside dir and reveals it.
func (ws *Workspace) createIn(dir, name string, isDir bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errInvalidLine{"empty name"}
	}
	p := filepath.Join(dir, name)
	var err error
	if isDir {
		err = os.MkdirAll(p, 0o755)
	} else {
		err = os.WriteFile(p, nil, 0o644)
	}
	if err != nil {
		return err
	}
	ws.reloadDir(dir)
	if !isDir {
		ws.OpenFile(p)
	}
	return nil
}

// renameNode renames a tree node on disk.
func (ws *Workspace) renameNode(node *TreeNode, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errInvalidLine{"empty name"}
	}
	next := filepath.Join(filepath.Dir(node.Path), name)
	if err := os.Rename(node.Path, next); err != nil {
		return err
	}
	// Keep open buffers pointing at the new path.
	for _, b := range ws.Buffers {
		if b.Path == node.Path {
			b.Path = next
			b.Name = name
		}
	}
	ws.reloadDir(filepath.Dir(node.Path))
	return nil
}

// deleteNode removes a file or folder from disk.
func (ws *Workspace) deleteNode(node *TreeNode) error {
	var err error
	if node.IsDir {
		err = os.RemoveAll(node.Path)
	} else {
		err = os.Remove(node.Path)
	}
	if err != nil {
		return err
	}
	// Close tabs for deleted files.
	kept := ws.Buffers[:0]
	for _, b := range ws.Buffers {
		inside := b.Path == node.Path
		if node.IsDir && b.Path != "" {
			if rel, err := filepath.Rel(node.Path, b.Path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
				inside = true
			}
		}
		if !inside {
			kept = append(kept, b)
		} else {
			ws.closeLanguage(b)
			delete(ws.markdownPreview, b)
			ws.closeDiff(b)
			ws.completion = nil
			ws.documentation = nil
		}
	}
	ws.Buffers = kept
	if ws.Active >= len(ws.Buffers) {
		ws.Active = max(0, len(ws.Buffers)-1)
	}
	ws.reloadDir(filepath.Dir(node.Path))
	return nil
}

// reloadDir refreshes the tree node for dir.
func (ws *Workspace) reloadDir(dir string) {
	if ws.Tree == nil {
		return
	}
	if n := ws.Tree.Find(dir); n != nil {
		n.Reload()
	} else {
		ws.Tree.Reload()
	}
}

// copyText writes to the system clipboard (via the captured window).
func (ws *Workspace) copyText(text string) {
	if ws.clipboard != nil {
		ws.clipboard(platform.ClipboardItem{Text: text})
	}
}

// relPath strips root from a path when possible.
func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
