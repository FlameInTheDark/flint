// Go module files: go.mod (the module definition) and go.sum (module
// checksums). Both get GoLand-style coloring: directives as bold keywords,
// quoted module paths as strings, semantic versions as numbers and
// comments as comments. go.mod shares Go's lexing rules for strings and
// block comments (the modfile grammar quotes like Go and allows /* */ and
// ` raw strings), so its cross-line context handling reuses the Go
// scanner; go.sum is line-oriented and carries no cross-line state.

package editor

import (
	"strings"

	gc "github.com/FlameInTheDark/go-gpui/color"
	"github.com/FlameInTheDark/go-gpui/text"
)

// gomodDirectives are go.mod's block and single-line directives (the
// modfile grammar's keywords, including Go 1.24's godebug).
var gomodDirectives = map[string]bool{
	"module":    true,
	"go":        true,
	"toolchain": true,
	"require":   true,
	"exclude":   true,
	"replace":   true,
	"retract":   true,
	"godebug":   true,
}

// gomodRunsIn tokenizes one line of go.mod starting inside the given
// cross-line context and returns the runs plus the context for the next
// line. Directives highlight as keywords, quoted tokens as strings,
// semantic versions as numbers, comments as comments.
func gomodRunsIn(line string, st LineState, p Palette) ([]text.TextRun, LineState) {
	var runs []text.TextRun
	emit := func(n int, color gc.Hsla, bold bool) {
		if n > 0 {
			runs = append(runs, text.TextRun{Len: n, Font: mono(bold), Color: color})
		}
	}
	i := 0
	// Resume a construct carried over from the previous line (same rules
	// as Go: ` raw strings and /* */ comments span lines).
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
			if isModWordByte(rest[0]) {
				n := 0
				for n < len(rest) && isModWordByte(rest[n]) {
					n++
				}
				word := rest[:n]
				color, bold := p.Text, false
				switch {
				case gomodDirectives[word]:
					color, bold = p.Keyword, true
				case isSemanticVersion(word):
					color = p.Number
				}
				emit(n, color, bold)
				i += n
				continue
			}
			if rest[0] >= '0' && rest[0] <= '9' {
				n := 0
				for n < len(rest) && (isModWordByte(rest[n]) || rest[n] == '.') {
					n++
				}
				emit(n, p.Number, false)
				i += n
				continue
			}
			if strings.HasPrefix(rest, "=>") {
				emit(2, p.Keyword, false)
				i += 2
				continue
			}
			emit(1, p.Text, false)
			i++
		}
	}
	return runs, StateNormal
}

// isModWordByte reports whether c can appear in a go.mod word (directive,
// module path fragment or version): letters, digits, dot, dash, plus,
// slash, underscore and tilde cover module paths (github.com/a/b),
// versions (v1.2.3-rc.1+incompatible) and pseudo-versions.
func isModWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '.' || c == '-' || c == '+' || c == '/' || c == '_' || c == '~'
}

// isSemanticVersion reports whether a go.mod word is a semantic version
// token (v1.2.3, v0.0.0-20240101..., v2.0.0+incompatible).
func isSemanticVersion(word string) bool {
	if len(word) < 2 || word[0] != 'v' || word[1] < '0' || word[1] > '9' {
		return false
	}
	return true
}

// gosumRuns tokenizes one line of go.sum. Lines are
// "module version [/go.mod] hash" plus ## annotation comments; the module
// path renders as text, the version as a number, the hash dimmed like a
// trailing annotation, and ## lines as comments.
func gosumRuns(line string, p Palette) []text.TextRun {
	var runs []text.TextRun
	emit := func(n int, color gc.Hsla, bold bool) {
		if n > 0 {
			runs = append(runs, text.TextRun{Len: n, Font: mono(bold), Color: color})
		}
	}
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "#") {
		emit(len(line), p.Comment, false)
		return runs
	}
	rest := line
	tok := 0 // token index: 0 = module path, 1 = version, 2+ = hashes
	for len(rest) > 0 {
		// Whitespace separates tokens.
		sp := 0
		for sp < len(rest) && (rest[sp] == ' ' || rest[sp] == '\t') {
			sp++
		}
		emit(sp, p.Text, false)
		rest = rest[sp:]
		if rest == "" {
			break
		}
		// Token: runs to the next space (module paths and hashes never
		// contain spaces).
		n := 0
		for n < len(rest) && rest[n] != ' ' && rest[n] != '\t' {
			n++
		}
		switch tok {
		case 0:
			emit(n, p.Text, false)
		case 1:
			// The version carries the optional /go.mod suffix
			// (go.sum lines listing the module's own go.mod tree hash).
			emit(n, p.Number, false)
		default:
			// h1:<base64> or the legacy sha256-<hex> hashes: dim them so
			// the eye lands on module paths and versions.
			emit(n, p.Comment, false)
		}
		tok++
		rest = rest[n:]
	}
	return runs
}
