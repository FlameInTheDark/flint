package lsp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/config"
)

func TestUTF16Positions(t *testing.T) {
	text := "a\U0001f600\u00e9\nhello"
	for _, tc := range []struct {
		offset int
		pos    Position
	}{
		{0, Position{0, 0}}, {1, Position{0, 1}}, {5, Position{0, 3}}, {7, Position{0, 4}}, {8, Position{1, 0}}, {13, Position{1, 5}},
	} {
		if got := PositionAt(text, tc.offset); got != tc.pos {
			t.Errorf("PositionAt(%d) = %#v", tc.offset, got)
		}
		if got, ok := OffsetAt(text, tc.pos); !ok || got != tc.offset {
			t.Errorf("OffsetAt(%#v) = %d, %v", tc.pos, got, ok)
		}
	}
	if _, ok := OffsetAt(text, Position{0, 2}); ok {
		t.Fatal("accepted a position inside a surrogate pair")
	}
	if _, ok := OffsetAt(text, Position{3, 0}); ok {
		t.Fatal("accepted an invalid line")
	}
}

func TestRootAndURI(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "space & \u00fc")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(child, "main.go")
	if got := Root(path, []string{"go.mod"}); got != dir {
		t.Fatalf("root = %q", got)
	}
	uri := FileURI(path)
	if !strings.HasPrefix(uri, "file:///") || !strings.Contains(uri, "space%20&%20%C3%BC") {
		t.Fatalf("URI = %q", uri)
	}
}

func TestHoverContentFormats(t *testing.T) {
	code := hoverText([]byte(`{"language":"go","value":"func Hello() {}"}`))
	if code != "```go\nfunc Hello() {}\n```" {
		t.Fatalf("marked code = %q", code)
	}
	plain := hoverText([]byte(`{"kind":"plaintext","value":"*literal*\nnext"}`))
	if plain != "\\*literal\\*  \nnext" {
		t.Fatalf("plaintext = %q", plain)
	}
	if got := hoverText([]byte(`[{"language":"go","value":"func F()"},"comment"]`)); !strings.Contains(got, "```go") || !strings.Contains(got, "comment") {
		t.Fatalf("hover array = %q", got)
	}
}

func TestDefinitionResponseShapes(t *testing.T) {
	inputs := []struct {
		raw     string
		wantURI string
		want    Position
		count   int
		invalid bool
	}{
		{`null`, "", Position{}, 0, false},
		{`{"uri":"file:///a.go","range":{"start":{"line":2,"character":3},"end":{"line":2,"character":5}}}`, "file:///a.go", Position{2, 3}, 1, false},
		{`[{"uri":"file:///b.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":2}}}]`, "file:///b.go", Position{0, 0}, 1, false},
		{`[{"targetUri":"file:///c.go","targetRange":{"start":{"line":10,"character":0},"end":{"line":15,"character":0}},"targetSelectionRange":{"start":{"line":11,"character":5},"end":{"line":11,"character":8}}}]`, "file:///c.go", Position{11, 5}, 1, false},
		{`{"uri":"file:///bad.go"}`, "", Position{}, 0, true},
		{`{"uri":"file:///bad.go","range":{"start":{"line":3,"character":0},"end":{"line":2,"character":0}}}`, "", Position{}, 0, true},
	}
	for _, tc := range inputs {
		got, err := parseDefinitions([]byte(tc.raw))
		if (err != nil) != tc.invalid {
			t.Fatalf("parse %s: %v", tc.raw, err)
		}
		if len(got) != tc.count {
			t.Fatalf("parse %s: got %d", tc.raw, len(got))
		}
		if tc.count > 0 && (got[0].URI != tc.wantURI || got[0].Range.Start != tc.want) {
			t.Fatalf("parse %s: %+v", tc.raw, got)
		}
	}
}

func TestDefinitionFileURI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "space & ü.go")
	got, err := PathFromURI(FileURI(path))
	if err != nil || got != path {
		t.Fatalf("URI roundtrip = %q, %v; want %q", got, err, path)
	}
	for _, uri := range []string{"https://example.com/a.go", "file:relative.go", "file:///tmp/file.go#fragment", "file:///tmp/%00.go"} {
		if _, err := PathFromURI(uri); err == nil {
			t.Errorf("accepted unsafe URI %q", uri)
		}
	}
	if runtime.GOOS == "windows" {
		unc := `\\server\share\source file.go`
		uri := FileURI(unc)
		if uri != "file://server/share/source%20file.go" {
			t.Fatalf("UNC file URI = %q", uri)
		}
		if got, err := PathFromURI(uri); err != nil || got != unc {
			t.Fatalf("UNC URI roundtrip = %q, %v", got, err)
		}
	}
}

// This test exercises real framing, initialization, document synchronization,
// completion and hover with gopls when installed; it does not require network.
func TestGoplsIntegration(t *testing.T) {
	command, err := config.ResolveLanguageServerCommand("gopls")
	if err != nil {
		t.Skip("gopls not installed:", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/demo\n\ngo 1.23\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.go")
	text := "package main\n\n// Greet returns a greeting.\nfunc Greet() string { return \"hi\" }\n\nfunc main() { Gre }\n"
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c, err := Start(ctx, command, []string{"serve"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	uri := FileURI(path)
	if err = c.Open(uri, "go", text); err != nil {
		t.Fatal(err)
	}
	pos := PositionAt(text, strings.LastIndex(text, "Gre")+3)
	items, err := c.Completion(ctx, uri, pos)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if strings.HasPrefix(item.Label, "Greet") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Greet missing from completion: %+v", items)
	}
	changed := strings.Replace(text, "{ Gre }", "{ Greet() }", 1)
	if err = c.Change(uri, changed, 2); err != nil {
		t.Fatal(err)
	}
	docs, err := c.Hover(ctx, uri, PositionAt(changed, strings.LastIndex(changed, "Greet")+2))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(docs, "Greet returns a greeting") {
		t.Fatalf("unexpected hover: %q", docs)
	}
	standard := "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hello\") }\n"
	if err = c.Change(uri, standard, 3); err != nil {
		t.Fatal(err)
	}
	locations, err := c.Definition(ctx, uri, PositionAt(standard, strings.Index(standard, "Println")+2))
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) == 0 {
		t.Fatal("gopls did not return a standard library definition")
	}
	target, err := PathFromURI(locations[0].URI)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(target); err != nil {
		t.Fatalf("definition path %q: %v", target, err)
	}
}
