// Package terminal implements the editor example's terminal pane with
// the upstream editor's terminal semantics: the shell runs interactively on a pseudo
// terminal (ConPTY on Windows, /dev/ptmx on Linux) from the moment the
// terminal opens, printing its own prompt and doing its own line editing
// and history. Output — including cursor-addressed full-screen programs —
// streams through a VT emulator into the rendered grid; keystrokes go to
// the shell as raw bytes, so nested shells (powershell inside cmd), REPLs
// and installers all work like a real terminal, and Ctrl+C is passed
// through the line discipline instead of killing the shell.
package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Cell is one rendered grid cell (a snapshot copy).
type Cell struct {
	R    rune
	Fg   vtColor
	Bg   vtColor
	At   cellAttrs
	Tail bool // second half of a wide pair (renders as a blank)
}

// SelPos is a selection endpoint: an absolute row (scrollback + screen)
// and a column.
type SelPos struct{ Row, Col int }

func (p SelPos) less(q SelPos) bool {
	if p.Row != q.Row {
		return p.Row < q.Row
	}
	return p.Col < q.Col
}

// Terminal is the pane's model.
type Terminal struct {
	mu sync.Mutex

	// Cwd is the session's working directory (initial dir, then OSC 7
	// reports from shell integration when the shell sends them).
	Cwd string
	// Title is the OSC title report ("" when the shell sent none).
	Title string
	// Lines/scrollback are inside the emulator (vtEmu.history + grid).

	// Shell runs the interactive session (empty = platform default; the
	// settings panel overrides it, the upstream editor's terminal.shell).
	Shell     string
	ShellArgs string
	// MaxLines caps the scrollback history; 0 = the 5000 default.
	MaxLines int

	emu     *vtEmu
	sess    ptyConn
	started bool
	cols    int
	rows    int
	closed  bool
	// AutoStart spawns the shell from the render pass (EnsureStarted).
	// Tests that render a synthetic grid set it false and feed Inject.
	AutoStart bool
	// writeSink, when set, receives everything a keystroke/paste would
	// write (the headless render tests' fake terminal; no shell runs).
	writeSink func([]byte)
	// lastExit throttles automatic respawn: a crashing shell must not
	// spin the spawner every frame; user input forces a restart.
	lastExit   time.Time
	forceStart bool
	exitNotice string
	command    []string // optional direct, one-shot program (e.g. Delve)

	// Scroll is the first visible absolute row; Follow pins the bottom.
	Scroll int
	Follow bool

	// Selection over the grid (absolute rows).
	selAnchor SelPos
	selHead   SelPos

	// OnOutput is invoked from the reader goroutine after output is
	// parsed (the redraw hook; nil in tests). Set through SetOutputHook.
	OnOutput func()
	// pendingNotice tracks a transient status notice row in the scrollback
	// (spawn error / exit notice): the next notice replaces it instead of
	// piling up one row per retry — a failing shell must not flood the pane.
	pendingNotice bool
}

// New creates a terminal in dir. The shell spawns on the first render
// (EnsureStarted), once the pane knows its real grid size.
func New(dir string) *Terminal {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return &Terminal{Cwd: dir, Follow: true, AutoStart: true}
}

// NewCommand opens an interactive program directly in a PTY without passing
// executable paths or arguments through a shell. It does not auto-restart.
func NewCommand(dir, program string, args ...string) *Terminal {
	t := New(dir)
	t.command = append([]string{program}, args...)
	return t
}

// defaultShell is the platform shell used when the settings don't
// override one: PowerShell on Windows (the upstream editor's Windows default — pwsh 7
// when installed, then Windows PowerShell, then %COMSPEC%/cmd), $SHELL
// (or sh) elsewhere — an interactive session, not a one-shot shape. The
// path is resolved with LookPath so CreateProcessW gets an absolute
// program path (it does not search PATH as reliably as a shell does).
func defaultShell() (string, string) {
	if runtime.GOOS == "windows" {
		for _, cand := range []string{"pwsh.exe", "powershell.exe"} {
			if p, err := exec.LookPath(cand); err == nil {
				return p, ""
			}
		}
		if comspec := os.Getenv("COMSPEC"); comspec != "" {
			return comspec, ""
		}
		return "cmd.exe", ""
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh, ""
	}
	return "sh", ""
}

