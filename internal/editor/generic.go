// Generic keyword highlighting: one tokenizer (comments, strings, numbers,
// keywords, identifiers) parameterized per language — C, C++, C#, Java,
// JavaScript, TypeScript, Python, Rust, Ruby, PHP, Shell and SQL get
// decent coloring without per-language hand-tokenizers. JSON/YAML/TOML
// keep their own structure-aware runs.

package editor

import (
	"strings"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/text"
)

// genericLangDef configures the generic tokenizer for one language.
type genericLangDef struct {
	// lineComment prefixes (any starts a comment to end of line).
	lineComments []string
	// blockComment delimiters (open, close) for C-style /* ... */.
	blockComments bool
	// tripleQuotes enables Python-style """...""" / '''...''' strings.
	tripleQuotes bool
	keywords     map[string]bool
}

var genericLang = map[string]*genericLangDef{
	"c":          cLike(cKeywords, cTypeKeywords),
	"cpp":        cLike(appendCopy(cKeywords, cppKeywords...), cppTypeKeywords),
	"csharp":     cLike(csharpKeywords, csharpTypeKeywords),
	"java":       cLike(javaKeywords, javaTypeKeywords),
	"javascript": cLike(jsKeywords, nil),
	"typescript": cLike(appendCopy(jsKeywords, tsKeywords...), nil),
	"rust":       cLike(rustKeywords, rustTypeKeywords),
	"python": {
		lineComments: []string{"#"},
		tripleQuotes: true,
		keywords:     pythonKeywords,
	},
	"ruby": hashComment(rubyKeywords),
	"php": {
		lineComments:  []string{"//", "#"},
		blockComments: true,
		keywords:      phpKeywords,
	},
	"shell": hashComment(shellKeywords),
	"sql":   {lineComments: []string{"--"}, keywords: sqlKeywords},
}

func cLike(keywords, types map[string]bool) *genericLangDef {
	m := map[string]bool{}
	for k := range keywords {
		m[k] = true
	}
	for k := range types {
		m[k] = true
	}
	return &genericLangDef{lineComments: []string{"//"}, blockComments: true, keywords: m}
}

func hashComment(keywords map[string]bool) *genericLangDef {
	return &genericLangDef{lineComments: []string{"#"}, keywords: keywords}
}

// genericRuns tokenizes one line with the generic tokenizer (top-of-file
// context).
func genericRuns(line string, def *genericLangDef, p Palette) []text.TextRun {
	runs, _ := genericRunsIn(line, def, StateNormal, p)
	return runs
}

