package terminal

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// vt.go — a compact VT100/xterm emulator: the byte stream from the pty is
// parsed into a cell grid the pane renders directly. Supported: C0
// controls, CSI (cursor movement, ED/EL, insert/delete, scrolling regions,
// SGR incl. 256-color/truecolor), OSC (title, cwd via OSC 7), alternate
// screen (1049/1047/1048), DECAWM, DECCKM, bracketed paste (2004), origin
// mode, and the query replies programs expect (DSR, DA). Scrolled-off
// primary-screen rows feed the scrollback history; the alternate screen
// never pushes history (the upstream editor/vte semantics).

// vtColor is a cell color: default, a palette index (0-255), or direct RGB.
type vtColor struct {
	rgb     bool
	idx     uint8
	r, g, b uint8
}

// defaultFg / defaultBg mean "the theme decides" (the pane maps them).
var defaultColor = vtColor{}

// cellAttrs are the SGR character attributes a run can carry.
type cellAttrs uint16

const (
	attrBold cellAttrs = 1 << iota
	attrDim
	attrItalic
	attrUnderline
	attrStrike
	attrReverse
)

// vtCell is one grid cell. Wide characters occupy their cell plus a tail
// cell with r == 0 (width 2 total).
type vtCell struct {
	r    rune
	fg   vtColor
	bg   vtColor
	at   cellAttrs
	tail bool // second half of a wide pair
}

// vtScreen is one screen buffer (primary or alternate).
type vtScreen struct {
	grid           [][]vtCell
	curX, curY     int
	savedX, savedY int
	top, bottom    int // scroll region (inclusive rows)
}

// vtEmu is the emulator state: two screens, scrollback, modes, parser.
type vtEmu struct {
	cols, rows int
	prim, alt  vtScreen
	cur        *vtScreen

	history    [][]vtCell // scrolled-off primary rows (maxHistory cap)
	maxHistory int

	altActive      bool
	cursorVisible  bool
	autowrap       bool
	pendingWrap    bool // last-column flag (DEC autowrap)
	originMode     bool
	appCursor      bool
	bracketedPaste bool
	title          string
	cwd            string // OSC 7 report

	// current SGR state, applied to cells as they are placed
	fg    vtColor
	bg    vtColor
	attrs cellAttrs

	// parser
	state    int
	params   []int
	paramBuf int
	paramAny bool
	private  byte
	osc      []byte
	utf8     []byte

	// replies collected for the pty (DSR / DA responses)
	replies []byte

	dirty bool
}

// parser states
const (
	stGround = iota
	stEsc
	stCsi
	stOsc
	stOscEsc
	stConsumeUntilST // DCS / PM / APC payloads
)

// newEmu creates an emulator with the given grid size.
func newEmu(cols, rows, maxHistory int) *vtEmu {
	e := &vtEmu{maxHistory: maxHistory, cursorVisible: true, autowrap: true}
	e.resize(cols, rows)
	e.cur = &e.prim
	e.resetScreen(&e.prim)
	e.resetScreen(&e.alt)
	return e
}

func (e *vtEmu) resetScreen(s *vtScreen) {
	s.curX, s.curY = 0, 0
	s.top, s.bottom = 0, e.rows-1
	for i := range s.grid {
		s.grid[i] = make([]vtCell, e.cols)
	}
}

