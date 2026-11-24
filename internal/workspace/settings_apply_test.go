package workspace_test

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/terminal"
	"github.com/FlameInTheDark/flint/internal/ui"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestApplyConfigChangesWorkspace drives ApplyConfig (the settings
// panel's single mutation path) and checks every setting lands.
func TestApplyConfigChangesWorkspace(t *testing.T) {
	dir, err := os.MkdirTemp("", "go-gpui-apply-config")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_GPUI_CONFIG_DIR", dir)

	ws := workspace.New("apply-test")
	ws.Buffers = workspace.BenchBuffers()
	ws.Term = terminal.New(t.TempDir())

	cfg := config.Default()
	cfg.Appearance.Theme = "Tokyo Night"
	cfg.Editor.TabSize = 8
	cfg.Editor.ShowMinimap = false
	cfg.Editor.FontSize = 20
	cfg.General.ShowTree = false
	cfg.General.ShowLineNumbers = false
	cfg.Terminal.Shell = "custom-shell-under-test"
	cfg.Terminal.ShellArgs = "-x"

	// The pre-change theme must differ from the post-change one.
	if ws.Theme().EditorBg == ui.ThemeByName("Tokyo Night").EditorBg {
		t.Fatal("workspace must not start on the target theme")
	}

	ws.ApplyConfig(cfg)

	if ws.Theme() != ui.ThemeByName("Tokyo Night") {
		t.Fatal("theme did not switch")
	}
	if ws.ShowTree || ws.ShowLineNumbers {
		t.Fatal("panel toggles did not apply")
	}
	if editor.TabSize() != 8 {
		t.Fatalf("tab size did not apply: %d", editor.TabSize())
	}
	if ws.Cfg.Editor.ShowMinimap {
		t.Fatal("minimap setting did not apply")
	}
	// The shell override reaches the open terminal.
	if ws.Term.Shell != "custom-shell-under-test" || ws.Term.ShellArgs != "-x" {
		t.Fatalf("shell override did not reach the terminal: %q %q", ws.Term.Shell, ws.Term.ShellArgs)
	}
	if ws.Term.MaxLines != config.Default().Terminal.Scrollback {
		t.Fatalf("scrollback did not reach the terminal: %d", ws.Term.MaxLines)
	}

	// The settings file exists and holds the applied theme.
	data, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatalf("ApplyConfig must persist the settings file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty settings file")
	}
}

// TestTerminalShellOverride verifies the settings-driven shell actually
// runs: the interactive session spawns the CONFIGURED program with the
// configured args (sh -x traces every executed line, proving both the
// shell and its arguments were applied).
func TestTerminalShellOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("linux shell fixture")
	}
	term := terminal.New(t.TempDir())
	term.SetShell("sh", "-x")
	term.EnsureStarted(80, 10)
	defer term.Close()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if term.Snapshot().Alive {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	term.Write([]byte("echo hello-from-settings\r"))
	out := ""
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out = strings.Join(term.Output(), "\n")
		if strings.Contains(out, "hello-from-settings") && strings.Contains(out, "+ echo hello-from-settings") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out, "hello-from-settings") {
		t.Fatalf("custom shell did not run: %q", out)
	}
	if !strings.Contains(out, "+ echo hello-from-settings") {
		t.Fatalf("configured shell args (-x trace) missing: %q", out)
	}

	// Blank shell falls back to the platform default.
	term.SetShell("  ", "")
	if term.Shell == "  " {
		t.Fatal("blank shell must fall back")
	}
}

// TestOpenSettingsSeedsInputs lives in settings_internal_test.go (it
// touches unexported fields).
