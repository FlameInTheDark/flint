package workspace_test

import (
	"testing"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"

	"github.com/FlameInTheDark/flint/internal/config"
)

// Regression (settings panel): the Terminal section's text fields used to be
// uneditable. Clicking a field moved focus to it, but the TrackFocus modal
// card's own focus-on-click handler ran later in the same event's bubble
// phase and stole the focus back to the modal — every keystroke then went to
// the card's Escape handler instead of the input. The innermost focusable
// element now wins the dispatch (gpui FocusMovedThisDispatch).
//
// The same session asserts the field's caret/text stay aligned: the input
// caret anchors at the field's padding box (layoutAbsolute now resolves
// absolute insets against the padding box, like CSS/taffy), which is also
// what keeps the dock terminal's input caret between exact cells.
func TestSettingsTextInputEditing(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp, handle, cx := openFullScene(t, t.TempDir())
	hw := hp.LastWindow()
	draw := func() { handle.Window.Draw() }
	draw()

	cardL, cardT, cardR, cardB := float32(110), float32(65), float32(890), float32(585)

	// Ctrl+, opens the modal.
	hw.DispatchInput(platform.KeyDownEvent{
		Keystroke: keymap.Keystroke{Key: ",", Modifiers: keymap.Modifiers{Control: true}},
	})
	draw()
	ws := handle.View.Read(cx)
	if !ws.SettingsOpen {
		t.Fatal("ctrl-, did not open the settings modal")
	}

	// Switch to the Terminal section.
	navs := boxesMatching(handle, cardL, cardL+170, cardT+40, cardB-50, 100, 168, 28, 32)
	if len(navs) < 4 {
		t.Fatalf("settings sidebar nav items missing: %d", len(navs))
	}
	clickRect(hw, navs[2])
	draw()

	// The two text fields render ~218-240px wide, 26px tall, right-aligned
	// in the card body (the scrollback stepper's value box is 64px and the
	// buttons 22px, so they never match this filter).
	fields := boxesMatching(handle, cardL+170, cardR, cardT, cardB, 200, 260, 22, 30)
	if len(fields) < 2 {
		t.Fatalf("terminal text fields missing: %d", len(fields))
	}

	// Click the Shell field: focus must land on the INPUT and stay there
	// (before the fix the modal card stole it back within the same click).
	focusBefore := handle.Window.Focused()
	clickRect(hw, fields[0])
	draw()
	focusAfter := handle.Window.Focused()
	if focusAfter == focusBefore {
		t.Fatal("clicking the shell field did not move focus into the input")
	}

	// Ctrl+A selects the seeded value, and typing replaces it and live-applies
	// the change to the config without sending the shortcut to the editor.
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "a", Modifiers: keymap.Modifiers{Control: true}}})
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "x", KeyChar: "x"}})
	draw()
	ws = handle.View.Read(cx)
	if got := ws.Cfg.Terminal.Shell; got != "x" {
		t.Fatalf("replacing the shell field did not apply: cfg shell=%q", got)
	}

	// The args field is editable too.
	clickRect(hw, fields[len(fields)-1])
	draw()
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "z", KeyChar: "z"}})
	draw()
	ws = handle.View.Read(cx)
	if got := ws.Cfg.Terminal.ShellArgs; len(got) == 0 || got[len(got)-1] != 'z' {
		t.Fatalf("typing into the args field did not apply: cfg args=%q", got)
	}

	// Enter commits (CloseSettings syncs both fields) and closes the modal.
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "enter"}})
	draw()
	ws = handle.View.Read(cx)
	if ws.SettingsOpen {
		t.Fatal("Enter did not close the settings modal")
	}
	if got := ws.Cfg.Terminal.Shell; got != "x" {
		t.Fatalf("shell edit lost on close: %q", got)
	}

	// The edits persisted to the settings file.
	cfg := config.Load()
	if cfg.Terminal.Shell != ws.Cfg.Terminal.Shell || cfg.Terminal.ShellArgs != ws.Cfg.Terminal.ShellArgs {
		t.Fatalf("settings file did not persist the edits: shell=%q args=%q",
			cfg.Terminal.Shell, cfg.Terminal.ShellArgs)
	}
	_ = geometry.Px(0)
}