// genericRunsIn tokenizes one line with the generic tokenizer, resuming
// the given cross-line context. Block comments, JS/shell template
// literals (backticks) and Python triple-quoted strings span lines; " and
// ' strings end at the newline in these languages. It returns the runs
// plus the context the next line starts in.
func genericRunsIn(line string, def *genericLangDef, st LineState, p Palette) ([]text.TextRun, LineState) {
	var runs []text.TextRun
	emit := func(n int, color gc.Hsla, bold bool) {
		if n > 0 {
			runs = append(runs, text.TextRun{Len: n, Font: mono(bold), Color: color})
		}
	}
	i := 0
	// Resume a construct carried over from the previous line.
	if st&StateTripleDouble != 0 || st&StateTripleSingle != 0 {
		quote := `"""`
		color := StateTripleDouble
		if st&StateTripleSingle != 0 {
			quote = "'''"
			color = StateTripleSingle
		}
		end := strings.Index(line[i:], quote)
		if end < 0 {
			emit(len(line), p.String, false)
			return runs, color
		}
		end += i + 3
		emit(end, p.String, false)
		i = end
	} else if st&StateBlockComment != 0 {
		end := strings.Index(line[i:], "*/")
		if end < 0 {
			emit(len(line), p.Comment, false)
			return runs, StateBlockComment
		}
		end += i + 2
		emit(end, p.Comment, false)
		i = end
	} else if st&StateRawString != 0 {
		end := strings.IndexByte(line[i:], '`')
		if end < 0 {
			emit(len(line), p.String, false)
			return runs, StateRawString
		}
		end += i + 1
		emit(end, p.String, false)
		i = end
	}
	for i < len(line) {
		rest := line[i:]
		// Comments.
		comment := -1
		for _, c := range def.lineComments {
			if strings.HasPrefix(rest, c) {
				comment = 0
				break
			}
		}
		if comment == 0 {
			emit(len(rest), p.Comment, false)
			return runs, StateNormal
		}
		if def.blockComments && strings.HasPrefix(rest, "/*") {
			end := strings.Index(rest[2:], "*/")
			if end < 0 {
				emit(len(rest), p.Comment, false)
				return runs, StateBlockComment
			}
			n := end + 4
			emit(n, p.Comment, false)
			i += n
			continue
		}
		// Python triple-quoted strings (before the plain quote check).
		if def.tripleQuotes && (strings.HasPrefix(rest, `"""`) || strings.HasPrefix(rest, "'''")) {
			quote := `"""`
			endState := StateTripleDouble
			if rest[0] == '\'' {
				quote = "'''"
				endState = StateTripleSingle
			}
			end := strings.Index(rest[3:], quote)
			if end < 0 {
				emit(len(rest), p.String, false)
				return runs, endState
			}
			emit(end+6, p.String, false)
			i += end + 6
			continue
		}
		// Strings.
		if rest[0] == '"' || rest[0] == '\'' || rest[0] == '`' {
			quote := rest[0]
			n, closed := scanQuoted(rest, quote, true)
			emit(n, p.String, false)
			i += n
			if !closed {
				if quote == '`' {
					// Template literals and command substitutions span
					// lines.
					return runs, StateRawString
				}
				// " and ' strings end at the newline in these languages.
				return runs, StateNormal
			}
			continue
		}
		// Numbers.
		if rest[0] >= '0' && rest[0] <= '9' {
			i2 := 0
			for i2 < len(rest) && isWordByte(rest[i2]) {
				i2++
			}
			emit(i2, p.Number, false)
			i += i2
			continue
		}
		// Words.
		if isWordStartByte(rest[0]) {
			i2 := 0
			for i2 < len(rest) && isWordByte(rest[i2]) {
				i2++
			}
			word := rest[:i2]
			bold := false
			color := p.Text
			switch {
			case def.keywords[word]:
				color = p.Keyword
				bold = true
			case isTypeLike(word):
				color = p.Type
			}
			// Call vs field: peek the next non-space char.
			tail := strings.TrimLeft(rest[i2:], " \t")
			if strings.HasPrefix(tail, "(") {
				color = p.Function
				bold = false
			}
			emit(i2, color, bold)
			i += i2
			continue
		}
		// Punctuation and whitespace: single byte.
		emit(1, p.Text, false)
		i++
	}
	return runs, StateNormal
}

// genericScanState is genericRunsIn's transitions without building runs:
// it returns the context the next line starts in.
func genericScanState(line string, def *genericLangDef, st LineState) LineState {
	i := 0
	if st&StateTripleDouble != 0 || st&StateTripleSingle != 0 {
		quote := `"""`
		if st&StateTripleSingle != 0 {
			quote = "'''"
		}
		end := strings.Index(line[i:], quote)
		if end < 0 {
			return st
		}
		i += end + 3
	} else if st&StateBlockComment != 0 {
		end := strings.Index(line[i:], "*/")
		if end < 0 {
			return StateBlockComment
		}
		i += end + 2
	} else if st&StateRawString != 0 {
		end := strings.IndexByte(line[i:], '`')
		if end < 0 {
			return StateRawString
		}
		i += end + 1
	}
	for i < len(line) {
		rest := line[i:]
		for _, c := range def.lineComments {
			if strings.HasPrefix(rest, c) {
				// Line comments never carry over.
				return StateNormal
			}
		}
		if def.blockComments && strings.HasPrefix(rest, "/*") {
			end := strings.Index(rest[2:], "*/")
			if end < 0 {
				return StateBlockComment
			}
			i += end + 4
			continue
		}
		if def.tripleQuotes && (strings.HasPrefix(rest, `"""`) || strings.HasPrefix(rest, "'''")) {
			quote := `"""`
			endState := StateTripleDouble
			if rest[0] == '\'' {
				quote = "'''"
				endState = StateTripleSingle
			}
			end := strings.Index(rest[3:], quote)
			if end < 0 {
				return endState
			}
			i += end + 6
			continue
		}
		if rest[0] == '"' || rest[0] == '\'' || rest[0] == '`' {
			n, closed := scanQuoted(rest, rest[0], true)
			i += n
			if !closed && rest[0] == '`' {
				return StateRawString
			}
			continue
		}
		i++
	}
	return StateNormal
}

func isWordStartByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

func isWordByte(b byte) bool {
	return isWordStartByte(b) || b >= '0' && b <= '9'
}

