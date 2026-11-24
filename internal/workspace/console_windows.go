//go:build windows

package workspace

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// stdErrorHandle is STD_ERROR_HANDLE ((DWORD)-12) as a uintptr.
const stdErrorHandle = ^uintptr(11)

// Crash diagnostics for the GUI-subsystem build: with -H windowsgui there
// is no console, so a Go panic's stderr write (and its exit code 2) is
// completely invisible. Two layers fix that:
//
//  1. crashLog redirects the process's stderr to a log file. The Go
//     runtime resolves the stderr handle via GetStdHandle(STD_ERROR_HANDLE)
//     on every write (runtime/os_windows.go write1), so SetStdHandle is
//     enough: panic traces, fatal runtime errors and anything written to
//     os.Stderr all land in the file from now on.
//  2. crashRecover recovers the entry goroutine's panics, writes the trace
//     to a SEPARATE, timestamped crash file (verified written before any
//     claim is made), shows a MessageBox with the exact absolute path —
//     or, when every write attempt failed, with the trace itself — and
//     exits with 2 like the runtime would.
//
// Panics on other goroutines skip layer 2 — the runtime still prints them
// to the redirected stderr (layer 1) before exiting with 2.

// logDir is the crash-log directory (shared by the stderr redirect and the
// per-crash files); empty when no usable directory was found.
var logDir string

// crashLogDir resolves the diagnostics directory: %LOCALAPPDATA%\go-gpui,
// falling back to the temp directory, then the executable's folder. The
// first location that can actually be created wins.
func crashLogDir() string {
	var candidates []string
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		candidates = append(candidates, filepath.Join(base, "go-gpui"))
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), "go-gpui"))
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		// Prove the directory is writable — a created-but-unwritable
		// directory would otherwise swallow the crash trace silently.
		probe := filepath.Join(dir, ".write-test")
		if err := os.WriteFile(probe, nil, 0o644); err != nil {
			continue
		}
		_ = os.Remove(probe)
		return dir
	}
	return ""
}

// crashLog opens the crash log and makes it the process stderr. Skipped
// for headless runs (tests must keep the real stderr).
func crashLog() {
	for _, a := range os.Args {
		if a == "-headless" || a == "--headless" {
			return
		}
	}
	dir := crashLogDir()
	if dir == "" {
		return
	}
	logDir = dir
	f, err := os.OpenFile(filepath.Join(dir, "editor.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	// The runtime's panic writer resolves GetStdHandle(STD_ERROR_HANDLE)
	// per write, so re-registering the process std handle is enough.
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	kernel32.NewProc("SetStdHandle").Call(stdErrorHandle, f.Fd())
	os.Stderr = f
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags)
	// Kept open for the process lifetime: f is never closed.
}

// writeCrashTrace persists the trace to a fresh timestamped file and
// returns its absolute path. Multiple locations are tried; the first
// successful write wins, and the last error is reported when all fail.
func writeCrashTrace(trace string) (string, error) {
	stamp := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("go-gpui-crash-%s.log", stamp)
	var candidates []string
	if logDir != "" {
		candidates = append(candidates, filepath.Join(logDir, name))
	}
	candidates = append(candidates,
		filepath.Join(os.TempDir(), name),
		filepath.Join(os.TempDir(), "go-gpui", name))
	var lastErr error
	for _, path := range candidates {
		if err := os.WriteFile(path, []byte(trace), 0o644); err != nil {
			lastErr = err
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		return abs, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no writable crash-log location")
	}
	return "", lastErr
}

// crashRecover handles the entry goroutine's panic: trace to the stderr
// log, a verified crash file, MessageBox (the only UI a GUI build can
// always show), exit 2. The box never CLAIMS a file that could not be
// written: on failure it embeds the trace head instead.
func crashRecover() {
	r := recover()
	if r == nil {
		return
	}
	trace := fmt.Sprintf("panic: %v\n\n%s", r, debug.Stack())
	// stderr is redirected at startup; print now so the trace is in the
	// editor.log even if the dedicated crash file cannot be written.
	fmt.Fprintln(os.Stderr, trace)
	msg := "Flint crashed unexpectedly.\n\n"
	if path, err := writeCrashTrace(trace); err == nil {
		msg += "The full trace was written to:\n" + path
	} else {
		msg += "Writing the crash log failed: " + err.Error() +
			"\n\n--- trace (head) ---\n" + traceExcerpt(trace, 800)
	}
	MessageBox(0, msg, "Flint", 0x10) // MB_ICONERROR
	os.Exit(2)
}

// traceExcerpt trims the trace to at most limit characters on line
// boundaries (a MessageBox can hold ~1000 lines but stays unread beyond
// a screenful).
func traceExcerpt(trace string, limit int) string {
	if len(trace) <= limit {
		return trace
	}
	cut := trace[:limit]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return cut + "\n…"
}

// MessageBox shows a Win32 message box (user32.MessageBoxW).
func MessageBox(hwnd uintptr, text, caption string, flags uint32) int {
	t, err1 := syscall.UTF16PtrFromString(text)
	c, err2 := syscall.UTF16PtrFromString(caption)
	if err1 != nil || err2 != nil {
		return 0
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	ret, _, _ := user32.NewProc("MessageBoxW").Call(
		hwnd,
		uintptr(unsafe.Pointer(t)),
		uintptr(unsafe.Pointer(c)),
		uintptr(flags))
	return int(ret)
}

// hideConsole detaches the process from its console. A console-subsystem
// binary launched by double-click gets an empty console window next to the
// app; FreeConsole closes it (launched from an existing terminal, the
// detach only affects this process — the terminal stays).
//
// Building with -ldflags "-H windowsgui" avoids even the brief flash; this
// is the runtime fallback so plain `go build` output behaves too.
func hideConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	freeConsole := kernel32.NewProc("FreeConsole")
	freeConsole.Call()
}
