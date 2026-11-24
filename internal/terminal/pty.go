package terminal

import (
	"fmt"
	"strings"
)

// ptyConn is the pseudo-terminal transport shared by the platform backends
// (ConPTY on Windows, /dev/ptmx on Linux). Everything the shell writes —
// including cursor-addressed full-screen programs — streams out of Read as
// VT bytes; everything the user types goes into Write as raw bytes.
type ptyConn interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	// Resize re-lets the kernel/pseudoconsole know the grid changed; the
	// child receives SIGWINCH (or the ConPTY equivalent) and redraws.
	Resize(cols, rows uint16) error
	Close() error
	// Wait blocks until the child process exits.
	Wait() error
}

// spawnShell launches the configured interactive shell on a pseudo
// terminal sized cols×rows cells. Unlike the previous piped model, the
// shell owns the whole session from the start: it prints its own prompt,
// does its own line editing and history, and runs nested programs
// (powershell inside cmd, REPLs, debuggers) with real terminal semantics.
func spawnShell(shell string, args []string, dir string, env []string, cols, rows uint16) (ptyConn, error) {
	if cols < 2 {
		cols = 80
	}
	if rows < 2 {
		rows = 24
	}
	return spawnShellForPlatform(shell, args, dir, env, cols, rows)
}

// interactiveArgv builds the shell's argument vector from the settings:
// the one-shot flags ("cmd /c", "sh -c") never make sense for a
// persistent interactive session, everything else the user configured is
// kept (the upstream editor's terminal.shell).
func interactiveArgv(shell, shellArgs string) []string {
	argv := []string{shell}
	for _, a := range strings.Fields(shellArgs) {
		switch a {
		case "/c", "/C", "/K", "/k", "-c":
			// one-shot flags: dropped
		default:
			argv = append(argv, a)
		}
	}
	return argv
}

// childEnv returns the child environment with TERM set like a real
// terminal (full-color xterm: programs pick 256-color/truecolor output).
func childEnv() []string {
	env := []string{"TERM=xterm-256color", "COLORTERM=truecolor", "WT_SESSION=gpui"}
	for _, e := range environ() {
		switch {
		case strings.HasPrefix(e, "TERM="), strings.HasPrefix(e, "COLORTERM="):
			// replaced above
		case strings.HasPrefix(e, "WT_SESSION="):
			// replaced above
		default:
			env = append(env, e)
		}
	}
	return env
}

// shellCommandName resolves the shell command for spawn errors.
func shellCommandName(argv []string) string {
	if len(argv) == 0 {
		return "<none>"
	}
	return fmt.Sprintf("%s %s", argv[0], strings.Join(argv[1:], " "))
}
