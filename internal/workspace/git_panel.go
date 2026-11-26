package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/FlameInTheDark/flint/internal/ui"
	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/style"
)

func renderGitPanel(ws *Workspace, t ui.Theme, update updateFn) *elements.Div {
	var result toolResult
	if load := ws.tools.git; load != nil {
		result = load.result
	}
	busy := ws.tools.gitOp != nil
	if !ws.tools.gitFocus.IsValid() {
		ws.tools.gitFocus = gpui.NewFocusHandle()
	}
	label := func(value string, c gc.Hsla) *elements.Text { return elements.NewText(value).WithTextColor(c) }
	button := func(id, text, tooltip string, run func(*gpui.Window)) *elements.Div {
		el := elements.NewDiv().ID(elements.ButtonID("git-" + id)).H(ui.Px(25)).Flex().ItemsCenter().FlexShrink(0).
			Px(ui.DefPx(5)).Rounded(ui.AbsPx(4)).BgColor(t.Surface1).TooltipBuilder(ui.Tooltip(tooltip))
		if run == nil {
			return el.Child(label(text, t.Dim))
		}
		return el.CursorPointer().Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
			OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { run(w); w.Refresh() }).Child(label(text, t.Text))
	}
	// iconButton is the row-action twin of button: a bare Lucide icon (plus
	// to stage, minus to unstage — like the staged-diff +/- marks) that
	// highlights on hover instead of sitting in a filled chip.
	iconButton := func(id, icon, tooltip string, run func(*gpui.Window)) *elements.Div {
		el := elements.NewDiv().ID(elements.ButtonID("git-" + id)).Flex().ItemsCenter().JustifyCenter().W(ui.Px(22)).H(ui.Px(25)).FlexShrink(0).
			Rounded(ui.AbsPx(4)).TooltipBuilder(ui.Tooltip(tooltip))
		if run == nil {
			return el.Child(ui.Icon(icon, t.Overlay, 14))
		}
		return el.CursorPointer().Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface2) }).
			OnClick(func(_ gpui.ClickEvent, w *gpui.Window) { run(w); w.Refresh() }).Child(ui.Icon(icon, t.Text, 14))
	}
	refresh := func(w *gpui.Window) {
		update(func(s *Workspace) { s.tools.gitMessage = ""; s.startToolLoad("git", update, w) })
	}
	tab := func(id, text string, history bool) *elements.Div {
		el := button(id, text, text, func(w *gpui.Window) {
			if ws.tools.gitHistory == history || busy {
				return
			}
			update(func(s *Workspace) {
				s.tools.gitHistory = history
				s.tools.leftScroll = elements.NewScrollHandle()
				s.startToolLoad("git", update, w)
			})
		}).Flex1().JustifyCenter().Rounded(ui.AbsPx(0)).H(ui.Px(32)).BgColor(t.Surface)
		if ws.tools.gitHistory == history {
			el.BgColor(t.EditorBg).BorderB_1().BorderColor(t.Accent)
		}
		return el
	}
	changesTitle := "Changes"
	if !ws.tools.gitHistory && len(result.entries) > 0 {
		changesTitle += fmt.Sprintf(" (%d)", len(result.entries))
	}
	header := elements.NewDiv().Flex().ItemsCenter().FlexShrink(0).BgColor(t.Surface).BorderB_1().BorderColor(t.Surface2).
		Child(tab("changes", changesTitle, false)).Child(tab("history", "History", true)).
		Child(button("refresh", "↻", "Refresh Git", refresh))
	list := debugList("git-list", ws.tools.leftScroll, nil, &ws.tools.leftTop)
	panel := elements.NewDiv().Flex().FlexCol().MinH_0().BgColor(t.EditorBg).TextSize(ui.AbsPx(12)).Child(header)
	if busy {
		list.Child(debugRow("Running Git…", t.Dim))
	}
	if msg := ws.tools.gitMessage; msg != "" {
		col := t.Dim
		if ws.tools.gitMessageError {
			col = t.Red
		}
		summary, _, _ := strings.Cut(msg, "\n")
		list.Child(debugRow(summary, col).ID(elements.ButtonID("git-message")).TooltipBuilder(ui.Tooltip(strings.Join(strings.Fields(msg), " "))))
	}
	if result.repo == "" {
		return panel.Child(list.Child(debugRow(result.info, t.Dim)))
	}
	run := func(action, value string, w *gpui.Window) {
		update(func(s *Workspace) {
			if err := s.startGitChange(result.repo, action, value, update, w); err != nil {
				s.tools.gitMessage, s.tools.gitMessageError = err.Error(), true
			}
		})
		w.Refresh()
	}
	branch := elements.NewDiv().Flex().ItemsCenter().FlexShrink(0).H(ui.Px(31)).Gap(ui.DefPx(4)).Px(ui.DefPx(6)).
		BgColor(t.Surface).BorderT_1().BorderColor(t.Surface2).
		Child(elements.NewDiv().Flex().ItemsCenter().MinW_0().Flex1().OverflowHidden().WhitespaceNowrap().TooltipBuilder(ui.Tooltip(result.info)).
			// The branch glyph rides a Lucide icon: the Unicode fork (⑂)
			// comes from a fallback font whose line metrics clip the row.
			Child(ui.Icon("git-branch", t.Dim, 13)).Child(label(result.branch, t.Dim)))
	if !busy {
		branch.Child(button("fetch", "Fetch", "Fetch remote changes without modifying files", func(w *gpui.Window) { run("fetch", "", w) })).
			Child(button("push", "Push", "Push the current branch", func(w *gpui.Window) { run("push", "", w) }))
	}
	if ws.tools.gitHistory {
		if len(result.history) == 0 {
			list.Child(debugRow("No commits to display", t.Dim))
		}
		for _, commit := range result.history {
			list.Child(elements.NewDiv().Flex().FlexCol().FlexShrink(0).MinW_0().OverflowHidden().WhitespaceNowrap().
				Py(ui.DefPx(5)).BorderB_1().BorderColor(t.Surface1).TooltipBuilder(ui.Tooltip(commit.subject)).
				Child(label(commit.subject, t.Text)).Child(label(commit.hash+"  ·  "+commit.date, t.Dim)))
		}
		return panel.Child(list).Child(branch)
	}
	var groups [3][]int
	var total gitLineStat
	for i, e := range result.entries {
		g := 1
		if e.staged {
			g = 0
		} else if e.badge == "?" {
			g = 2
		}
		groups[g] = append(groups[g], i)
		total.added += e.stat.added
		total.removed += e.stat.removed
	}
	stats := func(s gitLineStat) gpui.Element {
		nums := elements.NewDiv().Flex().Gap(ui.DefPx(4)).FlexShrink(0).WhitespaceNowrap()
		if s.added > 0 {
			nums.Child(ui.MonoText(fmt.Sprintf("+%d", s.added), t.Green))
		}
		if s.removed > 0 {
			nums.Child(ui.MonoText(fmt.Sprintf("−%d", s.removed), t.Red))
		}
		return nums
	}
	toolbar := elements.NewDiv().Flex().ItemsCenter().Gap(ui.DefPx(6)).FlexShrink(0).H(ui.Px(33)).Px(ui.DefPx(6)).
		Child(elements.NewDiv().Flex1().MinW_0().OverflowHidden().WhitespaceNowrap().Child(label("Uncommitted", t.Dim))).Child(stats(total))
	if !busy && len(groups[1])+len(groups[2]) > 0 {
		toolbar.Child(button("stage-all", "Stage All", "Stage all tracked and untracked changes in the repository", func(w *gpui.Window) { run("stage-all", "", w) }))
	}
	panel.Child(toolbar)
	if len(result.entries) == 0 {
		list.Child(debugRow("Working tree clean", t.Dim))
	}
	active := ws.diffs[ws.Buf()]
	for group, title := range []string{"Staged", "Tracked", "Untracked"} {
		if len(groups[group]) == 0 {
			continue
		}
		arrow := "⌄"
		if ws.tools.gitCollapsed[group] {
			arrow = "›"
		}
		list.Child(elements.NewDiv().Flex().ItemsCenter().H(ui.Px(26)).FlexShrink(0).CursorPointer().
			Child(elements.NewDiv().Flex1().Child(label(arrow+"  "+title, t.Dim))).Child(ui.MonoText(fmt.Sprint(len(groups[group])), t.Dim)).
			OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
				update(func(s *Workspace) { s.tools.gitCollapsed[group] = !s.tools.gitCollapsed[group] })
				w.Refresh()
			}))
		if ws.tools.gitCollapsed[group] {
			continue
		}
		for _, i := range groups[group] {
			e := result.entries[i]
			id := fmt.Sprint(i)
			row := elements.NewDiv().Flex().ItemsCenter().H(ui.Px(27)).FlexShrink(0).Gap(ui.DefPx(3))
			if active != nil && active.root == result.repo && active.entry.label == e.label && active.entry.staged == e.staged {
				row.BgColor(t.Accent.Alpha(.15))
			}
			color := t.Yellow
			if e.badge == "?" || e.badge == "A" {
				color = t.Green
			} else if e.deleted || e.conflict {
				color = t.Red
			}
			path := elements.NewDiv().ID(elements.ButtonID("git-diff-" + id)).Flex1().MinW_0().H(ui.Px(27)).Flex().ItemsCenter().Gap(ui.DefPx(5)).
				WhitespaceNowrap().OverflowHidden().CursorPointer().TooltipBuilder(ui.Tooltip(e.label + " · Open diff")).
				Hover(func(s *style.StyleRefinement) { s.BgColor(t.Surface1) }).
				Child(ui.MonoText(e.badge, color)).Child(label(filepath.Base(e.label), t.Text)).
				OnClick(func(_ gpui.ClickEvent, w *gpui.Window) {
					update(func(s *Workspace) { s.openGitDiff(result.repo, e, update, w) })
					w.Focus(ws.FocusEditor)
					w.Refresh()
				})
			if dir := filepath.Dir(e.label); dir != "." {
				path.Child(label(dir, t.Dim))
			}
			row.Child(path).Child(stats(e.stat))
			if !busy {
				action, icon, tip := "stage", "plus", "Stage file"
				if e.staged {
					action, icon, tip = "unstage", "minus", "Unstage file"
				}
				row.Child(iconButton(action+"-"+id, icon, tip, func(w *gpui.Window) { run(action, e.label, w) }))
			}
			list.Child(row)
		}
	}
	var disabledReason string
	switch {
	case busy:
		disabledReason = "Wait for the current Git action to finish"
	case len(groups[0]) == 0:
		disabledReason = "Stage at least one file before committing"
	case strings.TrimSpace(ws.tools.gitDraft.String()) == "":
		disabledReason = "Enter a commit message"
	}
	commit := func(action string, w *gpui.Window) {
		if disabledReason == "" {
			run(action, ws.tools.gitDraft.String(), w)
		}
	}
	commitButton := func(id, text, tip, action string) *elements.Div {
		if disabledReason != "" {
			return button(id, text, disabledReason, nil)
		}
		return button(id, text, tip, func(w *gpui.Window) { commit(action, w) })
	}
	footer := elements.NewDiv().Flex().FlexCol().FlexShrink(0).Gap(ui.DefPx(5)).P(ui.DefPx(6)).BgColor(t.Surface).
		Child(renderGitMessage(ws, t, update, func(w *gpui.Window) { commit("commit", w) })).
		Child(elements.NewDiv().Flex().ItemsCenter().JustifyEnd().Gap(ui.DefPx(4)).
			Child(commitButton("commit", "Commit", "Commit staged changes (Ctrl+Enter)", "commit")).
			Child(commitButton("commit-push", "Commit & Push", "Commit staged changes, then push the current branch", "commit-push")))
	return panel.Child(list).Child(branch).Child(footer)
}

