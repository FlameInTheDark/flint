//go:build windows

package terminal

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// ConPTY: the Windows pseudo console (Windows 10 1809+). The shell and
// everything it spawns run against a real console hosted by conhost —
// cursor-addressed programs, colors, interactive input and Ctrl+C all
// behave exactly like a native terminal (the upstream editor's terminal semantics).

var (
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreatePseudoConsole               = modKernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole               = modKernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole                = modKernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAttributeList = modKernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute         = modKernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList     = modKernel32.NewProc("DeleteProcThreadAttributeList")
	procCreateProcessW                    = modKernel32.NewProc("CreateProcessW")
	procTerminateProcess                  = modKernel32.NewProc("TerminateProcess")
	procWaitForSingleObject               = modKernel32.NewProc("WaitForSingleObject")
	procCloseHandle                       = modKernel32.NewProc("CloseHandle")
)

const (
	procThreadAttributePseudoconsole = 0x00020016
	extStartupInfoPresent            = 0x00080000
	createUnicodeEnvironment         = 0x00000400
	waitInfinite                     = 0xFFFFFFFF
)

// coord is the ConPTY size (cells).
type coord struct {
	X, Y int16
}

// startupInfoExW is STARTUPINFOEXW: the extended startup info carrying the
// pseudoconsole attribute for CreateProcessW.
type startupInfoExW struct {
	syscall.StartupInfo
	attributeList uintptr
}

// processInfo mirrors PROCESS_INFORMATION.
type processInfo struct {
	Process   syscall.Handle
	Thread    syscall.Handle
	ProcessID uint32
	ThreadID  uint32
}

// conPTY is the Windows ptyConn implementation.
type conPTY struct {
	hpc      uintptr
	proc     syscall.Handle
	in       *os.File // user keystrokes → conhost
	out      *os.File // conhost VT output → the emulator
	attrList uintptr
	// attrBuf keeps the attribute-list allocation alive: attrList is a
	// uintptr (invisible to the GC), so the buffer must be referenced by a
	// real pointer until DeleteProcThreadAttributeList runs in Close.
	attrBuf []uint64
}

// spawnConPTY starts shell argv on a pseudo console of cols×rows cells.
func spawnShellForPlatform(shell string, args []string, dir string, env []string, cols, rows uint16) (ptyConn, error) {
	// Two anonymous pipes: (inR → conhost input), (outW ← conhost output).
	sa := syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), InheritHandle: 0}
	var inR, inW, outR, outW syscall.Handle
	if err := syscall.CreatePipe(&inR, &inW, &sa, 0); err != nil {
		return nil, fmt.Errorf("conpty: input pipe: %v", err)
	}
	if err := syscall.CreatePipe(&outR, &outW, &sa, 0); err != nil {
		syscall.CloseHandle(inR)
		syscall.CloseHandle(inW)
		return nil, fmt.Errorf("conpty: output pipe: %v", err)
	}
	// CreatePseudoConsole consumes the pipe ends it is given: the parent
	// keeps inW (write) and outR (read) only.
	var hpc uintptr
	// Teardown for failures after CreatePseudoConsole succeeded: the
	// pseudoconsole consumed inR/outW, the parent owns inW/outR.
	fail := func(format string, a ...any) (ptyConn, error) {
		procClosePseudoConsole.Call(hpc)
		syscall.CloseHandle(inW)
		syscall.CloseHandle(outR)
		return nil, fmt.Errorf("conpty: "+format, a...)
	}
	hr, _, _ := procCreatePseudoConsole.Call(
		uintptr(*(*uint32)(unsafe.Pointer(&coord{X: int16(cols), Y: int16(rows)}))),
		uintptr(inR), uintptr(outW), 0, uintptr(unsafe.Pointer(&hpc)))
	if hr != 0 {
		syscall.CloseHandle(inR)
		syscall.CloseHandle(inW)
		syscall.CloseHandle(outR)
		syscall.CloseHandle(outW)
		return nil, fmt.Errorf("conpty: CreatePseudoConsole: 0x%08x (need Windows 10 1809+)", hr)
	}
	syscall.CloseHandle(inR)
	syscall.CloseHandle(outW)

	// Attribute list carrying PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE: without
	// it the child gets no console at all. InitializeProcThreadAttributeList
	// returns BOOL — nonzero on SUCCESS (unlike the HRESULT calls above) —
	// and the sizing probe with a NULL list deliberately "fails" with
	// ERROR_INSUFFICIENT_BUFFER while writing the required size.
	var attrSize uintptr
	procInitializeProcThreadAttributeList.Call(0, 1, 0, uintptr(unsafe.Pointer(&attrSize)))
	if attrSize == 0 {
		return fail("attribute-list sizing returned 0 bytes")
	}
	// The list holds pointers internally: allocate pointer-aligned (a plain
	// []byte can sit at an odd offset via the tiny allocator).
	attrBuf := make([]uint64, (attrSize+7)/8)
	attrList := uintptr(unsafe.Pointer(&attrBuf[0]))
	if ok, _, callErr := procInitializeProcThreadAttributeList.Call(attrList, 1, 0, uintptr(unsafe.Pointer(&attrSize))); ok == 0 {
		return fail("InitializeProcThreadAttributeList: %v", callErr)
	}
	if ok, _, _ := procUpdateProcThreadAttribute.Call(attrList, 0,
		procThreadAttributePseudoconsole, hpc, unsafe.Sizeof(hpc), 0, 0); ok == 0 {
		procDeleteProcThreadAttributeList.Call(attrList)
		return fail("UpdateProcThreadAttribute: PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE rejected")
	}

	// Command line + UTF-16 environment block (CREATE_UNICODE_ENVIRONMENT).
	cmdline := windowsCommandLine(append([]string{shell}, args...)...)
	cmdPtr, err := syscall.UTF16PtrFromString(cmdline)
	if err != nil {
		procDeleteProcThreadAttributeList.Call(attrList)
		procClosePseudoConsole.Call(hpc)
		syscall.CloseHandle(inW)
		syscall.CloseHandle(outR)
		return nil, fmt.Errorf("conpty: command line: %v", err)
	}
	// envBlockUTF16 keeps the NUL separators between entries —
	// syscall.UTF16FromString rejects embedded NULs with EINVAL
	// ("invalid argument" on Windows), which used to kill every spawn
	// after the attribute-list fix unblocked this far.
	envBlock := envBlockUTF16(env)
	var dirPtr *uint16
	if dir != "" {
		dirPtr, _ = syscall.UTF16PtrFromString(dir)
	}

	si := startupInfoExW{}
	si.Cb = uint32(unsafe.Sizeof(si))
	// Windows can duplicate the parent's redirected standard handles even
	// when handle inheritance is disabled. Explicitly clearing them lets the
	// ConPTY supply its console handles instead of leaking child I/O to the
	// editor's launcher or test runner.
	si.Flags = syscall.STARTF_USESTDHANDLES
	si.attributeList = attrList
	var pi processInfo
	ok, _, callErr := procCreateProcessW.Call(
		0,
		uintptr(unsafe.Pointer(cmdPtr)),
		0, 0,
		0, // bInheritHandles: FALSE — the pseudoconsole provides the handles
		extStartupInfoPresent|createUnicodeEnvironment,
		uintptr(unsafe.Pointer(&envBlock[0])),
		uintptr(unsafe.Pointer(dirPtr)),
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)))
	if ok == 0 { // CreateProcessW returns BOOL: 0 = failure
		procDeleteProcThreadAttributeList.Call(attrList)
		return fail("spawn %q: %v", cmdline, callErr)
	}
	syscall.CloseHandle(pi.Thread)

	c := &conPTY{
		hpc:      hpc,
		proc:     pi.Process,
		in:       os.NewFile(uintptr(inW), "conpty-in"),
		out:      os.NewFile(uintptr(outR), "conpty-out"),
		attrList: attrList,
		attrBuf:  attrBuf,
	}
	return c, nil
}