// SetMaxLines caps the scrollback (minimum 1000).
func (t *Terminal) SetMaxLines(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n < 1000 {
		n = 1000
	}
	t.MaxLines = n
	if t.emu != nil {
		t.emu.maxHistory = n
	}
}

// SetShell overrides the shell for the NEXT session: a live session is
// torn down and a fresh one spawns at the next render.
func (t *Terminal) SetShell(shell, args string) {
	t.mu.Lock()
	if strings.TrimSpace(shell) == "" {
		shell, args = defaultShell()
	}
	t.Shell, t.ShellArgs = shell, args
	s := t.sess
	t.sess = nil
	t.started = false
	t.emu = nil
	t.lastExit = time.Time{} // respawn is not throttled for settings changes
	t.mu.Unlock()
	if s != nil {
		go func() {
			_ = s.Close()
		}()
	}
}

// EnsureStarted spawns the shell when needed and applies the grid size.
// Called from the pane's render with the current pane geometry.
func (t *Terminal) EnsureStarted(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cols < 8 {
		cols = 8
	}
	if rows < 2 {
		rows = 2
	}
	t.cols, t.rows = cols, rows
	t.ensureLocked(false)
}

// ensureLocked starts (or restarts) the session and syncs the grid size.
func (t *Terminal) ensureLocked(force bool) {
	if t.closed || !t.AutoStart {
		return
	}
	if t.sess != nil {
		t.syncSizeLocked()
		return
	}
	// Respawn throttle: an instantly-crashing shell must not spin every
	// frame; explicit user input (force) always restarts.
	if !force && !t.lastExit.IsZero() && time.Since(t.lastExit) < time.Second {
		return
	}
	t.lastExit = time.Time{}
	t.forceStart = false

	shell, args := t.Shell, t.ShellArgs
	if strings.TrimSpace(shell) == "" {
		shell, args = defaultShell()
	}
	argv := interactiveArgv(shell, args)
	if len(t.command) > 0 {
		argv = append([]string(nil), t.command...)
	}
	env := childEnv()
	s, err := spawnShell(argv[0], argv[1:], t.Cwd, env, uint16(t.cols), uint16(t.rows))
	if err != nil {
		t.emuNotice(err.Error())
		t.lastExit = time.Now()
		return
	}
	t.pendingNotice = false
	t.sess = s
	t.started = true
	maxHist := t.MaxLines
	if maxHist <= 0 {
		maxHist = 5000
	}
	t.emu = newEmu(t.cols, t.rows, maxHist)
	go t.readLoop(s)
	go t.waitLoop(s)
}

// syncSizeLocked re-sizes the emulator grid + pty when the pane geometry
// changed (SIGWINCH drives program redraws).
func (t *Terminal) syncSizeLocked() {
	if t.emu == nil {
		return
	}
	if t.emu.cols == t.cols && t.emu.rows == t.rows {
		return
	}
	t.emu.resize(t.cols, t.rows)
	s := t.sess
	if s != nil {
		_ = s.Resize(uint16(t.cols), uint16(t.rows))
	}
}

