package workspace

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

const maxProjectFiles = 20000

type searchMode int

const (
	searchFiles searchMode = iota
	searchText
	searchStructure
	searchModeCount
)

var searchTabs = [...]struct{ id, label, placeholder string }{
	{"files", "Files", "Search file names and paths…"},
	{"text", "Text", "Find text inside files…"},
	{"structure", "Structure", "Search Go declarations…"},
}

type projectFile struct{ path, relative string }
type FileSearchState struct {
	Root                       string
	Filter                     ui.InputState
	Selected                   int
	Files                      []projectFile
	Loading                    bool
	Truncated                  bool
	Err                        string
	cancel                     context.CancelFunc
	Mode                       searchMode
	hits                       []searchHit
	queryCancel                context.CancelFunc
	queryVersion               uint64
	queryLoading, queryLimited bool
}

// projectFiles does not follow symlinks and skips the same ignored folders as
// the project tree. The cap keeps very large workspaces bounded.
func projectFiles(ctx context.Context, root string) ([]projectFile, bool, error) {
	files := make([]projectFile, 0, 512)
	truncated := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if path == root {
				return err
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || ignoredEntries[name] {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if len(files) >= maxProjectFiles {
			truncated = true
			return fs.SkipAll
		}
		rel, e := filepath.Rel(root, path)
		if e == nil {
			files = append(files, projectFile{path: path, relative: filepath.ToSlash(rel)})
		}
		return nil
	})
	return files, truncated, err
}

func fileSearchScore(path, query string) (int, bool) {
	path = strings.ToLower(path)
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return len(path), true
	}
	name := filepath.Base(path)
	if strings.HasPrefix(name, query) {
		return len(name) - len(query), true
	}
	if at := strings.Index(name, query); at >= 0 {
		return 100 + at, true
	}
	if at := strings.Index(path, query); at >= 0 {
		return 200 + at, true
	}
	q := []rune(query)
	at := 0
	gaps := 0
	for _, r := range path {
		if at < len(q) && r == q[at] {
			at++
		} else if at > 0 && at < len(q) {
			gaps++
		}
	}
	if at == len(q) {
		return 500 + gaps, true
	}
	return 0, false
}

