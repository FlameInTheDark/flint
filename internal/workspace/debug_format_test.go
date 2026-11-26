package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/debugger"
	"github.com/FlameInTheDark/flint/internal/ui"
)

// pausedSnapshotForTest builds a synthetic paused snapshot covering the
// value shapes the Delve DAP produces: primitives, composites, pointers,
// functions, channels and long strings.
func pausedSnapshotForTest() debugger.Snapshot {
	return debugger.Snapshot{
		State:    debugger.Paused,
		Detail:   "breakpoint hit",
		Thread:   1,
		StopID:   7,
		Selected: 0,
		Frames: []debugger.Frame{
			{ID: 1, Line: 3, Column: 12, Name: "main.helper", Source: debugger.Source{Path: "/tmp/proj/helper.go"}},
			{ID: 2, Line: 24, Column: 2, Name: "main.main", Source: debugger.Source{Path: "/tmp/proj/main.go"}},
		},
		Scopes: []debugger.Scope{
			{Name: "Locals", VariablesReference: 1000},
			{Name: "Globals", VariablesReference: 2000, Expensive: true},
		},
		Variables: map[int][]debugger.Variable{
			1000: {
				{Name: "n", Value: "42", Type: "int"},
				{Name: "ratio", Value: "3.14159", Type: "float64"},
				{Name: "greeting", Value: "\"hello\"", Type: "string"},
				{Name: "enabled", Value: "true", Type: "bool"},
				{Name: "missing", Value: "nil", Type: "*main.Config"},
				{Name: "steps", Value: "[]int len: 3, cap: 3, [1, 2, 3]", Type: "[]int"},
				{Name: "conf", Value: "main.Config{Verbose: true, Retries: 3, Tag: \"prod\"}", Type: "main.Config", VariablesReference: 1001},
				{Name: "ptr", Value: "0xc0000b4000", Type: "*main.Config", VariablesReference: 1002},
				{Name: "fn", Value: "func(int) int", Type: "func(int) int"},
				// A value wider than the segment viewport: the
				// variables view must pan horizontally to reach it
				// (the horizontal-scroll test drives it with the wheel).
				{Name: "wide", Value: "\"" + strings.Repeat("x", 200) + "\"", Type: "string"},
				// Extra rows so the segment overflows its viewport even in a
				// tall panel (the scroll-persistence test resizes to 300px).
				{Name: "v00", Value: "\"row\"", Type: "string"},
				{Name: "v01", Value: "1", Type: "int"},
				{Name: "v02", Value: "2", Type: "int"},
				{Name: "v03", Value: "3", Type: "int"},
				{Name: "v04", Value: "4", Type: "int"},
				{Name: "v05", Value: "5", Type: "int"},
				{Name: "v06", Value: "6", Type: "int"},
				{Name: "v07", Value: "7", Type: "int"},
				{Name: "v08", Value: "8", Type: "int"},
				{Name: "v09", Value: "9", Type: "int"},
				{Name: "v10", Value: "10", Type: "int"},
				{Name: "v11", Value: "11", Type: "int"},
				{Name: "v12", Value: "12", Type: "int"},
				{Name: "v13", Value: "13", Type: "int"},
				{Name: "v14", Value: "14", Type: "int"},
			},
			1001: {
				{Name: "Verbose", Value: "true", Type: "bool"},
				{Name: "Retries", Value: "3", Type: "int"},
			},
		},
		Breakpoints: map[string]map[int]debugger.Breakpoint{
			"/tmp/proj/helper.go": {3: {ID: 1, Line: 3, Verified: true}},
		},
	}
}

// runColors flattens a runBuilder into (text, color) pairs so tests can
// assert the exact coloring of a rendered row.
func runColors(b *runBuilder) [][2]string {
	value := b.value.String()
	out := make([][2]string, 0, len(b.runs))
	at := 0
	for _, r := range b.runs {
		out = append(out, [2]string{value[at : at+r.Len], r.Color.String()})
		at += r.Len
	}
	return out
}

func TestFormatDebugValue(t *testing.T) {
	if got := formatDebugValue("plain"); got != "plain" {
		t.Fatalf("plain: %q", got)
	}
	if got := formatDebugValue("a\nb\tc\x01d"); got != "a b c d" {
		t.Fatalf("control chars: %q", got)
	}
	long := strings.Repeat("x", debugValueMax+50)
	got := formatDebugValue(long)
	if len([]rune(got)) != debugValueMax+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long value not truncated: len=%d suffix=%q", len([]rune(got)), string([]rune(got)[len(got)-3:]))
	}
}