// readLoop streams pty output through the emulator. A panic here (or in
// the emulator on malformed VT) must not take the whole editor down: the
// session is marked dead with a notice, the app keeps running, and the
// trace goes to stderr — which the Windows crash logging redirects to
// %LOCALAPPDATA%/go-gpui/editor.log.
func (t *Terminal) readLoop(s ptyConn) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "terminal: reader panic: %v\n%s\n", r, debug.Stack())
			t.sessionDied(s)
		}
	}()
	buf := make([]byte, 8192)
	for {
		n, err := s.Read(buf)
		if n > 0 {
			t.mu.Lock()
			if t.sess == s && t.emu != nil {
				t.emu.Feed(buf[:n])
				if t.emu.cwd != "" && t.emu.cwd != t.Cwd {
					t.Cwd = t.emu.cwd
				}
				if t.emu.title != "" {
					t.Title = t.emu.title
				}
				// Query replies (DSR/DA) go back to the child.
				if r := t.emu.takeReplies(); len(r) > 0 {
					go func() { _, _ = s.Write(r) }()
				}
			}
			notify := t.OnOutput
			t.mu.Unlock()
			if notify != nil {
				notify()
			}
		}
		if err != nil {
			return
		}
	}
}

// waitLoop reaps the process; session death is normally noticed by the
// reader's EOF first, the waiter covers processes that die silently. It
// is panic-guarded like readLoop (a reaper failure must not kill the app).
func (t *Terminal) waitLoop(s ptyConn) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "terminal: waiter panic: %v\n%s\n", r, debug.Stack())
			t.sessionDied(s)
		}
	}()
	_ = s.Wait()
	t.sessionDied(s)
}

// sessionDied marks the session dead (idempotent per session) and notes
// the exit in the scrollback.
func (t *Terminal) sessionDied(s ptyConn) {
	t.mu.Lock()
	if t.sess != s {
		t.mu.Unlock()
		return
	}
	t.sess = nil
	t.started = false
	t.lastExit = time.Now()
	if t.closed {
		t.mu.Unlock()
		return
	}
	if len(t.command) > 0 {
		t.AutoStart = false
		t.emuNotice("debug session ended")
	} else {
		t.emuNotice("process exited — press any key to restart the shell")
	}
	notify := t.OnOutput
	t.mu.Unlock()
	_ = s.Close()
	if notify != nil {
		notify()
	}
}

// emuNotice appends a gray notice row to the scrollback (creating the
// emulator first when a spawn failed before any grid existed). A pending
// transient notice is REPLACED: spawn retries and respawn cycles keep one
// status line, not one per attempt.
func (t *Terminal) emuNotice(text string) {
	if t.emu == nil {
		maxHist := t.MaxLines
		if maxHist <= 0 {
			maxHist = 5000
		}
		cols := t.cols
		if cols < 8 {
			cols = 80
		}
		rows := t.rows
		if rows < 2 {
			rows = 24
		}
		t.emu = newEmu(cols, rows, maxHist)
	}
	if t.pendingNotice && len(t.emu.history) > 0 {
		t.emu.history = t.emu.history[:len(t.emu.history)-1]
	}
	t.emu.noticeRow(text)
	t.pendingNotice = true
}

// Write sends raw bytes to the shell (the session restarts on demand).
func (t *Terminal) Write(b []byte) {
	t.mu.Lock()
	t.ensureLocked(true)
	s := t.sess
	sink := t.writeSink
	t.mu.Unlock()
	if sink != nil {
		sink(b)
		return
	}
	if s == nil {
		return
	}
	_, _ = s.Write(b)
}

// SetTestWriteSink redirects keystrokes/paste into f instead of a shell
// (the headless render tests' fake terminal). Nil restores the pty.
func (t *Terminal) SetTestWriteSink(f func([]byte)) {
	t.mu.Lock()
	t.writeSink = f
	t.mu.Unlock()
}

// Inject feeds raw VT output into the emulator without a shell — the
// render tests' way to build a deterministic grid. The emulator is
// created on demand at the default size.
func (t *Terminal) Inject(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.emu == nil {
		cols := t.cols
		if cols < 8 {
			cols = 80
		}
		rows := t.rows
		if rows < 2 {
			rows = 24
		}
		maxHist := t.MaxLines
		if maxHist <= 0 {
			maxHist = 5000
		}
		t.emu = newEmu(cols, rows, maxHist)
	}
	t.emu.Feed([]byte(s))
}

