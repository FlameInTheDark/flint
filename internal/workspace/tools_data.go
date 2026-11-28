package workspace

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type toolEntry struct {
	label, badge     string
	where            goDiagnostic
	deleted          bool
	staged, conflict bool
	stat             gitLineStat
}

type gitLineStat struct{ added, removed int }
type gitCommit struct{ hash, date, subject string }

type toolResult struct {
	entries []toolEntry
	info    string
	repo    string
	branch  string
	history []gitCommit
}

func goOutline(src string) []toolEntry {
	set := token.NewFileSet()
	f, _ := parser.ParseFile(set, "", src, parser.SkipObjectResolution)
	if f == nil {
		return nil
	}
	var out []toolEntry
	add := func(label, kind string, p token.Pos) {
		pos := set.PositionFor(p, false)
		out = append(out, toolEntry{label: label, badge: kind, where: goDiagnostic{line: pos.Line, column: pos.Column}})
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			label, kind := d.Name.Name, "func"
			if d.Recv != nil && len(d.Recv.List) > 0 {
				var receiver strings.Builder
				_ = format.Node(&receiver, set, d.Recv.List[0].Type)
				label, kind = receiver.String()+"."+label, "method"
			}
			add(label, kind, d.Name.Pos())
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					add(s.Name.Name, "type", s.Name.Pos())
				case *ast.ValueSpec:
					for _, name := range s.Names {
						add(name.Name, d.Tok.String(), name.Pos())
					}
				}
			}
		}
	}
	return out
}

var todoTag = regexp.MustCompile(`\b(?:TODO|FIXME)\b`)

func fileTodos(path string, src []byte) []toolEntry {
	set := token.NewFileSet()
	file := set.AddFile(path, -1, len(src))
	var scan scanner.Scanner
	scan.Init(file, src, func(token.Position, string) {}, scanner.ScanComments)
	var out []toolEntry
	for {
		p, tok, lit := scan.Scan()
		if tok == token.EOF {
			return out
		}
		if tok != token.COMMENT {
			continue
		}
		pos := file.PositionFor(p, false)
		for i, line := range strings.Split(lit, "\n") {
			match := todoTag.FindStringIndex(line)
			if match == nil {
				continue
			}
			column := 1 + match[0]
			if i == 0 {
				column += pos.Column - 1
			}
			out = append(out, toolEntry{label: strings.TrimSpace(strings.TrimSuffix(line[match[0]:], "*/")), badge: line[match[0]:match[1]], where: goDiagnostic{path: path, line: pos.Line + i, column: column}})
			if len(out) == 500 {
				return out
			}
		}
	}
}

func scanTodos(ctx context.Context, root string, opened map[string]string) toolResult {
	const fileLimit, sizeLimit, resultLimit = 1500, 512 << 10, 500
	result := toolResult{info: "No TODO or FIXME comments in Go source."}
	seen, capped := 0, false
	add := func(path string, content []byte) {
		if len(content) > sizeLimit {
			return
		}
		found := fileTodos(path, content)
		left := resultLimit - len(result.entries)
		result.entries = append(result.entries, found[:min(left, len(found))]...)
		capped = len(result.entries) == resultLimit
	}
	paths := slices.Sorted(func(yield func(string) bool) {
		for path := range opened {
			if !yield(path) {
				return
			}
		}
	})
	for _, path := range paths {
		if ctx.Err() != nil || capped {
			break
		}
		add(path, []byte(opened[path]))
	}
	if root != "" && !capped && ctx.Err() == nil {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil || capped {
				return fs.SkipAll
			}
			if err != nil {
				if path == root {
					return err
				}
				return nil
			}
			if d.IsDir() {
				if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "testdata") {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !strings.EqualFold(filepath.Ext(path), ".go") {
				return nil
			}
			if _, ok := opened[path]; ok {
				return nil
			}
			seen++
			if seen > fileLimit {
				capped = true
				return fs.SkipAll
			}
			if info, err := d.Info(); err == nil && info.Size() <= sizeLimit {
				if b, err := os.ReadFile(path); err == nil {
					add(path, b)
				}
			}
			return nil
		})
		if err != nil {
			result.info = err.Error()
			return result
		}
	}
	if capped || ctx.Err() != nil {
		result.info = "Partial results · scan limit reached"
	} else if len(result.entries) > 0 {
		result.info = fmt.Sprintf("%d comments", len(result.entries))
	}
	slices.SortFunc(result.entries, func(a, b toolEntry) int {
		if cmp := strings.Compare(a.where.path, b.where.path); cmp != 0 {
			return cmp
		}
		return a.where.line - b.where.line
	})
	return result
}