// resize re-sizes the grid: rows are padded/truncated in place (no reflow
// — programs redraw after SIGWINCH anyway), the scroll region resets and
// the cursor clamps. Growing rows pulls lines back from the history when
// there is one (the screen grows into the scrollback, like xterm).
func (e *vtEmu) resize(cols, rows int) {
	if cols < 2 {
		cols = 2
	}
	if rows < 2 {
		rows = 2
	}
	grew := rows > e.rows
	e.cols, e.rows = cols, rows
	for _, s := range []*vtScreen{&e.prim, &e.alt} {
		// Fit columns.
		for i := range s.grid {
			row := s.grid[i]
			if len(row) < cols {
				s.grid[i] = append(row, make([]vtCell, cols-len(row))...)
			} else if len(row) > cols {
				s.grid[i] = row[:cols]
			}
		}
		// Fit rows: grow pulls from history on the primary screen.
		if len(s.grid) < rows {
			for len(s.grid) < rows {
				var row []vtCell
				if s == &e.prim && len(e.history) > 0 && grew {
					row = e.history[len(e.history)-1]
					e.history = e.history[:len(e.history)-1]
					if len(row) < cols {
						row = append(row, make([]vtCell, cols-len(row))...)
					} else if len(row) > cols {
						row = row[:cols]
					}
				} else {
					row = make([]vtCell, cols)
				}
				s.grid = append(s.grid, row)
			}
		} else if len(s.grid) > rows {
			// Shrink: drop blank rows first, then scroll rows off the top
			// into the history (primary only).
			for len(s.grid) > rows {
				last := s.grid[len(s.grid)-1]
				if blankRow(last) {
					s.grid = s.grid[:len(s.grid)-1]
					continue
				}
				if s == &e.prim {
					e.pushHistory(s.grid[0])
				}
				s.grid = append(s.grid[:0], s.grid[1:]...)
				if s.curY > 0 {
					s.curY--
				}
			}
		}
		s.top, s.bottom = 0, e.rows-1
		if s.curY >= e.rows {
			s.curY = e.rows - 1
		}
		if s.curX >= cols {
			s.curX = cols - 1
		}
	}
	e.pendingWrap = false
}

func blankRow(row []vtCell) bool {
	for _, c := range row {
		if c.r != 0 && c.r != ' ' {
			return false
		}
	}
	return true
}

func (e *vtEmu) pushHistory(row []vtCell) {
	cp := make([]vtCell, len(row))
	copy(cp, row)
	e.history = append(e.history, cp)
	if len(e.history) > e.maxHistory {
		e.history = e.history[len(e.history)-e.maxHistory:]
	}
}

// noticeRow appends a synthetic dim row to the scrollback (exit notices,
// spawn errors): the pty stream cannot produce it, so the model writes it.
func (e *vtEmu) noticeRow(text string) {
	row := make([]vtCell, e.cols)
	for i, r := range text {
		if i >= e.cols {
			break
		}
		row[i] = vtCell{r: r, fg: vtColor{idx: 8}}
	}
	e.pushHistory(row)
}

// rowTotal is the absolute row count: scrollback + live screen.
func (e *vtEmu) rowTotal() int {
	return len(e.history) + e.rows
}

// absoluteRow returns the scrollback+screen row at index i (a copy; rows
// are shared with the reader goroutine and may be trimmed at any time).
func (e *vtEmu) absoluteRow(i int) ([]vtCell, bool) {
	if i < 0 {
		return nil, false
	}
	var row []vtCell
	if i < len(e.history) {
		row = e.history[i]
	} else {
		y := i - len(e.history)
		if y >= e.rows {
			return nil, false
		}
		row = e.cur.grid[y]
	}
	cp := make([]vtCell, len(row))
	copy(cp, row)
	return cp, true
}

// ---------------------------------------------------------------------------
// feed: byte-stream parsing
// ---------------------------------------------------------------------------

// Feed parses one output chunk. Replies (DSR/DA) accumulate in e.replies;
// the caller drains them via e.takeReplies() and writes them to the pty.
func (e *vtEmu) Feed(b []byte) {
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch e.state {
		case stGround:
			e.ground(c)
		case stEsc:
			e.escDispatch(c, &i, b)
		case stCsi:
			e.csiByte(c)
		case stOsc:
			if c == 0x07 { // BEL terminator
				e.oscEnd()
			} else if c == 0x1b {
				e.state = stOscEsc
			} else {
				e.osc = append(e.osc, c)
			}
		case stOscEsc:
			if c == '\\' { // ST
				e.oscEnd()
				e.state = stGround
			} else {
				e.oscEnd() // ESC in OSC without ST: end it, reprocess
				e.state = stGround
				i--
			}
		case stConsumeUntilST:
			if c == 0x1b {
				e.state = stGround
				if i+1 < len(b) && b[i+1] == '\\' {
					i++
				}
			} else if c == 0x07 {
				e.state = stGround
			}
		}
	}
	e.dirty = true
}