// SendKey encodes a keystroke the way a real terminal would and writes
// it to the shell.
func (t *Terminal) SendKey(key, keyChar string, ctrl, alt, shift bool) {
	b := t.EncodeKey(key, keyChar, ctrl, alt, shift)
	if len(b) == 0 {
		return
	}
	t.Write(b)
}

// Paste writes clipboard text (bracketed paste when the program enabled
// it, so multi-line pastes do not execute line by line).
func (t *Terminal) Paste(text string) {
	text = strings.ReplaceAll(text, "\r\n", "\r")
	text = strings.ReplaceAll(text, "\n", "\r")
	t.mu.Lock()
	bracketed := t.emu != nil && t.emu.bracketedPaste
	t.mu.Unlock()
	if bracketed {
		t.Write([]byte("\x1b[200~" + text + "\x1b[201~"))
	} else {
		t.Write([]byte(text))
	}
}

// Interrupt passes Ctrl+C through as a control byte: the pty line
// discipline delivers SIGINT to the foreground process group, exactly
// like a native terminal (no tree killing — the shell survives).
func (t *Terminal) Interrupt() {
	t.Write([]byte{0x03})
}

// Clear empties the scrollback and the screen (the upstream editor's terminal clear).
func (t *Terminal) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.emu != nil {
		t.emu.history = nil
		t.emu.eraseDisplay(2)
		t.emu.moveCursor(0, 0)
	}
	t.Follow = true
	t.Scroll = 0
	t.ClearSelection()
}

// Close terminates the session and the whole child tree (tab close).
func (t *Terminal) Close() {
	t.mu.Lock()
	t.closed = true
	s := t.sess
	t.sess = nil
	t.mu.Unlock()
	if s != nil {
		_ = s.Close()
	}
}

// RowTotal is the absolute row count for the current screen (the
// alternate screen shows no history, like a real terminal).
func (t *Terminal) RowTotal() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.emu == nil {
		return 0
	}
	if t.emu.altActive {
		return t.emu.rows
	}
	return t.emu.rowTotal()
}

// rowText extracts a row's display runes (wide tails collapse to blanks).
func rowText(row []vtCell) []rune {
	out := make([]rune, 0, len(row))
	for _, c := range row {
		if c.tail {
			continue
		}
		if c.r == 0 {
			out = append(out, ' ')
			continue
		}
		out = append(out, c.r)
	}
	return out
}

// Output returns the grid's row texts top-to-bottom, trailing blanks
// trimmed (the headless verification's view of what the shell printed).
func (t *Terminal) Output() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.emu == nil {
		return nil
	}
	total := t.emu.rowTotal()
	out := make([]string, 0, total)
	for i := 0; i < total; i++ {
		row, ok := t.emu.absoluteRow(i)
		if !ok {
			continue
		}
		out = append(out, string(rowTrim(rowText(row))))
	}
	return out
}

// WorkingDirectory returns the current directory, which PTY output may
// update asynchronously.
func (t *Terminal) WorkingDirectory() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Cwd
}

// Snapshot is an immutable view for rendering.
type Snapshot struct {
	// Rows is the visible absolute-row window (copies of grid rows).
	Rows [][]Cell
	// FirstRow is the first visible absolute row; TotalRows the count.
	FirstRow, TotalRows int
	// Cursor position in absolute rows (hidden when CursorVisible false).
	CursorRow, CursorCol int
	CursorVisible        bool
	// Alive reports a live shell session.
	Alive bool
	// Title / scroll state for the pane chrome.
	Title  string
	Scroll int
	Follow bool
	// Selection range (normalized; SelOK false when empty).
	SelStart, SelEnd SelPos
	SelOK            bool
}

