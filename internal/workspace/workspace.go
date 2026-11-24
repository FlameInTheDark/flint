// Package workspace assembles the editor example's application state: a
// upstream-style workspace with buffers, tabs, a project panel, menus, modals,
// a terminal dock and the status bar.
package workspace

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/gotools"
	"github.com/FlameInTheDark/flint/internal/terminal"
	"github.com/FlameInTheDark/flint/internal/ui"
)

// Workspace is the editor application's root state.
type Workspace struct {
	// Buffers holds the open tabs; Active indexes the visible one.
	Buffers []*editor.Buffer
	Active  int
	// scrolls holds each buffer's scroll TARGET in rows (fractional: the
	// wheel accumulates sub-row deltas for pixel-precise positions, like
	// upstream's f32 scroll_position). Whole-row paths (keys, caret,
	// scrollbar seeks) write integral values.
	scrolls []float32
	// scrollAnims holds each buffer's RENDERED scroll while a wheel
	// animation eases toward the target. Invariant: when no animation is
	// running the anim equals the target (every settle and every snap
	// writes both), so a static frame is always exactly at the target.
	scrollAnims []float32
	// scrollAnimRuns marks buffers with an in-flight scroll ease.
	scrollAnimRuns []bool
	// lastScrollFrame timestamps the ease integration (frame-rate
	// independent exponential smoothing).
	lastScrollFrame time.Time
	// scrollFrameQueued deduplicates the next-frame callback Render
	// schedules while an ease is in flight (one pending frame at a time).
	scrollFrameQueued bool
	// scrollXs holds each buffer's horizontal pixel offset (wide lines
	// pan; clamped per buffer against its widest line).
	scrollXs []float32
	// imgZooms/imgPans/imgFits hold the image viewers' zoom/pan/fit state
	// per buffer (parallel to Buffers; the pane recomputes geometry).
	imgZooms []float32
	imgPans  [][2]float32
	imgFits  []bool
	// imgDragX/imgDragY track the image pan gesture's last position (the
	// element tree rebuilds every frame; gesture state lives here).
	imgDragX, imgDragY float32

	// Panels.
	ShowTree        bool
	ShowTerminal    bool
	ShowGoOutput    bool
	ShowProblems    bool
	ShowDebug       bool
	ShowTodos       bool
	ShowLineNumbers bool
	// Term is the ACTIVE terminal (Terms[TermIdx] after normalization);
	// Terms holds the dock's terminals (the upstream editor's terminal tabs).
	Term           *terminal.Terminal
	Terms          []*terminal.Terminal
	TermIdx        int
	GoTask         *gotools.Task
	goOutputScroll *elements.ScrollHandle
	goOutputFollow bool
	problemScroll  *elements.ScrollHandle
	goInput        ui.InputState
	debug          debugState
	tools          toolPanels
	diffs          map[*editor.Buffer]*diffTab

	// Panel sizes, adjusted by dragging the dock edges (the upstream editor's docks are
	// user-resizable). The values persist across toggles; effectiveTermHeight
	// / effectiveTreeWidth clamp them to what the window can hold.
	TermHeight float32
	TreeWidth  float32

	// treeTextW caches measured project-panel label widths (the rows'
	// definite widths drive the horizontal scroll extent).
	treeTextW map[string]float32

	// Scroll handles for the scrollable panels. The element tree rebuilds
	// every frame, so handles created inside a render would be fresh each
	// time and every cross-frame request (wheel deltas, drag seeks) would
	// be lost before the next prepaint. Persistent handles live here: the
	// div's prepaint mirrors the live offset/content/viewport into them
	// (one frame of lag for render-time reads), and requests survive
	// re-renders.
	treeScrollHandle *elements.ScrollHandle
	tabScrollHandle  *elements.ScrollHandle

	// ShowFPS toggles the frames-per-second readout (menu bar, top
	// right); FPS is sampled once per second from the window's frame
	// counter by the live run loop.
	ShowFPS bool
	FPS     int

	// Caret blink: CaretVisible flips on a 500ms timer in the live run
	// loop and is forced on around cursor activity (like any editor's
	// blinking caret). Headless mode leaves it on so verification PNGs
	// always show the caret.
	CaretVisible bool
	// lastCaretActivity is the last cursor move/edit time.
	lastCaretActivity time.Time

	// Project (a folder; "" when only files are open).
	Root         string
	Tree         *TreeNode
	launches     config.LaunchProfiles
	launchRoot   string
	launchErr    error
	launchEditor *launchEditorState

	// Overlays.
	Menu   ui.MenuState // menu bar state (Open = index or -1)
	Popup  *ui.PopupState
	Picker *PickerState
	// CommandPalette is the VS Code-style action launcher opened with
	// Ctrl+Shift+P. It deliberately lives alongside the other overlays so
	// filtering and keyboard selection survive a complete frame rebuild.
	CommandPalette *CommandPaletteState
	FileSearch     *FileSearchState
	Prompt         *PromptState
	Find           *editor.FindState
	// FindInput is the find bar's input field state.
	FindInput ui.InputState

	// Text metrics (measured once at startup).
	CharAdvance float32
	LineHeight  float32

	// tabReveal is the Active index the tab strip last revealed: Render
	// edge-detects changes and pans the scrollable strip to the new tab.
	tabReveal int

	// WindowSize is refreshed at each render (pane height math).
	WindowSize geometry.Size[geometry.Pixels]
	// StatusError is a transient message for the status bar.
	StatusError string
	// Language services and their overlays are owned by the UI thread.
	languageDocs    map[*editor.Buffer]*languageDoc
	languageServers map[string]*languageServer
	languageStatus  string
	languageRequest uint64
	completion      *completionState
	documentation   *documentationState
	markdownPreview map[*editor.Buffer]*markdownPreviewState

	// Cfg is the user settings model (loaded at startup, mutated by the
	// settings panel, persisted under ~/.go-gpui/editor/settings.json).
	Cfg config.Config
	// theme is the resolved palette (Cfg.Appearance.Theme → ui.ThemeByName).
	theme ui.Theme
	// measuredFontPx caches the font size the current metrics were
	// measured at, so Render re-measures only after a settings change.
	measuredFontPx int

	// LangMenu is the status-bar language selector panel (nil = closed).
	LangMenu *LangMenuState

	// CustomDecorations draws the in-app title bar (close/min/max, app
	// name) and the platform hides the OS frame; set from the platform
	// window's capability at startup.
	CustomDecorations bool
	// TitleBarH is the custom title bar's height (0 = OS title bar).
	TitleBarH float32

	// Settings modal state.
	SettingsOpen bool
	// SettingsTab selects the sidebar section ("appearance" default).
	SettingsTab string
	// Shell/args input states for the terminal section's text fields.
	shellInput *ui.InputState
	argsInput  *ui.InputState
	// Their focus handles (click-to-focus; minted on first open).
	shellFocus gpui.FocusHandle
	argsFocus  gpui.FocusHandle

	// confirm is the open confirmation dialog (nil = none).
	confirm *ConfirmState

	// Focus handles.
	FocusEditor     gpui.FocusHandle
	FocusTerminal   gpui.FocusHandle
	FocusPopup      gpui.FocusHandle
	FocusModal      gpui.FocusHandle
	FocusFind       gpui.FocusHandle
	FocusLangMenu   gpui.FocusHandle
	FocusCommand    gpui.FocusHandle
	FocusGoInput    gpui.FocusHandle
	FocusFileSearch gpui.FocusHandle

	// Title is the window title base.
	Title string

	// TermSync runs terminal commands synchronously (headless tests).
	TermSync bool

	// Environment hooks (wired at startup; nil-safe).
	// clipboard writes to the system clipboard.
	clipboard func(platform.ClipboardItem)
	// dispatch runs f on the main/UI thread (async terminal output).
	dispatch func(f func())
	// quit closes the application.
	quit    func()
	openURL func(string) error
	// promptPaths/promptPath show the platform's native file dialogs
	// (nil = in-app picker fallback); apply is the entity update the
	// dialog results mutate through.
	apply       func(func(*Workspace))
	promptPaths func(platform.FileDialogOptions) ([]string, bool)
	promptPath  func(platform.FileDialogOptions) (string, bool)
}

