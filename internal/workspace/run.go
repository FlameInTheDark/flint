package workspace

import (
        "flag"
        "fmt"
        "os"
        "runtime"
        "sync"
        "time"

        "github.com/FlameInTheDark/go-gpui/app"
        "github.com/FlameInTheDark/go-gpui/font"
        "github.com/FlameInTheDark/go-gpui/geometry"
        "github.com/FlameInTheDark/go-gpui/gpui"
        "github.com/FlameInTheDark/go-gpui/platform"
        "github.com/FlameInTheDark/go-gpui/style"
        "github.com/FlameInTheDark/go-gpui/text"

        "github.com/FlameInTheDark/flint/internal/demo"
        "github.com/FlameInTheDark/flint/internal/editor"
        "github.com/FlameInTheDark/flint/internal/ui"
)

// demoMain, demoUtil and demoReadme are the buffers the headless frame
// verification seeds (the interactive editor starts empty; see Run).
const (
        demoMain = `package main

import "fmt"

// Fib returns the n-th Fibonacci number.
func Fib(n int) int {
        if n < 2 {
                return n
        }
        a, b := 0, 1
        for i := 1; i < n; i++ {
                a, b = b, a+b
        }
        return b
}

func main() {
        for i := 0; i < 10; i++ {
                fmt.Printf("fib(%d) = %d", i, Fib(i))
        }
}`

        demoUtil = `package util

import "strings"

// Title upper-cases the first rune of every word.
func Title(s string) string {
        words := strings.Split(s, " ")
        for i, w := range words {
                if w == "" {
                        continue
                }
                words[i] = strings.ToUpper(w[:1]) + w[1:]
        }
        return strings.Join(words, " ")
}`

        demoReadme = `# Flint

A code editor built with go-gpui.

Select text by dragging over it.
Double-click selects a word.
Right-click for the context menu.
Open folders from the File menu.
Toggle the terminal with Ctrl+` + "`"
)

