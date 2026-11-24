package workspace_test

import (
	"testing"

	"github.com/FlameInTheDark/go-gpui/app"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestVisualFontSizeSetting applies a 20px font size and screenshots the
// re-measured editor (glyphs grow, cursor math must stay aligned).
func TestVisualFontSizeSetting(t *testing.T) {
	// Fresh settings dir: this test persists a 20px font size.
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	hp, handle, cx := openFullScene(t, t.TempDir())
	handle.Window.Draw()

	handle.View.Update(cx, func(state *workspace.Workspace, c *app.Context[workspace.Workspace]) any {
		cfg := config.Default()
		cfg.Editor.FontSize = 20
		state.ApplyConfig(cfg)
		c.Notify()
		return nil
	})
	handle.Window.Draw()
	shot(t, hp, "/tmp/render", "settings_font20.png")
}