// PromptState is a small text-input dialog (new file, rename, go to line).
type PromptState struct {
	Title    string
	Value    ui.InputState
	Submit   string // button label
	OnSubmit func(ws *Workspace, text string) error
	Err      string
}

// ConfirmState is a yes/no dialog (delete file/folder).
type ConfirmState struct {
	Text      string
	OnConfirm func(ws *Workspace) error
}

// LangMenuState is the status-bar language selector: a search filter over
// the editor's language catalog, a scroll offset and a selected row
// (keyboard navigation; ~6 rows visible, the list scrolls).
type LangMenuState struct {
	Filter ui.InputState
	Scroll int
	// Selected is the highlighted row into the filtered list.
	Selected int
}

// SetEnv wires the platform hooks.
func (ws *Workspace) SetEnv(clipboard func(platform.ClipboardItem), dispatch func(f func()), quit func()) {
	ws.clipboard = clipboard
	ws.dispatch = dispatch
	ws.quit = quit
}

// SetDialogs wires the platform's native file dialogs (upstream
// prompt_for_paths / prompt_for_new_path) plus the entity update function
// the mutations apply through. When promptPaths is set, Open File / Open
// Folder use the OS chooser (IFileDialog on Windows); the in-app modal
// picker stays as the fallback for platforms without native dialogs
// (headless, the Linux stubs). Pass nil prompts to force the in-app picker.
func (ws *Workspace) SetDialogs(
	apply func(func(*Workspace)),
	promptPaths func(platform.FileDialogOptions) ([]string, bool),
	promptPath func(platform.FileDialogOptions) (string, bool),
) {
	ws.apply = apply
	ws.promptPaths = promptPaths
	ws.promptPath = promptPath
}

// New creates the empty workspace: no buffers, no project. The editor
// area shows the welcome placeholder (Open File / Open Folder / New File)
// until the user opens or creates something; command-line paths and the
// headless verification seed buffers through OpenPath / direct assignment.
func New(title string) *Workspace {
	cfg := config.Load()
	// Server definitions are ordinary YAML files next to settings.json.
	// Creating the starter Go definition is deliberately best-effort: an
	// unwritable settings directory must never prevent the editor opening.
	_ = config.EnsureLanguageServerDefaults()
	ws := &Workspace{
		Title:            title,
		ShowTree:         cfg.General.ShowTree,
		ShowTerminal:     cfg.General.ShowTerminal,
		ShowLineNumbers:  cfg.General.ShowLineNumbers,
		ShowFPS:          false,
		CaretVisible:     true,
		Menu:             ui.MenuState{Open: -1},
		CharAdvance:      8,
		LineHeight:       18,
		TermHeight:       ui.TerminalHeight,
		TreeWidth:        ui.TreeWidth,
		treeScrollHandle: elements.NewScrollHandle(),
		tabScrollHandle:  elements.NewScrollHandle(),
		goOutputScroll:   elements.NewScrollHandle(),
		goOutputFollow:   true,
		problemScroll:    elements.NewScrollHandle(),
		FocusEditor:      gpui.NewFocusHandle(),
		FocusTerminal:    gpui.NewFocusHandle(),
		FocusPopup:       gpui.NewFocusHandle(),
		FocusModal:       gpui.NewFocusHandle(),
		FocusFind:        gpui.NewFocusHandle(),
		FocusLangMenu:    gpui.NewFocusHandle(),
		FocusCommand:     gpui.NewFocusHandle(),
		FocusGoInput:     gpui.NewFocusHandle(),
		FocusFileSearch:  gpui.NewFocusHandle(),
		Cfg:              cfg,
		theme:            ui.ThemeByName(cfg.Appearance.Theme),
	}
	editor.SetTabSize(cfg.Editor.TabSize)
	return ws
}

// Theme returns the resolved palette.
func (ws *Workspace) Theme() ui.Theme { return ws.theme }

// ApplyConfig applies a settings snapshot to the live workspace: panel
// toggles, theme, tab size, and every open terminal's shell + scrollback.
// The font size re-measures at the next Render (it needs the text
// system). Persists the file too.
func (ws *Workspace) ApplyConfig(cfg config.Config) {
	ws.Cfg = cfg
	ws.theme = ui.ThemeByName(cfg.Appearance.Theme)
	ws.ShowTree = cfg.General.ShowTree
	if cfg.General.ShowTerminal && ws.Term != nil && !ws.ShowDebug && !ws.ShowTodos {
		ws.ShowTerminal = true
	}
	ws.ShowLineNumbers = cfg.General.ShowLineNumbers
	editor.SetTabSize(cfg.Editor.TabSize)
	ws.eachTerminal(func(t *terminal.Terminal) {
		t.SetShell(cfg.Terminal.Shell, cfg.Terminal.ShellArgs)
		t.SetMaxLines(cfg.Terminal.Scrollback)
	})
	_ = cfg.Save()
}

