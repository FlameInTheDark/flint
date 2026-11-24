// Package lsp implements the stdio subset of the Language Server Protocol used
// by the editor. Calls may block and must not run on the UI thread.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location identifies a target document and the range of the symbol in it.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}
type PublishDiagnostics struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}
type CompletionItem struct {
	Label               string          `json:"label"`
	Detail              string          `json:"detail"`
	Kind                int             `json:"kind"`
	InsertText          string          `json:"insertText"`
	InsertTextFormat    int             `json:"insertTextFormat"`
	TextEdit            json.RawMessage `json:"textEdit"`
	AdditionalTextEdits []TextEdit      `json:"additionalTextEdits"`
}

// FileURI correctly escapes spaces, Unicode and Windows drive letters.
func FileURI(path string) string {
	p, _ := filepath.Abs(path)
	p = filepath.ToSlash(p)
	if runtime.GOOS == "windows" && strings.HasPrefix(p, "//") {
		parts := strings.SplitN(strings.TrimPrefix(p, "//"), "/", 2)
		if len(parts) == 2 {
			return (&url.URL{Scheme: "file", Host: parts[0], Path: "/" + parts[1]}).String()
		}
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// PathFromURI converts a local LSP file URI to a native path. Unsupported
// schemes are rejected rather than passed to the filesystem or shell.
func PathFromURI(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "file") || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("unsupported definition URI: %s", raw)
	}
	p := u.Path
	if strings.IndexByte(p, 0) >= 0 {
		return "", fmt.Errorf("invalid definition URI")
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		if runtime.GOOS != "windows" {
			return "", fmt.Errorf("nonlocal definition URI: %s", raw)
		}
		p = "//" + u.Host + p
	} else if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	p = filepath.FromSlash(p)
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("definition URI is not an absolute file path: %s", raw)
	}
	return filepath.Clean(p), nil
}

// PositionAt and OffsetAt translate between UTF-8 byte offsets and the
// protocol's default UTF-16 code units, including supplementary characters.
func PositionAt(text string, offset int) Position {
	if offset < 0 {
		offset = 0
	}
	if offset > len(text) {
		offset = len(text)
	}
	for offset > 0 && offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset--
	}
	before := text[:offset]
	i := strings.LastIndexByte(before, '\n')
	return Position{strings.Count(before, "\n"), len(utf16.Encode([]rune(before[i+1:])))}
}
func OffsetAt(text string, p Position) (int, bool) {
	if p.Line < 0 || p.Character < 0 {
		return 0, false
	}
	start := 0
	for i := 0; i < p.Line; i++ {
		n := strings.IndexByte(text[start:], '\n')
		if n < 0 {
			return 0, false
		}
		start += n + 1
	}
	units := 0
	for i, r := range text[start:] {
		if units == p.Character {
			return start + i, true
		}
		if r == '\n' {
			return 0, false
		}
		if r > 0xffff {
			units += 2
		} else {
			units++
		}
		if units > p.Character {
			return 0, false
		}
	}
	if units == p.Character {
		return len(text), true
	}
	return 0, false
}

// Root walks upward from the file, choosing the closest directory with any
// configured marker. Standalone files use their containing directory.
func Root(path string, markers []string) string {
	abs, _ := filepath.Abs(path)
	dir := filepath.Dir(abs)
	for d := dir; ; d = filepath.Dir(d) {
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return d
			}
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return dir
}

type response struct {
	result json.RawMessage
	err    error
}
type Client struct {
	cmd            *exec.Cmd
	in             io.WriteCloser
	writeMu        sync.Mutex
	mu             sync.Mutex
	next           int
	pending        map[int]chan response
	done           chan struct{}
	closeOnce      sync.Once
	onNotification func(string, json.RawMessage)
}

// SetNotificationHandler receives server notifications on the reader
// goroutine. The callback must not block or mutate UI state directly.
func (c *Client) SetNotificationHandler(f func(string, json.RawMessage)) {
	c.mu.Lock()
	c.onNotification = f
	c.mu.Unlock()
}

func Start(ctx context.Context, command string, args []string, root string) (*Client, error) {
	cmd := exec.Command(command, args...)
	configureServerProcess(cmd)
	cmd.Dir = root
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	// stderr must be drained; never mix it with the framed protocol stream.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		in.Close()
		return nil, err
	}
	c := &Client{cmd: cmd, in: in, pending: make(map[int]chan response), done: make(chan struct{})}
	go c.readLoop(out)
	go func() { _ = cmd.Wait(); c.fail() }()
	var result json.RawMessage
	err = c.Call(ctx, "initialize", map[string]any{
		"processId": os.Getpid(), "rootUri": FileURI(root),
		"capabilities": map[string]any{"textDocument": map[string]any{
			"completion": map[string]any{"completionItem": map[string]any{"snippetSupport": false}},
			"hover":      map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
			"definition": map[string]any{"linkSupport": true},
		}},
		"workspaceFolders": []any{map[string]any{"uri": FileURI(root), "name": filepath.Base(root)}},
	}, &result)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err = c.Notify("initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}
func (c *Client) Notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan response, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if result != nil {
			return json.Unmarshal(r.result, result)
		}
		return nil
	case <-ctx.Done():
		_ = c.Notify("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("language server exited")
	}
}
func (c *Client) fail() { c.closeOnce.Do(func() { close(c.done) }) }
func (c *Client) Close() {
	c.fail()
	_ = c.in.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}