// draftRow returns rune offsets of the row containing the draft cursor.
func draftRow(s *ui.InputState) (start, end int) {
	start, end = s.Cursor, s.Cursor
	for start > 0 && s.Value[start-1] != '\n' {
		start--
	}
	for end < len(s.Value) && s.Value[end] != '\n' {
		end++
	}
	return
}

func renderGitMessage(ws *Workspace, t ui.Theme, update updateFn, submit func(*gpui.Window)) *elements.Div {
	const visibleRows = 4
	draft := &ws.tools.gitDraft
	start, _ := draftRow(draft)
	line := strings.Count(string(draft.Value[:start]), "\n")
	rows := strings.Split(draft.String(), "\n")
	first := max(0, min(line-visibleRows+1, len(rows)-visibleRows))
	field := elements.NewDiv().ID(elements.ButtonID("git-commit-message")).Flex().FlexCol().FlexShrink(0).MinW_0().
		H(ui.Px(80)).P(ui.DefPx(5)).WhitespaceNowrap().OverflowHidden().BgColor(t.EditorBg).Border_1().BorderColor(t.Surface2).
		TrackFocus(ws.tools.gitFocus).CursorText().
		OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
			key := ev.Keystroke
			if key.Key == "enter" && ui.SecondaryMod(key.Modifiers) {
				submit(w)
				w.StopPropagation()
				return
			}
			consumed := true
			update(func(s *Workspace) {
				d := &s.tools.gitDraft
				shift := key.Modifiers.Shift
				switch {
				case key.Key == "enter":
					d.Insert("\n")
				case (key.Key == "home" || key.Key == "end") && !ui.SecondaryMod(key.Modifiers) && !key.Modifiers.Alt:
					lo, hi := draftRow(d)
					if key.Key == "home" {
						d.Move(lo, shift)
					} else {
						d.Move(hi, shift)
					}
				case key.Key == "up" || key.Key == "down":
					lo, hi := draftRow(d)
					col := d.Cursor - lo
					start := lo
					if key.Key == "up" && lo > 0 {
						start = lo - 1
						for start > 0 && d.Value[start-1] != '\n' {
							start--
						}
					} else if key.Key == "down" && hi < len(d.Value) {
						start = hi + 1
					} else {
						if key.Key == "up" {
							d.Move(0, shift)
						} else {
							d.Move(len(d.Value), shift)
						}
						return
					}
					end := start
					for end < len(d.Value) && d.Value[end] != '\n' {
						end++
					}
					d.Move(min(start+col, end), shift)
				default:
					consumed = d.MultilineKey(ev, w)
				}
			})
			if consumed {
				w.StopPropagation()
				w.Refresh()
			}
		})
	type messageRow struct {
		box    *elements.Div
		text   *ui.InputLine
		offset int
	}
	var painted []messageRow
	offset := utf8.RuneCountInString(strings.Join(rows[:first], "\n"))
	if first > 0 {
		offset++
	}
	lo, hi := draft.Selection()
	for i := first; i < min(len(rows), first+visibleRows); i++ {
		if draft.String() == "" {
			field.Child(elements.NewText("Enter commit message").WithTextColor(t.Dim))
			break
		}
		cursor := -1
		if i == line {
			cursor = draft.Cursor - start
		}
		row := ui.NewInputLine(rows[i], t, cursor, lo-offset, hi-offset)
		box := elements.NewDiv().Flex().H(ui.Px(17)).FlexShrink(0).Child(row)
		field.Child(box)
		painted = append(painted, messageRow{box, row, offset})
		offset += utf8.RuneCountInString(rows[i]) + 1
	}
	ui.BindInputPointer(field, ws.tools.gitFocus, func(f func(*ui.InputState)) {
		update(func(s *Workspace) { f(&s.tools.gitDraft) })
	}, func(p geometry.Point[geometry.Pixels], w *gpui.Window) int {
		for i, row := range painted {
			if i == len(painted)-1 || p.Y < w.ElementBounds(row.box).Bottom() {
				return row.offset + row.text.IndexAt(p.X, w)
			}
		}
		return 0
	})
	return field
}