// eachTerminal runs f over every open terminal.
func (ws *Workspace) eachTerminal(f func(*terminal.Terminal)) {
	ws.normalizeTerms()
	for _, t := range ws.Terms {
		f(t)
	}
}

// BenchBuffers returns the default demo buffers (used by the frame-cost
// benchmark; the live run wires the same three buffers).
func BenchBuffers() []*editor.Buffer {
	return []*editor.Buffer{
		editor.NewBuffer("", "main.go", "package main\n\nimport \"fmt\"\n\n// Fib returns the n-th Fibonacci number.\nfunc Fib(n int) int {\n   if n < 2 {\n            return n\n      }\n     a, b := 0, 1\n  for i := 1; i < n; i++ {\n              a, b = b, a+b\n }\n     return b\n}\n\nfunc main() {\n  for i := 0; i < 10; i++ {\n             fmt.Printf(\"fib(%d) = %d\", i, Fib(i))\n       }\n}"),
		editor.NewBuffer("", "util.go", "package util\n\nimport \"strings\"\n\n// Title upper-cases the first rune of every word.\nfunc Title(s string) string {\n      words := strings.Split(s, \" \")\n      for i, w := range words {\n             if w == \"\" {\n                        continue\n              }\n             words[i] = strings.ToUpper(w[:1]) + w[1:]\n     }\n     return strings.Join(words, \" \")\n}"),
		editor.NewBuffer("", "README.md", "# go-gpui\n\nA GPU-accelerated UI framework\nrewritten in Go from the upstream editor's gpui.\n"),
	}
}

// DebugScrollState reports the active buffer's (vertical line, horizontal
// px) scroll for headless verification.
func (ws *Workspace) DebugScrollState() (int, float32) {
	return ws.scroll(), ws.scrollX()
}

// DebugPanX pans the active buffer horizontally by delta px (clamped) for
// headless verification.
func (ws *Workspace) DebugPanX(delta float32) { ws.addScrollX(delta) }

// DebugPaneHeight reports the editor pane's height for headless
// verification (the minimap rail maps the whole file onto this height).
func (ws *Workspace) DebugPaneHeight() float32 { return ws.paneHeight() }

// SetScrolls installs the per-buffer scroll offsets (whole rows; snaps the
// animation — see setScroll).
func (ws *Workspace) SetScrolls(v ...int) {
	s := make([]float32, len(v))
	for i, n := range v {
		s[i] = float32(n)
	}
	ws.scrolls = s
	ws.scrollAnims = append([]float32(nil), s...)
	for len(ws.scrollAnimRuns) < len(v) {
		ws.scrollAnimRuns = append(ws.scrollAnimRuns, false)
	}
	for i := range ws.scrollAnimRuns {
		ws.scrollAnimRuns[i] = false
	}
}

// Measure fills the text metrics through the window's text system.
func (ws *Workspace) Measure(w *gpui.Window) { ws.measure(w) }

// BenchAccentRGB returns the theme accent as 0..1 RGB (bench helper used to
// verify the caret is actually painted).
func BenchAccentRGB() (r, g, b float32) {
	t := ui.DefaultTheme()
	rgb := t.Accent.ToRgb()
	return rgb.R, rgb.G, rgb.B
}

// NoteCaretActivity marks cursor activity: the caret stays solid through
// the blink grace window, then resumes blinking.
func (ws *Workspace) NoteCaretActivity() {
	ws.lastCaretActivity = time.Now()
	ws.CaretVisible = true
}

// TickCaretBlink is the blink timer's tick (live mode only): toggle the
// caret unless the cursor moved within the grace window.
func (ws *Workspace) TickCaretBlink() {
	if ws.lastCaretActivity.IsZero() || time.Since(ws.lastCaretActivity) >= 400*time.Millisecond {
		ws.CaretVisible = !ws.CaretVisible
	}
}

// Buf returns the active buffer (nil-safe).
func (ws *Workspace) Buf() *editor.Buffer {
	if ws.Active < 0 || ws.Active >= len(ws.Buffers) {
		return nil
	}
	return ws.Buffers[ws.Active]
}

// scroll returns the active buffer's scroll target rounded to rows (the
// integer approximation the whole-row paths use).
func (ws *Workspace) scroll() int {
	if ws.Active < len(ws.scrolls) {
		return int(math.Round(float64(ws.scrolls[ws.Active])))
	}
	return 0
}

// maxScrollRows is the active buffer's largest first-visible row.
func (ws *Workspace) maxScrollRows() int {
	b := ws.Buf()
	if b == nil {
		return 0
	}
	visible := editor.Metrics{LineHeight: ws.LineHeight}.VisibleLines(ws.paneHeight())
	max := b.LineCount() - visible
	if max < 0 {
		max = 0
	}
	return max
}

// ensureScrollArrays keeps the per-buffer scroll slices parallel to Buffers.
func (ws *Workspace) ensureScrollArrays() {
	for len(ws.scrolls) < len(ws.Buffers) {
		ws.scrolls = append(ws.scrolls, 0)
	}
	for len(ws.scrollAnims) < len(ws.Buffers) {
		ws.scrollAnims = append(ws.scrollAnims, 0)
	}
	for len(ws.scrollAnimRuns) < len(ws.Buffers) {
		ws.scrollAnimRuns = append(ws.scrollAnimRuns, false)
	}
}

// scrollF returns the active buffer's rendered scroll position (fractional
// rows): the eased animation while one is in flight, else the target.
func (ws *Workspace) scrollF() float32 {
	if ws.Active >= 0 && ws.Active < len(ws.scrollAnimRuns) && ws.scrollAnimRuns[ws.Active] &&
		ws.Active < len(ws.scrollAnims) {
		return ws.scrollAnims[ws.Active]
	}
	if ws.Active < len(ws.scrolls) {
		return ws.scrolls[ws.Active]
	}
	return 0
}

