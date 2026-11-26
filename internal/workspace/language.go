package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/FlameInTheDark/flint/internal/config"
	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/lsp"
	"github.com/FlameInTheDark/flint/internal/ui"
)

type languageRequest struct {
	text            string
	offset          int
	revision, token uint64
	kind            string
	x, y            float32
	buffer          *editor.Buffer
	documents       []languageSnapshot
	documentURI     string
	symbol          string
}
type languageSnapshot struct{ uri, text, language string }
type languageDoc struct {
	server      *languageServer
	uri         string
	text        string
	status      string // only accessed on the UI thread
	failed      bool
	diagnostics []lsp.Diagnostic
}
type sentLanguageDocument struct {
	version int
	text    string
}
type languageServer struct {
	key       string
	root      string
	config    config.LanguageServer
	documents map[*editor.Buffer]*languageDoc // UI thread only
	queue     chan languageRequest
	ctx       context.Context
	cancel    context.CancelFunc
	status    string
	failed    bool
	sentMu    sync.Mutex
	sent      map[string]sentLanguageDocument
}
type completionState struct {
	buffer   *editor.Buffer
	revision uint64
	cursor   int
	items    []lsp.CompletionItem
	selected int
}
type documentationState struct {
	buffer *editor.Buffer
	text   string
	x, y   float32
	view   *markdownView
	symbol string
}

// A language server is shared by documents with the same definition and root.
// The UI only sends immutable snapshots; the worker serializes protocol traffic.
func (ws *Workspace) startLanguage(b *editor.Buffer) {
	if ws.dispatch == nil || ws.apply == nil || b.Path == "" || b.Kind != editor.KindText {
		return
	}
	servers, errs := config.LoadLanguageServers()
	if len(errs) > 0 {
		ws.languageStatus = errs[0].Error()
	}
	server, ok := config.ServerForPath(servers, b.Path)
	if !ok {
		return
	}
	if ws.languageDocs == nil {
		ws.languageDocs = make(map[*editor.Buffer]*languageDoc)
	}
	if ws.languageDocs[b] != nil {
		return
	}
	root := lsp.Root(b.Path, server.RootMarkers)
	key := server.Source + "\x00" + root
	if ws.languageServers == nil {
		ws.languageServers = make(map[string]*languageServer)
	}
	group := ws.languageServers[key]
	if group == nil {
		ctx, cancel := context.WithCancel(context.Background())
		group = &languageServer{key: key, root: root, config: server, documents: make(map[*editor.Buffer]*languageDoc), queue: make(chan languageRequest, 1), ctx: ctx, cancel: cancel, status: server.Language + " · starting", sent: make(map[string]sentLanguageDocument)}
		ws.languageServers[key] = group
		ws.runLanguageServer(group)
	}
	doc := &languageDoc{server: group, uri: lsp.FileURI(b.Path), text: b.Text(), status: group.status, failed: group.failed}
	ws.languageDocs[b] = doc
	group.documents[b] = doc
	ws.requestLanguage(b, "sync", b.Cursor, 0, 0)
}