func (e *vtEmu) ground(c byte) {
	switch {
	case c == 0x1b:
		e.state = stEsc
		e.private = 0
		e.params = e.params[:0]
		e.paramBuf, e.paramAny = 0, false
	case c < 0x20:
		e.control(c)
	case c == 0x7f:
		// DEL: ignored
	default:
		// UTF-8 continuation: accumulate partial runes.
		if n := utf8FullLen(c); n > 1 && len(e.utf8) == 0 {
			e.utf8 = append(e.utf8, c)
			return
		}
		if len(e.utf8) > 0 {
			e.utf8 = append(e.utf8, c)
			r, size := utf8.DecodeRune(e.utf8)
			if size == 1 && r == utf8.RuneError {
				if len(e.utf8) < utf8FullLen(e.utf8[0]) {
					return // still incomplete
				}
				e.utf8 = e.utf8[:0]
				return // invalid sequence: dropped
			}
			e.utf8 = e.utf8[:0]
			e.putRune(r)
			return
		}
		r, _ := utf8.DecodeRune([]byte{c}) // ASCII fast path
		e.putRune(r)
	}
}

// utf8FullLen returns the encoded length a lead byte announces (1 for ASCII).
func utf8FullLen(b byte) int {
	switch {
	case b&0x80 == 0:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	}
	return 1
}

// control handles C0 controls in the ground state.
func (e *vtEmu) control(c byte) {
	switch c {
	case 0x07: // BEL
	case 0x08: // BS
		if e.cur.curX > 0 {
			e.cur.curX--
			e.pendingWrap = false
		}
	case 0x09: // HT: next 8-column stop
		e.cur.curX = nextTab(e.cur.curX, e.cols)
		e.pendingWrap = false
	case 0x0a, 0x0b, 0x0c: // LF VT FF
		e.index()
	case 0x0d: // CR
		e.cur.curX = 0
		e.pendingWrap = false
	case 0x0e, 0x0f: // SO/SI charsets: ignored
	case 0x1a, 0x18: // CAN/SUB: cancel sequences
		e.state = stGround
	}
}

func nextTab(x, cols int) int {
	n := (x/8 + 1) * 8
	if n >= cols {
		return cols - 1
	}
	return n
}

// escDispatch processes bytes after ESC.
func (e *vtEmu) escDispatch(c byte, i *int, b []byte) {
	switch c {
	case '[':
		e.state = stCsi
		e.private = 0
		e.params = e.params[:0]
		e.paramBuf, e.paramAny = 0, false
	case ']':
		e.state = stOsc
		e.osc = e.osc[:0]
	case 'P', '^', '_', 'X': // DCS PM APC SOS: consume until ST
		e.state = stConsumeUntilST
	case '7': // DECSC save cursor
		e.cur.savedX, e.cur.savedY = e.cur.curX, e.cur.curY
		e.state = stGround
	case '8': // DECRSC restore
		e.cur.curX, e.cur.curY = e.cur.savedX, e.cur.savedY
		e.pendingWrap = false
		e.state = stGround
	case 'D': // IND: index
		e.index()
		e.state = stGround
	case 'E': // NEL: next line
		e.cur.curX = 0
		e.index()
		e.state = stGround
	case 'M': // RI: reverse index
		e.reverseIndex()
		e.state = stGround
	case 'c': // RIS: full reset
		e.fullReset()
		e.state = stGround
	case '(', ')', '*', '+', '#', '%': // charset / line-size: consume one byte
		e.state = stGround
		if *i+1 < len(b) {
			*i++
		}
	case '=', '>': // keypad modes: ignored
		e.state = stGround
	case '\\': // ST
		e.state = stGround
	default:
		e.state = stGround
	}
}

// csiByte accumulates a CSI sequence until the final byte.
func (e *vtEmu) csiByte(c byte) {
	switch {
	case c >= '0' && c <= '9':
		e.paramBuf = e.paramBuf*10 + int(c-'0')
		e.paramAny = true
	case c == ';':
		e.params = append(e.params, e.paramValue())
		e.paramBuf, e.paramAny = 0, false
	case c == ':' || c == '$' || c == '"' || c == '\'' || c == ' ' || c == '!':
		// sub-parameter / intermediates: remembered only for SGR colon form (ignored)
	case c == '?':
		e.private = '?'
	case c == '>':
		e.private = '>'
	case c == '<':
		e.private = '<'
	case c >= '@' && c <= '~':
		// final byte: dispatch
		if e.paramAny || len(e.params) > 0 {
			e.params = append(e.params, e.paramValue())
		}
		e.csiDispatch(c)
		e.state = stGround
	default:
		e.state = stGround // malformed: abort
	}
}

func (e *vtEmu) paramValue() int {
	if !e.paramAny {
		return 0
	}
	return e.paramBuf
}