// scrollAnimating reports whether the active buffer's scroll ease is in
// flight (Render keeps requesting frames while it is).
func (ws *Workspace) scrollAnimating() bool {
	return ws.Active >= 0 && ws.Active < len(ws.scrollAnimRuns) && ws.scrollAnimRuns[ws.Active]
}

// scrollEaseTau is the wheel-ease time constant: a wheel notch glides to
// its position in roughly 3τ (~120ms), while trackpad deltas track almost 1:1.
const scrollEaseTau = 40 * time.Millisecond

// addScrollDelta moves the active buffer's scroll target by a wheel delta
// (fractional rows) and lets the rendered position ease toward it. The ease
// starts from the currently rendered position, so a trackpad's dense small
// deltas track directly while a mouse notch glides softly.
func (ws *Workspace) addScrollDelta(df float32) {
	ws.ensureScrollArrays()
	if ws.Buf() == nil {
		return
	}
	maxF := float32(ws.maxScrollRows())
	prev := ws.scrollF()
	target := ws.scrolls[ws.Active] + df
	if target < 0 {
		target = 0
	}
	if target > maxF {
		target = maxF
	}
	ws.scrolls[ws.Active] = target
	if !ws.scrollAnimRuns[ws.Active] {
		ws.scrollAnims[ws.Active] = prev
		ws.scrollAnimRuns[ws.Active] = true
		ws.lastScrollFrame = time.Now()
	}
}

// advanceScrollAnim integrates one frame of the wheel ease at the wall clock.
func (ws *Workspace) advanceScrollAnim() { ws.advanceScrollAnimAt(time.Now()) }

// advanceScrollAnimAt integrates one frame of the wheel ease (exponential
// approach, frame-rate independent via dt). The injectable clock keeps the
// ease deterministic under test. Called from a next-frame callback Render
// schedules while the ease is in flight.
func (ws *Workspace) advanceScrollAnimAt(now time.Time) {
	if !ws.scrollAnimating() {
		return
	}
	dt := now.Sub(ws.lastScrollFrame)
	if dt <= 0 {
		dt = time.Millisecond
	}
	if dt > 250*time.Millisecond {
		dt = 250 * time.Millisecond // a stall (tab switch, breakpoint) must not teleport the ease
	}
	ws.lastScrollFrame = now
	alpha := 1 - float32(math.Exp(-float64(dt)/float64(scrollEaseTau)))
	a := ws.scrollAnims[ws.Active] + (ws.scrolls[ws.Active]-ws.scrollAnims[ws.Active])*alpha
	if math.Abs(float64(ws.scrolls[ws.Active]-a)) < 0.005 {
		a = ws.scrolls[ws.Active]
		ws.scrollAnimRuns[ws.Active] = false
	}
	ws.scrollAnims[ws.Active] = a
}

// setScroll updates the active buffer's scroll to a whole row and SNAPS the
// rendered position to it: keys, caret moves, scrollbar and minimap seeks
// must be exact, only the wheel glides (see addScrollDelta).
func (ws *Workspace) setScroll(v int) {
	ws.ensureScrollArrays()
	if ws.Buf() == nil {
		return
	}
	max := ws.maxScrollRows()
	if v < 0 {
		v = 0
	}
	if v > max {
		v = max
	}
	ws.scrolls[ws.Active] = float32(v)
	ws.scrollAnims[ws.Active] = float32(v)
	ws.scrollAnimRuns[ws.Active] = false
}

// scrollX returns the active buffer's horizontal pixel offset.
func (ws *Workspace) scrollX() float32 {
	if ws.Active < len(ws.scrollXs) {
		return ws.scrollXs[ws.Active]
	}
	return 0
}

// clampF clamps v into [lo, hi] (hi < lo collapses to lo).
func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// chromeTop is the fixed chrome above the editor pane: the menu bar
// plus the custom title bar when client-side decorations are on.
func (ws *Workspace) chromeTop() float32 { return ui.MenuBarHeight + ws.TitleBarH }

// effectiveTermHeight is the rendered terminal dock height: the user's
// resized height clamped to what the window can hold. Without the clamp a
// small window let the fixed-height dock push the status bar (or the dock
// itself) out of the window — the clipped-under-the-bottom-line bug.
func (ws *Workspace) effectiveTermHeight() float32 {
	h := ws.TermHeight
	if h <= 0 {
		h = ui.TerminalHeight
	}
	winH := float32(ws.WindowSize.Height.AsF32())
	if winH <= 0 {
		winH = 600
	}
	max := winH - ws.chromeTop() - ui.TabBarHeight - ui.StatusHeight - ui.MinEditorHeight
	return clampF(h, ui.TermMinHeight, max)
}

// effectiveTreeWidth is the rendered project panel width (see
// effectiveTermHeight).
func (ws *Workspace) effectiveTreeWidth() float32 {
	w := ws.TreeWidth
	if w <= 0 {
		w = ui.TreeWidth
	}
	winW := float32(ws.WindowSize.Width.AsF32())
	if winW <= 0 {
		winW = 960
	}
	max := winW - ws.railWidth() - ui.MinEditorWidth
	return clampF(w, ui.TreeMinWidth, max)
}

// SetTermHeight resizes the terminal dock from a drag (y = pointer window y
// on the dock's top edge): the edge sticks to the pointer. The stored
// height clamps to the window's capacity so the drag can never push the
// status bar or itself out of the window.
func (ws *Workspace) SetTermHeight(pointerY float32) {
	winH := float32(ws.WindowSize.Height.AsF32())
	if winH <= 0 {
		winH = 600
	}
	max := winH - ws.chromeTop() - ui.TabBarHeight - ui.StatusHeight - ui.MinEditorHeight
	ws.TermHeight = clampF(winH-ui.StatusHeight-pointerY, ui.TermMinHeight, max)
}

// SetTreeWidth resizes the project panel from a drag (x = pointer window x
// on the panel's inner edge); clamped by the effective renderer.
func (ws *Workspace) SetTreeWidth(pointerX float32) {
	ws.TreeWidth = pointerX - ws.railWidth()
}

// DebugPanelSizes reports the effective dock sizes (headless verification).
func (ws *Workspace) DebugPanelSizes() (termH, treeW float32) {
	return ws.effectiveTermHeight(), ws.effectiveTreeWidth()
}

