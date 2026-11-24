package terminal

import (
	"strings"
	"testing"
)

// The unit suite pins the VT emulator and the input encoder: the renderer
// consumes the emulator's grid, so the cell-level behavior here is what
// the terminal shows.

// rowStrings renders the emu's absolute rows to text (test helper).
// Blank rows (the grid's empty screen area) are dropped.
func rowStrings(e *vtEmu) []string {
	out := make([]string, 0, e.rowTotal())
	for i := 0; i < e.rowTotal(); i++ {
		row, ok := e.absoluteRow(i)
		if !ok {
			continue
		}
		s := string(rowTrim(rowText(row)))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func TestVTPrintAndWrap(t *testing.T) {
	e := newEmu(10, 4, 100)
	e.Feed([]byte("hello"))
	if got := rowStrings(e); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("print: %q", got)
	}
	// Autowrap at the last column: "abcdefghijK" wraps K to row 2.
	e.Feed([]byte("abcdefghijK"))
	got := rowStrings(e)
	if len(got) != 2 || got[0] != "helloabcde" || got[1] != "fghijK" {
		t.Fatalf("autowrap: %q", got)
	}
}

// TestVTScrollback pins that scrolled-off primary rows land in the
// history (the pane's scrollback), capped at maxHistory.
func TestVTScrollback(t *testing.T) {
	e := newEmu(20, 4, 100)
	for i := 0; i < 10; i++ {
		e.Feed([]byte("line" + string(rune('0'+i)) + "\r\n"))
	}
	got := rowStrings(e)
	if len(got) < 10 {
		t.Fatalf("rows: %q", got)
	}
	if !strings.Contains(got[0], "line0") {
		t.Fatalf("first history row: %q", got[0])
	}
	// The cap: overflow trims the front.
	e2 := newEmu(20, 4, 5)
	for i := 0; i < 20; i++ {
		e2.Feed([]byte("x\r\n"))
	}
	if len(e2.history) > 5 {
		t.Fatalf("history cap: %d", len(e2.history))
	}
}

func TestVTSGRColorsAndAttrs(t *testing.T) {
	e := newEmu(20, 4, 100)
	e.Feed([]byte("\x1b[32mgreen\x1b[0m plain \x1b[1;31merr\x1b[0m"))
	row, _ := e.absoluteRow(0)
	// "green" cells carry palette index 2; "err" is bold red.
	if row[0].fg.idx != 2 {
		t.Fatalf("green fg idx = %d", row[0].fg.idx)
	}
	if row[6].fg.idx != 0 {
		t.Fatalf("reset fg idx = %d", row[6].fg.idx)
	}
	last := row[13] // 'e' of "err" (after "green plain ")
	if last.fg.idx != 1 || last.at&attrBold == 0 {
		t.Fatalf("bold red cell: %+v", last)
	}
	// 256-color and truecolor.
	e2 := newEmu(20, 4, 100)
	e2.Feed([]byte("\x1b[38;5;123mX\x1b[0m\x1b[38;2;10;20;30mY"))
	row2, _ := e2.absoluteRow(0)
	if row2[0].fg.idx != 123 {
		t.Fatalf("256-color idx = %d", row2[0].fg.idx)
	}
	if !row2[1].fg.rgb || row2[1].fg.r != 10 || row2[1].fg.g != 20 || row2[1].fg.b != 30 {
		t.Fatalf("truecolor fg = %+v", row2[1].fg)
	}
}

func TestVTCursorMovementAndErase(t *testing.T) {
	e := newEmu(10, 4, 100)
	e.Feed([]byte("abcdef\x1b[2GXY"))
	row, _ := e.absoluteRow(0)
	// CHA to column 2: "XY" overwrites "bc".
	if got := string([]rune{row[1].r, row[2].r}); got != "XY" {
		t.Fatalf("CHA overwrite: %q", got)
	}
	// EL from cursor: erase to end of line.
	e.Feed([]byte("\x1b[1;1H\x1b[K"))
	row, _ = e.absoluteRow(0)
	blank := true
	for _, c := range row {
		if c.r != 0 {
			blank = false
		}
	}
	if !blank {
		t.Fatal("EL 0 did not clear the row")
	}
}

// TestVTAltScreen pins the 1049 alternate screen: entering clears the alt
// grid and moves the cursor home; leaving restores the primary contents
// and cursor.
func TestVTAltScreen(t *testing.T) {
	e := newEmu(10, 4, 100)
	e.Feed([]byte("primary\x1b[2;3H")) // cursor parked mid-screen
	e.Feed([]byte("\x1b[?1049h"))
	if !e.altActive {
		t.Fatal("1049h did not switch to the alt screen")
	}
	if strings.Contains(strings.Join(rowStrings(e), "|"), "primary") {
		t.Fatalf("alt screen shows primary content: %q", rowStrings(e))
	}
	e.Feed([]byte("altstuff"))
	e.Feed([]byte("\x1b[?1049l"))
	if e.altActive {
		t.Fatal("1049l did not restore the primary screen")
	}
	got := rowStrings(e)
	if len(got) == 0 || !strings.Contains(got[0], "primary") {
		t.Fatalf("primary content lost: %q", got)
	}
	if e.cur.curX != 2 || e.cur.curY != 1 {
		t.Fatalf("primary cursor not restored: %d,%d", e.cur.curX, e.cur.curY)
	}
	// The alt screen never pushed history.
	if len(e.history) != 0 {
		t.Fatalf("alt screen leaked history: %d", len(e.history))
	}
}

func TestVTWideChars(t *testing.T) {
	e := newEmu(10, 4, 100)
	e.Feed([]byte("汉字x"))
	row, _ := e.absoluteRow(0)
	// 汉 at col 0 with its tail at col 1, 字 at col 2 with its tail at
	// col 3, then the plain ascii 'x'.
	if row[0].tail || !row[1].tail || row[2].tail || !row[3].tail {
		t.Fatalf("wide char layout: tail flags %v %v %v %v", row[0].tail, row[1].tail, row[2].tail, row[3].tail)
	}
	if row[0].r != '汉' || row[2].r != '字' {
		t.Fatalf("wide chars: %q %q", row[0].r, row[2].r)
	}
	if row[4].r != 'x' {
		t.Fatalf("ascii after wide: %q", row[4].r)
	}
}

func TestVTBracketedPasteAndQueries(t *testing.T) {
	e := newEmu(20, 4, 100)
	e.Feed([]byte("\x1b[?2004h"))
	if !e.bracketedPaste {
		t.Fatal("2004h did not enable bracketed paste")
	}
	e.Feed([]byte("\x1b[6n"))
	rep := e.takeReplies()
	if !strings.HasPrefix(string(rep), "\x1b[") || !strings.HasSuffix(string(rep), "R") {
		t.Fatalf("DSR reply: %q", rep)
	}
	e.Feed([]byte("\x1b[c"))
	if rep = e.takeReplies(); !strings.HasPrefix(string(rep), "\x1b[?") {
		t.Fatalf("DA reply: %q", rep)
	}
}

// TestEncodeKey pins the keystroke encoding contract: Ctrl+C passes
// through as 0x03 (SIGINT via the line discipline — never a kill), Enter
// is CR, arrows are CSI (SS3 in application cursor mode), and printable
// chars are UTF-8 bytes.
func TestEncodeKey(t *testing.T) {
	tr := New("")
	cases := []struct {
		key, ch string
		ctrl    bool
		want    string
	}{
		{"c", "", true, "\x03"},
		{"enter", "", false, "\r"},
		{"backspace", "", false, "\x7f"},
		{"tab", "", false, "\t"},
		{"escape", "", false, "\x1b"},
		{"up", "", false, "\x1b[A"},
		{"down", "", false, "\x1b[B"},
		{"right", "", false, "\x1b[C"},
		{"left", "", false, "\x1b[D"},
		{"home", "", false, "\x1b[H"},
		{"end", "", false, "\x1b[F"},
		{"pageup", "", false, "\x1b[5~"},
		{"delete", "", false, "\x1b[3~"},
		{"a", "a", false, "a"},
		{"space", " ", false, " "},
		{"l", "", true, "\x0c"},
	}
	for _, c := range cases {
		got := tr.EncodeKey(c.key, c.ch, c.ctrl, false, false)
		if string(got) != c.want {
			t.Fatalf("EncodeKey(%q ctrl=%v) = %q want %q", c.key, c.ctrl, got, c.want)
		}
	}
	// Shift+Tab is the backtab CSI.
	if got := tr.EncodeKey("tab", "", false, false, true); string(got) != "\x1b[Z" {
		t.Fatalf("shift+tab = %q", got)
	}
	// Alt prefixes ESC.
	if got := tr.EncodeKey("a", "a", false, true, false); string(got) != "\x1ba" {
		t.Fatalf("alt+a = %q", got)
	}
	// Application cursor keys (DECCKM): arrows go SS3.
	tr.Inject("\x1b[?1h")
	if got := tr.EncodeKey("up", "", false, false, false); string(got) != "\x1bOA" {
		t.Fatalf("app-cursor up = %q", got)
	}
}

// TestScrollClamps pins the follow/clamp semantics on grid rows.
func TestScrollClamps(t *testing.T) {
	tr := New("")
	tr.AutoStart = false
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("line\r\n")
	}
	tr.Inject(sb.String())
	total := tr.RowTotal()
	first := tr.ScrollLines(5, total, 4)
	if first != 5 {
		t.Fatalf("scroll after wheel down: %d want 5", first)
	}
	first = tr.ScrollLines(1000, total, 4)
	if first != total-4 {
		t.Fatalf("scroll clamp: %d want %d", first, total-4)
	}
	first = tr.ScrollLines(-3, total, 4)
	if first != total-7 {
		t.Fatalf("scroll up: %d want %d", first, total-7)
	}
	if tr.Snapshot().Follow {
		t.Fatal("follow stayed on after scrolling up")
	}
	tr.SetFollow(true)
	if s := tr.Snapshot(); !s.Follow {
		t.Fatal("SetFollow did not pin the bottom")
	}
}

