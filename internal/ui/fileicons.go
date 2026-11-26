// File-type icons: one Lucide-style glyph per file type, each in a fixed
// accent color (brand-like colors work on every theme, like the VS Code
// icon themes). Every icon shares the same 24x24 stroke geometry —
// stroke-width 2, round caps and joins — so the whole set reads as one
// family. Unknown types keep the plain document glyph in the theme's dim
// color. Geometry comes from Lucide 0.468.0 (see NOTICE); the markdown
// glyph is this project's own drawing in the same style.

package ui

import (
	"fmt"
	"strings"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/elements"
	"github.com/FlameInTheDark/go-gpui/gpui"
)

// fileIconSpec is one file type: the glyph shape's key and its fixed hex
// color.
type fileIconSpec struct {
	shape string
	color string // #RRGGBB
}

// fileTypeNames maps whole file names (case-insensitive) to icons.
var fileTypeNames = map[string]fileIconSpec{
	"makefile":    {"hammer", "#6DB26D"},
	"gnumakefile": {"hammer", "#6DB26D"},
	"dockerfile":  {"package", "#5BA8D4"},
	"license":     {"scale", "#8A9199"},
	"license.txt": {"scale", "#8A9199"},
	"readme":      {"file-text", "#519ABA"},
	"go.work":     {"package", "#00ADD8"},
}

