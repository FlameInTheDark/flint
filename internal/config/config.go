// Package config holds the editor's user settings: the model, JSON
// persistence under the user's home directory, and the defaults applied
// when no (or a corrupt) settings file exists.
//
// JSON was chosen over YAML on purpose: encoding/json ships with Go (no
// third-party dependency for a single file format), round-trips structs
// deterministically, and fails loudly on typos instead of silently
// re-indenting them. The file lives at:
//
//	~/.go-gpui/editor/settings.json
//
// and is safe to hand-edit — every field falls back to its default when
// missing or out of range, and unknown fields are ignored.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// General holds the workspace panel toggles.
type General struct {
	ShowTree        bool `json:"show_tree"`
	ShowTerminal    bool `json:"show_terminal"`
	ShowLineNumbers bool `json:"show_line_numbers"`
}

// Editor holds code-editing behavior.
type Editor struct {
	// FontSize is the code/glyph size in px (0 = the 16px default). The
	// window re-measures its monospace metrics when it changes.
	FontSize int `json:"font_size"`
	// TabSize is the tab stop distance in display columns (2..8).
	TabSize int `json:"tab_size"`
	// ShowMinimap toggles the minimap rail right of the code.
	ShowMinimap bool `json:"show_minimap"`
	// DisableRainbowBrackets turns the depth-colored bracket pairs and
	// the matching-bracket highlight off. The flag is inverted so a
	// settings file saved before the feature existed (the key absent,
	// false) keeps the feature enabled.
	DisableRainbowBrackets bool `json:"disable_rainbow_brackets"`
	// DisableAutoClosePairs turns the smart auto-pairing keys off:
	// typing ( no longer inserts the closing half, selections do not
	// wrap, closers do not type over. Same inverted-default reasoning.
	DisableAutoClosePairs bool `json:"disable_auto_close_pairs"`
}

// RainbowEnabled reports whether the depth-colored bracket pairs are on
// (the Disable flags are inverted so a settings file saved before the
// feature existed keeps it enabled).
func (e Editor) RainbowEnabled() bool { return !e.DisableRainbowBrackets }

// AutoCloseEnabled reports whether the smart auto-pairing keys are on.
func (e Editor) AutoCloseEnabled() bool { return !e.DisableAutoClosePairs }

// Terminal holds the dock's shell behavior.
type Terminal struct {
	// Shell is the executable used to run command lines. Empty = the
	// platform default (cmd on Windows, sh elsewhere).
	Shell string `json:"shell"`
	// ShellArgs are the shell's arguments, space-separated, that precede
	// the command line (e.g. "-c" or "/c"). Empty = the platform default.
	ShellArgs string `json:"shell_args"`
	// Scrollback caps the dock's line history (1000..100000).
	Scrollback int `json:"scrollback"`
}

// Appearance holds the look.
type Appearance struct {
	// Theme is one of ui.ThemeNames(); unknown names fall back to the
	// default palette.
	Theme string `json:"theme"`
}

// Config is the settings file model.
type Config struct {
	General    General    `json:"general"`
	Editor     Editor     `json:"editor"`
	Terminal   Terminal   `json:"terminal"`
	Appearance Appearance `json:"appearance"`
}

// Platform shell defaults (the terminal dock's pre-config behavior).
func DefaultShell() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "sh", "-c"
}

// Default returns the settings a fresh install starts with.
func Default() Config {
	shell, args := DefaultShell()
	return Config{
		General: General{
			ShowTree:        true,
			ShowTerminal:    false,
			ShowLineNumbers: true,
		},
		Editor: Editor{
			FontSize:    0,
			TabSize:     4,
			ShowMinimap: true,
		},
		Terminal: Terminal{
			Shell:      shell,
			ShellArgs:  args,
			Scrollback: 5000,
		},
		Appearance: Appearance{
			Theme: "Flint Dark",
		},
	}
}

// Dir returns the settings directory (~/.go-gpui/editor), creating it on
// first save. GO_GPUI_CONFIG_DIR overrides it (tests point it at a temp
// dir so they stay hermetic); a resolvable home directory is required
// otherwise, falling back to a temp dir in headless sandboxes.
func Dir() string {
	if d := os.Getenv("GO_GPUI_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".go-gpui", "editor")
}

// Path is the settings file path.
func Path() string { return filepath.Join(Dir(), "settings.json") }

// Load reads the settings file. A missing file returns the defaults; a
// corrupt file ALSO returns the defaults (a hand edit must never keep the
// editor from starting) — the caller can check PathExists to distinguish.
func Load() Config {
	cfg := Default()
	data, err := os.ReadFile(Path())
	if err != nil {
		return cfg
	}
	var parsed Config
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Default()
	}
	cfg.General.ShowTree = parsed.General.ShowTree
	cfg.General.ShowTerminal = parsed.General.ShowTerminal
	cfg.General.ShowLineNumbers = parsed.General.ShowLineNumbers
	cfg.Editor = parsed.Editor
	cfg.Terminal = parsed.Terminal
	cfg.Appearance = parsed.Appearance
	cfg.clamp()
	return cfg
}

// clamp forces every value into its safe range, so a hand-edited or
// out-of-date file cannot produce an unusable editor.
func (c *Config) clamp() {
	if c.Editor.FontSize < 0 {
		c.Editor.FontSize = 0
	}
	if c.Editor.FontSize > 40 {
		c.Editor.FontSize = 40
	}
	if c.Editor.TabSize < 2 {
		c.Editor.TabSize = 2
	}
	if c.Editor.TabSize > 8 {
		c.Editor.TabSize = 8
	}
	if c.Terminal.Scrollback < 1000 {
		c.Terminal.Scrollback = 1000
	}
	if c.Terminal.Scrollback > 100000 {
		c.Terminal.Scrollback = 100000
	}
	if strings.TrimSpace(c.Terminal.Shell) == "" {
		c.Terminal.Shell, c.Terminal.ShellArgs = DefaultShell()
	}
}

// Save writes the settings file (indented for hand editing), creating the
// directory when needed.
func (c Config) Save() error {
	c.clamp()
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), append(data, '\n'), 0o644)
}
