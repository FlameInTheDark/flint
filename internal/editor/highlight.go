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
	// Todo colors TODO/FIXME tags inside comments (GoLand paints them
	// in a dedicated color so they jump out of gray comment text).
	Todo gc.Hsla
}

// langByExt maps file extensions to highlight languages (the language
// picker's detection side; overrides go through Buffer.LangOverride).
var langByExt = map[string]string{
	".go": "go", ".md": "markdown", ".markdown": "markdown",
	".mod": "gomod", ".sum": "gosum",
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
	{"Go Module", "gomod", []string{"mod"}},
	{"Go Sum", "gosum", []string{"sum"}},
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

// LineState carries the highlighting context from one line to the next:
// Go raw strings (`...`), C-like block comments (/* ... */) and Python
// triple-quoted strings span lines, so a line's colors depend on how the
// text before it ended. Zero is the top-of-file context.
type LineState uint8

const (
	// StateNormal starts a line outside any multi-line construct.
	StateNormal LineState = 0
	// StateRawString: inside a `...` literal that opened on an earlier
	// line (Go raw string, JS/TS template literal, shell command
	// substitution).
	StateRawString LineState = 1
	// StateBlockComment: inside a /* ... */ comment that opened on an
	// earlier line (Go and the C-like generic languages).
	StateBlockComment LineState = 2
	// StateTripleDouble / StateTripleSingle: inside a Python """..."""
	// or '''...''' string that opened on an earlier line.
	StateTripleDouble LineState = 4
	StateTripleSingle LineState = 8
)

// Highlight tokenizes one line into styled runs (top-of-file context; use
// HighlightIn for a line that continues a multi-line construct).
func Highlight(line, lang string, p Palette) []text.TextRun {
	runs, _ := HighlightIn(line, lang, StateNormal, p)
	return runs
}

// HighlightIn tokenizes one line starting inside the given cross-line
// context. It returns the styled runs plus the context the next line
// starts in (raw string / block comment / triple quote still open, or
// StateNormal).
func HighlightIn(line, lang string, st LineState, p Palette) ([]text.TextRun, LineState) {
	runs, end := highlightIn(line, lang, st, p)
	return splitTodoTags(runs, line, p), end
}

// highlightIn is HighlightIn's tokenizer dispatch.
func highlightIn(line, lang string, st LineState, p Palette) ([]text.TextRun, LineState) {
	switch lang {
	case "go":
		return goRunsIn(line, st, p)
	case "gomod":
		return gomodRunsIn(line, st, p)
	case "gosum":
		return gosumRuns(line, p), StateNormal
	case "markdown":
		return markdownRuns(line, p), StateNormal
	case "json":
		return jsonRuns(line, p), StateNormal
	case "yaml", "toml":
		return yamlRuns(line, p), StateNormal
	default:
		if def := genericLang[lang]; def != nil {
			return genericRunsIn(line, def, st, p)
		}
		return []text.TextRun{{Len: len(line), Font: mono(false), Color: p.Text}}, StateNormal
	}
}

// todoTags are the comment tag words repainted with the palette's Todo
// color (GoLand's default TODO/FIXME patterns, matched word-bounded and
// case-insensitively; the workspace's TODO panel scans the same tags).
var todoTags = []string{"todo", "fixme"}

// splitTodoTags repaints TODO/FIXME tag words inside comment-colored runs
// with the palette's Todo color, bold like GoLand's TODO style. Tags only
// repaint inside comments (the runs whose color is exactly p.Comment), so
// "TODO" in a string literal keeps its string color. A palette without a
// Todo color (zero alpha) skips the pass.
func splitTodoTags(runs []text.TextRun, line string, p Palette) []text.TextRun {
	if p.Todo.A == 0 {
		return runs
	}
	var out []text.TextRun
	off := 0
	for _, r := range runs {
		start, end := off, off+r.Len
		off = end
		if end > len(line) || r.Color != p.Comment {
			out = append(out, r)
			continue
		}
		seg := line[start:end]
		matches := [][2]int{}
		for i := 0; i < len(seg); {
			if i > 0 && isTodoWordByte(seg[i-1]) {
				i++
				continue
			}
			n := 0
			for _, tag := range todoTags {
				if len(tag) > n && i+len(tag) <= len(seg) &&
					strings.EqualFold(seg[i:i+len(tag)], tag) {
					n = len(tag)
				}
			}
			if n == 0 {
				i++
				continue
			}
			// Word-boundary on the right edge.
			if i+n < len(seg) && isTodoWordByte(seg[i+n]) {
				i++
				continue
			}
			matches = append(matches, [2]int{i, i + n})
			i += n
		}
		if len(matches) == 0 {
			out = append(out, r)
			continue
		}
		prev := 0
		for _, m := range matches {
			if m[0] > prev {
				out = append(out, text.TextRun{Len: m[0] - prev, Font: r.Font, Color: r.Color})
			}
			out = append(out, text.TextRun{Len: m[1] - m[0], Font: mono(true), Color: p.Todo})
			prev = m[1]
		}
		if prev < len(seg) {
			out = append(out, text.TextRun{Len: len(seg) - prev, Font: r.Font, Color: r.Color})
		}
	}
	return out
}

func isTodoWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// nextLineState scans line for construct boundaries only (no runs) and
// returns the context the next line starts in. The scanners and the
// tokenizers must agree on every transition; highlight_state_test.go
// asserts that for each language.
func nextLineState(line, lang string, st LineState) LineState {
	switch lang {
	case "go", "gomod":
		// go.mod quotes and comments lex exactly like Go's.
		return goScanState(line, st)
	case "markdown", "json", "yaml", "toml", "gosum", "text":
		return StateNormal // these languages have no multi-line constructs
	}
	if def := genericLang[lang]; def != nil {
		return genericScanState(line, def, st)
	}
	return StateNormal
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

// scanQuoted scans a quoted literal from s[0] (s[0] must be the quote
// byte), honoring backslash escapes; it returns the consumed length and
// whether the closing quote was found.
func scanQuoted(s string, quote byte) (int, bool) {
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if s[i] == quote {
			return i + 1, true
		}
	}
	return len(s), false
}

// scanGoRawString scans a backtick-delimited Go raw string. Backslashes
// have no special meaning, including immediately before the closing tick.
func scanGoRawString(s string) (int, bool) {
	if end := strings.IndexByte(s[1:], '`'); end >= 0 {
		return end + 2, true
	}
	return len(s), false
}

// goRuns tokenizes one line of Go source (top-of-file context).
func goRuns(line string, p Palette) []text.TextRun {
	runs, _ := goRunsIn(line, StateNormal, p)
	return runs
}

// goRunsIn tokenizes one line of Go source starting inside the given
// context and returns the runs plus the context for the next line. Raw
// strings and /* */ comments span lines; interpreted strings and line
// comments cannot.
func goRunsIn(line string, st LineState, p Palette) ([]text.TextRun, LineState) {
	var runs []text.TextRun
	emit := func(n int, color gc.Hsla, bold bool) {
		if n > 0 {
			runs = append(runs, text.TextRun{Len: n, Font: mono(bold), Color: color})
		}
	}
	i := 0
	// Resume a construct carried over from the previous line.
	if st&StateRawString != 0 {
		end := strings.IndexByte(line, '`')
		if end < 0 {
			emit(len(line), p.String, false)
			return runs, StateRawString
		}
		end++
		emit(end, p.String, false)
		i = end
	} else if st&StateBlockComment != 0 {
		end := strings.Index(line, "*/")
		if end < 0 {
			emit(len(line), p.Comment, false)
			return runs, StateBlockComment
		}
		end += 2
		emit(end, p.Comment, false)
		i = end
	}
	for i < len(line) {
		rest := line[i:]
		switch {
		case strings.HasPrefix(rest, "//"):
			emit(len(rest), p.Comment, false)
			return runs, StateNormal
		case strings.HasPrefix(rest, "/*"):
			end := strings.Index(rest[2:], "*/")
			if end < 0 {
				emit(len(rest), p.Comment, false)
				return runs, StateBlockComment
			}
			emit(end+4, p.Comment, false)
			i += end + 4
		case rest[0] == '"':
			// Interpreted strings cannot span lines; an unterminated one
			// ends at the newline.
			n, _ := scanQuoted(rest, '"')
			emit(n, p.String, false)
			i += n
		case rest[0] == '`':
			n, closed := scanGoRawString(rest)
			emit(n, p.String, false)
			i += n
			if !closed {
				return runs, StateRawString
			}
		default:
			n := strings.IndexFunc(rest, func(r rune) bool {
				return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
			})
			if n < 0 {
				n = len(rest) // the line ends inside this word
			}
			if n == 0 {
				emit(1, p.Text, false)
				i++
				continue
			}
			word := rest[:n]
			color, bold := p.Text, false
			switch {
			case goKeywords[word]:
				color, bold = p.Keyword, true
			case goTypes[word]:
				color = p.Type
			case goConstants[word]:
				color = p.Keyword
			case word[0] >= 'A' && word[0] <= 'Z':
				color = p.Type
			case word[0] >= '0' && word[0] <= '9':
				color = p.Number
			default:
				// Call vs field: peek the next non-space char.
				tail := strings.TrimLeft(rest[n:], " \t")
				if strings.HasPrefix(tail, "(") {
					color = p.Function
				} else if strings.HasPrefix(tail, ".") {
					color = p.Field
				}
			}
			emit(n, color, bold)
			i += n
		}
	}
	return runs, StateNormal
}

// goScanState is goRunsIn's transitions without building runs: it returns
// the context the next line starts in.
func goScanState(line string, st LineState) LineState {
	i := 0
	if st&StateRawString != 0 {
		end := strings.IndexByte(line, '`')
		if end < 0 {
			return StateRawString
		}
		i = end + 1
	} else if st&StateBlockComment != 0 {
		end := strings.Index(line, "*/")
		if end < 0 {
			return StateBlockComment
		}
		i = end + 2
	}
	for i < len(line) {
		c := line[i]
		switch {
		case c == '"':
			n, _ := scanQuoted(line[i:], '"')
			i += n
		case c == '`':
			n, closed := scanGoRawString(line[i:])
			i += n
			if !closed {
				return StateRawString
			}
		case c == '/' && strings.HasPrefix(line[i:], "//"):
			// A line comment runs to the newline and never carries over
			// (a backtick inside a comment does not open a raw string).
			return StateNormal
		case c == '/' && strings.HasPrefix(line[i:], "/*"):
			end := strings.Index(line[i+2:], "*/")
			if end < 0 {
				return StateBlockComment
			}
			i += end + 4
		default:
			i++
		}
	}
	return StateNormal
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