// fileTypeExts maps file extensions (lowercase, with dot) to icons.
var fileTypeExts = map[string]fileIconSpec{
	// Brand marks: language icons use the same glyphs the major IDEs show
	// (GoLand / Zed / VS Code icon themes), not generic shapes — the Go
	// wordmark for Go files, the two-snake mark for Python, the gear for
	// Rust.
	".go":   {"go-logo", "#00ADD8"},
	".mod":  {"package", "#00ADD8"},
	".sum":  {"hash", "#58A6CE"},
	".work": {"package", "#00ADD8"},

	".json":  {"braces", "#CBCB41"},
	".jsonc": {"braces", "#CBCB41"},

	".yaml": {"list", "#D19A66"},
	".yml":  {"list", "#D19A66"},
	".toml": {"settings-2", "#D19A66"},
	".ini":  {"settings-2", "#D19A66"},
	".cfg":  {"settings-2", "#D19A66"},
	".conf": {"settings-2", "#D19A66"},
	".env":  {"settings-2", "#D19A66"},

	".md":       {"markdown", "#519ABA"},
	".markdown": {"markdown", "#519ABA"},

	".html": {"globe", "#E37933"},
	".htm":  {"globe", "#E37933"},
	".xml":  {"code", "#E37933"},
	".css":  {"braces", "#519ABA"},
	".scss": {"braces", "#C7709C"},
	".less": {"braces", "#519ABA"},

	".js":  {"code", "#CBCB41"},
	".jsx": {"code", "#CBCB41"},
	".mjs": {"code", "#CBCB41"},
	".cjs": {"code", "#CBCB41"},
	".ts":  {"code", "#519ABA"},
	".tsx": {"code", "#519ABA"},

	".py":     {"python-logo", "#3776AB"},
	".pyw":    {"python-logo", "#3776AB"},
	".rs":     {"rust-logo", "#DEA584"},
	".c":      {"code", "#A8B9CC"},
	".h":      {"code", "#A8B9CC"},
	".cpp":    {"code", "#A8B9CC"},
	".cc":     {"code", "#A8B9CC"},
	".cxx":    {"code", "#A8B9CC"},
	".hpp":    {"code", "#A8B9CC"},
	".hh":     {"code", "#A8B9CC"},
	".cs":     {"code", "#68A063"},
	".java":   {"code", "#B07219"},
	".kt":     {"code", "#B07219"},
	".swift":  {"code", "#DE7B5F"},
	".rb":     {"code", "#CC5E56"},
	".php":    {"code", "#8993BE"},
	".lua":    {"code", "#519ABA"},
	".dart":   {"code", "#519ABA"},
	".scala":  {"code", "#B07219"},
	".pl":     {"code", "#8A9BD4"},
	".vue":    {"code", "#519ABA"},
	".svelte": {"code", "#D97E4A"},

	".sh":   {"terminal", "#86C46B"},
	".bash": {"terminal", "#86C46B"},
	".zsh":  {"terminal", "#86C46B"},
	".fish": {"terminal", "#86C46B"},
	".bat":  {"terminal", "#86C46B"},
	".cmd":  {"terminal", "#86C46B"},
	".ps1":  {"terminal", "#86C46B"},

	".sql": {"database", "#D38677"},
	".csv": {"table", "#6DB26D"},
	".tsv": {"table", "#6DB26D"},

	".png":  {"image", "#A074C4"},
	".jpg":  {"image", "#A074C4"},
	".jpeg": {"image", "#A074C4"},
	".gif":  {"image", "#A074C4"},
	".bmp":  {"image", "#A074C4"},
	".webp": {"image", "#A074C4"},
	".svg":  {"image", "#A074C4"},
	".ico":  {"image", "#A074C4"},
	".tiff": {"image", "#A074C4"},
	".psd":  {"image", "#A074C4"},

	".mp4":  {"video", "#C9825E"},
	".mov":  {"video", "#C9825E"},
	".avi":  {"video", "#C9825E"},
	".mkv":  {"video", "#C9825E"},
	".webm": {"video", "#C9825E"},
	".m4v":  {"video", "#C9825E"},

	".mp3":  {"music", "#7EC4A2"},
	".wav":  {"music", "#7EC4A2"},
	".ogg":  {"music", "#7EC4A2"},
	".flac": {"music", "#7EC4A2"},
	".m4a":  {"music", "#7EC4A2"},
	".aac":  {"music", "#7EC4A2"},

	".zip": {"archive", "#C9A26E"},
	".tar": {"archive", "#C9A26E"},
	".gz":  {"archive", "#C9A26E"},
	".tgz": {"archive", "#C9A26E"},
	".bz2": {"archive", "#C9A26E"},
	".xz":  {"archive", "#C9A26E"},
	".7z":  {"archive", "#C9A26E"},
	".rar": {"archive", "#C9A26E"},
	".iso": {"archive", "#C9A26E"},

	".exe":   {"cpu", "#8FA4B8"},
	".dll":   {"cpu", "#8FA4B8"},
	".so":    {"cpu", "#8FA4B8"},
	".dylib": {"cpu", "#8FA4B8"},
	".bin":   {"cpu", "#8FA4B8"},
	".o":     {"cpu", "#8FA4B8"},
	".a":     {"cpu", "#8FA4B8"},
	".out":   {"cpu", "#8FA4B8"},
	".app":   {"cpu", "#8FA4B8"},
	".class": {"cpu", "#8FA4B8"},
	".pyc":   {"cpu", "#8FA4B8"},
	".wasm":  {"cpu", "#8FA4B8"},

	".txt":  {"file-text", "#8A9199"},
	".log":  {"file-text", "#8A9199"},
	".pdf":  {"file-text", "#DE6B6B"},
	".doc":  {"file-text", "#4C8FC4"},
	".docx": {"file-text", "#4C8FC4"},
	".rtf":  {"file-text", "#4C8FC4"},
	".xls":  {"file-text", "#6DB26D"},
	".xlsx": {"file-text", "#6DB26D"},
	".ppt":  {"file-text", "#D97E4A"},
	".pptx": {"file-text", "#D97E4A"},

	".ttf":   {"type", "#B48EAD"},
	".otf":   {"type", "#B48EAD"},
	".woff":  {"type", "#B48EAD"},
	".woff2": {"type", "#B48EAD"},
	".eot":   {"type", "#B48EAD"},

	".lock": {"lock", "#C9A26E"},
	".pem":  {"lock", "#C9A26E"},
	".key":  {"lock", "#C9A26E"},
	".crt":  {"shield", "#6DB26D"},
	".cer":  {"shield", "#6DB26D"},

	".gitignore":     {"git-branch", "#D9715E"},
	".gitattributes": {"git-branch", "#D9715E"},
	".gitmodules":    {"git-branch", "#D9715E"},

	".mk": {"hammer", "#6DB26D"},
}

