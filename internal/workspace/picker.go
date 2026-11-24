package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"

	"github.com/FlameInTheDark/flint/internal/ui"
)

// PickerMode selects what the picker opens.
type PickerMode uint8

const (
	// PickerOpenFile opens one file (folders navigate).
	PickerOpenFile PickerMode = iota
	// PickerOpenFolder selects a folder as the new workspace root.
	PickerOpenFolder
)

// PickerState is the open-file/open-folder modal's state.
type PickerState struct {
	Mode    PickerMode
	Dir     string
	Entries []pickerEntry
	Err     error
	// Filter is the filter input (value + cursor).
	Filter ui.InputState
	Sel    int // index into visible entries (-1 = none)
}

type pickerEntry struct {
	Name  string
	Path  string
	IsDir bool
	Up    bool // the ".." row
}

// NewPicker opens a picker in dir.
func NewPicker(mode PickerMode, dir string) *PickerState {
	p := &PickerState{Mode: mode, Sel: -1}
	p.cd(dir)
	return p
}

// cd lists a directory (dirs first, then files; ".." pinned on top).
func (p *PickerState) cd(dir string) {
	p.Dir = dir
	p.Filter.Set("")
	p.Sel = -1
	p.Entries = nil
	entries, err := os.ReadDir(dir)
	if err != nil {
		p.Err = err
		return
	}
	p.Err = nil
	p.Entries = append(p.Entries, pickerEntry{Name: "..", Path: filepath.Dir(dir), IsDir: true, Up: true})
	var dirs, files []pickerEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || ignoredEntries[name] {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil {
				isDir = info.IsDir()
			}
		}
		entry := pickerEntry{Name: name, Path: filepath.Join(dir, name), IsDir: isDir}
		if isDir {
			dirs = append(dirs, entry)
		} else {
			files = append(files, entry)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })
	p.Entries = append(p.Entries, append(dirs, files...)...)
}

// visible returns the entries matching the filter.
func (p *PickerState) visible() []pickerEntry {
	if p.Filter.String() == "" {
		return p.Entries
	}
	f := strings.ToLower(p.Filter.String())
	var out []pickerEntry
	for _, e := range p.Entries {
		if e.Up {
			continue // ".." hidden while filtering
		}
		if strings.Contains(strings.ToLower(e.Name), f) {
			out = append(out, e)
		}
	}
	return out
}

// PickerCallbacks wires the picker to the workspace.
type PickerCallbacks struct {
	// Update applies a PickerState mutation (redraw).
	Update func(f func(*PickerState))
	// OnOpenFile opens a file in a tab.
	OnOpenFile func(path string)
	// OnOpenFolder sets the workspace root.
	OnOpenFolder func(path string)
	// OnClose closes the modal.
	OnClose func()
	// RestoreFocus refocuses the editor.
	RestoreFocus func(w *gpui.Window)
	// Focus is the modal's focus handle (all keys route here).
	Focus gpui.FocusHandle
}

