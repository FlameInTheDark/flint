package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withTempDir pins the settings dir to a fresh temp dir for one test.
func withTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GO_GPUI_CONFIG_DIR", dir)
	return dir
}

func TestDefaults(t *testing.T) {
	cfg := Default()
	if cfg.General.ShowTree != true {
		t.Fatal("default show_tree must be true (the upstream editor/VSCode show the project panel)")
	}
	if cfg.Editor.TabSize != 4 || cfg.Editor.ShowMinimap != true {
		t.Fatal("editor defaults: tab 4, minimap on")
	}
	shell, args := DefaultShell()
	if cfg.Terminal.Shell != shell || cfg.Terminal.ShellArgs != args {
		t.Fatalf("terminal defaults must match the platform shell: %q %q", cfg.Terminal.Shell, cfg.Terminal.ShellArgs)
	}
	if cfg.Terminal.Scrollback != 5000 {
		t.Fatal("default scrollback 5000")
	}
}

func TestRoundTrip(t *testing.T) {
	withTempDir(t)
	cfg := Default()
	cfg.Editor.FontSize = 18
	cfg.Editor.TabSize = 2
	cfg.Editor.ShowMinimap = false
	cfg.General.ShowTree = false
	cfg.Terminal.Shell = "pwsh"
	cfg.Terminal.ShellArgs = "-NoLogo -Command"
	cfg.Terminal.Scrollback = 20000
	cfg.Appearance.Theme = "Tokyo Night"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	// The file must be valid JSON (the format promise).
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings file is not valid JSON: %v", err)
	}

	got := Load()
	if got.Editor.FontSize != 18 || got.Editor.TabSize != 2 || got.Editor.ShowMinimap {
		t.Fatalf("editor round-trip mismatch: %+v", got.Editor)
	}
	if got.General.ShowTree {
		t.Fatal("general round-trip mismatch")
	}
	if got.Terminal.Shell != "pwsh" || got.Terminal.Scrollback != 20000 {
		t.Fatalf("terminal round-trip mismatch: %+v", got.Terminal)
	}
	if got.Appearance.Theme != "Tokyo Night" {
		t.Fatalf("theme round-trip mismatch: %q", got.Appearance.Theme)
	}
	if filepath.Dir(Path()) != Dir() {
		t.Fatal("Path must live in Dir")
	}
}

func TestCorruptFileFallsBackToDefaults(t *testing.T) {
	dir := withTempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, ".go-gpui", "editor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte("{ not json !!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if got.Editor.TabSize != Default().Editor.TabSize {
		t.Fatal("a corrupt settings file must yield defaults")
	}
}

func TestClamp(t *testing.T) {
	dir := withTempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, ".go-gpui", "editor"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"editor":{"font_size":99,"tab_size":41},"terminal":{"scrollback":1,"shell":"  "}}`
	if err := os.WriteFile(Path(), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Load()
	if got.Editor.FontSize != 40 || got.Editor.TabSize != 8 {
		t.Fatalf("out-of-range editor values must clamp: %+v", got.Editor)
	}
	if got.Terminal.Scrollback != 1000 {
		t.Fatalf("out-of-range scrollback must clamp: %d", got.Terminal.Scrollback)
	}
	shell, args := DefaultShell()
	if got.Terminal.Shell != shell || got.Terminal.ShellArgs != args {
		t.Fatal("a blank shell must fall back to the platform default")
	}
}
