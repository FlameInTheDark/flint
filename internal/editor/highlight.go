package editor

import (
	"strings"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/font"
	"github.com/FlameInTheDark/go-gpui/text"
)

// Palette carries the highlight colors (a subset of the UI theme so this
// package stays independent of the ui package).
type Palette struct {
	Keyword  gc.Hsla
	Type     gc.Hsla
	String   gc.Hsla
	Comment  gc.Hsla
	Number   gc.Hsla
	Function gc.Hsla
	Field    gc.Hsla
	Text     gc.Hsla
	Heading  gc.Hsla
	Link     gc.Hsla
}

// langByExt maps file extensions to highlight languages (the language
// picker's detection side; overrides go through Buffer.LangOverride).
var langByExt = map[string]string{
	".go": "go", ".md": "markdown", ".markdown": "markdown",
	".c": "c", ".h": "c",
	".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp",
	".cs":   "csharp",
	".java": "java",
	".js":   "javascript", ".jsx": "javascript", ".mjs": "javascript",
	".ts": "typescript", ".tsx": "typescript",
	".py":  "python",
	".rs":  "rust",
	".rb":  "ruby",
	".php": "php",
	".sh":  "shell", ".bash": "shell", ".zsh": "shell",
	".sql":  "sql",
	".json": "json",
	".css":  "css",
	".html": "html", ".htm": "html",
	".toml": "toml", ".yaml": "yaml", ".yml": "yaml",
	".xml": "xml",
	".txt": "text",
}

// Languages is the status-bar picker's catalog: every language the editor
// knows (display name, id, common extensions). Selections drive
// highlighting; languages without a dedicated tokenizer fall back to the
// plain text runs but keep their name in the bar.
var Languages = []LanguageInfo{
	{"Plain Text", "text", []string{"txt"}},
	{"Go", "go", []string{"go"}},
	{"Markdown", "markdown", []string{"md", "markdown"}},
	{"C", "c", []string{"c", "h"}},
	{"C++", "cpp", []string{"cpp", "cc", "cxx", "hpp"}},
	{"C#", "csharp", []string{"cs"}},
	{"CSS", "css", []string{"css"}},
	{"HTML", "html", []string{"html", "htm"}},
	{"Java", "java", []string{"java"}},
	{"JavaScript", "javascript", []string{"js", "jsx", "mjs"}},
	{"JSON", "json", []string{"json"}},
	{"PHP", "php", []string{"php"}},
	{"Python", "python", []string{"py"}},
	{"Ruby", "ruby", []string{"rb"}},
	{"Rust", "rust", []string{"rs"}},
	{"Shell Script", "shell", []string{"sh", "bash", "zsh"}},
	{"SQL", "sql", []string{"sql"}},
	{"TOML", "toml", []string{"toml"}},
	{"TypeScript", "typescript", []string{"ts", "tsx"}},
	{"XML", "xml", []string{"xml"}},
	{"YAML", "yaml", []string{"yaml", "yml"}},
}

// LanguageInfo is one picker entry.
type LanguageInfo struct {
	Name string // display
	Id   string // highlight id
	Exts []string
}

// LanguageLabel returns the display name of a language id.
func LanguageLabel(id string) string {
	for _, l := range Languages {
		if l.Id == id {
			return l.Name
		}
	}
	return id
}

// Language detects the highlighting language from a file name.
func Language(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return "text"
	}
	if lang, ok := langByExt[strings.ToLower(name[i:])]; ok {
		return lang
	}
	return "text"
}

// Highlight tokenizes one line into styled runs.
func Highlight(line, lang string, p Palette) []text.TextRun {
	switch lang {
	case "go":
		return goRuns(line, p)
	case "markdown":
		return markdownRuns(line, p)
	case "json":
		return jsonRuns(line, p)
	case "yaml", "toml":
		return yamlRuns(line, p)
	default:
		if def := genericLang[lang]; def != nil {
			return genericRuns(line, def, p)
		}
		return []text.TextRun{{Len: len(line), Font: mono(false), Color: p.Text}}
	}
}

func mono(bold bool) font.Font {
	if bold {
		return font.Font{Family: "Monospace", Weight: font.WeightBold}
	}
	return font.Font{Family: "Monospace", Weight: font.WeightNormal}
}

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

var goTypes = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true, "int8": true,
	"int16": true, "int32": true, "int64": true, "rune": true, "string": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "any": true,
}

var goConstants = map[string]bool{
	"true": true, "false": true, "nil": true, "iota": true,
}

// goRuns tokenizes one line of Go source.
func goRuns(line string, p Palette) []text.TextRun {
	var runs []text.TextRun
	rest := line
	for len(rest) > 0 {
		_, color, bold, n := goToken(rest, p)
		f := mono(false)
		if bold {
			f = mono(true)
		}
		runs = append(runs, text.TextRun{Len: n, Font: f, Color: color})
		rest = rest[n:]
	}
	return runs
}