// paneWidth is the editor pane's width (window minus the project panel).
func (ws *Workspace) paneWidth() float32 {
	w := float32(ws.WindowSize.Width.AsF32())
	if w <= 0 {
		w = 960
	}
	return w - ws.paneLeft()
}

// maxScrollX is the horizontal pan extent of the active buffer.
func (ws *Workspace) maxScrollX() float32 {
	b := ws.Buf()
	if b == nil {
		return 0
	}
	content := float32(b.MaxLineCols())*ws.CharAdvance + 16
	code := ws.paneWidth() - editor.RailWidth - 1
	if ws.ShowLineNumbers {
		code -= editor.GutterWidth(b.LineCount(), ws.CharAdvance)
	} else {
		code -= editor.GutterLeftInset
	}
	if code < 40 {
		code = 40
	}
	m := content - code
	if m < 0 {
		return 0
	}
	return m
}

// scrollCaretIntoViewX pans horizontally so the caret stays visible when
// typing or moving past the right edge (like any editor's horizontal
// auto-scroll).
func (ws *Workspace) scrollCaretIntoViewX() {
	b := ws.Buf()
	if b == nil {
		return
	}
	for len(ws.scrollXs) < len(ws.Buffers) {
		ws.scrollXs = append(ws.scrollXs, 0)
	}
	line, col := b.OffsetToLC(b.Cursor)
	runeCol := utf8.RuneCountInString(b.Line(line)[:col])
	caretX := float32(editor.DisplayRuneCol(b.Line(line), runeCol))*ws.CharAdvance + 8
	code := ws.paneWidth() - editor.RailWidth - 1
	if ws.ShowLineNumbers {
		code -= editor.GutterWidth(b.LineCount(), ws.CharAdvance)
	} else {
		code -= editor.GutterLeftInset
	}
	if code < 40 {
		code = 40
	}
	scroll := ws.scrollX()
	if caretX-8 < scroll {
		scroll = caretX - 8
	}
	if caretX > scroll+code-8 {
		scroll = caretX - code + 8
	}
	ws.scrollXs[ws.Active] = scroll
}

// addScrollX applies a horizontal pixel delta, clamped to the content.
func (ws *Workspace) addScrollX(delta float32) {
	for len(ws.scrollXs) < len(ws.Buffers) {
		ws.scrollXs = append(ws.scrollXs, 0)
	}
	v := ws.scrollX() + delta
	if v < 0 {
		v = 0
	}
	if m := ws.maxScrollX(); v > m {
		v = m
	}
	ws.scrollXs[ws.Active] = v
}

// paneHeight computes the editor pane's height from the window size and
// the visible chrome (all fixed-height rows).
func (ws *Workspace) paneHeight() float32 {
	h := float32(ws.WindowSize.Height.AsF32())
	if h <= 0 {
		h = 600
	}
	h -= ws.chromeTop() + ui.TabBarHeight + ui.StatusHeight
	if ws.Find != nil {
		h -= ui.FindBarHeight
	}
	if ws.ShowTerminal || ws.ShowGoOutput || ws.ShowProblems || ws.ShowDebug || ws.ShowTodos {
		h -= ws.effectiveTermHeight()
	}
	if h < 40 {
		h = 40
	}
	return h
}

// ---------------------------------------------------------------------------
// Tabs and buffers
// ---------------------------------------------------------------------------

// OpenPath opens a file or a folder (folders become the project root).
func (ws *Workspace) OpenPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return ws.OpenFolder(path)
	}
	ws.OpenFile(path)
	return nil
}

// OpenFile opens a file in a new tab (or focuses an existing tab).
// Unknown binaries open as a read-only hex dump; supported images open
// in the zoomable viewer (see editor.OpenBuffer).
func sameFilePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, ae := filepath.Abs(a)
	bb, be := filepath.Abs(b)
	if ae != nil || be != nil {
		return a == b
	}
	return aa == bb || (runtime.GOOS == "windows" && strings.EqualFold(aa, bb))
}

func (ws *Workspace) OpenFile(path string) {
	for i, b := range ws.Buffers {
		if sameFilePath(b.Path, path) {
			if ws.Active != i {
				ws.completion = nil
				ws.documentation = nil
				ws.languageRequest++
			}
			ws.Active = i
			return
		}
	}
	b, err := editor.OpenBuffer(path)
	if err != nil {
		ws.SetPromptError(err.Error())
		return
	}
	ws.addBuffer(b)
}

// addBuffer opens an already loaded buffer, preserving all per-tab state.
func (ws *Workspace) addBuffer(b *editor.Buffer) {
	ws.Buffers = append(ws.Buffers, b)
	ws.completion = nil
	ws.documentation = nil
	ws.languageRequest++
	ws.ensureScrollArrays()
	ws.scrollXs = append(ws.scrollXs, 0)
	ws.imgZooms = append(ws.imgZooms, 0)
	ws.imgPans = append(ws.imgPans, [2]float32{})
	ws.imgFits = append(ws.imgFits, true)
	ws.Active = len(ws.Buffers) - 1
	ws.startLanguage(b)
}

// OpenFolder sets the project root and shows the project panel.
func (ws *Workspace) OpenFolder(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return fmt.Errorf("not a folder: %s", abs)
	}
	ws.closeFileSearch()
	ws.Root = abs
	ws.tools.resetProject()
	ws.ShowTodos = false
	ws.launchEditor = nil
	ws.loadLaunchProfiles()
	ws.Tree = NewTree(abs)
	ws.ShowTree = true
	return nil
}

// NewFile adds an empty untitled buffer.
func (ws *Workspace) NewFile() {
	name := "untitled"
	for n := 2; ; n++ {
		taken := false
		for _, b := range ws.Buffers {
			if b.Name == name {
				taken = true
				break
			}
		}
		if !taken {
			break
		}
		name = fmt.Sprintf("untitled-%d", n)
	}
	ws.Buffers = append(ws.Buffers, editor.NewBuffer("", name, ""))
	ws.ensureScrollArrays()
	ws.scrollXs = append(ws.scrollXs, 0)
	ws.imgZooms = append(ws.imgZooms, 0)
	ws.imgPans = append(ws.imgPans, [2]float32{})
	ws.imgFits = append(ws.imgFits, true)
	ws.Active = len(ws.Buffers) - 1
}

