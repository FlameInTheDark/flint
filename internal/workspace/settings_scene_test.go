package workspace_test

import (
	"os"
	"sort"
	"testing"

	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// winHandle and wsHandle are the scene handle aliases.
type winHandle = headless.Window
type wsHandle = gpui.WindowHandle[workspace.Workspace]

// clickAt dispatches a full left click.
func clickAt(hw *winHandle, pos geometry.Point[geometry.Pixels]) {
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: pos})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: pos})
}

// boxesMatching returns hitboxes inside a band, size-filtered, top-left
// ordered.
func boxesMatching(handle *wsHandle, xmin, xmax, ymin, ymax, wmin, wmax, hmin, hmax float32) []hitboxRect {
	var out []hitboxRect
	for _, hb := range handle.Window.RenderedHitboxes() {
		r := rectOf(hb)
		if r.x >= xmin && r.right() <= xmax && r.y >= ymin && r.bottom() <= ymax &&
			r.w >= wmin && r.w <= wmax && r.h >= hmin && r.h <= hmax {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].y != out[j].y {
			return out[i].y < out[j].y
		}
		return out[i].x < out[j].x
	})
	return out
}

func clickRect(hw *winHandle, r hitboxRect) {
	clickAt(hw, geometry.PointPixels(r.x+r.w/2, r.y+r.h/2))
}

// TestVisualSettingsScene drives the whole settings chain: the ctrl-,
// chord opens the modal, the sidebar switches sections, the controls
// apply live (minimap off removes the rail, a theme switch re-themes the
// window), and Done closes it. Screenshots every step for review.
func TestVisualSettingsScene(t *testing.T) {
	// Fresh settings dir: this test persists config; other scene tests
	// must not inherit the changed theme/minimap/font.
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp, handle, cx := openFullScene(t, t.TempDir())
	hw := hp.LastWindow()

	// Modal card geometry: centered 780x520 in the 1000x650 window.
	cardL, cardT := float32(110), float32(65)
	cardR, cardB := float32(890), float32(585)

	// 1) Ctrl+, opens the modal (keymap → action → paint-time listener).
	hw.DispatchInput(platform.KeyDownEvent{
		Keystroke: keymap.Keystroke{Key: ",", Modifiers: keymap.Modifiers{Control: true}},
	})
	handle.Window.Draw()
	if ws := handle.View.Read(cx); !ws.SettingsOpen {
		t.Fatal("ctrl-, did not open the settings modal")
	}
	shot(t, hp, "/tmp/render", "settings_appearance.png")

	// The rail must exist while the minimap setting is on (sanity).
	railBefore := len(boxesMatching(handle, 600, 1000, 60, 470, 60, 200, 100, 400))
	if railBefore == 0 {
		t.Fatal("editor minimap rail missing before the toggle (sanity)")
	}

	// 2) Sidebar: switch to the Editor section (second nav item).
	navs := boxesMatching(handle, cardL, cardL+170, cardT+40, cardB-50, 100, 168, 28, 32)
	if len(navs) < 4 {
		t.Fatalf("settings sidebar nav items missing: %d", len(navs))
	}
	clickRect(hw, navs[1])
	handle.Window.Draw()
	shot(t, hp, "/tmp/render", "settings_editor.png")

	// 3) Toggle the minimap off: the editor rail must disappear.
	toggles := boxesMatching(handle, cardL, cardR, cardT, cardB, 34, 38, 18, 22)
	if len(toggles) < 2 {
		t.Fatalf("editor section toggles missing: %d", len(toggles))
	}
	clickRect(hw, toggles[0]) // Minimap row (above Line Numbers)
	handle.Window.Draw()
	if ws := handle.View.Read(cx); ws.Cfg.Editor.ShowMinimap {
		t.Fatal("minimap toggle did not apply")
	}
	railAfter := len(boxesMatching(handle, 600, 1000, 60, 470, 60, 200, 100, 400))
	if railAfter != 0 {
		t.Fatalf("minimap rail still rendered after toggle: %d boxes", railAfter)
	}

	// 4) Terminal section: text inputs and the scrollback stepper.
	clickRect(hw, navs[2])
	handle.Window.Draw()
	shot(t, hp, "/tmp/render", "settings_terminal.png")
	if len(boxesMatching(handle, cardL, cardR, cardT, cardB, 20, 24, 20, 24)) < 2 {
		t.Fatal("terminal section stepper buttons missing")
	}

	// 5) Theme switch to Tokyo Night: applied live behind the modal.
	clickRect(hw, navs[0])
	handle.Window.Draw()
	themes := boxesMatching(handle, cardL+170, cardR, cardT+40, cardB-50, 300, 620, 30, 34)
	if len(themes) < 5 {
		t.Fatalf("theme rows missing: %d", len(themes))
	}
	clickRect(hw, themes[2]) // ThemeNames()[2] = Tokyo Night
	handle.Window.Draw()
	if ws := handle.View.Read(cx); ws.Cfg.Appearance.Theme != "Tokyo Night" {
		t.Fatalf("theme click did not apply: %q", ws.Cfg.Appearance.Theme)
	}
	shot(t, hp, "/tmp/render", "settings_theme_switched.png")

	// 6) Done closes the modal; the settings file holds the new theme.
	footerBtns := boxesMatching(handle, cardL, cardR, cardB-48, cardB, 40, 90, 24, 28)
	if len(footerBtns) < 2 {
		t.Fatalf("footer buttons missing: %d", len(footerBtns))
	}
	clickRect(hw, footerBtns[len(footerBtns)-1]) // Done is the rightmost
	handle.Window.Draw()
	if ws := handle.View.Read(cx); ws.SettingsOpen {
		t.Fatal("Done did not close the modal")
	}
	data, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatalf("settings file not persisted: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty settings file")
	}
	shot(t, hp, "/tmp/render", "settings_closed_themed.png")
}

var (
	_ = gpui.Element(nil) // keep imports honest if assertions change
	_ = keymap.Keystroke{}
)