func goToken(s string, p Palette) (string, gc.Hsla, bool, int) {
	switch {
	case strings.HasPrefix(s, "//"):
		return s, p.Comment, false, len(s)
	case strings.HasPrefix(s, "\""):
		if end := strings.IndexByte(s[1:], '"'); end >= 0 {
			return s[:end+2], p.String, false, end + 2
		}
		return s, p.String, false, len(s)
	case strings.HasPrefix(s, "`"):
		if end := strings.IndexByte(s[1:], '`'); end >= 0 {
			return s[:end+2], p.String, false, end + 2
		}
		return s, p.String, false, len(s)
	}
	n := strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	})
	if n <= 0 {
		n = 1
		return s[:n], p.Text, false, n
	}
	word := s[:n]
	switch {
	case goKeywords[word]:
		return word, p.Keyword, true, n
	case goTypes[word]:
		return word, p.Type, false, n
	case goConstants[word]:
		return word, p.Keyword, false, n
	case word[0] >= 'A' && word[0] <= 'Z':
		return word, p.Type, false, n
	case word[0] >= '0' && word[0] <= '9':
		return word, p.Number, false, n
	}
	// Call vs field: peek the next non-space char.
	rest := s[n:]
	rest = strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(rest, "(") {
		return word, p.Function, false, n
	}
	if strings.HasPrefix(rest, ".") {
		return word, p.Field, false, n
	}
	return word, p.Text, false, n
}

// markdownRuns tokenizes one line of markdown.
func markdownRuns(line string, p Palette) []text.TextRun {
	if strings.HasPrefix(line, "#") {
		return []text.TextRun{{Len: len(line), Font: font.Font{Family: "", Weight: font.WeightBold}, Color: p.Heading}}
	}
	// Inline `code` spans and [links].
	var runs []text.TextRun
	rest := line
	for len(rest) > 0 {
		if strings.HasPrefix(rest, "`") {
			if end := strings.IndexByte(rest[1:], '`'); end >= 0 {
				runs = append(runs, text.TextRun{Len: end + 2, Font: mono(false), Color: p.String})
				rest = rest[end+2:]
				continue
			}
		}
		if strings.HasPrefix(rest, "[") {
			if end := strings.IndexByte(rest, ']'); end >= 0 {
				runs = append(runs, text.TextRun{Len: end + 1, Font: mono(false), Color: p.Link})
				rest = rest[end+1:]
				continue
			}
		}
		i := strings.IndexAny(rest, "`[")
		if i <= 0 {
			i = len(rest)
			if i == 0 {
				i = 1
			}
		}
		runs = append(runs, text.TextRun{Len: i, Font: mono(false), Color: p.Text})
		rest = rest[i:]
	}
	return runs
}

// Overlay applies selection and find-match backgrounds over a line's runs.
// lineStart is the line's byte offset in the buffer; sel and match ranges
// are buffer-wide [start,end) pairs (nil = none).
func Overlay(runs []text.TextRun, lineStart int, lineText string,
	selStart, selEnd int, matches [][2]int, selBg, matchBg *gc.Hsla) []text.TextRun {

	type span struct {
		start, end int
		sel, match bool
	}
	var spans []span
	for _, m := range matches {
		spans = append(spans, span{m[0], m[1], false, true})
	}
	if selEnd > selStart {
		spans = append(spans, span{selStart, selEnd, true, false})
	}
	if len(spans) == 0 {
		return runs
	}
	lineEnd := lineStart + len(lineText)
	for _, sp := range spans {
		if sp.end <= lineStart || sp.start >= lineEnd {
			continue
		}
		// Clip to this line and split runs.
		rs := sp.start
		if rs < lineStart {
			rs = lineStart
		}
		re := sp.end
		if re > lineEnd {
			re = lineEnd
		}
		runs = splitRunsWithBg(runs, rs-lineStart, re-lineStart, sp.sel, sp.match, selBg, matchBg)
	}
	return runs
}

// splitRunsWithBg sets a background on [rs,re) (line-relative bytes),
// splitting runs as needed. Selection wins over match when overlapping.
func splitRunsWithBg(runs []text.TextRun, rs, re int, isSel, isMatch bool, selBg, matchBg *gc.Hsla) []text.TextRun {
	var out []text.TextRun
	pos := 0
	for _, r := range runs {
		start, end := pos, pos+r.Len
		if end <= rs || start >= re {
			out = append(out, r)
			pos = end
			continue
		}
		// head
		if start < rs {
			out = append(out, text.TextRun{Len: rs - start, Font: r.Font, Color: r.Color})
		}
		// highlighted middle
		ms := max(start, rs)
		me := min(end, re)
		mid := r
		mid.Len = me - ms
		if isSel {
			mid.BackgroundColor = selBg
		} else {
			mid.BackgroundColor = matchBg
		}
		out = append(out, mid)
		// tail
		if end > re {
			out = append(out, text.TextRun{Len: end - re, Font: r.Font, Color: r.Color})
		}
		pos = end
	}
	return out
}

// OverlaySpans uses byte offsets in the displayed (tab-expanded) text.
// Selection wins when spans overlap (it is applied last).
func OverlaySpans(runs []text.TextRun, spans []Span, selBg, matchBg *gc.Hsla) []text.TextRun {
	if len(spans) == 0 {
		return runs
	}
	for _, sp := range spans {
		runs = splitRunsWithBg(runs, sp.Start, sp.End, sp.Sel, !sp.Sel, selBg, matchBg)
	}
	return runs
}