func (ws *Workspace) runLanguageServer(group *languageServer) {
	dispatch, apply, server, ctx := ws.dispatch, ws.apply, group.config, group.ctx
	post := func(f func(*Workspace)) {
		dispatch(func() {
			defer func() { _ = recover() }()
			apply(func(s *Workspace) {
				if s.languageServers[group.key] == group {
					f(s)
				}
			})
		})
	}
	status := func(message string, failed bool) {
		post(func(s *Workspace) {
			group.status, group.failed = message, failed
			for _, doc := range group.documents {
				doc.status, doc.failed = message, failed
			}
		})
	}
	go func() {
		command, err := config.ResolveLanguageServerCommand(server.Command)
		if err != nil {
			status(server.Language+" · "+err.Error(), true)
			return
		}
		initCtx, done := context.WithTimeout(ctx, 30*time.Second)
		client, err := lsp.Start(initCtx, command, server.Args, group.root)
		done()
		if err != nil {
			if ctx.Err() == nil {
				status(server.Language+" · "+err.Error(), true)
			}
			return
		}
		defer client.Close()
		client.SetNotificationHandler(func(method string, raw json.RawMessage) {
			if method != "textDocument/publishDiagnostics" {
				return
			}
			var event lsp.PublishDiagnostics
			if json.Unmarshal(raw, &event) != nil || event.URI == "" {
				return
			}
			group.sentMu.Lock()
			sent, ok := group.sent[event.URI]
			group.sentMu.Unlock()
			if !ok || (event.Version != nil && *event.Version != sent.version) {
				return
			}
			if len(event.Diagnostics) > 500 {
				event.Diagnostics = event.Diagnostics[:500]
			}
			post(func(s *Workspace) {
				for _, doc := range group.documents {
					if doc.uri == event.URI && doc.text == sent.text {
						doc.diagnostics = event.Diagnostics
						break
					}
				}
			})
		})
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if client.Call(shutdownCtx, "shutdown", nil, nil) == nil {
				_ = client.Notify("exit", nil)
			}
		}()
		status(server.Language+" · ready", false)
		type openedDocument struct {
			text    string
			version int
		}
		opened := map[string]openedDocument{}
		defer func() {
			for uri := range opened {
				_ = client.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
			}
		}()
		for {
			var req languageRequest
			select {
			case <-ctx.Done():
				return
			case req = <-group.queue:
			}
			// Coalesce rapid typing and give the server the newest full snapshot.
			if req.kind == "completion" {
				timer := time.NewTimer(120 * time.Millisecond)
			wait:
				for {
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case next := <-group.queue:
						req = next
						if req.kind != "completion" {
							timer.Stop()
							break wait
						}
						if !timer.Stop() {
							select {
							case <-timer.C:
							default:
							}
						}
						timer.Reset(120 * time.Millisecond)
					case <-timer.C:
						break wait
					}
				}
			}
			// Reconcile every open document before serving a request, so
			// completion sees unsaved changes in sibling tabs as well.
			present := make(map[string]bool, len(req.documents))
			for _, snap := range req.documents {
				present[snap.uri] = true
				current, ok := opened[snap.uri]
				if !ok {
					group.sentMu.Lock()
					group.sent[snap.uri] = sentLanguageDocument{version: 1, text: snap.text}
					group.sentMu.Unlock()
					err = client.Open(snap.uri, snap.language, snap.text)
					current = openedDocument{text: snap.text, version: 1}
				} else if current.text != snap.text {
					current.version++
					group.sentMu.Lock()
					group.sent[snap.uri] = sentLanguageDocument{version: current.version, text: snap.text}
					group.sentMu.Unlock()
					err = client.Change(snap.uri, snap.text, current.version)
					current.text = snap.text
				}
				if err != nil {
					status(err.Error(), true)
					return
				}
				opened[snap.uri] = current
			}
			for uri := range opened {
				if !present[uri] {
					if err = client.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}}); err != nil {
						status(err.Error(), true)
						return
					}
					delete(opened, uri)
					group.sentMu.Lock()
					delete(group.sent, uri)
					group.sentMu.Unlock()
				}
			}
			if req.kind == "sync" {
				continue
			}
			uri := req.documentURI
			if !present[uri] {
				continue
			}
			b := req.buffer
			if req.kind == "save" {
				if err := client.Notify("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}}); err != nil {
					status(err.Error(), true)
					return
				}
				continue
			}
			callCtx, stop := context.WithTimeout(ctx, 10*time.Second)
			if req.kind == "completion" {
				items, callErr := client.Completion(callCtx, uri, lsp.PositionAt(req.text, req.offset))
				stop()
				post(func(s *Workspace) {
					doc := s.languageDocs[b]
					if doc == nil || doc.server != group || s.Buf() != b || b.Revision != req.revision || b.Cursor != req.offset || s.languageRequest != req.token {
						return
					}
					if callErr != nil {
						doc.status = server.Language + " · " + callErr.Error()
						doc.failed = true
						return
					}
					filtered := items[:0]
					for _, it := range items {
						if it.InsertTextFormat != 2 && it.Label != "" {
							filtered = append(filtered, it)
						}
					}
					if len(filtered) > 0 {
						s.completion = &completionState{buffer: b, revision: req.revision, cursor: req.offset, items: filtered}
					}
				})
			} else if req.kind == "hover" {
				text, callErr := client.Hover(callCtx, uri, lsp.PositionAt(req.text, req.offset))
				stop()
				post(func(s *Workspace) {
					doc := s.languageDocs[b]
					if doc == nil || doc.server != group || s.Buf() != b || b.Revision != req.revision || s.languageRequest != req.token {
						return
					}
					if callErr != nil {
						doc.status = server.Language + " · " + callErr.Error()
						doc.failed = true
						return
					}
					if text == "" {
						text = "No documentation available for this symbol."
					}
					s.documentation = &documentationState{buffer: b, text: text, x: req.x, y: req.y, symbol: req.symbol}
				})
			} else if req.kind == "definition" {
				locations, callErr := client.Definition(callCtx, uri, lsp.PositionAt(req.text, req.offset))
				stop()
				post(func(s *Workspace) {
					doc := s.languageDocs[b]
					if doc == nil || doc.server != group || s.Buf() != b || b.Revision != req.revision || s.languageRequest != req.token {
						return
					}
					if callErr != nil {
						doc.status = server.Language + " · " + callErr.Error()
						return
					}
					if len(locations) == 0 {
						doc.status = server.Language + " · no definition found"
						return
					}
					if err := s.navigateDefinition(locations); err != nil {
						doc.status = server.Language + " · " + err.Error()
					}
				})
			} else {
				stop()
			}
		}
	}()
}

