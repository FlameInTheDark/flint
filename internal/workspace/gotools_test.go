package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FlameInTheDark/flint/internal/editor"
	"github.com/FlameInTheDark/flint/internal/gotools"
	"github.com/FlameInTheDark/go-gpui/app"
	"github.com/FlameInTheDark/go-gpui/geometry"
	"github.com/FlameInTheDark/go-gpui/gpui"
	"github.com/FlameInTheDark/go-gpui/keymap"
	"github.com/FlameInTheDark/go-gpui/platform"
	"github.com/FlameInTheDark/go-gpui/platform/headless"
)

func TestGoTargetAndUnsavedPreflight(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	pkg := filepath.Join(root, "cmd", "app")
	if err := os.MkdirAll(pkg, 0755); err != nil {
		t.Fatal(err)
	}
	ws := New("go tools")
	ws.Root = root
	ws.Buffers = []*editor.Buffer{editor.NewBuffer(filepath.Join(pkg, "main.go"), "main.go", "package main\n")}
	if got, err := ws.goTarget(goRun); err != nil || got != pkg {
		t.Fatalf("run dir = %q, %v", got, err)
	}
	if got, err := ws.goTarget(goVet); err != nil || got != root {
		t.Fatalf("vet dir = %q, %v", got, err)
	}
	ws.Buf().Insert("x")
	if err := ws.goPreflight(); err == nil {
		t.Fatal("running with unsaved Go files was allowed")
	}
	if !ws.Buf().Dirty {
		t.Fatal("preflight modified the buffer")
	}
}

func TestGoDiagnosticLocations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	for _, tc := range []struct {
		line              string
		wantLine, wantCol int
	}{{"./main.go:12:7: undefined: item", 12, 7}, {path + ":3: warning", 3, 1}} {
		d, ok := parseGoDiagnostic(tc.line, dir)
		if !ok || !sameFilePath(d.path, path) || d.line != tc.wantLine || d.column != tc.wantCol {
			t.Fatalf("parse %q = %+v, %v", tc.line, d, ok)
		}
	}
	if _, ok := parseGoDiagnostic("not a diagnostic", dir); ok {
		t.Fatal("non-diagnostic output parsed as a location")
	}
}

func TestGoRunToolbarStreamsOutput(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/app\n\ngo 1.23\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Print(\"name?\"); var name string; fmt.Scanln(&name); fmt.Println(\"hello from\",name)}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hp := headless.New(geometry.SizePixels(1000, 650))
	application := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](application, platform.WindowOptions{Title: "go task"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("go task")
		ws.Root = dir
		ws.OpenFile(file)
		ws.Measure(w)
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
	dispatch := make(chan func(), 32)
	update := func(f func(*Workspace)) {
		handle.View.Update(application.App, func(s *Workspace, c *app.Context[Workspace]) any { f(s); c.Notify(); return nil })
	}
	update(func(s *Workspace) {
		s.SetEnv(nil, func(f func()) { dispatch <- f }, nil)
		s.SetDialogs(update, nil, nil)
	})
	handle.Window.Draw()
	var button gpui.Hitbox
	for _, hb := range handle.Window.RenderedHitboxes() {
		if hb.Key == "n:btn-toolbar-run" {
			button = hb
			break
		}
	}
	if button.Bounds.Size.Width <= 0 {
		t.Fatal("Go run toolbar button missing")
	}
	p := geometry.PointPixels(button.Bounds.Origin.X.AsF32()+button.Bounds.Size.Width.AsF32()/2, button.Bounds.Origin.Y.AsF32()+button.Bounds.Size.Height.AsF32()/2)
	hw := hp.LastWindow()
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
	ws := handle.View.Read(application.App)
	task := ws.GoTask
	if task == nil || !ws.ShowGoOutput {
		t.Fatalf("run did not open output: %+v", ws.StatusError)
	}
	defer task.Stop()
	deadline := time.After(30 * time.Second)
	for !strings.Contains(task.Snapshot().Output, "name?") {
		select {
		case f := <-dispatch:
			f()
		case <-task.Done():
			t.Fatalf("run exited before input: %s", task.Snapshot().Output)
		case <-deadline:
			t.Fatal("program prompt did not arrive")
		}
	}
	handle.Window.Draw()
	handle.Window.Focus(handle.View.Read(application.App).FocusGoInput)
	for _, r := range "go" {
		s := string(r)
		hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: s, KeyChar: s}})
		handle.Window.Draw()
	}
	hw.DispatchInput(platform.KeyDownEvent{Keystroke: keymap.Keystroke{Key: "enter"}})
	waiting := true
	for waiting {
		select {
		case f := <-dispatch:
			f()
		case <-task.Done():
			waiting = false
		case <-deadline:
			t.Fatal("Go run did not finish")
		}
	}
	snap := task.Snapshot()
	if snap.Status != "Finished" || !strings.Contains(snap.Output, "hello from go") {
		t.Fatalf("run output: %+v", snap)
	}
	handle.Window.Draw()
	if ws.DebugPaneHeight() >= 650 {
		t.Fatal("Go output did not reserve space")
	}
}

