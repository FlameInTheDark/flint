package workspace_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FlameInTheDark/flint/internal/workspace"
)

// TestVisualMinimapScenes renders the upstream-style minimap at several scroll
// positions of a 5000-line document (content slides slowly, rail constant).
func TestVisualMinimapScenes(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&sb, "// line %d: some code with an identifier = %d\n", i+1, i)
	}
	hp, handle, cx := openEditorWindowWith(t, sb.String())
	for i, s := range []int{0, 1200, 2500, 4900} {
		editorUpdate(handle, cx, func(st *workspace.Workspace) { st.SetScrolls(s) })
		handle.Window.Draw()
		editorShot(t, hp, "/tmp/render", fmt.Sprintf("minimap_s%d.png", i))
	}
}