// isTypeLike: capitalized identifiers read as types (Go convention plus
// C++/Rust/Java class names).
func isTypeLike(word string) bool {
	if word == "" {
		return false
	}
	c := word[0]
	return c >= 'A' && c <= 'Z'
}

// jsonRuns colors JSON structure: keys (before the colon) in one color,
// strings, numbers, and the literals.
func jsonRuns(line string, p Palette) []text.TextRun {
	var runs []text.TextRun
	rest := line
	// Key phase: an optional opening brace, whitespace, then a string
	// followed by a colon.
	trimmed := strings.TrimLeft(rest, " \t")
	if strings.HasPrefix(trimmed, "{") {
		trimmed = strings.TrimLeft(trimmed[1:], " \t")
	}
	if strings.HasPrefix(trimmed, "\"") {
		if end := strings.Index(trimmed[1:], "\""); end >= 0 {
			keyLen := len(rest) - len(trimmed) + end + 2
			tail := strings.TrimLeft(trimmed[end+2:], " \t")
			if strings.HasPrefix(tail, ":") {
				runs = append(runs, text.TextRun{Len: keyLen, Font: mono(false), Color: p.Function})
				rest = rest[keyLen:]
			}
		}
	}
	for len(rest) > 0 {
		switch {
		case rest[0] == '"':
			i := 1
			for i < len(rest) {
				if rest[i] == '\\' && i+1 < len(rest) {
					i += 2
					continue
				}
				if rest[i] == '"' {
					i++
					break
				}
				i++
			}
			runs = append(runs, text.TextRun{Len: i, Font: mono(false), Color: p.String})
			rest = rest[i:]
		case rest[0] >= '0' && rest[0] <= '9' || rest[0] == '-':
			i := 0
			for i < len(rest) && (isWordByte(rest[i]) || rest[i] == '.' || rest[i] == '-' || rest[i] == '+' || rest[i] == 'e' || rest[i] == 'E') {
				i++
			}
			runs = append(runs, text.TextRun{Len: i, Font: mono(false), Color: p.Number})
			rest = rest[i:]
		case strings.HasPrefix(rest, "true") || strings.HasPrefix(rest, "false") || strings.HasPrefix(rest, "null"):
			n := 4
			if rest[0] == 'n' {
				n = 4
			}
			runs = append(runs, text.TextRun{Len: n, Font: mono(false), Color: p.Keyword})
			rest = rest[n:]
		default:
			runs = append(runs, text.TextRun{Len: 1, Font: mono(false), Color: p.Text})
			rest = rest[1:]
		}
	}
	return runs
}

// yamlRuns colors YAML/TOML structure: keys, comments, and scalars.
func yamlRuns(line string, p Palette) []text.TextRun {
	// Comment tail first.
	if i := strings.Index(line, "#"); i == 0 {
		return []text.TextRun{{Len: len(line), Font: mono(false), Color: p.Comment}}
	}
	// Key: leading whitespace, word/dash/dot chars, then a colon/equal.
	trimmed := strings.TrimLeft(line, " \t")
	if i := strings.IndexAny(trimmed, ":="); i > 0 {
		keyLen := len(line) - len(trimmed) + i
		var runs []text.TextRun
		runs = append(runs, text.TextRun{Len: keyLen, Font: mono(false), Color: p.Function})
		rest := line[keyLen:]
		// Value coloring: numbers, booleans, strings.
		val := strings.TrimSpace(rest)
		switch {
		case val == "true" || val == "false" || val == "null" || val == "yes" || val == "no":
			runs = append(runs, text.TextRun{Len: len(rest), Font: mono(false), Color: p.Keyword})
		case isNumericString(val):
			runs = append(runs, text.TextRun{Len: len(rest), Font: mono(false), Color: p.Number})
		default:
			if j := strings.Index(rest, "#"); j >= 0 {
				runs = append(runs, text.TextRun{Len: j, Font: mono(false), Color: p.String})
				runs = append(runs, text.TextRun{Len: len(rest) - j, Font: mono(false), Color: p.Comment})
			} else {
				runs = append(runs, text.TextRun{Len: len(rest), Font: mono(false), Color: p.String})
			}
		}
		return runs
	}
	return []text.TextRun{{Len: len(line), Font: mono(false), Color: p.Text}}
}

func isNumericString(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= '0' && b <= '9' || b == '.' || b == '-' || b == '+' || b == 'e' || b == 'E' {
			continue
		}
		return false
	}
	return true
}