// windowsCommandLine quotes argv into a CreateProcessW command line
// (argv[0] as-is when it has no spaces; each argument quoted when needed).
func windowsCommandLine(argv ...string) string {
	quote := func(s string) string {
		if s == "" || strings.ContainsAny(s, " \t\"") {
			return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
		}
		return s
	}
	parts := make([]string, len(argv))
	for i, a := range argv {
		if i == 0 && !strings.ContainsAny(a, " \t\"") {
			parts[i] = a
			continue
		}
		parts[i] = quote(a)
	}
	return strings.Join(parts, " ")
}

func (c *conPTY) Read(p []byte) (int, error)  { return c.out.Read(p) }
func (c *conPTY) Write(p []byte) (int, error) { return c.in.Write(p) }

func (c *conPTY) Resize(cols, rows uint16) error {
	hr, _, _ := procResizePseudoConsole.Call(c.hpc,
		uintptr(*(*uint32)(unsafe.Pointer(&coord{X: int16(cols), Y: int16(rows)}))))
	if hr != 0 {
		return fmt.Errorf("conpty: ResizePseudoConsole: 0x%08x", hr)
	}
	return nil
}

func (c *conPTY) Wait() error {
	if s, _, _ := procWaitForSingleObject.Call(uintptr(c.proc), waitInfinite); s != 0 {
		return fmt.Errorf("conpty: WaitForSingleObject: 0x%08x", s)
	}
	return nil
}

// conptyCloseWaitMS bounds the wait for the client process to die after
// TerminateProcess (TerminateProcess is asynchronous; a clean exit is
// normally instantaneous).
const conptyCloseWaitMS = 2000

// Close tears the pseudo console down and releases every handle. The
// sequence is the deadlock-free teardown order:
//
//  1. a drain goroutine reads the output pipe until it errors — conhost's
//     final flush backpressures into ClosePseudoConsole when nobody reads,
//     and the close then blocks forever (the editor freeze on a quick
//     open→close→open cycle);
//  2. the client process is terminated with a bounded wait — a live shell
//     keeps conhost alive, and ClosePseudoConsole waits for attached
//     clients; a child ignoring the console close would otherwise hold
//     the teardown indefinitely;
//  3. ClosePseudoConsole, then the pipes, then the remaining handles.
//
// The attribute-list buffer is still referenced here via attrBuf (a
// uintptr alone would not keep it alive for the GC).
func (c *conPTY) Close() error {
	go func() {
		buf := make([]byte, 8192)
		for {
			if _, err := c.out.Read(buf); err != nil {
				return
			}
		}
	}()
	procTerminateProcess.Call(uintptr(c.proc), 1)
	procWaitForSingleObject.Call(uintptr(c.proc), conptyCloseWaitMS)
	procClosePseudoConsole.Call(c.hpc)
	c.hpc = 0
	_ = c.in.Close()
	_ = c.out.Close()
	syscall.CloseHandle(c.proc)
	procDeleteProcThreadAttributeList.Call(c.attrList)
	runtime.KeepAlive(c.attrBuf)
	return nil
}

// environ returns the process environment (hook point for tests).
func environ() []string { return os.Environ() }
