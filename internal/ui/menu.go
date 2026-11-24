package ui

import (
	"fmt"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

// MenuItem is one row of a dropdown or context menu.
type MenuItem struct {
	// Label is the display text ("" for separators).
	Label string
	// Shortcut is a display-only hint, e.g. "Ctrl+S".
	Shortcut string
	// Separator renders a horizontal rule instead of a row.
	Separator bool
	// Disabled grays the item out and ignores activation.
	Disabled bool
	// HasCheck reserves the checkmark column; Checked draws the check.
	HasCheck bool
	Checked  bool
	// Action runs on activation (Enter or click).
	Action func()
}

// Menu is one menu-bar entry. Items is a function so enabled/checked
// state is recomputed every time the menu opens.
type Menu struct {
	Title string
	Items func() []MenuItem
}

// MenuState tracks the menu bar and any open dropdown.
type MenuState struct {
	// Open is the index of the open top-level menu (-1 = closed).
	Open int
	// Hover is the hovered row of the open dropdown (-1 = none).
	Hover int
	// X is the dropdown origin (left edge of its menu-bar item).
	X float32
}

// PopupState is an open menu popup (dropdown or context menu).
type PopupState struct {
	Items []MenuItem
	// X, Y is the desired origin (usually the mouse position for context
	// menus, the menu-bar item's left edge for dropdowns).
	X, Y float32
	// Hover is the keyboard-hovered row (-1 = none).
	Hover int
	// Width is the panel width used by ClampedOrigin; 0 falls back to
	// MenuWidth. RenderPopupLayer computes it from the items so long labels
	// and shortcuts never overflow the panel.
	Width float32
}

// NextEnabled returns the next selectable row index from i (wrapping), or
// -1 when the menu has no selectable rows.
func (p *PopupState) NextEnabled(from int, forward bool) int {
	if len(p.Items) == 0 {
		return -1
	}
	step := 1
	if !forward {
		step = -1
	}
	for n := 0; n < len(p.Items); n++ {
		i := ((from+step*n+step)%len(p.Items) + len(p.Items)) % len(p.Items)
		if !p.Items[i].Separator && !p.Items[i].Disabled {
			return i
		}
	}
	return -1
}

// Height estimates the popup's rendered height (for viewport clamping).
func (p *PopupState) Height() float32 {
	h := float32(2 * MenuPadY)
	for _, it := range p.Items {
		if it.Separator {
			h += 9
		} else {
			h += MenuItemHeight
		}
	}
	return h
}

// ClampedOrigin returns the popup origin clamped so the popup stays fully
// inside the window (the "never overflow the app view" rule; menus open
// upward/leftward when there is no room below/right, like the upstream editor). The width
// defaults to MenuWidth when the popup's own measured width is unset.
func (p *PopupState) ClampedOrigin(winW, winH float32) (float32, float32) {
	w := p.Width
	if w <= 0 {
		w = MenuWidth
	}
	h := p.Height()
	x := p.X
	y := p.Y
	pad := float32(ContextPad)
	if x+w > winW-pad {
		x = winW - w - pad
	}
	if y+h > winH-pad {
		y = winH - h - pad
	}
	if y < pad {
		y = pad
	}
	if x < pad {
		x = pad
	}
	return x, y
}

// ---------------------------------------------------------------------------
// Menu bar
// ---------------------------------------------------------------------------

// MenuBarConfig configures RenderMenuBar.
type MenuBarConfig struct {
	Menus []Menu
	State *MenuState
	Theme Theme
	// Project is the passive project label ("" hides it).
	Project string
	// FPS is the right-aligned frames-per-second readout ("" hides it).
	FPS string
	// Update applies a MenuState mutation (workspace update + redraw).
	Update func(f func(*MenuState))
	// OnOpen opens menu idx at the given x (captures keyboard focus).
	OnOpen func(idx int, x float32)
	// OnClose closes the open menu.
	OnClose func()
	// Optional workspace controls; omitted when the menu bar is used standalone.
	OnSearch    func(*gpui.Window)
	OnSettings  func(*gpui.Window)
	OnRun       func(*gpui.Window)
	OnRunConfig func(*gpui.Window)
	RunTarget   string
	OnBuild     func(*gpui.Window)
	OnTest      func(*gpui.Window)
	OnVet       func(*gpui.Window)
	Compact     bool
	VeryCompact bool
}

// RenderMenuBar renders the application menu bar. Each entry toggles its
// dropdown; while a dropdown is open, moving the mouse across the bar
// switches menus (Windows/the upstream editor behavior).
func RenderMenuBar(cfg MenuBarConfig) gpui.Element {
	t := cfg.Theme
	bar := elements.NewDiv().Flex().ItemsStretch().H(Px(MenuBarHeight)).TextSize(AbsPx(13)).
		FlexShrink(0). // fixed chrome: never yields height to a content overflow
		BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1)

	for i, menu := range cfg.Menus {
		idx := i
		active := cfg.State.Open == idx
		// Top-level navigation must stay readable when inactive; using the
		// metadata color here made it disappear into dark window chrome.
		labelColor := t.Text.Alpha(0.78)
		if active {
			labelColor = t.Text
		}
		bg := gc.TransparentBlack()
		if active {
			bg = t.Surface1
		}
		bar = bar.Child(elements.NewDiv().
			ID(elements.ButtonID("menubar-" + menu.Title)).
			Flex().ItemsCenter().Px(DefPx(12)).
			CursorPointer().
			BgColor(bg).
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
			OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
				if cfg.State.Open == idx {
					cfg.OnClose()
					cfg.Update(func(*MenuState) {})
				} else {
					x := float32(0)
					if hb, ok := w.ListenerHitbox(); ok {
						x = float32(hb.Bounds.Origin.X)
					}
					cfg.OnOpen(idx, x)
				}
			}).
			OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
				// Switch menus on hover while one is open (but not while
				// the user is pressing the mouse over the bar).
				if cfg.State.Open >= 0 && cfg.State.Open != idx && !ev.Dragging() {
					x := cfg.State.X
					if hb, ok := w.ListenerHitbox(); ok {
						x = float32(hb.Bounds.Origin.X)
					}
					cfg.OnOpen(idx, x)
				}
			}).
			Child(elements.NewText(menu.Title).WithTextColor(labelColor)))
	}

	// Compact IDE toolbar: all buttons invoke real workspace actions.
	if cfg.Project != "" && !cfg.Compact {
		label := cfg.Project
		if runes := []rune(label); len(runes) > 18 {
			label = string(runes[:17]) + "…"
		}
		bar.Child(elements.NewDiv().Flex().ItemsCenter().Px(DefPx(9)).Child(elements.NewText(label).WithTextColor(t.Dim)))
	}
	bar = bar.Child(elements.NewDiv().FlexGrow(1))
	if cfg.OnRunConfig != nil && !cfg.VeryCompact {
		target := cfg.RunTarget
		if target == "" {
			target = "Active Editor"
		}
		selector := elements.NewDiv().ID(elements.ButtonID("toolbar-run-config")).Flex().ItemsCenter().Gap(DefPx(5)).Px(DefPx(6)).CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).Tooltip("Go run configurations: " + target).
			OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { cfg.OnRunConfig(w) })
		if !cfg.Compact && cfg.RunTarget != "" {
			if r := []rune(target); len(r) > 18 {
				target = string(r[:17]) + "…"
			}
			selector.Child(elements.NewText(target).WithTextColor(t.Text))
		}
		bar.Child(selector.Child(Icon("chevron-down", t.Dim, 13)))
	}
	tool := func(id, tip string, action func(*gpui.Window)) gpui.Element {
		color := t.Dim
		if id == "run" {
			color = t.Green
		}
		return elements.NewDiv().ID(elements.ButtonID("toolbar-" + id)).Flex().ItemsCenter().JustifyCenter().W(Px(32)).CursorPointer().
			Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).Tooltip(tip).
			OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { action(w) }).Child(ToolbarIcon(id, color))
	}
	if cfg.OnRun != nil {
		bar = bar.Child(tool("run", "Run Go package (Shift+F10)", cfg.OnRun))
	}
	if cfg.OnBuild != nil && !cfg.VeryCompact {
		bar = bar.Child(tool("build", "Build Go project (Ctrl+F9)", cfg.OnBuild))
	}
	if cfg.OnTest != nil && !cfg.VeryCompact {
		bar = bar.Child(tool("test", "Test Go project", cfg.OnTest))
	}
	if cfg.OnVet != nil && !cfg.VeryCompact {
		bar = bar.Child(tool("vet", "Inspect with Go vet", cfg.OnVet))
	}
	if cfg.OnSearch != nil {
		if cfg.Compact {
			bar = bar.Child(tool("search", "Search Project (Ctrl+P)", cfg.OnSearch))
		} else {
			bar = bar.Child(elements.NewDiv().ID(elements.ButtonID("toolbar-search")).Flex().ItemsCenter().Gap(DefPx(7)).Px(DefPx(9)).CursorPointer().
				Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).Tooltip("Search Project (Ctrl+P)").
				OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { cfg.OnSearch(w) }).Child(ToolbarIcon("search", t.Dim)).
				Child(elements.NewText("Search Project").WithTextColor(t.Dim)).Child(elements.NewDiv().TextSize(AbsPx(11)).Child(elements.NewText("Ctrl+P").WithTextColor(t.Overlay))))
		}
	}
	if cfg.OnSettings != nil {
		bar = bar.Child(tool("settings", "Settings (Ctrl+,)", cfg.OnSettings))
	}
	if cfg.FPS != "" {
		bar = bar.Child(elements.NewDiv().Flex().ItemsCenter().Px(DefPx(12)).
			Child(elements.NewText(cfg.FPS).WithTextColor(t.Teal)))
	}
	return bar
}