// Run is Flint's entry point. It parses the
// flags (--headless, --out, --timeout) plus optional positional paths:
// files open as buffers, folders become the project root.
func Run() {
        // A double-clicked console-subsystem binary drags an empty console
        // window next to the app: detach it (see console_windows.go; building
        // with -ldflags "-H windowsgui" avoids the flash entirely).
        hideConsole()
        // GUI-subsystem builds have no console: redirect stderr into a log
        // file (panic traces) and guard the entry goroutine with a
        // MessageBox (exit code 2 used to be completely silent).
        crashLog()
        defer crashRecover()

        // Custom window decorations (Windows): the app draws its own title
        // bar with close/minimize/maximize and the app name; the OS frame is
        // hidden. GPUI_NATIVE_DECORATIONS=1 restores the OS frame (debug).
        useCustomDecorations := runtime.GOOS == "windows" &&
                os.Getenv("GPUI_NATIVE_DECORATIONS") == ""

        // shouldClose vetoes the window close while buffers hold unsaved
        // changes: a prompt (upstream window.prompt) asks what to do, and
        // quitting resumes once answered (Discard & Quit / Cancel).
        shouldClose := func() bool { return true }
        a := demo.Start[Workspace](demo.Config{
                Name:              "flint",
                Title:             "Flint",
                Size:              geometry.SizePixels(1100, 700),
                OnShouldClose:     func() bool { return shouldClose() },
                CustomDecorations: useCustomDecorations,
        }, func(w *gpui.Window, cx *app.Context[Workspace]) Workspace {
                ws := New("Flint")
                ws.measure(w)
                // Client-side decorations: the window confirms support and the
                // workspace reserves the title bar's row in the layout math.
                if pw := w.PlatformWindow(); pw != nil && pw.SupportsCustomDecorations() {
                        ws.CustomDecorations = true
                        ws.TitleBarH = ui.TitleBarHeight
                }
                // The editor owns focus from the start (typing works immediately).
                w.Focus(ws.FocusEditor)
                return *ws
        })
        cx := a.App.App
        handle := a.Handle

        update := func(f func(*Workspace)) {
                handle.View.Update(cx, func(state *Workspace, c *app.Context[Workspace]) any {
                        f(state)
                        c.Notify()
                        return nil
                })
        }

        // Headless dispatch must be SERIALIZED onto the goroutine that draws:
        // the headless platform's DispatchOnMainThread runs inline on the
        // CALLING goroutine, so a background completion (a git status scan, a
        // diff subprocess, a PTY read) would mutate workspace state while the
        // scene driver paints — a fatal concurrent map read/write. The queue
        // drains at the start of every frame, which is exactly the between-
        // frames moment the real platforms deliver posted closures in.
        headlessMode := a.Headless != nil
        var dispatchQueue []func()
        var dispatchMu sync.Mutex
        drainDispatch := func() {
                dispatchMu.Lock()
                if len(dispatchQueue) == 0 {
                        dispatchMu.Unlock()
                        return
                }
                queued := dispatchQueue
                dispatchQueue = nil
                dispatchMu.Unlock()
                for _, f := range queued {
                        f()
                }
        }
        dispatch := func(f func()) { a.Platform.DispatchOnMainThread(f) }
        if headlessMode {
                dispatch = func(f func()) {
                        dispatchMu.Lock()
                        dispatchQueue = append(dispatchQueue, f)
                        dispatchMu.Unlock()
                }
        }

        // Wire the environment hooks (platform access needs the started app).
        update(func(ws *Workspace) {
                ws.SetEnv(
                        func(item platform.ClipboardItem) {
                                if pw := handle.Window.PlatformWindow(); pw != nil {
                                        pw.WriteClipboard(item)
                                }
                        },
                        dispatch,
                        func() { a.Platform.Quit() },
                )
                ws.openURL = a.Platform.OpenURL
                // Native file dialogs: only the interactive platforms have
                // them; headless verification keeps the in-app picker so the
                // modal stays testable. The apply hook is wired in both cases
                // (openPicker mutates through it after the dialog returns).
                if headlessMode {
                        ws.SetDialogs(update, nil, nil)
                } else {
                        ws.SetDialogs(update, a.Platform.PromptForPaths, a.Platform.PromptForPath)
                }
                ws.TermSync = headlessMode
                ws.tools.rail = !headlessMode // keep the scripted demo's fixed-coordinate layout
                // The fixed-coordinate headless verification needs the
                // demo layout; the interactive editor starts EMPTY (the
                // welcome placeholder invites the user to open something).
                // (One flat update: nesting an entity update inside another
                // panics — "already being updated".)
                if headlessMode {
                        ws.Buffers = []*editor.Buffer{
                                editor.NewBuffer("", "main.go", demoMain),
                                editor.NewBuffer("", "util.go", demoUtil),
                                editor.NewBuffer("", "README.md", demoReadme),
                        }
                        ws.scrolls = []float32{0, 0, 0}
                }
                // Open the positional paths (files and folders).
                for _, arg := range flag.Args() {
                        if err := ws.OpenPath(arg); err != nil {
                                fmt.Fprintln(os.Stderr, "editor:", err)
                        }
                }
        })

        // The close prompt: OnShouldClose runs on the UI thread; it vetoes the
        // close, opens the modal prompt and quits through the chosen
        // action. Only one prompt is open at a time (Prompt deduplicates).
        shouldClose = func() bool {
                state := handle.View.Read(cx)
                dirty := 0
                for _, b := range state.Buffers {
                        if b.Dirty {
                                dirty++
                        }
                }
                if dirty == 0 {
                        return true
                }
                resp := handle.Window.Prompt(gpui.PromptLevelWarning, "Unsaved changes",
                        fmt.Sprintf("%d buffer(s) have unsaved changes. Quit anyway?", dirty), "",
                        []gpui.PromptAction{{Label: "Discard & Quit"}, {Label: "Cancel"}})
                if resp == nil {
                        return false // identical prompt already open
                }
                go func() {
                        <-resp.Changed() // fires when the prompt is answered or superseded
                        if idx, ok := resp.Index(); ok && idx == 0 {
                                a.Platform.DispatchOnMainThread(func() { a.Platform.Quit() })
                        }
                }()
                return false
        }

        a.SetRoot(func(w *gpui.Window, c *app.App) gpui.Element {
                // Background completions land between frames, never during a
                // paint (the headless platform's inline dispatch made this a
                // data race before the queue existed).
                drainDispatch()
                return handle.View.Read(c).Render(&handle, c, w)
        })

        if a.Headless == nil {
                stop := make(chan struct{})
                startFPSSampler(handle.Window, update, a.Platform.DispatchOnMainThread, stop)
                startCaretBlink(handle.Window, update, a.Platform.DispatchOnMainThread, stop)
                a.Loop() // blocks until the window is closed
                close(stop)
                if task := handle.View.Read(cx).GoTask; task != nil {
                        task.Stop()
                }
                if session := handle.View.Read(cx).debug.session; session != nil {
                        session.Stop()
                        <-session.Done()
                }
                handle.View.Read(cx).tools.resetProject()
                handle.View.Read(cx).closeFileSearch()
                handle.View.Read(cx).closeLanguages()
                return
        }
        verifyHeadless(a, cx)
}