// p returns param i (1-based position in the sequence) or def when absent/0.
func (e *vtEmu) p(i int, def int) int {
	if i < len(e.params) && e.params[i] != 0 {
		return e.params[i]
	}
	return def
}

func (e *vtEmu) csiDispatch(final byte) {
	cur := e.cur
	switch final {
	case 'A': // CUU
		cur.curY = max(cur.top, cur.curY-e.p(0, 1))
		e.pendingWrap = false
	case 'B', 'e': // CUD
		cur.curY = min(cur.bottom, cur.curY+e.p(0, 1))
		e.pendingWrap = false
	case 'C': // CUF
		cur.curX = min(e.cols-1, cur.curX+e.p(0, 1))
		e.pendingWrap = false
	case 'D': // CUB
		cur.curX = max(0, cur.curX-e.p(0, 1))
		e.pendingWrap = false
	case 'E': // CNL
		cur.curY = min(cur.bottom, cur.curY+e.p(0, 1))
		cur.curX = 0
	case 'F': // CPL
		cur.curY = max(cur.top, cur.curY-e.p(0, 1))
		cur.curX = 0
	case 'G', '`': // CHA / HPA
		cur.curX = clamp(e.p(0, 1)-1, 0, e.cols-1)
		e.pendingWrap = false
	case 'H', 'f': // CUP / HVP
		e.moveCursor(e.p(0, 1)-1, e.p(1, 1)-1)
	case 'd': // VPA
		y := e.p(0, 1) - 1
		if e.originMode {
			y += cur.top
		}
		cur.curY = clamp(y, 0, e.rows-1)
	case 'J': // ED
		e.eraseDisplay(e.p(0, 0))
	case 'K': // EL
		e.eraseLine(e.p(0, 0))
	case 'L': // IL
		if cur.curY >= cur.top && cur.curY <= cur.bottom {
			n := e.p(0, 1)
			for k := 0; k < n; k++ {
				e.insertLine(cur.curY)
			}
		}
	case 'M': // DL
		if cur.curY >= cur.top && cur.curY <= cur.bottom {
			n := e.p(0, 1)
			for k := 0; k < n; k++ {
				e.deleteLine(cur.curY)
			}
		}
	case 'P': // DCH
		e.deleteChars(e.p(0, 1))
	case '@': // ICH
		e.insertChars(e.p(0, 1))
	case 'X': // ECH
		e.eraseChars(e.p(0, 1))
	case 'S': // SU
		for k := 0; k < e.p(0, 1); k++ {
			e.scrollUp()
		}
	case 'T': // SD
		for k := 0; k < e.p(0, 1); k++ {
			e.scrollDown()
		}
	case 'r': // DECSTBM
		top := e.p(0, 1) - 1
		bottom := e.p(1, e.rows) - 1
		if top < 0 {
			top = 0
		}
		if bottom >= e.rows {
			bottom = e.rows - 1
		}
		if top < bottom {
			cur.top, cur.bottom = top, bottom
			e.moveCursor(0, 0) // cursor homes (origin-relative)
		}
	case 'h': // SM
		e.setModes(e.params, e.private == '?', true)
	case 'l': // RM
		e.setModes(e.params, e.private == '?', false)
	case 'm': // SGR
		e.sgr(e.params)
	case 'n': // DSR
		switch e.p(0, 0) {
		case 5:
			e.replies = append(e.replies, "\x1b[0n"...)
		case 6:
			y := cur.curY + 1
			if e.originMode {
				y = cur.curY - cur.top + 1
			}
			e.replies = append(e.replies, []byte(fmt.Sprintf("\x1b[%d;%dR", y, cur.curX+1))...)
		}
	case 'c': // DA
		if e.private == '>' {
			e.replies = append(e.replies, "\x1b[>0;0;0c"...)
		} else {
			e.replies = append(e.replies, "\x1b[?6c"...)
		}
	case 's': // save cursor (ANSI.SYS)
		cur.savedX, cur.savedY = cur.curX, cur.curY
	case 'u': // restore cursor
		cur.curX, cur.curY = cur.savedX, cur.savedY
		e.pendingWrap = false
	case 't': // window manipulation: ignored
	}
}

// takeReplies drains the accumulated query responses.
func (e *vtEmu) takeReplies() []byte {
	if len(e.replies) == 0 {
		return nil
	}
	r := e.replies
	e.replies = nil
	return r
}