// Snapshot returns a consistent view of the terminal state.
func (t *Terminal) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := Snapshot{
		Scroll: t.Scroll,
		Follow: t.Follow,
		Alive:  t.sess != nil,
	}
	if t.emu == nil {
		return snap
	}
	snap.Title = t.Title
	total := t.emu.rowTotal()
	if t.emu.altActive {
		total = t.emu.rows
	}
	snap.TotalRows = total
	snap.CursorVisible = t.emu.cursorVisible
	snap.CursorCol = t.emu.cur.curX
	if t.emu.altActive {
		snap.CursorRow = t.emu.cur.curY
	} else {
		snap.CursorRow = len(t.emu.history) + t.emu.cur.curY
	}
	if t.HasSelectionLocked() {
		start, end := t.selAnchor, t.selHead
		if end.less(start) {
			start, end = end, start
		}
		snap.SelStart, snap.SelEnd, snap.SelOK = start, end, true
	}
	return snap
}

// VisibleRows returns the absolute-row window [first, first+visible) as
// cell copies (the renderer's virtual window).
func (t *Terminal) VisibleRows(first, visible int) [][]Cell {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.emu == nil {
		return nil
	}
	total := t.emu.rowTotal()
	if t.emu.altActive {
		total = t.emu.rows
	}
	out := make([][]Cell, 0, visible)
	for i := first; i < first+visible && i < total; i++ {
		row, ok := t.emu.absoluteRow(i)
		if !ok {
			break
		}
		cells := make([]Cell, len(row))
		for j, c := range row {
			cells[j] = Cell{R: c.r, Fg: c.fg, Bg: c.bg, At: c.at, Tail: c.tail}
		}
		out = append(out, cells)
	}
	return out
}

// ---------------------------------------------------------------------------
// Scrolling (absolute rows; the upstream editor pins the bottom until the user scrolls up)
// ---------------------------------------------------------------------------

// ScrollLines adjusts the scroll offset (positive = down).
func (t *Terminal) ScrollLines(delta, total, visible int) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	maxScroll := total - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	t.Scroll += delta
	if t.Scroll < 0 {
		t.Scroll = 0
	}
	if t.Scroll > maxScroll {
		t.Scroll = maxScroll
	}
	t.Follow = t.Scroll >= maxScroll
	return t.Scroll
}

// SetScroll pins the first visible row (scrollbar drags).
func (t *Terminal) SetScroll(first int) {
	t.mu.Lock()
	t.Scroll = first
	t.Follow = false
	t.mu.Unlock()
}

// SetFollow pins or unpins the bottom view.
func (t *Terminal) SetFollow(f bool) {
	t.mu.Lock()
	t.Follow = f
	if f {
		t.Scroll = 1 << 30 // clamped at render
	}
	t.mu.Unlock()
}

// EffectiveScroll returns the first visible row for a viewport of visible
// rows: the clamped scroll position, or the bottom while Follow is set.
func (t *Terminal) EffectiveScroll(total, visible int) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	maxScroll := total - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if t.Follow {
		t.Scroll = maxScroll
		return maxScroll
	}
	if t.Scroll < 0 {
		t.Scroll = 0
	}
	if t.Scroll > maxScroll {
		t.Scroll = maxScroll
	}
	return t.Scroll
}

// ---------------------------------------------------------------------------
// Selection (the upstream editor's terminal: drag, double-click word, triple-click line)
// ---------------------------------------------------------------------------

// clampSelPos clamps a position into the grid (callers hold t.mu).
func (t *Terminal) clampSelPos(p SelPos) SelPos {
	total := 0
	cols := 0
	if t.emu != nil {
		if t.emu.altActive {
			total = t.emu.rows
		} else {
			total = t.emu.rowTotal()
		}
		cols = t.emu.cols
	}
	if total == 0 {
		return SelPos{}
	}
	if p.Row < 0 {
		p.Row = 0
	}
	if p.Row >= total {
		p.Row = total - 1
	}
	if cols > 0 && p.Col > cols {
		p.Col = cols
	}
	if p.Col < 0 {
		p.Col = 0
	}
	return p
}

// rowAt returns the cell runes of an absolute row (callers hold t.mu).
func (t *Terminal) rowAt(i int) []rune {
	if t.emu == nil {
		return nil
	}
	row, ok := t.emu.absoluteRow(i)
	if !ok {
		return nil
	}
	return rowText(row)
}