func appendCopy(base map[string]bool, more ...string) map[string]bool {
	m := map[string]bool{}
	for k := range base {
		m[k] = true
	}
	for _, k := range more {
		m[k] = true
	}
	return m
}

var cKeywords = map[string]bool{
	"auto": true, "break": true, "case": true, "const": true, "continue": true,
	"default": true, "do": true, "else": true, "enum": true, "extern": true,
	"for": true, "goto": true, "if": true, "inline": true, "register": true,
	"restrict": true, "return": true, "sizeof": true, "static": true,
	"struct": true, "switch": true, "typedef": true, "union": true,
	"volatile": true, "while": true, "include": true, "define": true,
	"ifdef": true, "ifndef": true, "endif": true, "pragma": true,
	"NULL": true,
}

var cTypeKeywords = map[string]bool{
	"char": true, "double": true, "float": true, "int": true, "long": true,
	"short": true, "signed": true, "unsigned": true, "void": true, "bool": true,
	"size_t": true, "FILE": true,
}

var cppKeywords = []string{
	"class", "namespace", "template", "typename", "using", "try", "catch",
	"throw", "new", "delete", "operator", "public", "private", "protected",
	"virtual", "override", "final", "constexpr", "nullptr", "explicit",
	"friend", "this", "dynamic_cast", "static_cast", "const_cast",
}

var cppTypeKeywords = map[string]bool{
	"auto": true, "string": true, "vector": true, "map": true, "set": true,
}

var csharpKeywords = map[string]bool{
	"abstract": true, "as": true, "base": true, "bool": true, "break": true,
	"byte": true, "case": true, "catch": true, "char": true, "checked": true,
	"class": true, "const": true, "continue": true, "decimal": true,
	"default": true, "delegate": true, "do": true, "double": true, "else": true,
	"enum": true, "event": true, "explicit": true, "extern": true, "false": true,
	"finally": true, "fixed": true, "float": true, "for": true, "foreach": true,
	"get": true, "goto": true, "if": true, "implicit": true, "in": true,
	"int": true, "interface": true, "internal": true, "is": true, "lock": true,
	"long": true, "namespace": true, "new": true, "null": true, "object": true,
	"operator": true, "out": true, "override": true, "params": true,
	"private": true, "protected": true, "public": true, "readonly": true,
	"ref": true, "return": true, "sbyte": true, "sealed": true, "set": true,
	"short": true, "sizeof": true, "stackalloc": true, "static": true,
	"string": true, "struct": true, "switch": true, "this": true, "throw": true,
	"true": true, "try": true, "typeof": true, "uint": true, "ulong": true,
	"unchecked": true, "unsafe": true, "ushort": true, "using": true,
	"var": true, "virtual": true, "void": true, "volatile": true, "while": true,
	"async": true, "await": true, "partial": true, "where": true,
}

var csharpTypeKeywords = map[string]bool{}

var javaKeywords = map[string]bool{
	"abstract": true, "assert": true, "boolean": true, "break": true,
	"byte": true, "case": true, "catch": true, "char": true, "class": true,
	"const": true, "continue": true, "default": true, "do": true, "double": true,
	"else": true, "enum": true, "extends": true, "final": true, "finally": true,
	"float": true, "for": true, "goto": true, "if": true, "implements": true,
	"import": true, "instanceof": true, "int": true, "interface": true,
	"long": true, "native": true, "new": true, "package": true, "private": true,
	"protected": true, "public": true, "return": true, "short": true,
	"static": true, "strictfp": true, "super": true, "switch": true,
	"synchronized": true, "this": true, "throw": true, "throws": true,
	"transient": true, "try": true, "void": true, "volatile": true,
	"while": true, "true": true, "false": true, "null": true, "var": true,
	"record": true,
}

var javaTypeKeywords = map[string]bool{}

var jsKeywords = map[string]bool{
	"async": true, "await": true, "break": true, "case": true, "catch": true,
	"class": true, "const": true, "continue": true, "debugger": true,
	"default": true, "delete": true, "do": true, "else": true, "export": true,
	"extends": true, "finally": true, "for": true, "function": true, "if": true,
	"import": true, "in": true, "instanceof": true, "let": true, "new": true,
	"of": true, "return": true, "static": true, "super": true, "switch": true,
	"this": true, "throw": true, "try": true, "typeof": true, "var": true,
	"void": true, "while": true, "with": true, "yield": true, "true": true,
	"false": true, "null": true, "undefined": true,
}

var tsKeywords = []string{
	"interface", "type", "enum", "implements", "private", "protected",
	"public", "readonly", "namespace", "declare", "abstract", "as", "satisfies",
}