// navigateDefinition reuses an open buffer (and its unsaved contents), then
// resolves the server's UTF-16 position against that buffer rather than disk.
func (ws *Workspace) navigateDefinition(locations []lsp.Location) error {
	var lastErr error
	for _, loc := range locations {
		path, err := lsp.PathFromURI(loc.URI)
		if err != nil {
			lastErr = err
			continue
		}
		index := -1
		for i, b := range ws.Buffers {
			if b.Path == "" {
				continue
			}
			if sameFilePath(b.Path, path) {
				index = i
				break
			}
		}
		var b *editor.Buffer
		if index < 0 {
			b, err = editor.OpenBuffer(path)
			if err != nil {
				lastErr = fmt.Errorf("open definition: %w", err)
				continue
			}
		} else {
			b = ws.Buffers[index]
		}
		if b.Kind != editor.KindText {
			lastErr = fmt.Errorf("definition is not a text file: %s", path)
			continue
		}
		offset, ok := lsp.OffsetAt(b.Text(), loc.Range.Start)
		if !ok {
			lastErr = fmt.Errorf("definition position is outside %s", path)
			continue
		}
		previous := ws.Buf()
		if index < 0 {
			ws.addBuffer(b)
			index = ws.Active
		}
		ws.Active = index
		ws.languageRequest++
		ws.completion = nil
		ws.documentation = nil
		if preview := ws.markdownPreview[b]; preview != nil {
			preview.enabled = false
		}
		if ws.Find != nil && previous != b {
			ws.RefreshFind()
		}
		b.SetCursor(offset)
		line, _ := b.OffsetToLC(offset)
		visible := (editor.Metrics{LineHeight: ws.LineHeight}).VisibleLines(ws.paneHeight())
		if line < ws.scroll() || line >= ws.scroll()+visible {
			ws.setScroll(line - visible/3)
		}
		ws.scrollCaretIntoViewX()
		ws.NoteCaretActivity()
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no definition found")
}

// showCaretDocumentation requests hover at the caret, anchored alongside it.
func (ws *Workspace) showCaretDocumentation() {
	b := ws.Buf()
	if b == nil || b.Kind != editor.KindText {
		return
	}
	offset := b.Cursor
	line, col := b.OffsetToLC(offset)
	lineText := b.Line(line)
	// At the boundary immediately after a word, query its last rune. This
	// makes Ctrl+Q useful after typing a symbol without moving the caret.
	if symbolAt(b, offset) == "" && col > 0 {
		_, n := utf8.DecodeLastRuneInString(lineText[:col])
		if symbolAt(b, offset-n) != "" {
			offset -= n
		}
	}
	x := float32(0)
	x += ws.paneLeft()
	if ws.ShowLineNumbers {
		x += editor.GutterWidth(b.LineCount(), ws.CharAdvance, editor.Foldable(b.LanguageOf()))
	} else {
		x += editor.GutterLeftInset
	}
	caretX, _ := ws.codeShaper.CaretMetrics(b, ws.Theme())
	x += caretX - min(ws.scrollX(), ws.maxScrollX())
	y := ws.chromeTop() + ui.TabBarHeight + (float32(line)-ws.scrollF())*ws.LineHeight
	if ws.Find != nil {
		y += ui.FindBarHeight
	}
	ws.completion = nil
	ws.documentation = nil
	ws.requestLanguage(b, "hover", offset, x, y)
}

func (ws *Workspace) closeLanguage(b *editor.Buffer) {
	if d := ws.languageDocs[b]; d != nil {
		delete(ws.languageDocs, b)
		group := d.server
		delete(group.documents, b)
		if len(group.documents) == 0 {
			delete(ws.languageServers, group.key)
			group.cancel()
		} else {
			ws.queueLanguage(group, languageRequest{kind: "sync"})
		}
	}
	ws.languageRequest++
}
func (ws *Workspace) closeLanguages() {
	for b := range ws.languageDocs {
		ws.closeLanguage(b)
	}
}
func (ws *Workspace) requestLanguage(b *editor.Buffer, kind string, offset int, x, y float32) {
	d := ws.languageDocs[b]
	if d != nil && d.failed && kind != "sync" {
		group := d.server
		buffers := make([]*editor.Buffer, 0, len(group.documents))
		for buf := range group.documents {
			buffers = append(buffers, buf)
			delete(ws.languageDocs, buf)
		}
		delete(ws.languageServers, group.key)
		group.cancel()
		for _, buf := range buffers {
			ws.startLanguage(buf)
		}
		d = ws.languageDocs[b]
	}
	if d == nil {
		if kind != "sync" {
			ws.languageStatus = "No language server for this file"
		}
		return
	}
	ws.languageRequest++
	d.text = b.Text()
	req := languageRequest{text: d.text, offset: offset, revision: b.Revision, token: ws.languageRequest, kind: kind, x: x, y: y, buffer: b, documentURI: d.uri}
	if kind == "hover" || kind == "definition" {
		req.symbol = symbolAt(b, offset)
	}
	ws.queueLanguage(d.server, req)
}

// symbolAt labels a hover without modifying the caret or selection.
func symbolAt(b *editor.Buffer, offset int) string {
	line, col := b.OffsetToLC(offset)
	s := b.Line(line)
	if col < 0 || col >= len(s) {
		return ""
	}
	isSymbol := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' || r == '$'
	}
	r, n := utf8.DecodeRuneInString(s[col:])
	if !isSymbol(r) {
		return ""
	}
	start, end := col, col+n
	for start > 0 {
		r, n = utf8.DecodeLastRuneInString(s[:start])
		if !isSymbol(r) {
			break
		}
		start -= n
	}
	for end < len(s) {
		r, n = utf8.DecodeRuneInString(s[end:])
		if !isSymbol(r) {
			break
		}
		end += n
	}
	return s[start:end]
}

