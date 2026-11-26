package ui

import "testing"

func TestFileTypeIconNameResolution(t *testing.T) {
	cases := map[string]string{
		"main.go":     "go-logo",
		"go.mod":      "package",
		"go.sum":      "hash",
		"go.work":     "package",
		"data.json":   "braces",
		"style.css":   "braces",
		"cfg.yaml":    "list",
		"cfg.yml":     "list",
		"app.toml":    "settings-2",
		"notes.md":    "markdown",
		"index.html":  "globe",
		"site.xml":    "code",
		"app.js":      "code",
		"app.ts":      "code",
		"calc.py":     "python-logo",
		"lib.rs":      "rust-logo",
		"core.c":      "code",
		"run.sh":      "terminal",
		"query.sql":   "database",
		"photo.png":   "image",
		"pic.jpg":     "image",
		"clip.mp4":    "video",
		"song.mp3":    "music",
		"bundle.zip":  "archive",
		"pack.tar.gz": "archive",
		"app.exe":     "cpu",
		"lib.so":      "cpu",
		"readme.txt":  "file-text",
		"doc.pdf":     "file-text",
		"font.ttf":    "type",
		"Dockerfile":  "package",
		"Makefile":    "hammer",
		"makefile":    "hammer",
		"LICENSE":     "scale",
		"cargo.lock":  "lock",
		"creds.pem":   "lock",
		"site.crt":    "shield",
		".gitignore":  "git-branch",
	}
	for name, want := range cases {
		if got := FileTypeIconName(name); got != want {
			t.Errorf("%s -> %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"unknown.xyz", "Makefile2", "json"} {
		if got := FileTypeIconName(name); got != "" {
			t.Errorf("%s -> %q, want the fallback \"\"", name, got)
		}
	}
}

func TestFileTypeIconCaseInsensitive(t *testing.T) {
	if got := FileTypeIconName("MAIN.GO"); got != "go-logo" {
		t.Fatalf("MAIN.GO -> %q", got)
	}
	if got := FileTypeIconName("Photo.PNG"); got != "image" {
		t.Fatalf("Photo.PNG -> %q", got)
	}
}

func TestFileAccentColorParsed(t *testing.T) {
	theme := DefaultTheme()
	c := FileAccentColor("main.go", theme)
	rgb := c.ToRgb()
	// The Go accent is #00ADD8.
	if absI := func(a, b int) int {
		if a < b {
			return b - a
		}
		return a - b
	}; absI(int(rgb.R*255+0.5), 0x00) > 1 || absI(int(rgb.G*255+0.5), 0xAD) > 1 || absI(int(rgb.B*255+0.5), 0xD8) > 1 {
		t.Fatalf("go accent = %#v, want #00ADD8", rgb)
	}
	// Unknown types fall back to the theme's dim color.
	if FileAccentColor("unknown.xyz", theme) != theme.Dim {
		t.Fatal("unknown extension must fall back to theme.Dim")
	}
}