func (s *FileSearchState) visible() []projectFile {
	type scored struct {
		file  projectFile
		score int
	}
	var matches []scored
	query := s.Filter.String()
	for _, f := range s.Files {
		if score, ok := fileSearchScore(f.relative, query); ok {
			matches = append(matches, scored{f, score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return strings.ToLower(matches[i].file.relative) < strings.ToLower(matches[j].file.relative)
	})
	if len(matches) > 200 {
		matches = matches[:200]
	}
	out := make([]projectFile, len(matches))
	for i, m := range matches {
		out[i] = m.file
	}
	return out
}

func (ws *Workspace) closeFileSearch() {
	if s := ws.FileSearch; s != nil {
		if s.cancel != nil {
			s.cancel()
		}
		if s.queryCancel != nil {
			s.queryCancel()
		}
		ws.FileSearch = nil
	}
}

func (ws *Workspace) openFileSearch(mode searchMode, update updateFn, w *gpui.Window) {
	var state *FileSearchState
	var ctx context.Context
	update(func(s *Workspace) {
		s.closeFileSearch()
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		state = &FileSearchState{Root: s.Root, Mode: mode, Loading: s.Root != "", cancel: cancel}
		if s.Root == "" {
			for _, b := range s.Buffers {
				if b.Path == "" {
					continue
				}
				if path, err := filepath.Abs(b.Path); err == nil {
					state.Files = append(state.Files, projectFile{path: path, relative: b.Name})
				}
			}
		}
		s.FileSearch = state
		s.CommandPalette = nil
		s.Popup = nil
		s.Menu.Open = -1
	})
	w.Focus(ws.FocusFileSearch)
	w.Refresh()
	if state.Root == "" {
		return
	}
	apply := func(files []projectFile, truncated bool, err error) {
		update(func(s *Workspace) {
			if s.FileSearch != state || s.Root != state.Root {
				return
			}
			state.Files = files
			state.Truncated = truncated
			state.Loading = false
			if err != nil && err != context.Canceled {
				state.Err = err.Error()
			}
		})
		ws.searchProjectContents(state, update, w)
		w.Refresh()
	}
	if ws.dispatch == nil {
		files, truncated, err := projectFiles(ctx, state.Root)
		apply(files, truncated, err)
		return
	}
	dispatch := ws.dispatch
	go func() {
		files, truncated, err := projectFiles(ctx, state.Root)
		if ctx.Err() != nil {
			return
		}
		defer func() { _ = recover() }()
		dispatch(func() { defer func() { _ = recover() }(); apply(files, truncated, err) })
	}()
}

// Jobs own immutable paths and buffer snapshots. A new keystroke, tab, or
// dialog cancels the previous scan; the version check also drops callbacks
// already queued on the UI thread.
func (ws *Workspace) searchProjectContents(s *FileSearchState, update updateFn, w *gpui.Window) {
	var ctx context.Context
	var cancel context.CancelFunc
	var files []projectFile
	var open map[string]string
	var mode searchMode
	var query string
	var version uint64
	update(func(state *Workspace) {
		if state.FileSearch != s || state.Root != s.Root {
			return
		}
		if s.queryCancel != nil {
			s.queryCancel()
			s.queryCancel = nil
		}
		s.queryVersion++
		s.hits, s.queryLoading, s.queryLimited = nil, false, false
		mode, query, version = s.Mode, s.Filter.String(), s.queryVersion
		if mode == searchFiles || s.Loading || s.Err != "" || strings.TrimSpace(query) == "" {
			return
		}
		ctx, cancel = context.WithCancel(context.Background())
		s.queryCancel, s.queryLoading = cancel, true
		files = s.Files // index contents are immutable once published
		open = state.searchBufferText()
	})
	if ctx == nil {
		return
	}
	apply := func(hits []searchHit, limited bool) {
		update(func(state *Workspace) {
			if state.FileSearch != s || s.queryVersion != version {
				return
			}
			s.hits, s.queryLoading, s.queryLimited = hits, false, limited
			s.Selected = 0
		})
		w.Refresh()
	}
	if ws.dispatch == nil {
		hits, limited := findProjectContent(ctx, files, open, mode, query)
		cancel()
		apply(hits, limited)
		return
	}
	dispatch := ws.dispatch
	go func() {
		defer cancel()
		timer := time.NewTimer(140 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		hits, limited := findProjectContent(ctx, files, open, mode, query)
		if ctx.Err() != nil {
			return
		}
		defer func() { _ = recover() }()
		dispatch(func() { defer func() { _ = recover() }(); apply(hits, limited) })
	}()
}

func renderFileSearch(ws *Workspace, t ui.Theme, update updateFn, w *gpui.Window, winW, winH float32) []gpui.Element {
	s := ws.FileSearch
	var visible []searchHit
	if s.Mode == searchFiles {
		for _, f := range s.visible() {
			visible = append(visible, searchHit{file: f, label: filepath.Base(f.relative)})
		}
	} else {
		visible = s.hits
	}
	selected := s.Selected
	if selected < 0 || selected >= len(visible) {
		selected = 0
	}
	close := func(win *gpui.Window) {
		update(func(st *Workspace) { st.closeFileSearch() })
		win.Focus(ws.FocusEditor)
		win.Refresh()
	}
	open := func(hit searchHit, win *gpui.Window) {
		update(func(st *Workspace) {
			st.closeFileSearch()
			if hit.line > 0 {
				st.openGoDiagnostic(goDiagnostic{path: hit.file.path, line: hit.line, column: hit.column})
			} else {
				st.OpenFile(hit.file.path)
			}
		})
		win.Focus(ws.FocusEditor)
		win.Refresh()
	}
	selectMode := func(mode searchMode, win *gpui.Window) {
		if mode == s.Mode {
			return
		}
		update(func(st *Workspace) {
			if st.FileSearch == s {
				s.Mode, s.Selected = mode, 0
			}
		})
		ws.searchProjectContents(s, update, win)
		win.Refresh()
	}
	key := func(ev platform.KeyDownEvent, win *gpui.Window) {
		switch ev.Keystroke.Key {
		case "escape":
			close(win)
			return
		case "tab":
			delta := searchMode(1)
			if ev.Keystroke.Modifiers.Shift {
				delta = -1
			}
			selectMode((s.Mode+delta+searchModeCount)%searchModeCount, win)
			return
		case "down", "up":
			if len(visible) > 0 {
				delta := 1
				if ev.Keystroke.Key == "up" {
					delta = -1
				}
				update(func(st *Workspace) {
					if st.FileSearch == s {
						s.Selected = (selected + delta + len(visible)) % len(visible)
					}
				})
			}
		case "enter":
			if len(visible) > 0 {
				open(visible[selected], win)
				return
			}
		default:
			changed := false
			update(func(st *Workspace) {
				if st.FileSearch == s {
					before := s.Filter.String()
					s.Filter.Key(ev)
					changed = before != s.Filter.String()
					if changed {
						s.Selected = 0
					}
				}
			})
			if changed && s.Mode != searchFiles {
				ws.searchProjectContents(s, update, win)
			}
		}
		win.Refresh()
	}
	body := func() gpui.Element {
		tabs := elements.NewDiv().Flex().ItemsStretch().H(ui.Px(34)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(8)).BorderB_1().BorderColor(t.Surface1)
		for i, tab := range searchTabs {
			mode := searchMode(i)
			color := t.Dim
			button := elements.NewDiv().ID(elements.ButtonID("search-tab-" + tab.id)).Flex().ItemsCenter().Px(ui.DefPx(12)).CursorPointer().
				OnClick(func(_ gpui.ClickEvent, win *gpui.Window) { selectMode(mode, win) }).
				Hover(func(st *style.StyleRefinement) { st.BgColor(t.Surface1) })
			if mode == s.Mode {
				color = t.Accent
				button.BorderB_2().BorderColor(t.Accent)
			}
			tabs.Child(button.Child(elements.NewText(tab.label).WithTextColor(color)))
		}
		search := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(46)).FlexShrink(0).Px(ui.DefPx(12)).Gap(ui.DefPx(10)).BorderB_1().BorderColor(t.Surface1).
			Child(ui.ToolbarIcon("search", t.Accent)).Child(elements.NewDiv().Flex1().Child(ui.RenderInput(ui.InputConfig{State: &s.Filter, Theme: t, Placeholder: searchTabs[s.Mode].placeholder})))
		list := elements.NewDiv().Flex().FlexCol().Flex1().OverflowHidden().P(ui.DefPx(6)).OnScroll(func(ev platform.ScrollEvent, win *gpui.Window) {
			if dy := ev.Delta.PixelDelta(geometry.Px(24)).Y.AsF32(); dy != 0 && len(visible) > 0 {
				delta := 3
				if dy > 0 {
					delta = -3
				}
				update(func(st *Workspace) {
					if st.FileSearch == s {
						s.Selected = max(0, min(len(visible)-1, s.Selected+delta))
					}
				})
				win.StopPropagation()
				win.Refresh()
			}
		})
		message := ""
		color := t.Dim
		if s.Loading {
			message = "Indexing project files…"
		} else if s.Err != "" {
			message, color = s.Err, t.Red
		} else if s.queryLoading {
			message = "Searching…"
		} else if strings.TrimSpace(s.Filter.String()) == "" && s.Mode != searchFiles {
			if s.Mode == searchText {
				message = "Type text to search across files."
			} else {
				message = "Type a Go function, method, type, or other declaration."
			}
		} else if s.Root == "" && len(s.Files) == 0 {
			message = "Open a folder to search a project, or open a file to search it here."
		} else if len(visible) == 0 {
			message = "No matches"
		}
		if message != "" {
			list.Child(elements.NewDiv().P(ui.DefPx(12)).Child(elements.NewText(message).WithTextColor(color)))
		}
		height := min(float32(496), winH-16)
		rows := min(max(1, int((height-164)/34)), len(visible))
		first := max(0, min(selected-rows+1, len(visible)-rows))
		for i := first; i < first+rows; i++ {
			idx, hit := i, visible[i]
			row := elements.NewDiv().ID(elements.ButtonID(fmt.Sprintf("file-search-%d", i))).Flex().ItemsCenter().H(ui.Px(34)).FlexShrink(0).Px(ui.DefPx(10)).Gap(ui.DefPx(9)).Rounded(ui.AbsPx(4)).CursorPointer().OverflowHidden().
				Hover(func(st *style.StyleRefinement) { st.BgColor(t.Surface1) }).OnClick(func(_ gpui.ClickEvent, win *gpui.Window) { open(hit, win) }).
				OnMouseMove(func(ev platform.MouseMoveEvent, win *gpui.Window) {
					if !ev.Dragging() && selected != idx {
						update(func(st *Workspace) {
							if st.FileSearch == s {
								s.Selected = idx
							}
						})
						win.Refresh()
					}
				})
			if i == selected {
				row = row.BgColor(t.Accent.Alpha(.18))
			}
			detail := filepath.ToSlash(filepath.Dir(hit.file.relative))
			if hit.line > 0 {
				detail = fmt.Sprintf("%s:%d", hit.file.relative, hit.line)
			} else if detail == "." {
				detail = ""
			}
			row.Child(ui.FileIcon(treeFileColor(hit.file.path, t)))
			if hit.kind != "" {
				row.Child(elements.NewDiv().TextSize(ui.AbsPx(12)).FlexShrink(0).Child(elements.NewText(hit.kind).WithTextColor(t.Accent)))
			}
			row.Child(elements.NewDiv().Flex1().MinW_0().OverflowHidden().WhitespaceNowrap().TextEllipsis().Child(elements.NewText(hit.label).WithTextColor(t.Text)))
			if detail != "" {
				row.Child(elements.NewDiv().MaxW(ui.Px(max(80, min(250, winW/3)))).OverflowHidden().WhitespaceNowrap().TextEllipsis().TextSize(ui.AbsPx(12)).Child(elements.NewText(detail).WithTextColor(t.Dim)))
			}
			list = list.Child(row)
		}
		footer := "↑ ↓ navigate    Enter open    Tab switch    Esc close"
		if s.Root == "" {
			footer += "    ·    Open files only"
		}
		if s.Truncated {
			footer += "    ·    File index limited"
		}
		if s.queryLimited {
			footer += "    ·    Partial results"
		}
		return elements.NewDiv().Flex().FlexCol().Flex1().TrackFocus(ws.FocusFileSearch).OnKeyDown(key).Child(tabs).Child(search).Child(list).
			Child(elements.NewDiv().H(ui.Px(30)).FlexShrink(0).Flex().ItemsCenter().Px(ui.DefPx(12)).BorderT_1().BorderColor(t.Surface1).TextSize(ui.AbsPx(12)).Child(elements.NewText(footer).WithTextColor(t.Dim)))
	}
	return ui.RenderModal(ui.ModalConfig{Theme: t, Title: "Search Project", Width: max(1, min(720, winW-16)), Height: max(1, min(496, winH-16)), WindowSize: geometry.SizePixels(winW, winH), Focus: ws.FocusFileSearch, Body: body, OnClose: close})
}