var rustKeywords = map[string]bool{
	"as": true, "async": true, "await": true, "break": true, "const": true,
	"continue": true, "crate": true, "dyn": true, "else": true, "enum": true,
	"extern": true, "false": true, "fn": true, "for": true, "if": true,
	"impl": true, "in": true, "let": true, "loop": true, "match": true,
	"mod": true, "move": true, "mut": true, "pub": true, "ref": true,
	"return": true, "self": true, "static": true, "struct": true, "super": true,
	"trait": true, "true": true, "type": true, "unsafe": true, "use": true,
	"where": true, "while": true,
}

var rustTypeKeywords = map[string]bool{
	"i8": true, "i16": true, "i32": true, "i64": true, "i128": true,
	"u8": true, "u16": true, "u32": true, "u64": true, "u128": true,
	"usize": true, "isize": true, "f32": true, "f64": true, "bool": true,
	"char": true, "str": true, "String": true, "Vec": true, "Option": true,
	"Result": true, "Box": true,
}

var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
	"self": true,
}

var rubyKeywords = map[string]bool{
	"begin": true, "end": true, "def": true, "class": true, "module": true,
	"if": true, "elsif": true, "else": true, "unless": true, "case": true,
	"when": true, "while": true, "until": true, "for": true, "do": true,
	"return": true, "yield": true, "nil": true, "true": true, "false": true,
	"self": true, "lambda": true, "proc": true, "raise": true, "require": true,
	"attr_accessor": true, "attr_reader": true, "attr_writer": true,
	"include": true, "extend": true,
}

var phpKeywords = map[string]bool{
	"abstract": true, "and": true, "array": true, "as": true, "break": true,
	"callable": true, "case": true, "catch": true, "class": true, "clone": true,
	"const": true, "continue": true, "declare": true, "default": true, "do": true,
	"echo": true, "else": true, "elseif": true, "empty": true, "enddeclare": true,
	"endfor": true, "endforeach": true, "endif": true, "endswitch": true,
	"endwhile": true, "extends": true, "final": true, "finally": true, "fn": true,
	"for": true, "foreach": true, "function": true, "global": true, "goto": true,
	"if": true, "implements": true, "include": true, "instanceof": true,
	"insteadof": true, "interface": true, "isset": true, "list": true,
	"namespace": true, "new": true, "or": true, "print": true, "private": true,
	"protected": true, "public": true, "require": true, "return": true,
	"static": true, "switch": true, "throw": true, "trait": true, "try": true,
	"unset": true, "use": true, "var": true, "while": true, "xor": true,
	"yield": true, "true": true, "false": true, "null": true, "this": true,
}

var shellKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true, "for": true,
	"while": true, "until": true, "do": true, "done": true, "case": true, "esac": true,
	"in": true, "function": true, "return": true, "local": true, "export": true,
	"source": true, "alias": true, "echo": true, "cd": true, "exit": true,
	"set": true, "unset": true, "shift": true, "read": true,
}

var sqlKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "INSERT": true, "INTO": true,
	"VALUES": true, "UPDATE": true, "SET": true, "DELETE": true, "CREATE": true,
	"TABLE": true, "ALTER": true, "DROP": true, "INDEX": true, "VIEW": true,
	"JOIN": true, "INNER": true, "LEFT": true, "RIGHT": true, "OUTER": true,
	"ON": true, "AS": true, "AND": true, "OR": true, "NOT": true, "NULL": true,
	"PRIMARY": true, "KEY": true, "FOREIGN": true, "REFERENCES": true,
	"GROUP": true, "BY": true, "ORDER": true, "HAVING": true, "LIMIT": true,
	"OFFSET": true, "UNION": true, "ALL": true, "DISTINCT": true, "CASE": true,
	"WHEN": true, "THEN": true, "ELSE": true, "END": true, "BEGIN": true,
	"COMMIT": true, "ROLLBACK": true, "DEFAULT": true, "EXISTS": true,
	"BETWEEN": true, "LIKE": true, "IN": true, "IS": true, "WITH": true,
	"select": true, "from": true, "where": true, "insert": true, "update": true,
	"delete": true, "join": true, "inner": true, "left": true, "right": true,
	"outer": true, "on": true, "and": true, "or": true, "not": true,
	"group": true, "by": true, "order": true, "having": true, "limit": true,
	"union": true, "distinct": true, "case": true, "when": true, "then": true,
	"else": true, "end": true, "with": true, "as": true,
}