// fileTypeShapes holds the glyph geometry (Lucide 0.468.0 inner markup;
// see NOTICE). Colors come from the spec, so the shapes stay plain. The
// brand marks (go-logo, python-logo, rust-logo) are this project's own
// drawings in the same stroke style, matched to the marks GoLand, Zed and
// the VS Code icon themes show.
var fileTypeShapes = map[string]string{
	// Go wordmark: three speed dashes, a G (arc with the inward bar) and
	// an O — the official Go logo, drawn in one cyan stroke. The wider
	// viewBox gives the wordmark room; the svg box keeps it centered.
	"go-logo":    `<line x1="1" x2="4.2" y1="7.6" y2="7.6" /><line x1="1" x2="4.4" y1="12" y2="12" /><line x1="1" x2="4.2" y1="16.4" y2="16.4" /><path d="M13.4 8.6A4.2 4.2 0 1 0 15.2 12L13 12" /><circle cx="22" cy="12" r="4.2" />`,
	"package":    `<path d="M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4z" /><path d="M12 22V12" /><path d="m3.3 7 8.7 5 8.7-5" />`,
	"hash":       `<line x1="4" x2="20" y1="9" y2="9" /><line x1="4" x2="20" y1="15" y2="15" /><line x1="10" x2="8" y1="3" y2="21" /><line x1="16" x2="14" y1="3" y2="21" />`,
	"braces":     `<path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5c0 1.1.9 2 2 2h1" /><path d="M16 21h1a2 2 0 0 0 2-2v-5c0-1.1.9-2 2-2a2 2 0 0 1-2-2V5a2 2 0 0 0-2-2h-1" />`,
	"code":       `<path d="m16 18 6-6-6-6" /><path d="m8 6-6 6 6 6" />`,
	"globe":      `<circle cx="12" cy="12" r="10" /><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20" /><path d="M2 12h20" />`,
	"list":       `<line x1="8" x2="21" y1="6" y2="6" /><line x1="8" x2="21" y1="12" y2="12" /><line x1="8" x2="21" y1="18" y2="18" /><line x1="3" x2="3.01" y1="6" y2="6" /><line x1="3" x2="3.01" y1="12" y2="12" /><line x1="3" x2="3.01" y1="18" y2="18" />`,
	"settings-2": `<path d="M20 7h-9" /><path d="M14 17H5" /><circle cx="17" cy="17" r="3" /><circle cx="7" cy="7" r="3" />`,
	"markdown":   `<path d="M4 16V8l4 4 4-4v8" /><path d="M17 8v8" /><path d="m14 13 3 3 3-3" />`,
	"terminal":   `<polyline points="4 17 10 11 4 5" /><line x1="12" x2="20" y1="19" y2="19" />`,
	"database":   `<ellipse cx="12" cy="5" rx="9" ry="3" /><path d="M3 5V19A9 3 0 0 0 21 19V5" /><path d="M3 12A9 3 0 0 0 21 12" />`,
	"table":      `<rect width="18" height="18" x="3" y="3" rx="2" /><path d="M12 3v18" /><path d="M3 9h18" /><path d="M3 15h18" />`,
	"image":      `<rect width="18" height="18" x="3" y="3" rx="2" ry="2" /><circle cx="9" cy="9" r="2" /><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />`,
	"video":      `<circle cx="12" cy="12" r="10" /><polygon points="10 8 16 12 10 16 10 8" />`,
	"music":      `<path d="M9 18V5l12-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="18" cy="16" r="3" />`,
	"archive":    `<rect width="20" height="5" x="2" y="3" rx="1" /><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8" /><path d="M10 12h4" />`,
	"cpu":        `<rect x="4" y="4" width="16" height="16" rx="2" /><rect x="9" y="9" width="6" height="6" /><path d="M15 2v2" /><path d="M15 20v2" /><path d="M2 15h2" /><path d="M2 9h2" /><path d="M20 15h2" /><path d="M20 9h2" /><path d="M9 2v2" /><path d="M9 20v2" />`,
	"type":       `<polyline points="4 7 4 4 20 4 20 7" /><line x1="9" x2="15" y1="20" y2="20" /><line x1="12" x2="12" y1="4" y2="20" />`,
	"file-text":  `<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z" /><path d="M14 2v4a2 2 0 0 0 2 2h4" /><path d="M10 9H8" /><path d="M16 13H8" /><path d="M16 17H8" />`,
	"scale":      `<path d="m16 16 3-8 3 8c-.87.65-1.92 1-3 1s-2.13-.35-3-1Z" /><path d="m2 16 3-8 3 8c-.87.65-1.92 1-3 1s-2.13-.35-3-1Z" /><path d="M7 21h10" /><path d="M12 3v18" /><path d="M3 7h2c2 0 5-1 7-2 2 1 5 2 7 2h2" />`,
	"lock":       `<rect width="18" height="11" x="3" y="11" rx="2" ry="2" /><path d="M7 11V7a5 5 0 0 1 10 0v4" />`,
	"shield":     `<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z" />`,
	"git-branch": `<line x1="6" x2="6" y1="3" y2="15" /><circle cx="18" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="M18 9a9 9 0 0 1-9 9" />`,
	"hammer":     `<path d="m15 12-8.373 8.373a1 1 0 1 1-3-3L12 9" /><path d="m18 15 4-4" /><path d="m21.5 11.5-1.914-1.914A2 2 0 0 1 19 8.172V7l-2.26-2.26a6 6 0 0 0-4.202-1.756L9 2.96l.92.82A6.18 6.18 0 0 1 12 8.4V10l2 2h1.172a2 2 0 0 1 1.414.586L18.5 14.5" />`,
	// Python: the two interlocking snakes — blue upper hook with the eye,
	// yellow lower hook with the eye — per-element colors, so this shape
	// carries its own stroke attributes.
	"python-logo": `<path stroke="#3776AB" d="M12 3.2H8.4a3.2 3.2 0 0 0-3.2 3.2v3.6a3.2 3.2 0 0 0 3.2 3.2H12" /><circle cx="8.6" cy="6.7" r="1" fill="#3776AB" stroke="none" /><path stroke="#FFD43B" d="M12 20.8h3.6a3.2 3.2 0 0 0 3.2-3.2V14a3.2 3.2 0 0 0-3.2-3.2H12" /><circle cx="15.4" cy="17.3" r="1" fill="#FFD43B" stroke="none" />`,
	// Rust: the gear — a ring, a hub and eight teeth (the Rust logo's
	// cog, without the R which disappears at icon sizes).
	"rust-logo": `<circle cx="12" cy="12" r="5.2" /><circle cx="12" cy="12" r="1.6" /><line x1="18.6" x2="20.6" y1="12" y2="12" /><line x1="16.67" x2="18.08" y1="16.67" y2="18.08" /><line x1="12" x2="12" y1="18.6" y2="20.6" /><line x1="7.33" x2="5.92" y1="16.67" y2="18.08" /><line x1="5.4" x2="3.4" y1="12" y2="12" /><line x1="7.33" x2="5.92" y1="7.33" y2="5.92" /><line x1="12" x2="12" y1="5.4" y2="3.4" /><line x1="16.67" x2="18.08" y1="7.33" y2="5.92" />`,
}