func (ws *Workspace) toggleMarkdownPreview(b *editor.Buffer) {
	if ws.markdownPreview == nil {
		ws.markdownPreview = make(map[*editor.Buffer]*markdownPreviewState)
	}
	state := ws.markdownPreview[b]
	if state == nil {
		state = &markdownPreviewState{}
		ws.markdownPreview[b] = state
	}
	state.enabled = !state.enabled
	ws.completion = nil
	ws.documentation = nil
	ws.languageRequest++
}

// CloseEditor closes the active tab. Closing the last tab leaves the
// workspace with zero buffers: the empty-state placeholder (Open File /
// Open Folder / New File) appears, like the upstream editor's empty workspace.
func (ws *Workspace) CloseEditor() {
	if len(ws.Buffers) == 0 {
		return
	}
	idx := ws.Active
	ws.closeLanguage(ws.Buffers[idx])
	ws.closeDiff(ws.Buffers[idx])
	delete(ws.markdownPreview, ws.Buffers[idx])
	ws.completion = nil
	ws.documentation = nil
	ws.Buffers = append(ws.Buffers[:idx], ws.Buffers[idx+1:]...)
	if idx < len(ws.scrolls) {
		ws.scrolls = append(ws.scrolls[:idx], ws.scrolls[idx+1:]...)
	}
	if idx < len(ws.scrollAnims) {
		ws.scrollAnims = append(ws.scrollAnims[:idx], ws.scrollAnims[idx+1:]...)
	}
	if idx < len(ws.scrollAnimRuns) {
		ws.scrollAnimRuns = append(ws.scrollAnimRuns[:idx], ws.scrollAnimRuns[idx+1:]...)
	}
	if idx < len(ws.scrollXs) {
		ws.scrollXs = append(ws.scrollXs[:idx], ws.scrollXs[idx+1:]...)
	}
	if idx < len(ws.imgZooms) {
		ws.imgZooms = append(ws.imgZooms[:idx], ws.imgZooms[idx+1:]...)
	}
	if idx < len(ws.imgPans) {
		ws.imgPans = append(ws.imgPans[:idx], ws.imgPans[idx+1:]...)
	}
	if idx < len(ws.imgFits) {
		ws.imgFits = append(ws.imgFits[:idx], ws.imgFits[idx+1:]...)
	}
	if len(ws.Buffers) == 0 {
		ws.Active = 0
		return
	}
	if ws.Active >= len(ws.Buffers) {
		ws.Active = len(ws.Buffers) - 1
	}
}

// Save persists the active buffer (no-op for untitled).
func (ws *Workspace) Save() {
	b := ws.Buf()
	if b == nil {
		return
	}
	if b.Path == "" || !b.Dirty {
		return // saving untitled buffers needs a path prompt (out of scope)
	}
	if err := b.Save(); err != nil {
		ws.SetPromptError(err.Error())
	} else {
		ws.tools.gitRefresh = true
		if ws.languageDocs[b] != nil {
			ws.requestLanguage(b, "save", b.Cursor, 0, 0)
		}
	}
}

// SaveAll persists every dirty buffer.
func (ws *Workspace) SaveAll() {
	for _, b := range ws.Buffers {
		if b.Path != "" && b.Dirty {
			if b.Save() == nil {
				ws.tools.gitRefresh = true
				if ws.languageDocs[b] != nil {
					ws.requestLanguage(b, "save", b.Cursor, 0, 0)
				}
			}
		}
	}
}

// SetPromptError surfaces an error in the status bar.
func (ws *Workspace) SetPromptError(msg string) {
	ws.StatusError = msg
}

// ---------------------------------------------------------------------------
// Editing actions (menu + shortcuts share these)
// ---------------------------------------------------------------------------

// Edit applies f to the active buffer.
func (ws *Workspace) Edit(f func(*editor.Buffer)) {
	if b := ws.Buf(); b != nil {
		revision, cursor := b.Revision, b.Cursor
		f(b)
		ws.changedLanguage(b, revision, cursor)
	}
}

// Copy copies the selection to the clipboard.
func (ws *Workspace) Copy(w *gpui.Window) {
	if b := ws.Buf(); b != nil && b.HasSelection() {
		if pw := w.PlatformWindow(); pw != nil {
			pw.WriteClipboard(platform.ClipboardItem{Text: b.SelectedText()})
		}
	}
}

// Cut copies then deletes the selection.
func (ws *Workspace) Cut(w *gpui.Window) {
	b := ws.Buf()
	if b == nil || !b.HasSelection() {
		return
	}
	if pw := w.PlatformWindow(); pw != nil {
		pw.WriteClipboard(platform.ClipboardItem{Text: b.SelectedText()})
	}
	ws.Edit(func(b *editor.Buffer) { b.Insert("") })
}

// Paste inserts the clipboard at the cursor.
func (ws *Workspace) Paste(w *gpui.Window) {
	b := ws.Buf()
	if b == nil {
		return
	}
	if pw := w.PlatformWindow(); pw != nil {
		if item, ok := pw.ReadClipboard(); ok && item.Text != "" {
			ws.Edit(func(b *editor.Buffer) { b.Insert(item.Text) })
		}
	}
}

// ---------------------------------------------------------------------------
// Panels and overlays
// ---------------------------------------------------------------------------

// ToggleTree shows/hides the project panel.
func (ws *Workspace) ToggleTree() {
	wasOther := ws.tools.left != ""
	ws.tools.left = ""
	ws.ShowTree = wasOther || !ws.ShowTree
}

// ToggleTerminal shows/hides the terminal dock.
func (ws *Workspace) ToggleTerminal(w *gpui.Window) {
	if ws.ShowGoOutput || ws.ShowProblems || ws.ShowDebug || ws.ShowTodos {
		ws.ShowGoOutput = false
		ws.ShowProblems = false
		ws.ShowDebug = false
		ws.ShowTodos = false
		ws.ShowTerminal = true
	} else {
		ws.ShowTerminal = !ws.ShowTerminal
	}
	if ws.ShowTerminal {
		if ws.Term == nil {
			ws.NewTerminal()
		}
		if w != nil {
			w.Focus(ws.FocusTerminal)
		}
	} else if w != nil {
		w.Focus(ws.FocusEditor)
	}
}