// moveCursor places the cursor (CUP); origin mode offsets within the region.
func (e *vtEmu) moveCursor(row, col int) {
	cur := e.cur
	if e.originMode {
		row += cur.top
		row = clamp(row, cur.top, cur.bottom)
	} else {
		row = clamp(row, 0, e.rows-1)
	}
	cur.curY = row
	cur.curX = clamp(col, 0, e.cols-1)
	e.pendingWrap = false
}

// setModes applies SM/RM (and DEC private modes).
func (e *vtEmu) setModes(params []int, private, on bool) {
	for _, m := range params {
		if !private {
			continue // ANSI SM/RM (IRM insert mode, new-line mode): ignored
		}
		switch m {
		case 1:
			e.appCursor = on
		case 6:
			e.originMode = on
			e.moveCursor(0, 0)
		case 7:
			e.autowrap = on
			if !on {
				e.pendingWrap = false
			}
		case 25:
			e.cursorVisible = on
		case 47, 1047:
			e.switchScreen(on)
		case 1048:
			if on {
				e.cur.savedX, e.cur.savedY = e.cur.curX, e.cur.curY
			} else {
				e.cur.curX, e.cur.curY = e.cur.savedX, e.cur.savedY
				e.pendingWrap = false
			}
		case 1049:
			if on {
				e.cur.savedX, e.cur.savedY = e.cur.curX, e.cur.curY
				e.switchScreen(true)
				e.eraseDisplay(2)
			} else {
				e.switchScreen(false)
				e.cur.curX, e.cur.curY = e.cur.savedX, e.cur.savedY
				e.pendingWrap = false
			}
		case 2004:
			e.bracketedPaste = on
		}
	}
}

// switchScreen toggles between primary and alternate. The alternate screen
// never touches the history.
func (e *vtEmu) switchScreen(alt bool) {
	if alt == e.altActive {
		return
	}
	e.altActive = alt
	if alt {
		e.cur = &e.alt
	} else {
		e.cur = &e.prim
	}
	e.cur.top, e.cur.bottom = 0, e.rows-1
	e.pendingWrap = false
}

// fullReset (ESC c).
func (e *vtEmu) fullReset() {
	e.altActive = false
	e.cur = &e.prim
	e.cursorVisible = true
	e.autowrap = true
	e.originMode = false
	e.appCursor = false
	e.bracketedPaste = false
	e.resetScreen(&e.prim)
	e.resetScreen(&e.alt)
	e.pendingWrap = false
}

// ---------------------------------------------------------------------------
// cell placement and scrolling
// ---------------------------------------------------------------------------

// putRune writes one rune at the cursor (DEC autowrap semantics).
func (e *vtEmu) putRune(r rune) {
	cur := e.cur
	w := runeWidth(r)
	if e.pendingWrap {
		// The wrap-pending flag: the next glyph starts a new line.
		cur.curX = 0
		e.index()
		e.pendingWrap = false
	}
	if w == 2 && cur.curX == e.cols-1 {
		// A wide char straddling the edge: wrap first (or drop it).
		if e.autowrap {
			cur.curX = 0
			e.index()
		} else {
			return
		}
	}
	cur.grid[cur.curY][cur.curX] = vtCell{r: r, fg: e.fg, bg: e.bg, at: e.attrs}
	if w == 2 && cur.curX+1 < e.cols {
		cur.grid[cur.curY][cur.curX+1] = vtCell{tail: true, fg: e.fg, bg: e.bg, at: e.attrs}
	}
	cur.curX += w
	if cur.curX >= e.cols {
		if e.autowrap {
			cur.curX = e.cols - 1
			e.pendingWrap = true
		} else {
			cur.curX = e.cols - 1
		}
	}
}

// index moves the cursor down one row, scrolling the region at its bottom.
func (e *vtEmu) index() {
	cur := e.cur
	if cur.curY == cur.bottom {
		e.scrollUp()
	} else if cur.curY < e.rows-1 {
		cur.curY++
	}
}

func (e *vtEmu) reverseIndex() {
	cur := e.cur
	if cur.curY == cur.top {
		e.scrollDown()
	} else if cur.curY > 0 {
		cur.curY--
	}
}