// fileTypeViewBoxes overrides the 24x24 box for shapes whose geometry
// needs it (the Go wordmark's extra width).
var fileTypeViewBoxes = map[string]string{
	"go-logo": "0 0 28 24",
}

// FileTypeIconName resolves a file name to its icon shape key ("" = the
// generic document icon). Whole names win over extensions, so a Makefile
// never renders as an unknown "ke" file.
func FileTypeIconName(name string) string {
	if spec, ok := fileTypeNames[strings.ToLower(name)]; ok {
		return spec.shape
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		if spec, ok := fileTypeExts[strings.ToLower(name[i:])]; ok {
			return spec.shape
		}
	}
	return ""
}

// FileTypeIcon renders the file-type glyph for a file name. Unknown types
// fall back to the plain document glyph in the theme's dim color.
func FileTypeIcon(name string, t Theme) gpui.Element {
	if spec, ok := fileTypeNames[strings.ToLower(name)]; ok {
		return fileTypeSvg(spec)
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		if spec, ok := fileTypeExts[strings.ToLower(name[i:])]; ok {
			return fileTypeSvg(spec)
		}
	}
	return FileIcon(t.Dim)
}

func fileTypeSvg(spec fileIconSpec) gpui.Element {
	shape := fileTypeShapes[spec.shape]
	if shape == "" {
		shape = lucideIcons["file"]
	}
	viewBox := "0 0 24 24"
	if vb, ok := fileTypeViewBoxes[spec.shape]; ok {
		viewBox = vb
	}
	// Brand shapes that carry per-element stroke colors (the two-color
	// Python mark) omit the wrapper's global stroke attribute.
	strokeAttr := fmt.Sprintf(` stroke="%s"`, spec.color)
	if strings.Contains(shape, `stroke="`) {
		strokeAttr = ""
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="%s" fill="none"%s stroke-width="2" stroke-linecap="round" stroke-linejoin="round">%s</svg>`,
		viewBox, strokeAttr, shape)
	return elements.NewSvgFromData([]byte(svg)).W(Px(14)).H(Px(14)).FlexShrink(0)
}

// FileAccentColor exposes a file type's icon color for callers that paint
// related chrome (unused fallback = the theme's dim color).
func FileAccentColor(name string, t Theme) gc.Hsla {
	if spec, ok := fileTypeNames[strings.ToLower(name)]; ok {
		return parseHexColor(spec.color)
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		if spec, ok := fileTypeExts[strings.ToLower(name[i:])]; ok {
			return parseHexColor(spec.color)
		}
	}
	return t.Dim
}

// parseHexColor parses a #RRGGBB constant (the icon palette's own format).
func parseHexColor(hex string) gc.Hsla {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return gc.RGB(0x808080).Hsla()
	}
	v := int64(0)
	for i := 0; i < 6; i++ {
		c := hex[i]
		var d int64
		switch {
		case c >= '0' && c <= '9':
			d = int64(c - '0')
		case c >= 'a' && c <= 'f':
			d = int64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int64(c-'A') + 10
		default:
			return gc.RGB(0x808080).Hsla()
		}
		v = v*16 + d
	}
	return gc.RGB(uint32(v>>16)&0xFF<<16 | uint32(v>>8)&0xFF<<8 | uint32(v)&0xFF).Hsla()
}
