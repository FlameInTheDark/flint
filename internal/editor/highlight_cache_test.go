package editor

import (
	"github.com/FlameInTheDark/flint/internal/ui"
	"testing"
)

func TestHighlightCacheTracksTheme(t *testing.T) {
	dark := PaletteOfTheme(ui.ThemeByName("Catppuccin Mocha"))
	light := PaletteOfTheme(ui.ThemeByName("Catppuccin Latte"))
	a := HighlightCached("func main()", "go", dark)
	b := HighlightCached("func main()", "go", light)
	if len(a) == 0 || len(b) == 0 || a[0].Color == b[0].Color {
		t.Fatal("highlight cache reused colors from another theme")
	}
}