// normalizeTerms rebuilds the Terms list around a directly-assigned Term
// (tests and older callers set ws.Term alone): one terminal in the list,
// selected.
func (ws *Workspace) normalizeTerms() {
	if len(ws.Terms) == 0 {
		if ws.Term == nil {
			return
		}
		ws.Terms = []*terminal.Terminal{ws.Term}
		ws.TermIdx = 0
		return
	}
	if ws.TermIdx >= len(ws.Terms) {
		ws.TermIdx = len(ws.Terms) - 1
	}
	if ws.TermIdx < 0 {
		ws.TermIdx = 0
	}
	ws.Term = ws.Terms[ws.TermIdx]
}

// NewTerminal appends a terminal to the dock and selects it (the dock's
// "+" button; the upstream editor opens each terminal in the project's working directory).
func (ws *Workspace) NewTerminal() {
	cwd := ws.Root
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	ws.Terms = append(ws.Terms, terminal.New(cwd))
	ws.TermIdx = len(ws.Terms) - 1
	ws.Term = ws.Terms[ws.TermIdx]
}

// SelectTerminal switches the dock's active terminal tab.
func (ws *Workspace) SelectTerminal(i int) {
	if i < 0 || i >= len(ws.Terms) {
		return
	}
	ws.TermIdx = i
	ws.Term = ws.Terms[i]
}

// CloseTerminal removes the i-th terminal; closing the last one hides the
// dock (like the upstream editor closing the terminal pane). The terminal's persistent
// shell process is terminated with it.
func (ws *Workspace) CloseTerminal(i int) {
	if i < 0 || i >= len(ws.Terms) {
		return
	}
	ws.Terms[i].Close()
	ws.Terms = append(ws.Terms[:i], ws.Terms[i+1:]...)
	if len(ws.Terms) == 0 {
		ws.Term = nil
		ws.TermIdx = 0
		ws.ShowTerminal = false
		return
	}
	if ws.TermIdx >= len(ws.Terms) {
		ws.TermIdx = len(ws.Terms) - 1
	} else if ws.TermIdx > i {
		ws.TermIdx--
	}
	ws.Term = ws.Terms[ws.TermIdx]
}

// terminalTab is one dock tab's render info.
type terminalTab struct {
	Title  string
	Active bool
}

// terminalTabs normalizes the terminal list and derives the tab titles:
// each tab shows its working directory's base name, suffixed when two
// terminals share it.
func (ws *Workspace) terminalTabs() []terminalTab {
	ws.normalizeTerms()
	if len(ws.Terms) == 0 {
		return nil
	}
	tabs := make([]terminalTab, len(ws.Terms))
	counts := map[string]int{}
	for i, term := range ws.Terms {
		name := term.WorkingDirectory()
		for j := len(name) - 1; j >= 0; j-- {
			if name[j] == '/' || name[j] == '\\' {
				name = name[j+1:]
				break
			}
		}
		if name == "" {
			name = "terminal"
		}
		counts[name]++
		n := counts[name]
		if n > 1 {
			name = fmt.Sprintf("%s %d", name, n)
		}
		tabs[i] = terminalTab{Title: name, Active: i == ws.TermIdx}
	}
	return tabs
}

// ToggleLineNumbers shows/hides the gutter.
func (ws *Workspace) ToggleLineNumbers() { ws.ShowLineNumbers = !ws.ShowLineNumbers }

// OpenFind opens the find bar (prefilled with the selection).
func (ws *Workspace) OpenFind() {
	query := ""
	if b := ws.Buf(); b != nil && b.HasSelection() {
		query = b.SelectedText()
		if strings.Contains(query, "\n") {
			query = query[:strings.IndexByte(query, '\n')]
		}
	}
	ws.Find = &editor.FindState{Query: query, Current: -1}
	ws.FindInput.Set(query)
	if query != "" {
		ws.RefreshFind()
	}
}

// CloseFind closes the find bar.
func (ws *Workspace) CloseFind() { ws.Find = nil }

// RefreshFind recomputes matches from the query.
func (ws *Workspace) RefreshFind() {
	if ws.Find == nil {
		return
	}
	b := ws.Buf()
	if b == nil || ws.Find.Query == "" {
		ws.Find.Matches = nil
		ws.Find.Current = -1
		return
	}
	ws.Find.Matches = editor.FindMatches(b.Text(), ws.Find.Query)
	ws.Find.Current = firstMatchAtOrAfter(ws.Find.Matches, b.Cursor)
}

// FindNext jumps to the next match.
func (ws *Workspace) FindNext() {
	if ws.Find == nil || len(ws.Find.Matches) == 0 {
		return
	}
	ws.Find.Current = (ws.Find.Current + 1) % len(ws.Find.Matches)
	ws.selectCurrentMatch()
}

// FindPrev jumps to the previous match.
func (ws *Workspace) FindPrev() {
	if ws.Find == nil || len(ws.Find.Matches) == 0 {
		return
	}
	ws.Find.Current = (ws.Find.Current - 1 + len(ws.Find.Matches)) % len(ws.Find.Matches)
	ws.selectCurrentMatch()
}

func (ws *Workspace) selectCurrentMatch() {
	b := ws.Buf()
	if b == nil || ws.Find == nil || ws.Find.Current < 0 || ws.Find.Current >= len(ws.Find.Matches) {
		return
	}
	m := ws.Find.Matches[ws.Find.Current]
	b.SetCursor(m[0])
	b.Anchor = m[1]
	// Keep the match visible.
	line, _ := b.OffsetToLC(b.Cursor)
	visible := editor.Metrics{LineHeight: ws.LineHeight}.VisibleLines(ws.paneHeight())
	if line < ws.scroll() {
		ws.setScroll(line)
	} else if line >= ws.scroll()+visible {
		ws.setScroll(line - visible + 1)
	}
}

func firstMatchAtOrAfter(matches [][2]int, off int) int {
	for i, m := range matches {
		if m[0] >= off {
			return i
		}
	}
	if len(matches) > 0 {
		return 0
	}
	return -1
}

// ---------------------------------------------------------------------------
// Go to line
// ---------------------------------------------------------------------------