func (c *Client) readLoop(out io.Reader) {
	defer c.fail()
	r := bufio.NewReader(out)
	for {
		length := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Content-Length") {
				length, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
			}
		}
		if length < 0 || length > 32<<20 {
			return
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Params json.RawMessage `json:"params"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		if m.Method != "" {
			// Servers may issue requests during initialization (e.g. configuration).
			if len(m.ID) > 0 && string(m.ID) != "null" {
				var result any
				if m.Method == "workspace/configuration" {
					var p struct {
						Params struct {
							Items []json.RawMessage `json:"items"`
						} `json:"params"`
					}
					_ = json.Unmarshal(body, &p)
					result = make([]any, len(p.Params.Items))
				}
				_ = c.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
			} else {
				c.mu.Lock()
				notify := c.onNotification
				c.mu.Unlock()
				if notify != nil {
					notify(m.Method, m.Params)
				}
			}
			continue
		}
		var id int
		if json.Unmarshal(m.ID, &id) != nil {
			continue
		}
		var err error
		if m.Error != nil {
			err = fmt.Errorf("LSP %d: %s", m.Error.Code, m.Error.Message)
		}
		c.mu.Lock()
		ch := c.pending[id]
		c.mu.Unlock()
		if ch != nil {
			ch <- response{m.Result, err}
		}
	}
}

func (c *Client) Open(uri, language, text string) error {
	return c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": language, "version": 1, "text": text}})
}
func (c *Client) Change(uri, text string, version int) error {
	return c.Notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": version}, "contentChanges": []any{map[string]any{"text": text}}})
}
func (c *Client) Completion(ctx context.Context, uri string, pos Position) ([]CompletionItem, error) {
	var raw json.RawMessage
	err := c.Call(ctx, "textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos}, &raw)
	if err != nil || string(raw) == "null" {
		return nil, err
	}
	var items []CompletionItem
	if len(raw) > 0 && raw[0] == '[' {
		err = json.Unmarshal(raw, &items)
	} else {
		var list struct {
			Items []CompletionItem `json:"items"`
		}
		err = json.Unmarshal(raw, &list)
		items = list.Items
	}
	return items, err
}
func (c *Client) Hover(ctx context.Context, uri string, pos Position) (string, error) {
	var raw struct {
		Contents json.RawMessage `json:"contents"`
	}
	err := c.Call(ctx, "textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos}, &raw)
	if err != nil {
		return "", err
	}
	return hoverText(raw.Contents), nil
}

// Definition accepts all LSP definition response shapes: a single Location,
// an array of Locations or LocationLinks, and null when no target exists.
func (c *Client) Definition(ctx context.Context, uri string, pos Position) ([]Location, error) {
	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/definition", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": pos}, &raw); err != nil {
		return nil, err
	}
	return parseDefinitions(raw)
}

func parseDefinitions(raw json.RawMessage) ([]Location, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var entries []json.RawMessage
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, err
		}
	} else {
		entries = []json.RawMessage{raw}
	}
	locations := make([]Location, 0, len(entries))
	for _, entry := range entries {
		var v struct {
			URI                  string `json:"uri"`
			Range                *Range `json:"range"`
			TargetURI            string `json:"targetUri"`
			TargetRange          *Range `json:"targetRange"`
			TargetSelectionRange *Range `json:"targetSelectionRange"`
		}
		if err := json.Unmarshal(entry, &v); err != nil {
			return nil, err
		}
		uri, r := v.URI, v.Range
		if v.TargetURI != "" {
			uri = v.TargetURI
			r = v.TargetSelectionRange
			if r == nil {
				r = v.TargetRange
			}
		}
		if uri == "" || r == nil || r.Start.Line < 0 || r.Start.Character < 0 || r.End.Line < 0 || r.End.Character < 0 || r.End.Line < r.Start.Line || (r.End.Line == r.Start.Line && r.End.Character < r.Start.Character) {
			return nil, fmt.Errorf("invalid definition location")
		}
		locations = append(locations, Location{URI: uri, Range: *r})
	}
	return locations, nil
}
func hoverText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Value    string `json:"value"`
		Kind     string `json:"kind"`
		Language string `json:"language"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Value != "" {
		if obj.Language != "" {
			fence := "```"
			for strings.Contains(obj.Value, fence) {
				fence += "`"
			}
			return fence + obj.Language + "\n" + obj.Value + "\n" + fence
		}
		if obj.Kind == "plaintext" {
			var escaped strings.Builder
			for _, r := range obj.Value {
				if strings.ContainsRune(`\`+"`*_{}[]<>#+-.!|()~", r) {
					escaped.WriteByte('\\')
				}
				escaped.WriteRune(r)
			}
			return strings.ReplaceAll(escaped.String(), "\n", "  \n")
		}
		return obj.Value
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		parts := []string{}
		for _, x := range arr {
			if s := hoverText(x); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}