// scrollUp scrolls the region one row: the top row leaves (to the history
// on the primary screen when the region is the full screen), a blank row
// lands at the region bottom.
func (e *vtEmu) scrollUp() {
	cur := e.cur
	if cur.top == 0 && cur.bottom == e.rows-1 && !e.altActive {
		e.pushHistory(cur.grid[0])
	}
	copy(cur.grid[cur.top:cur.bottom], cur.grid[cur.top+1:cur.bottom+1])
	cur.grid[cur.bottom] = make([]vtCell, e.cols)
}

// scrollDown scrolls the region down one row.
func (e *vtEmu) scrollDown() {
	cur := e.cur
	copy(cur.grid[cur.top+1:cur.bottom+1], cur.grid[cur.top:cur.bottom])
	cur.grid[cur.top] = make([]vtCell, e.cols)
}

// insertLine inserts a blank line at y inside the region (IL).
func (e *vtEmu) insertLine(y int) {
	cur := e.cur
	if y < cur.top || y > cur.bottom {
		return
	}
	copy(cur.grid[y+1:cur.bottom+1], cur.grid[y:cur.bottom])
	cur.grid[y] = make([]vtCell, e.cols)
}

// deleteLine removes the line at y inside the region (DL).
func (e *vtEmu) deleteLine(y int) {
	cur := e.cur
	if y < cur.top || y > cur.bottom {
		return
	}
	copy(cur.grid[y:cur.bottom], cur.grid[y+1:cur.bottom+1])
	cur.grid[cur.bottom] = make([]vtCell, e.cols)
}

// eraseDisplay (ED): 0 cursor→end, 1 start→cursor, 2 all, 3 all + history.
func (e *vtEmu) eraseDisplay(n int) {
	cur := e.cur
	switch n {
	case 0:
		e.eraseCells(cur.curY, cur.curX, e.rows-1, e.cols-1)
	case 1:
		e.eraseCells(0, 0, cur.curY, cur.curX)
	case 2, 3:
		for i := range cur.grid {
			cur.grid[i] = make([]vtCell, e.cols)
		}
		if n == 3 {
			e.history = nil
		}
	}
}

// eraseLine (EL).
func (e *vtEmu) eraseLine(n int) {
	cur := e.cur
	switch n {
	case 0:
		e.eraseCells(cur.curY, cur.curX, cur.curY, e.cols-1)
	case 1:
		e.eraseCells(cur.curY, 0, cur.curY, cur.curX)
	case 2:
		e.eraseCells(cur.curY, 0, cur.curY, e.cols-1)
	}
}

// eraseCells blanks rows [r0..r1], columns [c0..c1] (inclusive).
func (e *vtEmu) eraseCells(r0, c0, r1, c1 int) {
	cur := e.cur
	for y := r0; y <= r1 && y < e.rows; y++ {
		row := cur.grid[y]
		for x := c0; x <= c1 && x < e.cols; x++ {
			row[x] = vtCell{}
		}
	}
}

func (e *vtEmu) deleteChars(n int) {
	cur := e.cur
	row := cur.grid[cur.curY]
	x := cur.curX
	if x >= e.cols {
		return
	}
	if n > e.cols-x {
		n = e.cols - x
	}
	copy(row[x:], row[x+n:])
	tail := row[len(row)-n:]
	for i := range tail {
		tail[i] = vtCell{}
	}
}

func (e *vtEmu) insertChars(n int) {
	cur := e.cur
	row := cur.grid[cur.curY]
	x := cur.curX
	if x >= e.cols {
		return
	}
	if n > e.cols-x {
		n = e.cols - x
	}
	copy(row[x+n:], row[x:])
	for i := x; i < x+n; i++ {
		row[i] = vtCell{}
	}
}

func (e *vtEmu) eraseChars(n int) {
	cur := e.cur
	x := cur.curX
	end := x + n
	if end > e.cols {
		end = e.cols
	}
	for i := x; i < end; i++ {
		cur.grid[cur.curY][i] = vtCell{}
	}
}

// ---------------------------------------------------------------------------
// SGR
// ---------------------------------------------------------------------------

