//go:build !windows

package workspace

// hideConsole is a no-op off Windows (no console is allocated for the
// process), and crash logging is unnecessary: stderr (panic traces) is
// visible on every non-Windows platform.
func hideConsole() {}

// crashLog is a no-op off Windows: panics print to the visible stderr.
func crashLog() {}

// crashRecover is a no-op off Windows: the runtime's own panic trace is
// visible and reaches the terminal.
func crashRecover() {}