func (ws *Workspace) queueLanguage(group *languageServer, req languageRequest) {
	for _, doc := range group.documents {
		req.documents = append(req.documents, languageSnapshot{uri: doc.uri, text: doc.text, language: group.config.Language})
	}
	select {
	case group.queue <- req:
	default:
		select {
		case <-group.queue:
		default:
		}
		select {
		case group.queue <- req:
		default:
		}
	}
}

// changedLanguage is called after any pane edit or caret movement.
func (ws *Workspace) changedLanguage(b *editor.Buffer, revision uint64, cursor int) {
	if b.Revision == revision && b.Cursor == cursor {
		return
	}
	ws.languageRequest++
	ws.completion = nil
	ws.documentation = nil
	if b.Revision == revision {
		return
	}
	if doc := ws.languageDocs[b]; doc != nil {
		doc.diagnostics = nil
	}
	kind := "sync"
	line, col := b.OffsetToLC(b.Cursor)
	if col > 0 {
		r, _ := utf8.DecodeLastRuneInString(b.Line(line)[:col])
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' {
			kind = "completion"
		}
	}
	ws.requestLanguage(b, kind, b.Cursor, 0, 0)
}

// completionEdits validates all edits against the original snapshot and
// combines them into one buffer operation, so a single Undo reverses imports
// as well as the inserted completion.
func completionEdits(b *editor.Buffer, item lsp.CompletionItem) error {
	type edit struct {
		start, end int
		text       string
		primary    bool
	}
	text := b.Text()
	var edits []edit
	add := func(r lsp.Range, value string) bool {
		a, ok := lsp.OffsetAt(text, r.Start)
		if !ok {
			return false
		}
		z, ok := lsp.OffsetAt(text, r.End)
		if !ok || z < a {
			return false
		}
		edits = append(edits, edit{start: a, end: z, text: value})
		return true
	}
	if len(item.TextEdit) > 0 && string(item.TextEdit) != "null" {
		var e struct {
			Range   *lsp.Range `json:"range"`
			Insert  *lsp.Range `json:"insert"`
			NewText string     `json:"newText"`
		}
		if json.Unmarshal(item.TextEdit, &e) != nil {
			return fmt.Errorf("invalid completion edit")
		}
		r := e.Range
		if r == nil {
			r = e.Insert
		}
		if r == nil || !add(*r, e.NewText) {
			return fmt.Errorf("invalid completion range")
		}
	} else {
		start := b.Cursor
		for start > 0 {
			r, size := utf8.DecodeLastRuneInString(text[:start])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				break
			}
			start -= size
		}
		value := item.InsertText
		if value == "" {
			value = item.Label
		}
		edits = append(edits, edit{start: start, end: b.Cursor, text: value})
	}
	edits[0].primary = true
	for _, e := range item.AdditionalTextEdits {
		if !add(e.Range, e.NewText) {
			return fmt.Errorf("invalid additional completion edit")
		}
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	for i := 1; i < len(edits); i++ {
		if edits[i].start < edits[i-1].end || edits[i].start == edits[i-1].start {
			return fmt.Errorf("overlapping completion edits")
		}
	}
	start, end := edits[0].start, edits[len(edits)-1].end
	var replacement strings.Builder
	prev, caret := start, start
	for _, e := range edits {
		replacement.WriteString(text[prev:e.start])
		replacement.WriteString(e.text)
		if e.primary {
			caret = start + replacement.Len()
		}
		prev = e.end
	}
	b.Anchor, b.Cursor = start, end
	b.Insert(replacement.String())
	b.SetCursor(caret)
	return nil
}

func (ws *Workspace) acceptCompletion(index int) {
	c := ws.completion
	if c == nil || c.buffer != ws.Buf() || c.revision != c.buffer.Revision || index < 0 || index >= len(c.items) {
		ws.completion = nil
		return
	}
	if err := completionEdits(c.buffer, c.items[index]); err != nil {
		ws.languageStatus = err.Error()
	} else {
		ws.requestLanguage(c.buffer, "sync", c.buffer.Cursor, 0, 0)
	}
	ws.completion = nil
	ws.scrollCaretIntoViewX()
}