// TestSelection pins the grid selection: drag spans, word select, line
// select and the copied text. The grid is 24 rows tall, so the two text
// rows are absolute rows 0 and 1.
func TestSelection(t *testing.T) {
	tr := New("")
	tr.AutoStart = false
	tr.Inject("hello world 42\r\nsecond row\r\n")
	tr.SelectWordAt(0, 2) // "hello" on the first row
	if got := tr.SelectedText(); got != "hello" {
		t.Fatalf("word select: %q", got)
	}
	tr.SelectWordAt(0, 6) // "world"
	if got := tr.SelectedText(); got != "world" {
		t.Fatalf("word select 2: %q", got)
	}
	tr.SelectLineAt(0)
	if got := tr.SelectedText(); !strings.HasPrefix(got, "hello world 42") {
		t.Fatalf("line select: %q", got)
	}
	tr.StartSelection(0, 6)
	tr.ExtendSelection(1, 3)
	if got := tr.SelectedText(); got != "world 42\nsec" {
		t.Fatalf("span select: %q", got)
	}
	tr.ClearSelection()
	if tr.HasSelection() {
		t.Fatal("clear left a selection")
	}
}

// TestPasteBracketed pins paste: raw bytes normally, wrapped in the
// bracketed-paste markers when the program enabled mode 2004.
func TestPasteBracketed(t *testing.T) {
	tr := New("")
	tr.AutoStart = false
	var got []byte
	tr.SetTestWriteSink(func(b []byte) { got = append(got, b...) })
	tr.Paste("line1\nline2")
	if string(got) != "line1\rline2" {
		t.Fatalf("raw paste: %q", got)
	}
	tr.Inject("\x1b[?2004h")
	got = nil
	tr.Paste("x")
	if string(got) != "\x1b[200~x\x1b[201~" {
		t.Fatalf("bracketed paste: %q", got)
	}
}