// rowTrim drops trailing blanks of a row's rune copy.
func rowTrim(rs []rune) []rune {
	for len(rs) > 0 && (rs[len(rs)-1] == ' ' || rs[len(rs)-1] == 0) {
		rs = rs[:len(rs)-1]
	}
	return rs
}

// HasSelectionLocked reports a non-empty selection (callers hold mu).
func (t *Terminal) HasSelectionLocked() bool {
	return t.selAnchor != t.selHead
}

// HasSelection reports a non-empty selection.
func (t *Terminal) HasSelection() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.HasSelectionLocked()
}

// ClearSelection drops the selection.
func (t *Terminal) ClearSelection() {
	t.mu.Lock()
	t.selAnchor, t.selHead = SelPos{}, SelPos{}
	t.mu.Unlock()
}

// StartSelection collapses the selection onto a position.
func (t *Terminal) StartSelection(row, col int) {
	t.mu.Lock()
	p := t.clampSelPos(SelPos{row, col})
	t.selAnchor, t.selHead = p, p
	t.mu.Unlock()
}

// ExtendSelection moves the selection head (drag).
func (t *Terminal) ExtendSelection(row, col int) {
	t.mu.Lock()
	t.selHead = t.clampSelPos(SelPos{row, col})
	t.mu.Unlock()
}

// isWordRune matches the upstream editor's terminal word set (word chars + path
// punctuation, so filenames and URLs select whole).
func isWordRune(r rune) bool {
	return r == '_' || r == '-' || r == '.' || r == '/' || r == ':' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r >= 0x80
}

// SelectWordAt selects the word (or path) under a position.
func (t *Terminal) SelectWordAt(row, col int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.clampSelPos(SelPos{row, col})
	text := t.rowAt(p.Row)
	text = rowTrim(text)
	if len(text) == 0 || p.Col >= len(text) || !isWordRune(text[p.Col]) {
		t.selAnchor, t.selHead = p, p
		return
	}
	lo, hi := p.Col, p.Col
	for lo > 0 && isWordRune(text[lo-1]) {
		lo--
	}
	for hi < len(text)-1 && isWordRune(text[hi+1]) {
		hi++
	}
	t.selAnchor, t.selHead = SelPos{p.Row, lo}, SelPos{p.Row, hi + 1}
}

// SelectLineAt selects a whole row (triple click).
func (t *Terminal) SelectLineAt(row int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.clampSelPos(SelPos{row, 0})
	t.selAnchor, t.selHead = SelPos{p.Row, 0}, SelPos{p.Row, 1 << 20}
}

// SelectedText returns the selected rows' text joined by newlines.
func (t *Terminal) SelectedText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.HasSelectionLocked() {
		return ""
	}
	start, end := t.selAnchor, t.selHead
	if end.less(start) {
		start, end = end, start
	}
	var parts []string
	for i := start.Row; i <= end.Row; i++ {
		text := t.rowAt(i)
		text = rowTrim(text)
		lo, hi := 0, len(text)
		if i == start.Row && start.Col < len(text) {
			lo = start.Col
		}
		if i == end.Row && end.Col <= len(text) {
			hi = end.Col
		}
		if lo > hi {
			lo = hi
		}
		parts = append(parts, string(text[lo:hi]))
	}
	return strings.Join(parts, "\n")
}

// SetOutputHook installs the redraw hook fired after pty output is
// parsed. The render thread calls this every frame; the reader goroutine
// reads it under the mutex — a bare field assignment from render would
// race the reader.
func (t *Terminal) SetOutputHook(f func()) {
	t.mu.Lock()
	t.OnOutput = f
	t.mu.Unlock()
}

// Apply runs f with the terminal (pane update hook). It intentionally
// does NOT hold the mutex: pane mutations go through the locking helpers.
func (t *Terminal) Apply(f func(*Terminal)) {
	f(t)
}
