package workspace

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"
)

type diffLine struct {
	n    int
	text string
}
type diffRow struct {
	left, right diffLine
	changed     bool
}
type diffResult struct {
	rows []diffRow
	note string
}

type diffTab struct {
	root   string
	entry  toolEntry
	result diffResult
	cancel context.CancelFunc
	top    float32 // first visible row, with fractional wheel input accumulated
}

func (d *diffTab) setResult(result diffResult) {
	d.result = result
	for i, row := range result.rows {
		if row.changed {
			d.top = float32(max(0, i-2))
			break
		}
	}
}

const diffLimit = 512 << 10

func readWorktree(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		return []byte(target), err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > diffLimit {
		return nil, fmt.Errorf("file is too large for the diff viewer")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, diffLimit+1))
	if len(b) > diffLimit {
		return nil, fmt.Errorf("file is too large for the diff viewer")
	}
	return b, err
}

func loadDiff(ctx context.Context, root string, e toolEntry) diffResult {
	if e.conflict {
		return diffResult{note: "This file has an unresolved merge conflict. Open it in the editor to resolve it."}
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return diffResult{note: err.Error()}
	}
	object := func(rev string) ([]byte, error) {
		buf := &boundedGitOutput{limit: diffLimit}
		cmd := gitCommand(ctx, git, root, true, "cat-file", "blob", rev+":"+filepath.ToSlash(e.label))
		cmd.Stdout = buf
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("cannot read %s version: %w", rev, err)
		}
		if buf.truncated {
			return nil, fmt.Errorf("file is too large for the diff viewer")
		}
		return buf.bytes, nil
	}
	var left, right []byte
	if e.staged {
		if e.badge != "A" {
			left, err = object("HEAD")
		}
		if err == nil && !e.deleted {
			right, err = object("")
		}
	} else {
		if e.badge != "?" {
			left, err = object("")
		}
		if err == nil && !e.deleted {
			right, err = readWorktree(e.where.path)
		}
	}
	if err != nil {
		return diffResult{note: err.Error()}
	}
	for _, b := range [][]byte{left, right} {
		if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
			return diffResult{note: "Binary file · a text diff is not available"}
		}
	}
	if bytes.Equal(left, right) {
		return diffResult{note: "No text differences"}
	}
	a, b := diffLines(left), diffLines(right)
	if len(a) > 12000 || len(b) > 12000 {
		return diffResult{note: "Too many lines for the diff viewer"}
	}
	return diffResult{rows: alignDiff(a, b)}
}

func diffLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.HasSuffix(text, "\n") {
		return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	return append(strings.Split(text, "\n"), "⟪ no newline at end of file ⟫")
}

// Align equal lines and pair adjacent deletes/inserts as replacements. Trim
// the shared ends first and cap the LCS table so unrelated files cannot make
// a diff consume unbounded memory or CPU.
func alignDiff(a, b []string) []diffRow {
	var rows []diffRow
	match := func(i, j int) {
		rows = append(rows, diffRow{left: diffLine{i + 1, a[i]}, right: diffLine{j + 1, b[j]}})
	}
	replace := func(i, j, endA, endB int) {
		for i < endA || j < endB {
			row := diffRow{changed: true}
			if i < endA {
				row.left = diffLine{i + 1, a[i]}
				i++
			}
			if j < endB {
				row.right = diffLine{j + 1, b[j]}
				j++
			}
			rows = append(rows, row)
		}
	}
	prefix, endA, endB := 0, len(a), len(b)
	for prefix < endA && prefix < endB && a[prefix] == b[prefix] {
		match(prefix, prefix)
		prefix++
	}
	for endA > prefix && endB > prefix && a[endA-1] == b[endB-1] {
		endA--
		endB--
	}
	n, m := endA-prefix, endB-prefix
	if n == 0 || m == 0 || n*m > 2_000_000 {
		replace(prefix, prefix, endA, endB)
	} else {
		cols := m + 1
		lcs := make([]uint16, (n+1)*cols)
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				k := i*cols + j
				if a[prefix+i] == b[prefix+j] {
					lcs[k] = 1 + lcs[k+cols+1]
				} else {
					lcs[k] = max(lcs[k+1], lcs[k+cols])
				}
			}
		}
		i, j, startI, startJ := 0, 0, 0, 0
		for i < n && j < m {
			if a[prefix+i] == b[prefix+j] {
				replace(prefix+startI, prefix+startJ, prefix+i, prefix+j)
				match(prefix+i, prefix+j)
				i++
				j++
				startI, startJ = i, j
			} else if lcs[(i+1)*cols+j] >= lcs[i*cols+j+1] {
				i++
			} else {
				j++
			}
		}
		replace(prefix+startI, prefix+startJ, endA, endB)
	}
	for i, j := endA, endB; i < len(a) && j < len(b); i, j = i+1, j+1 {
		match(i, j)
	}
	return rows
}