// startCaretBlink flips the editor caret on a 500ms tick (any editor's
// blinking caret). Like the FPS sampler it never touches app state from
// its own goroutine: the tick is dispatched onto the UI thread, where the
// update notifies the view and the invalidation flush draws one frame.
// Cursor activity (typing, clicking, dragging) forces the caret visible
// for a 400ms grace window first, so it never disappears mid-action.
func startCaretBlink(w *gpui.Window, update func(f func(*Workspace)), dispatch func(f func()), stop <-chan struct{}) {
        go func() {
                ticker := time.NewTicker(500 * time.Millisecond)
                defer ticker.Stop()
                for {
                        select {
                        case <-stop:
                                return
                        case <-ticker.C:
                                dispatch(func() {
                                        defer func() { _ = recover() }() // shutdown race
                                        update(func(ws *Workspace) { ws.TickCaretBlink() })
                                        w.Refresh()
                                })
                        }
                }
        }()
}

// startFPSSampler samples the window's frame counter once per second and
// notifies the workspace so the menu-bar readout tracks the real present
// rate. The presents themselves are vsync-locked by the renderer (Present
// interval 1 = the display's refresh rate; GPUI_VSYNC=0 disables).
//
// The sampler NEVER touches the app from its own goroutine: the tick only
// posts the sample through dispatch (DispatchOnMainThread), so the frame
// counter read, the entity update and the invalidation-driven redraw all
// run on the UI thread. Running the update on the ticker goroutine would
// race the main-thread draw inside wndProc (concurrent layout-tree
// mutation corrupted the tree: "index out of range" in Tree.NewLeaf).
//
// The sampler stops when the main loop returns; the closure is guarded
// against the shutdown race (a tick can land between entity teardown and
// process exit, where the view entity no longer exists).
func startFPSSampler(w *gpui.Window, update func(f func(*Workspace)), dispatch func(f func()), stop <-chan struct{}) {
        go func() {
                ticker := time.NewTicker(time.Second)
                defer ticker.Stop()
                last := 0
                dispatch(func() { last = w.DrawCount() })
                for {
                        select {
                        case <-stop:
                                return
                        case <-ticker.C:
                                dispatch(func() {
                                        defer func() { _ = recover() }() // shutdown race
                                        n := w.DrawCount()
                                        update(func(ws *Workspace) { ws.FPS = n - last })
                                        last = n
                                })
                        }
                }
        }()
}

// measure fills the text metrics (monospace advance) through the text
// system, like the upstream editor measuring its font at startup.
func (ws *Workspace) measure(w *gpui.Window) {
        // Window keymap: the editor's global shortcuts (zoom chords; the View
        // menu mirrors them). The keymap outlives frames, unlike the paint-time
        // action listeners in onZoomActions.
        InstallZoomKeys(w)
        ws.measureMetrics(w)
}

// measureMetrics measures the monospace metrics at the configured font
// size (settings panel; 0 = the default 16px). Runs at startup and again
// whenever the settings change a font size.
func (ws *Workspace) measureMetrics(w *gpui.Window) {
        ws.codeShaper = editor.NewCodeShaper(w, float32(ws.Cfg.Editor.FontSize))
        if sys := w.TextSystem(); sys != nil {
                ts := style.DefaultTextStyle()
                if ws.Cfg.Editor.FontSize > 0 {
                        ts.FontSize = ui.AbsPx(float32(ws.Cfg.Editor.FontSize))
                }
                fid, ok := sys.FontID(font.Font{Family: "Monospace", Weight: font.WeightNormal})
                if !ok {
                        fid, _ = sys.FontID(font.Font{Family: "", Weight: font.WeightNormal})
                }
                l := sys.LayoutLine("0000000000", ts.FontSize.ToPixels(w.RemSize()),
                        []text.FontRun{{Len: 10, FontID: fid}})
                if adv := l.Width.AsF32() / 10; adv > 0 {
                        ws.CharAdvance = adv
                }
                if lh := ts.LineHeightInPixels(w.RemSize()); lh > 0 {
                        ws.LineHeight = lh
                }
        }
        if ws.CharAdvance <= 0 {
                ws.CharAdvance = 8
        }
        if ws.LineHeight <= 0 {
                ws.LineHeight = 18
        }
        ws.measuredFontPx = ws.Cfg.Editor.FontSize
}
