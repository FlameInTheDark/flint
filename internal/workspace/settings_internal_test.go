package workspace

import (
	"testing"

	"github.com/FlameInTheDark/flint/internal/terminal"
)

// TestOpenSettingsSeedsInputs checks the modal's state seeding (unexported
// fields, so this test lives inside the package).
func TestOpenSettingsSeedsInputs(t *testing.T) {
	// Fresh settings dir: CloseSettings persists the config.
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	ws := New("settings-test")
	ws.Term = terminal.New(t.TempDir())
	ws.OpenSettings()
	if !ws.SettingsOpen {
		t.Fatal("OpenSettings must open the modal")
	}
	if ws.shellInput == nil || ws.shellInput.String() != ws.Cfg.Terminal.Shell {
		t.Fatalf("shell input not seeded from config: %q vs %q", ws.shellInput.String(), ws.Cfg.Terminal.Shell)
	}
	if ws.argsInput == nil || ws.argsInput.String() != ws.Cfg.Terminal.ShellArgs {
		t.Fatal("args input not seeded from config")
	}
	if !ws.shellFocus.IsValid() || !ws.argsFocus.IsValid() {
		t.Fatal("input focus handles not minted")
	}

	// Switching sections and closing behave.
	ws.SettingsTab = settingsTabEditor
	ws.CloseSettings()
	if ws.SettingsOpen {
		t.Fatal("CloseSettings must close the modal")
	}
}