func TestGoBuildDoesNotOverwriteProjectExecutable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/build\n\ngo 1.23\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "build.exe")
	if err := os.WriteFile(artifact, []byte("user data"), 0644); err != nil {
		t.Fatal(err)
	}
	spec, err := goTaskSpec(goBuild, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(spec.OutputDir) // unique directory created by goTaskSpec
	task := gotools.Start(spec, nil)
	select {
	case <-task.Done():
	case <-time.After(30 * time.Second):
		task.Stop()
		t.Fatal("Go build timed out")
	}
	if s := task.Snapshot(); s.Status != "Finished" {
		t.Fatalf("build failed: %s", s.Output)
	}
	data, err := os.ReadFile(artifact)
	if err != nil || string(data) != "user data" {
		t.Fatalf("project executable changed: %q %v", data, err)
	}
}

func TestGoVetReportsClickableDiagnostic(t *testing.T) {
	t.Setenv("GO_GPUI_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/vet\n\ngo 1.23\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Printf(\"%d\", \"wrong\")}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("notes"), 0644); err != nil {
		t.Fatal(err)
	}
	hp := headless.New(geometry.SizePixels(1000, 650))
	application := gpui.NewApplication(hp)
	handle, err := gpui.OpenWindow[Workspace](application, platform.WindowOptions{Title: "go vet"}, func(w *gpui.Window, c *app.Context[Workspace]) Workspace {
		ws := New("go vet")
		ws.Root = dir
		ws.OpenFile(other)
		ws.Measure(w)
		w.Focus(ws.FocusEditor)
		return *ws
	})
	if err != nil {
		t.Fatal(err)
	}
	handle.Window.SetRootView(handle.View.ID(), func(w *gpui.Window, c *app.App) gpui.Element { return handle.View.Read(c).Render(&handle, c, w) })
	dispatch := make(chan func(), 32)
	update := func(f func(*Workspace)) {
		handle.View.Update(application.App, func(s *Workspace, c *app.Context[Workspace]) any { f(s); c.Notify(); return nil })
	}
	update(func(s *Workspace) {
		s.SetEnv(nil, func(f func()) { dispatch <- f }, nil)
		s.SetDialogs(update, nil, nil)
	})
	handle.Window.Draw()
	var btn gpui.Hitbox
	for _, hb := range handle.Window.RenderedHitboxes() {
		if hb.Key == "n:btn-toolbar-vet" {
			btn = hb
			break
		}
	}
	if btn.Bounds.Size.Width <= 0 {
		t.Fatal("Go vet toolbar button missing")
	}
	hw := hp.LastWindow()
	p := geometry.PointPixels(btn.Bounds.Origin.X.AsF32()+8, btn.Bounds.Origin.Y.AsF32()+10)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
	task := handle.View.Read(application.App).GoTask
	if task == nil {
		t.Fatal("Go vet did not start")
	}
	defer task.Stop()
	deadline := time.After(30 * time.Second)
	waiting := true
	for waiting {
		select {
		case f := <-dispatch:
			f()
		case <-task.Done():
			waiting = false
		case <-deadline:
			t.Fatal("Go vet timed out")
		}
	}
	snap := task.Snapshot()
	if snap.Status != "Failed" || !strings.Contains(snap.Output, "main.go:") {
		t.Fatalf("missing Go vet diagnostic: %s", snap.Output)
	}
	handle.Window.Draw()
	handle.Window.Draw()
	var row gpui.Hitbox
	for _, hb := range handle.Window.RenderedHitboxes() {
		if strings.HasPrefix(hb.Key, "n:btn-go-diagnostic-") {
			row = hb
			break
		}
	}
	if row.Bounds.Size.Width <= 0 {
		t.Fatal("diagnostic row not clickable")
	}
	p = geometry.PointPixels(row.Bounds.Origin.X.AsF32()+10, row.Bounds.Origin.Y.AsF32()+row.Bounds.Size.Height.AsF32()/2)
	hw.DispatchInput(platform.MouseDownEvent{Button: platform.ButtonLeft, Position: p})
	hw.DispatchInput(platform.MouseUpEvent{Button: platform.ButtonLeft, Position: p})
	buf := handle.View.Read(application.App).Buf()
	line, _ := buf.OffsetToLC(buf.Cursor)
	if !sameFilePath(buf.Path, source) || line != 2 {
		t.Fatalf("diagnostic opened %q line %d", buf.Path, line+1)
	}
}