// ---------------------------------------------------------------------------
// Popup (dropdown / context menu)
// ---------------------------------------------------------------------------

// PopupConfig configures RenderPopupLayer.
type PopupConfig struct {
	State *PopupState
	Theme Theme
	// WindowSize is the current window content size (for clamping).
	WindowSize geometry.Size[geometry.Pixels]
	// Focus is the popup's keyboard focus handle.
	Focus gpui.FocusHandle
	// Measure returns the rendered pixel width of a string in the menu
	// font (the workspace passes a text-system measurer). With it, the
	// panel sizes itself to the widest item so labels and shortcuts never
	// overflow; without it the fixed MenuWidth is used.
	Measure func(string) float32
	// Update applies a PopupState mutation.
	Update func(f func(*PopupState))
	// OnClose closes the popup (click outside, Escape, item activation).
	OnClose func()
	// RestoreFocus is called after closing so focus returns to the editor.
	RestoreFocus func(w *gpui.Window)
}

// RenderPopupLayer renders the [dismiss layer, popup panel] overlay stack.
// The dismiss layer covers the window and closes the popup on any press;
// the panel is stacked above it (hit testing is reverse-insertion, so it
// wins) and swallows clicks on its own background.
func RenderPopupLayer(cfg PopupConfig) []gpui.Element {
	t := cfg.Theme
	st := cfg.State
	winW := float32(cfg.WindowSize.Width.AsF32())
	winH := float32(cfg.WindowSize.Height.AsF32())

	// Panel width: at least MenuWidth, else wide enough for the widest
	// label + shortcut row, capped by the window (upstream menus size to
	// content; long entries ellipsize rather than overflow). Labels that
	// still cannot fit after the clamp truncate with an ellipsis so a
	// row never spills outside the panel.
	rowChrome := float32(MenuItemPadX-4)*2 + 8 + 2 // row Px + Mx + rounding slack
	checkChrome := float32(13 + 6)
	shortcutGap := float32(12)
	measure := cfg.Measure
	labels := make([]string, len(st.Items))
	for i, item := range st.Items {
		labels[i] = item.Label
	}
	itemNeed := func(item MenuItem, label string) float32 {
		need := rowChrome + float32(len(label))
		if measure != nil {
			need = rowChrome + measure(label)
		}
		if item.HasCheck {
			need += checkChrome
		}
		if item.Shortcut != "" {
			if measure != nil {
				need += shortcutGap + measure(item.Shortcut)
			} else {
				need += shortcutGap + float32(len(item.Shortcut))*6
			}
		}
		return need
	}
	panelW := float32(MenuWidth)
	if measure != nil {
		for _, item := range st.Items {
			if item.Separator {
				continue
			}
			if need := itemNeed(item, item.Label); need > panelW {
				panelW = need
			}
		}
	}
	if panelW > winW-2*float32(ContextPad) {
		panelW = winW - 2*float32(ContextPad)
	}
	if measure != nil {
		for i, item := range st.Items {
			if item.Separator {
				continue
			}
			label := labels[i]
			for label != "" && itemNeed(item, label) > panelW {
				runes := []rune(label)
				if len(runes) <= 4 {
					label = "…"
					break
				}
				label = string(runes[:len(runes)-2]) + "…"
			}
			labels[i] = label
		}
	}
	st.Width = panelW
	x, y := st.ClampedOrigin(winW, winH)

	// Layer 1: click-anywhere dismiss.
	dismiss := elements.NewDiv().
		Absolute().Left(Px(0)).Top(Px(0)).
		W(Px(winW)).H(Px(winH)).
		Occlude().
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			cfg.OnClose()
			if cfg.RestoreFocus != nil {
				cfg.RestoreFocus(w)
			}
			w.Refresh()
		})

	// Layer 2: the panel.
	panel := elements.NewDiv().
		Absolute().Left(Px(x)).Top(Px(y)).
		W(Px(panelW)).
		MaxH(Px(winH - 2*float32(ContextPad))).
		Flex().FlexCol().
		OverflowHidden(). // a menu taller than the window clips instead of spilling rows past the panel
		BgColor(t.Crust).
		Border_1().BorderColor(t.Surface1).
		Rounded(AbsPx(6)).
		Py(DefPx(MenuPadY)).
		Occlude().
		TrackFocus(cfg.Focus).
		OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
			key := ev.Keystroke.Key
			switch key {
			case "escape":
				cfg.OnClose()
				if cfg.RestoreFocus != nil {
					cfg.RestoreFocus(w)
				}
				w.Refresh()
			case "down", "up":
				from := st.Hover
				next := st.NextEnabled(from, key == "down")
				if next >= 0 {
					cfg.Update(func(s *PopupState) { s.Hover = next })
					w.Refresh()
				}
			case "home":
				if next := st.NextEnabled(-1, true); next >= 0 {
					cfg.Update(func(s *PopupState) { s.Hover = next })
					w.Refresh()
				}
			case "end":
				if next := st.NextEnabled(len(st.Items), false); next >= 0 {
					cfg.Update(func(s *PopupState) { s.Hover = next })
					w.Refresh()
				}
			case "enter":
				if st.Hover >= 0 && st.Hover < len(st.Items) {
					it := st.Items[st.Hover]
					if !it.Separator && !it.Disabled && it.Action != nil {
						cfg.OnClose()
						if cfg.RestoreFocus != nil {
							cfg.RestoreFocus(w)
						}
						it.Action()
						w.Refresh()
					}
				}
			}
		}).
		OnMouseDown(func(ev platform.MouseDownEvent, w *gpui.Window) {
			w.StopPropagation() // clicks on the panel itself do not dismiss
		})

	for i, item := range st.Items {
		idx := i
		if item.Separator {
			panel = panel.Child(elements.NewDiv().H(Px(9)).Flex().ItemsCenter().Mx(Px(8)).
				Child(elements.NewDiv().FlexGrow(1).H(Px(1)).BgColor(t.Surface1)))
			continue
		}
		labelColor := t.Text
		if item.Disabled {
			labelColor = t.Overlay
		}
		hovered := st.Hover == idx
		bg := gc.TransparentBlack()
		if hovered && !item.Disabled {
			bg = t.Surface1
		}
		row := elements.NewDiv().
			ID(elements.ButtonID(fmt.Sprintf("menu-item-%d", idx))).
			Flex().ItemsCenter().H(Px(MenuItemHeight)).Mx(Px(4)).Rounded(AbsPx(4)).
			Px(DefPx(MenuItemPadX - 4)).
			WhitespaceNowrap().
			BgColor(bg).
			CursorPointer().
			OnClick(func(ev gpui.ClickEvent, w *gpui.Window) {
				if item.Disabled || item.Action == nil {
					return
				}
				cfg.OnClose()
				if cfg.RestoreFocus != nil {
					cfg.RestoreFocus(w)
				}
				item.Action()
				w.Refresh()
			}).
			OnMouseMove(func(ev platform.MouseMoveEvent, w *gpui.Window) {
				if st.Hover != idx && !ev.Dragging() {
					cfg.Update(func(s *PopupState) { s.Hover = idx })
					w.Refresh()
				}
			})
		// Checkmark column.
		if item.HasCheck {
			if item.Checked {
				row = row.Child(CheckIcon(t.Accent))
			} else {
				row = row.Child(elements.NewDiv().W(Px(13)).H(Px(13)))
			}
			row = row.Child(elements.NewDiv().W(Px(6)))
		}
		row = row.Child(elements.NewText(labels[idx]).WithTextColor(labelColor))
		row = row.Child(elements.NewDiv().FlexGrow(1))
		if item.Shortcut != "" {
			row = row.Child(elements.NewText(item.Shortcut).WithTextColor(t.Dim))
		}
		panel = panel.Child(row)
	}
	return []gpui.Element{dismiss, panel}
}
