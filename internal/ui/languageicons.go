// Language picker icons: every catalog language shows the SAME artwork
// its files get in the file tree — for the branded languages the official
// devicon logo (langlogos.go: the Go wordmark, the Python two-snake mark,
// the Rust gear, the Java coffee cup, the JS/TS badges, and so on — the
// same family VS Code's icon themes, GoLand and Zed draw from), for the
// generic formats the matching Lucide-style glyph (braces for JSON, lists
// for YAML, and so on). Unknown languages keep the plain document glyph
// in the theme's dim color — exactly like an unrecognized file in the
// tree.

package ui

import "github.com/FlameInTheDark/go-gpui/gpui"

// languageIconFile maps a language catalog id (the ids used by the status
// bar and the picker; the editor's language table) to a representative
// file name whose file-type icon the picker row shows. The file name
// resolves through the same whole-name/extension tables the file tree
// uses, so picker and tree can never drift apart.
var languageIconFile = map[string]string{
	"text":       "notes.txt",
	"go":         "main.go",
	"gomod":      "go.mod",
	"gosum":      "go.sum",
	"markdown":   "notes.md",
	"c":          "main.c",
	"cpp":        "main.cpp",
	"csharp":     "Program.cs",
	"css":        "style.css",
	"html":       "index.html",
	"java":       "Main.java",
	"javascript": "script.js",
	"json":       "data.json",
	"php":        "index.php",
	"python":     "main.py",
	"ruby":       "app.rb",
	"rust":       "main.rs",
	"shell":      "run.sh",
	"sql":        "query.sql",
	"toml":       "config.toml",
	"typescript": "app.ts",
	"xml":        "config.xml",
	"yaml":       "config.yaml",
}

// LanguageIconName resolves a language id to the representative file name
// its picker icon is built from ("" = the generic document icon). Exposed
// for tests: every catalog language must resolve to a real file icon.
func LanguageIconName(langId string) string {
	return languageIconFile[langId]
}

// LanguageIcon renders the picker row's icon: the same glyph the file tree
// shows for the language's files, in the tree's fixed accent color.
// Unknown languages fall back to the plain document glyph in the theme's
// dim color.
func LanguageIcon(langId string, t Theme) gpui.Element {
	if f, ok := languageIconFile[langId]; ok {
		return FileTypeIcon(f, t)
	}
	return FileIcon(t.Dim)
}