// GoToLine jumps to a 1-based line number ("n" or "n:m").
func (ws *Workspace) GoToLine(spec string) bool {
	b := ws.Buf()
	if b == nil {
		return false
	}
	line := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(spec), "%d", &line); err != nil || line < 1 {
		return false
	}
	if line > b.LineCount() {
		line = b.LineCount()
	}
	b.SetCursor(b.LineStart(line - 1))
	visible := editor.Metrics{LineHeight: ws.LineHeight}.VisibleLines(ws.paneHeight())
	if line-1 < ws.scroll() {
		ws.setScroll(line - 1)
	} else if line-1 >= ws.scroll()+visible {
		ws.setScroll(line - 1 - visible + 1)
	}
	return true
}

// Prompt opens a text-input dialog.
func (ws *Workspace) OpenPrompt(title, value, submit string, onSubmit func(*Workspace, string) error) {
	p := &PromptState{
		Title:    title,
		Submit:   submit,
		OnSubmit: onSubmit,
	}
	p.Value.Set(value)
	ws.Prompt = p
}

// Confirm opens a confirmation dialog.
func (ws *Workspace) Confirm(text string, onConfirm func(*Workspace) error) {
	ws.Prompt = nil
	ws.confirm = &ConfirmState{Text: text, OnConfirm: onConfirm}
}

// ---------------------------------------------------------------------------
// Image viewer state (zoom/pan/fit, parallel to Buffers)
// ---------------------------------------------------------------------------

func (ws *Workspace) growImgArrays() {
	for len(ws.imgZooms) < len(ws.Buffers) {
		ws.imgZooms = append(ws.imgZooms, 0)
	}
	for len(ws.imgPans) < len(ws.Buffers) {
		ws.imgPans = append(ws.imgPans, [2]float32{})
	}
	for len(ws.imgFits) < len(ws.Buffers) {
		ws.imgFits = append(ws.imgFits, true)
	}
}

func (ws *Workspace) imgZoom(i int) float32 {
	ws.growImgArrays()
	if i < 0 || i >= len(ws.imgZooms) {
		return 0
	}
	return ws.imgZooms[i]
}

func (ws *Workspace) imgPan(i int) [2]float32 {
	ws.growImgArrays()
	if i < 0 || i >= len(ws.imgPans) {
		return [2]float32{}
	}
	return ws.imgPans[i]
}

func (ws *Workspace) imgFit(i int) bool {
	ws.growImgArrays()
	if i < 0 || i >= len(ws.imgFits) {
		return true
	}
	return ws.imgFits[i]
}

func (ws *Workspace) setImgZoom(z float32) {
	ws.growImgArrays()
	if ws.Active >= 0 && ws.Active < len(ws.imgZooms) {
		ws.imgZooms[ws.Active] = z
	}
}

func (ws *Workspace) setImgPan(x, y float32) {
	ws.growImgArrays()
	if ws.Active >= 0 && ws.Active < len(ws.imgPans) {
		ws.imgPans[ws.Active] = [2]float32{x, y}
	}
}

func (ws *Workspace) setImgFit(f bool) {
	ws.growImgArrays()
	if ws.Active >= 0 && ws.Active < len(ws.imgFits) {
		ws.imgFits[ws.Active] = f
	}
}

// toggleImgFit flips between fit-to-window and 1:1 (double-click, reset).
func (ws *Workspace) toggleImgFit() {
	ws.setImgFit(!ws.imgFit(ws.Active))
	if ws.imgFit(ws.Active) {
		ws.setImgPan(0, 0)
	} else if z := ws.imgZoom(ws.Active); z == 0 {
		ws.setImgZoom(1)
	}
}

// ---------------------------------------------------------------------------
// Language selector (status bar)
// ---------------------------------------------------------------------------

// OpenLangMenu opens the status-bar language selector for the active
// buffer (binary buffers keep the panel closed — they have no language).
func (ws *Workspace) OpenLangMenu() {
	if b := ws.Buf(); b != nil && b.Kind != editor.KindText {
		return
	}
	ws.LangMenu = &LangMenuState{}
}

// SelectLangMenu applies the i-th filtered language to the active buffer.
func (ws *Workspace) SelectLangMenu(i int) {
	b := ws.Buf()
	if b == nil || ws.LangMenu == nil {
		return
	}
	langs := filteredLanguages(ws.LangMenu.Filter.String())
	if i < 0 || i >= len(langs) {
		return
	}
	b.SetLanguageOverride(langs[i].Id)
	ws.CloseLangMenu()
}

// CloseLangMenu dismisses the selector.
func (ws *Workspace) CloseLangMenu() { ws.LangMenu = nil }

// filteredLanguages filters the catalog by a case-insensitive substring
// over the display name and the extensions. The result is alphabetical
// (the upstream editor's language selector lists languages A→Z, not catalog order).
func filteredLanguages(filter string) []editor.LanguageInfo {
	filter = strings.ToLower(strings.TrimSpace(filter))
	out := make([]editor.LanguageInfo, len(editor.Languages))
	copy(out, editor.Languages)
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if filter == "" {
		return out
	}
	var matched []editor.LanguageInfo
	for _, l := range out {
		if strings.Contains(strings.ToLower(l.Name), filter) {
			matched = append(matched, l)
			continue
		}
		for _, ext := range l.Exts {
			if strings.Contains(ext, filter) {
				matched = append(matched, l)
				break
			}
		}
	}
	return matched
}

// Debug image-viewer hooks (headless verification).
func (ws *Workspace) DebugImgFit() bool           { return ws.imgFit(ws.Active) }
func (ws *Workspace) DebugToggleImgFit()          { ws.toggleImgFit() }
func (ws *Workspace) DebugSetImgZoom(z float32)   { ws.setImgZoom(z) }
func (ws *Workspace) DebugSetImgPan(x, y float32) { ws.setImgPan(x, y) }
func (ws *Workspace) DebugImgZoom() float32       { return ws.imgZoom(ws.Active) }
func (ws *Workspace) DebugImgPan() (float32, float32) {
	p := ws.imgPan(ws.Active)
	return p[0], p[1]
}

// SetCustomDecorationsForTest force-enables the in-app title bar on a
// headless window (the platform capability is what drives it live).
func (ws *Workspace) SetCustomDecorationsForTest() {
	ws.CustomDecorations = true
	ws.TitleBarH = ui.TitleBarHeight
}

// SetWindowSizeForTest seeds the window size for layout math (the renderer
// normally fills it each frame).
func (ws *Workspace) SetWindowSizeForTest(w, h float32) {
	ws.WindowSize = geometry.SizePixels(w, h)
}