// sgr applies an SGR parameter list (CSI ... m).
func (e *vtEmu) sgr(params []int) {
	for i := 0; i < len(params); i++ {
		n := params[i]
		switch {
		case n == 0:
			e.fg, e.bg = defaultColor, defaultColor
			e.attrs = 0
		case n == 1:
			e.attrs |= attrBold
		case n == 2:
			e.attrs |= attrDim
		case n == 22:
			e.attrs &^= attrBold | attrDim
		case n == 3:
			e.attrs |= attrItalic
		case n == 23:
			e.attrs &^= attrItalic
		case n == 4:
			e.attrs |= attrUnderline
		case n == 24:
			e.attrs &^= attrUnderline
		case n == 9:
			e.attrs |= attrStrike
		case n == 29:
			e.attrs &^= attrStrike
		case n == 7:
			e.attrs |= attrReverse
		case n == 27:
			e.attrs &^= attrReverse
		case n >= 30 && n <= 37:
			e.fg = vtColor{idx: uint8(n - 30)}
		case n == 38, n == 48:
			// Extended color: 5;idx or 2;r;g;b — consumes parameters.
			c, adv := extendedColor(params[i+1:])
			if adv > 0 {
				i += adv
				if n == 38 {
					e.fg = c
				} else {
					e.bg = c
				}
			}
		case n == 39:
			e.fg = defaultColor
		case n >= 40 && n <= 47:
			e.bg = vtColor{idx: uint8(n - 40)}
		case n == 49:
			e.bg = defaultColor
		case n >= 90 && n <= 97:
			e.fg = vtColor{idx: uint8(n - 90 + 8)}
		case n >= 100 && n <= 107:
			e.bg = vtColor{idx: uint8(n - 100 + 8)}
		}
	}
}

// extendedColor parses 5;n / 2;r;g;b after 38/48; returns the advance.
func extendedColor(rest []int) (vtColor, int) {
	if len(rest) == 0 {
		return defaultColor, 0
	}
	switch rest[0] {
	case 5:
		if len(rest) < 2 {
			return defaultColor, 1
		}
		return vtColor{idx: uint8(clamp(rest[1], 0, 255))}, 2
	case 2:
		if len(rest) < 4 {
			return defaultColor, len(rest)
		}
		return vtColor{rgb: true,
			r: uint8(clamp(rest[1], 0, 255)),
			g: uint8(clamp(rest[2], 0, 255)),
			b: uint8(clamp(rest[3], 0, 255))}, 4
	}
	return defaultColor, 0
}

// ---------------------------------------------------------------------------
// OSC
// ---------------------------------------------------------------------------

// oscEnd dispatches a completed OSC payload.
func (e *vtEmu) oscEnd() {
	defer func() {
		e.osc = e.osc[:0]
	}()
	payload := string(e.osc)
	sep := strings.IndexByte(payload, ';')
	if sep < 0 {
		return
	}
	code := payload[:sep]
	body := payload[sep+1:]
	switch code {
	case "0", "2":
		e.title = body
	case "7":
		// Shell integration cwd report: file://host/path → path.
		if p, ok := oscFileURL(body); ok {
			e.cwd = p
		}
	}
}

// oscFileURL decodes file:// URLs from OSC 7 (percent escapes included).
func oscFileURL(u string) (string, bool) {
	u = strings.TrimPrefix(u, "file://")
	// strip host
	if i := strings.IndexByte(u, '/'); i > 0 {
		u = u[i:]
	} else {
		return "", false
	}
	var sb strings.Builder
	for i := 0; i < len(u); i++ {
		if u[i] == '%' && i+2 < len(u) {
			hi, ok1 := hexVal(u[i+1])
			lo, ok2 := hexVal(u[i+2])
			if ok1 && ok2 {
				sb.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		sb.WriteByte(u[i])
	}
	return sb.String(), true
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// runeWidth is the display width: East Asian wide/fullwidth ranges are 2,
// everything else 1 (combining marks are folded to 1 — terminals rarely
// rely on exact zero-width layout).
func runeWidth(r rune) int {
	if r >= 0x1100 && (r <= 0x115F || // Hangul Jamo
		r == 0x2329 || r == 0x232A ||
		(r >= 0x2E80 && r <= 0x303E) || // CJK radicals, Kangxi
		(r >= 0x3041 && r <= 0x33FF) || // Hiragana .. CJK compat
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified
		(r >= 0xA000 && r <= 0xA4CF) || // Yi
		(r >= 0xAC00 && r <= 0xD7A3) || // Hangul syllables
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) || // fullwidth forms
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x20000 && r <= 0x2FFFD) ||
		(r >= 0x30000 && r <= 0x3FFFD)) {
		return 2
	}
	return 1
}