// RenderPicker renders the open-file/open-folder modal. All keyboard input
// is handled by the card (filter typing and list navigation in one handler,
// like the upstream editor's file finder).
func RenderPicker(p *PickerState, t ui.Theme, cb PickerCallbacks, winW, winH float32) []gpui.Element {
	title := "Open File"
	if p.Mode == PickerOpenFolder {
		title = "Open Folder"
	}
	visible := p.visible()

	// The keyboard handler: filter typing + list navigation.
	keyHandler := func(ev platform.KeyDownEvent, w *gpui.Window) {
		k := ev.Keystroke
		switch k.Key {
		case "down":
			if n := len(visible); n > 0 {
				next := p.Sel + 1
				if next >= n {
					next = 0
				}
				cb.Update(func(s *PickerState) { s.Sel = next })
				w.Refresh()
			}
		case "up":
			if n := len(visible); n > 0 {
				next := p.Sel - 1
				if next < 0 {
					next = n - 1
				}
				cb.Update(func(s *PickerState) { s.Sel = next })
				w.Refresh()
			}
		case "enter":
			pickerEnter(p, cb, w)
		case "escape":
			cb.OnClose()
			cb.RestoreFocus(w)
			w.Refresh()
		default:
			if k.KeyChar != "" || k.Key == "backspace" || k.Key == "left" ||
				k.Key == "right" || k.Key == "home" || k.Key == "end" {
				cb.Update(func(s *PickerState) {
					if s.Filter.Key(ev) {
						s.Sel = -1
					}
				})
				w.Refresh()
			}
		}
	}

	body := func() gpui.Element {
		// Path + filter display.
		head := elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(8)).Px(ui.DefPx(12)).Pt(ui.DefPx(10)).
			Child(ui.MonoText(p.Dir, t.Dim)).
			Child(elements.NewDiv().FlexGrow(1)).
			Child(elements.NewDiv().W(ui.Px(200)).
				Child(ui.RenderInput(ui.InputConfig{
					State:       &p.Filter,
					Theme:       t,
					Placeholder: "Filter…",
					Focus:       gpui.FocusHandle{}, // display-only
				})))

		list := elements.NewDiv().Flex().FlexCol().FlexGrow(1).Px(ui.DefPx(8)).OverflowYScroll()
		if p.Err != nil {
			list = list.Child(ui.MonoText(truncateMiddle(p.Err.Error(), 66), t.Red))
		}
		if p.Err == nil && len(visible) == 0 {
			list = list.Child(elements.NewText("No matching entries").WithTextColor(t.Overlay))
		}
		for i, e := range visible {
			idx := i
			selected := idx == p.Sel
			row := elements.NewDiv().
				ID(elements.ButtonID("picker-" + e.Path)).
				Flex().ItemsCenter().H(ui.Px(26)).Px(ui.DefPx(8)).Rounded(ui.AbsPx(4)).
				Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) })
			if selected {
				row = row.BgColor(t.Accent.Alpha(0.15))
			}
			row = row.
				Child(func() gpui.Element {
					if e.IsDir {
						return ui.FolderIcon(t.Mauve, false)
					}
					return ui.FileIcon(treeFileColor(e.Name, t))
				}()).
				Child(elements.NewDiv().W(ui.Px(8))).
				Child(elements.NewText(e.Name).WithTextColor(pickerLabelColor(e, t))).
				Child(elements.NewDiv().FlexGrow(1)).
				OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
					pickerActivate(p, cb, e, w)
				}).
				OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
					if p.Sel != idx && !ev.Dragging() {
						cb.Update(func(s *PickerState) { s.Sel = idx })
						w.Refresh()
					}
				})
			list = list.Child(row)
		}

		// Footer: hint + buttons.
		hint := "type to filter · up/down navigate · enter open · esc close"
		if p.Mode == PickerOpenFolder {
			hint = "enter to open a folder · esc close"
		}
		foot := elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(8)).
			Px(ui.DefPx(12)).Pb(ui.DefPx(10)).
			Child(elements.NewText(hint).WithTextColor(t.Overlay)).
			Child(elements.NewDiv().FlexGrow(1))
		if p.Mode == PickerOpenFolder {
			foot = foot.Child(ui.Button("picker-open", "Open this folder", t, true, func(w *gpui.Window) {
				cb.OnOpenFolder(p.Dir)
				cb.OnClose()
				cb.RestoreFocus(w)
				w.Refresh()
			}))
		}
		foot = foot.Child(ui.Button("picker-cancel", "Cancel", t, false, func(w *gpui.Window) {
			cb.OnClose()
			cb.RestoreFocus(w)
			w.Refresh()
		}))

		return elements.NewDiv().Flex().FlexCol().FlexGrow(1).
			Child(head).
			Child(list).
			Child(foot)
	}

	card := elements.NewDiv().
		Flex().FlexCol().FlexGrow(1).
		TrackFocus(cb.Focus).
		OnKeyDown(keyHandler)

	return ui.RenderModal(ui.ModalConfig{
		Theme:      t,
		Title:      title,
		Width:      620,
		Height:     420,
		WindowSize: geometry.SizePixels(winW, winH),
		Focus:      cb.Focus,
		Body: func() gpui.Element {
			return card.Child(body())
		},
		OnClose: func(w *gpui.Window) {
			cb.OnClose()
			cb.RestoreFocus(w)
		},
	})
}

func pickerLabelColor(e pickerEntry, t ui.Theme) gc.Hsla {
	if e.IsDir {
		return t.Text
	}
	return t.Text.Alpha(0.9)
}

// pickerEnter activates the selected (or first) entry.
func pickerEnter(p *PickerState, cb PickerCallbacks, w *gpui.Window) {
	visible := p.visible()
	if len(visible) == 0 {
		return
	}
	idx := p.Sel
	if idx < 0 || idx >= len(visible) {
		idx = 0
	}
	pickerActivate(p, cb, visible[idx], w)
}

// pickerActivate opens/navigates one entry.
func pickerActivate(p *PickerState, cb PickerCallbacks, e pickerEntry, w *gpui.Window) {
	if e.IsDir {
		cb.Update(func(s *PickerState) { s.cd(e.Path) })
		w.Refresh()
		return
	}
	if p.Mode == PickerOpenFile {
		cb.OnOpenFile(e.Path)
		cb.OnClose()
		cb.RestoreFocus(w)
		w.Refresh()
	}
}

// truncateMiddle shortens s to maxChars, keeping the head and tail with an
// ellipsis between (long paths stay recognizable: drive + filename).
func truncateMiddle(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	if maxChars < 5 {
		return string(runes[:maxChars])
	}
	head := (maxChars - 1) / 2
	tail := maxChars - 1 - head
	return string(runes[:head]) + "\u2026" + string(runes[len(runes)-tail:])
}
