package workspace_test

import (
	"testing"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/ui"
)

// TestLanguageIconsCoverCatalog pins the language picker's icon rule: every
// language in the catalog must resolve to a real file-type glyph (the same
// one the file tree shows for that language's files — GoLand/Zed/VS Code
// style), never to the fallback document glyph. A new catalog entry
// without a mapping breaks this test on purpose: a text badge or a
// generic glyph in the picker is exactly what the icons round removed.
func TestLanguageIconsCoverCatalog(t *testing.T) {
	for _, lang := range editor.Languages {
		if ui.LanguageIconName(lang.Id) == "" {
			t.Errorf("language %q (%s) has no picker icon mapping", lang.Id, lang.Name)
		}
	}
	// Spot checks: the brand languages keep their tree icons.
	if got := ui.LanguageIconName("go"); got != "main.go" {
		t.Errorf("go icon file = %q, want main.go", got)
	}
	if got := ui.LanguageIconName("python"); got != "main.py" {
		t.Errorf("python icon file = %q, want main.py", got)
	}
	if got := ui.LanguageIconName("rust"); got != "main.rs" {
		t.Errorf("rust icon file = %q, want main.rs", got)
	}
}
