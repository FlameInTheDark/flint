package workspace

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/FlameInTheDark/flint/internal/editor"
)

const (
	searchMaxHits      = 200
	searchMaxFileSize  = 1 << 20
	searchMaxBytes     = 64 << 20
	searchMaxScanFiles = 6000
)

type searchHit struct {
	file         projectFile
	label, kind  string
	line, column int // one-based; column is a byte offset within the original line
	score        int
}

func searchPathKey(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// Unsaved edits take precedence over the disk snapshot for open text files.
func (ws *Workspace) searchBufferText() map[string]string {
	open := make(map[string]string)
	for _, b := range ws.Buffers {
		if b.Path == "" || b.Kind != editor.KindText {
			continue
		}
		if path, err := filepath.Abs(b.Path); err == nil {
			open[searchPathKey(path)] = b.Text()
		}
	}
	return open
}

func readSearchFile(path string) ([]byte, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	if info.Size() > searchMaxFileSize {
		return nil, true
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, searchMaxFileSize+1))
	if err != nil {
		return nil, false
	}
	if len(b) > searchMaxFileSize {
		return nil, true
	}
	return b, false
}

func findProjectContent(ctx context.Context, files []projectFile, open map[string]string, mode searchMode, query string) ([]searchHit, bool) {
	if strings.TrimSpace(query) == "" {
		return nil, false
	}
	var hits []searchHit
	var re *regexp.Regexp
	if mode == searchText {
		// Literal, Unicode case-folded matching without altering byte positions.
		var err error
		re, err = regexp.Compile("(?i)" + regexp.QuoteMeta(query))
		if err != nil {
			return nil, false
		}
	}
	readBytes, scanned := 0, 0
	limited := false
	for _, file := range files {
		if ctx.Err() != nil {
			return nil, false
		}
		if mode == searchStructure && !strings.EqualFold(filepath.Ext(file.path), ".go") {
			continue
		}
		if scanned == searchMaxScanFiles || readBytes >= searchMaxBytes {
			limited = true
			break
		}
		scanned++
		var data []byte
		if src, ok := open[searchPathKey(file.path)]; ok {
			if len(src) > searchMaxFileSize {
				limited = true
				continue
			}
			data = []byte(src)
		} else {
			var skipped bool
			data, skipped = readSearchFile(file.path)
			limited = limited || skipped
		}
		readBytes += len(data)
		if len(data) == 0 || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			continue
		}
		if mode == searchStructure {
			for _, entry := range goOutline(string(data)) {
				if score, ok := fileSearchScore(entry.label, query); ok {
					hits = append(hits, searchHit{file: file, label: entry.label, kind: entry.badge, line: entry.where.line, column: entry.where.column, score: score})
				}
			}
			// Keep the best results even if later files contain closer matches.
			if len(hits) > 2*searchMaxHits {
				sortStructureHits(hits)
				hits = hits[:searchMaxHits]
				limited = true
			}
			continue
		}
		if re == nil {
			continue
		}
		for src, lineNo := string(data), 1; src != ""; lineNo++ {
			if ctx.Err() != nil {
				return nil, false
			}
			line, rest, _ := strings.Cut(src, "\n")
			src = rest
			line = strings.TrimSuffix(line, "\r")
			for _, match := range re.FindAllStringIndex(line, searchMaxHits-len(hits)) {
				hits = append(hits, searchHit{file: file, label: searchSnippet(line, match[0]), line: lineNo, column: match[0] + 1})
				if len(hits) == searchMaxHits {
					return hits, true
				}
			}
		}
	}
	if mode == searchStructure {
		sortStructureHits(hits)
		if len(hits) > searchMaxHits {
			hits = hits[:searchMaxHits]
			limited = true
		}
	}
	return hits, limited
}

func sortStructureHits(hits []searchHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if a.label != b.label {
			return a.label < b.label
		}
		if a.file.relative != b.file.relative {
			return a.file.relative < b.file.relative
		}
		return a.line < b.line
	})
}

// Show nearby context for a match deep inside a long line.
func searchSnippet(line string, at int) string {
	start := at
	for n := 0; n < 45 && start > 0; n++ {
		_, width := utf8.DecodeLastRuneInString(line[:start])
		start -= width
	}
	end := start
	for n := 0; n < 160 && end < len(line); n++ {
		_, width := utf8.DecodeRuneInString(line[end:])
		end += width
	}
	snippet := strings.TrimSpace(line[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(line) {
		snippet += "…"
	}
	return snippet
}