func (ws *Workspace) openGitDiff(root string, e toolEntry, update updateFn, w *gpui.Window) {
	var buf *editor.Buffer
	for i, b := range ws.Buffers {
		if d := ws.diffs[b]; d != nil && d.root == root && d.entry.label == e.label && d.entry.staged == e.staged {
			d.cancel()
			ws.Active, buf = i, b
			break
		}
	}
	if buf == nil {
		prefix := "Diff: "
		if e.staged {
			prefix = "Staged: "
		}
		buf = editor.NewBuffer("", prefix+filepath.Base(e.label), "")
		buf.Kind = editor.KindDiff
		ws.addBuffer(buf)
	}
	if ws.diffs == nil {
		ws.diffs = make(map[*editor.Buffer]*diffTab)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	tab := &diffTab{root: root, entry: e, result: diffResult{note: "Loading diff…"}, cancel: cancel}
	ws.diffs[buf] = tab
	if dispatch := ws.dispatch; dispatch != nil {
		go func() {
			result := loadDiff(ctx, root, e)
			cancel()
			defer func() { _ = recover() }()
			dispatch(func() {
				defer func() { _ = recover() }()
				update(func(s *Workspace) {
					if s.diffs[buf] == tab {
						tab.setResult(result)
					}
				})
				w.Refresh()
			})
		}()
	} else {
		tab.setResult(loadDiff(ctx, root, e))
		cancel()
	}
}

func (ws *Workspace) closeDiff(b *editor.Buffer) {
	if d := ws.diffs[b]; d != nil {
		d.cancel()
		delete(ws.diffs, b)
	}
}

func renderGitDiff(ws *Workspace, b *editor.Buffer, t ui.Theme, update updateFn) gpui.Element {
	d := ws.diffs[b]
	if d == nil {
		return elements.NewDiv()
	}
	left, right := "Index", "Working Tree"
	if d.entry.staged {
		left, right = "HEAD", "Index"
	}
	const headerHeight = 32
	rowHeight := max(float32(1), ws.LineHeight)
	// The workspace knows the viewport from its fixed-height chrome. Lay
	// out only the rows that can appear in it, without nesting a measured
	// list inside this flex pane (which can lose its measured row height).
	page := max(1, int((ws.paneHeight()-headerHeight)/rowHeight))
	maxTop := float32(max(0, len(d.result.rows)-page))
	d.top = min(max(d.top, 0), maxTop)
	scroll := func(top float32, w *gpui.Window) {
		d.top = min(max(top, 0), maxTop)
		w.StopPropagation()
		w.Refresh()
	}
	leftColor, rightColor := t.Red.Alpha(.16), t.Green.Alpha(.16)
	cell := func(label string) *elements.Div {
		return elements.NewDiv().Flex().Flex1().MinW_0().ItemsCenter().Px(ui.DefPx(8)).WhitespaceNowrap().OverflowHidden().Child(elements.NewText(label).WithTextColor(t.Text))
	}
	bar := elements.NewDiv().Flex().ItemsCenter().FlexShrink(0).H(ui.Px(headerHeight)).BgColor(t.Surface).BorderB_1().BorderColor(t.Surface1).
		Child(cell(left + " · " + d.entry.label).BorderR_1().BorderColor(t.Surface1)).Child(cell(right + " · " + d.entry.label))
	rowSide := func(line diffLine, changed bool, color gc.Hsla) *elements.Div {
		col := elements.NewDiv().Flex().Flex1().MinW_0().ItemsCenter().WhitespaceNowrap().OverflowHidden().Gap(ui.DefPx(8)).Px(ui.DefPx(7))
		if changed {
			if line.n == 0 {
				col.BgColor(t.Surface)
			} else {
				col.BgColor(color)
			}
		}
		if line.n == 0 {
			return col
		}
		str := editor.DisplayLine(line.text)
		if r := []rune(str); len(r) > 600 {
			str = string(r[:600]) + "…"
		}
		return col.Child(elements.NewDiv().W(ui.Px(34)).FlexShrink(0).Child(ui.MonoText(fmt.Sprint(line.n), t.Dim))).Child(ui.MonoText(str, t.Text))
	}
	pane := elements.NewDiv().Flex().FlexCol().Flex1().MinW_0().MinH_0().TrackFocus(ws.FocusEditor).Child(bar).
		OnKeyDown(func(ev platform.KeyDownEvent, w *gpui.Window) {
			if ui.SecondaryMod(ev.Keystroke.Modifiers) && ev.Keystroke.Key == "w" {
				update(func(s *Workspace) { s.CloseEditor() })
				w.Refresh()
				return
			}
			switch ev.Keystroke.Key {
			case "up":
				scroll(d.top-1, w)
			case "down":
				scroll(d.top+1, w)
			case "pageup":
				scroll(d.top-float32(page), w)
			case "pagedown":
				scroll(d.top+float32(page), w)
			case "home":
				scroll(0, w)
			case "end":
				scroll(maxTop, w)
			}
		})
	if d.result.note != "" {
		return pane.Child(elements.NewDiv().P(ui.DefPx(12)).Child(elements.NewText(d.result.note).WithTextColor(t.Dim)))
	}
	content := elements.NewDiv().ID(elements.ButtonID("git-diff-content-" + d.entry.label)).Flex().FlexCol().Flex1().MinH_0().OverflowHidden().
		OnScroll(func(ev platform.ScrollEvent, w *gpui.Window) {
			delta := ev.Delta.PixelDelta(geometry.Px(rowHeight)).Y.AsF32()
			if delta != 0 {
				scroll(d.top-delta/rowHeight, w)
			}
		})
	first := int(d.top)
	for _, r := range d.result.rows[first:min(len(d.result.rows), first+page+1)] {
		row := elements.NewDiv().Flex().FlexShrink(0).H(ui.Px(rowHeight))
		if size := ws.Cfg.Editor.FontSize; size > 0 {
			row.TextSize(ui.AbsPx(float32(size)))
		}
		content.Child(row.Child(rowSide(r.left, r.changed, leftColor).BorderR_1().BorderColor(t.Surface1)).Child(rowSide(r.right, r.changed, rightColor)))
	}
	return pane.Child(content)
}