func gitStatus(ctx context.Context, dir string, history bool) toolResult {
	if dir == "" {
		return toolResult{info: "Open a project or a file to see Git status."}
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return toolResult{info: "Git was not found on PATH."}
	}
	command := func(args ...string) *exec.Cmd { return gitCommand(ctx, git, dir, true, args...) }
	root, err := gitTopLevel(ctx, git, dir)
	if err != nil {
		return toolResult{info: "No accessible Git repository for this folder."}
	}
	cmd := command("status", "--porcelain=v1", "-z", "--branch", "--no-renames", "--untracked-files=all")
	buf := &boundedGitOutput{limit: 256 << 10}
	cmd.Stdout = buf
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return toolResult{info: msg}
	}
	result := parseGitStatus(root, buf.bytes, buf.truncated)
	if history {
		var commits boundedGitOutput
		commits.limit = 64 << 10
		log := command("log", "-50", "--date=short", "--format=%h%x09%ad%x09%s")
		log.Stdout = &commits
		if err := log.Run(); err == nil {
			for _, line := range strings.Split(string(commits.bytes), "\n") {
				if fields := strings.SplitN(strings.TrimSuffix(line, "\r"), "\t", 3); len(fields) == 3 {
					result.history = append(result.history, gitCommit{hash: fields[0], date: fields[1], subject: fields[2]})
				}
			}
		}
		return result
	}
	for _, staged := range []bool{false, true} {
		args := []string{"diff", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "--no-renames"}
		if staged {
			args = append(args, "--cached")
		}
		out := &boundedGitOutput{limit: 256 << 10}
		cmd := command(args...)
		cmd.Stdout = out
		if err := cmd.Run(); err != nil {
			continue // status is still usable without line counts
		}
		stats := parseGitNumstat(out.bytes)
		for i := range result.entries {
			if e := &result.entries[i]; e.staged == staged && !e.conflict {
				e.stat = stats[e.label]
			}
		}
	}
	return result
}

func parseGitNumstat(data []byte) map[string]gitLineStat {
	stats := make(map[string]gitLineStat)
	for len(data) > 0 {
		end := bytes.IndexByte(data, 0)
		if end < 0 {
			break // output can be cut at the byte limit
		}
		parts := strings.SplitN(string(data[:end]), "\t", 3)
		data = data[end+1:]
		if len(parts) != 3 {
			continue
		}
		added, aerr := strconv.Atoi(parts[0])
		removed, rerr := strconv.Atoi(parts[1])
		if aerr == nil && rerr == nil { // binary files report '-' counts
			stats[filepath.FromSlash(parts[2])] = gitLineStat{added: added, removed: removed}
		}
	}
	return stats
}

type boundedGitOutput struct {
	limit     int
	bytes     []byte
	truncated bool
}

func (b *boundedGitOutput) Write(p []byte) (int, error) {
	n := min(len(p), b.limit-len(b.bytes))
	b.bytes = append(b.bytes, p[:n]...)
	b.truncated = b.truncated || n < len(p)
	return len(p), nil
}

func parseGitStatus(root string, data []byte, limited bool) toolResult {
	result := toolResult{repo: root, info: "Working tree clean"}
	if limited {
		if last := bytes.LastIndexByte(data, 0); last >= 0 {
			data = data[:last+1]
		} else {
			data = nil
		}
	}
	for _, record := range strings.Split(string(data), "\x00") {
		if strings.HasPrefix(record, "## ") {
			result.info = strings.TrimPrefix(record, "## ")
			result.branch, _, _ = strings.Cut(result.info, "...")
			result.branch = strings.TrimPrefix(strings.TrimPrefix(result.branch, "No commits yet on "), "Initial commit on ")
			if result.branch == "HEAD (no branch)" {
				result.branch = "Detached HEAD"
			}
			continue
		}
		if len(record) < 4 || record[2] != ' ' {
			continue
		}
		code, rel := record[:2], filepath.FromSlash(record[3:])
		if !filepath.IsLocal(rel) {
			continue
		}
		entry := toolEntry{label: rel, where: goDiagnostic{path: filepath.Join(root, rel), line: 1, column: 1}}
		switch code {
		case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
			entry.badge, entry.conflict = code, true
			result.entries = append(result.entries, entry)
		case "??":
			entry.badge = "?"
			result.entries = append(result.entries, entry)
		default:
			for i := range code {
				if code[i] == ' ' || code[i] == '!' {
					continue
				}
				entry.badge, entry.staged, entry.deleted = code[i:i+1], i == 0, code[i] == 'D'
				result.entries = append(result.entries, entry)
			}
		}
		if len(result.entries) >= 500 {
			result.entries = result.entries[:500]
			limited = true
			break
		}
	}
	if len(result.entries) == 0 && result.info != "Working tree clean" {
		result.info += " · working tree clean"
	}
	if limited {
		result.info += " · partial results"
	}
	return result
}