func TestDebugVarRunsLayout(t *testing.T) {
	tm := ui.DefaultTheme()
	v := debugger.Variable{Name: "n", Value: "42", Type: "int"}
	b := debugVarRuns(v, tm, "")
	parts := runColors(b)
	text := b.value.String()
	if text != "n = 42   (int)" {
		t.Fatalf("row text = %q", text)
	}
	if len(parts) < 4 {
		t.Fatalf("expected name/sep/value/type runs, got %d", len(parts))
	}
	if parts[0][0] != "n" || parts[1][0] != " = " || parts[2][0] != "42" || parts[3][0] != "   " || parts[4][0] != "(int)" {
		t.Fatalf("unexpected run split: %v", parts)
	}
	if parts[2][1] == parts[0][1] {
		t.Fatal("value run must be colored differently from the name run")
	}
}

func TestDebugValueRunsColoring(t *testing.T) {
	tm := ui.DefaultTheme()
	colorOf := func(value string) map[string]string {
		b := &runBuilder{}
		debugValueRuns(b, value, tm)
		m := map[string]string{}
		for _, p := range runColors(b) {
			m[p[0]] = p[1]
		}
		return m
	}

	green := tm.Green.String()
	peach := tm.Peach.String()
	mauve := tm.Mauve.String()
	teal := tm.Teal.String()
	overlay := tm.Overlay.String()

	if got := colorOf("\"hello, 世界\""); got["\"hello, 世界\""] != green {
		t.Fatalf("string color: %v", got)
	}
	if got := colorOf("3.14159"); got["3.14159"] != peach {
		t.Fatalf("float color: %v", got)
	}
	if got := colorOf("42"); got["42"] != peach {
		t.Fatalf("int color: %v", got)
	}
	if got := colorOf("true"); got["true"] != mauve {
		t.Fatalf("bool color: %v", got)
	}
	if got := colorOf("nil"); got["nil"] != mauve {
		t.Fatalf("nil color: %v", got)
	}
	if got := colorOf("0xc0000b4000"); got["0xc0000b4000"] != overlay {
		t.Fatalf("pointer color: %v", got)
	}

	// Struct literal: the type peach, field keys teal, values by type.
	b := &runBuilder{}
	debugValueRuns(b, `main.Config{Verbose: true, Retries: 3, Tag: "prod"}`, tm)
	text := b.value.String()
	if text != `main.Config{Verbose: true, Retries: 3, Tag: "prod"}` {
		t.Fatalf("struct text mangled: %q", text)
	}
	colors := runColors(b)
	find := func(s string) string {
		for _, p := range colors {
			if p[0] == s {
				return p[1]
			}
		}
		return ""
	}
	// The tokenizer splits dotted type names: "main" plain, "Config" as the
	// composite type before the brace.
	if got := find("Config"); got != peach {
		t.Fatalf("struct type color %q", got)
	}
	if got := find("Verbose"); got != teal {
		t.Fatalf("field color %q", got)
	}
	if got := find("\"prod\""); got != green {
		t.Fatalf("nested string color %q", got)
	}

	// String with an escape: consumed as one run, no run split inside.
	b = &runBuilder{}
	debugValueRuns(b, `"a\nb"`, tm)
	if len(b.runs) != 1 || b.value.String() != `"a\nb"` {
		t.Fatalf("escaped string runs: %+v %q", b.runs, b.value.String())
	}
}

func TestDebugFrameRuns(t *testing.T) {
	tm := ui.DefaultTheme()
	b := debugFrameRuns("/tmp/proj/main.go", "main.main", 24, tm, false)
	if b.value.String() != "main.go:24 · main.main" {
		t.Fatalf("frame text = %q", b.value.String())
	}
	colors := runColors(b)
	if colors[0][0] != "main.go:24" || colors[0][1] != tm.Accent.String() {
		t.Fatalf("file:line run: %v", colors[0])
	}
	if colors[2][0] != "main.main" || colors[2][1] != tm.Sky.String() {
		t.Fatalf("function run: %v", colors[2])
	}
}

func TestDebugBreakpointRuns(t *testing.T) {
	tm := ui.DefaultTheme()
	b := debugBreakpointRuns("/tmp/proj/main.go", 7, 11, "", tm)
	if b.value.String() != "/tmp/proj/main.go:7 → 11" {
		t.Fatalf("moved bp text = %q", b.value.String())
	}
	b = debugBreakpointRuns("/tmp/proj/main.go", 7, 0, "unverified", tm)
	if b.value.String() != "/tmp/proj/main.go:7 · unverified" {
		t.Fatalf("message bp text = %q", b.value.String())
	}
}

func TestOfflineSessionLifecycle(t *testing.T) {
	snap := pausedSnapshotForTest()
	s := debugger.OfflineSession(snap)
	if s.Snapshot().State != debugger.Paused || len(s.Snapshot().Frames) != 2 {
		t.Fatalf("offline snapshot mismatch: %+v", s.Snapshot())
	}
	// Requests are dropped without a Delve process (no panic, no change).
	if s.Request(debugger.Continue, 0, snap.StopID) {
		t.Fatal("offline session accepted a request")
	}
	if s.Snapshot().State != debugger.Paused {
		t.Fatal("offline snapshot changed")
	}
	s.Stop()
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("offline session did not close on Stop")
	}
}
