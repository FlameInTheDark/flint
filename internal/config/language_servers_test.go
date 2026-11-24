package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLanguageServers(t *testing.T) {
	dir := withTempDir(t)
	serversDir := filepath.Join(dir, "language-servers")
	if err := os.MkdirAll(serversDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "language: go\ncommand: C:/Projects/Go/bin/gopls.exe\nargs:\n  - serve\nextensions:\n  - .go\nroot_markers:\n  - go.mod\n"
	if err := os.WriteFile(filepath.Join(serversDir, "go.yaml"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	servers, errs := LoadLanguageServers()
	if len(errs) != 0 || len(servers) != 1 {
		t.Fatalf("servers=%+v errors=%v", servers, errs)
	}
	if got, ok := ServerForPath(servers, "main.go"); !ok || got.Command != "C:/Projects/Go/bin/gopls.exe" {
		t.Fatalf("lookup = %+v, %v", got, ok)
	}
	resolved, err := ResolveLanguageServerCommand(servers[0].Command)
	if err != nil || resolved != servers[0].Command {
		t.Fatalf("resolve explicit command = %q, %v", resolved, err)
	}
}

func TestEnsureLanguageServerDefaults(t *testing.T) {
	withTempDir(t)
	if err := EnsureLanguageServerDefaults(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(LanguageServersDir(), "go.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Fatal("starter YAML is empty")
	}
	if err := os.WriteFile(filepath.Join(LanguageServersDir(), "go.yaml"), []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLanguageServerDefaults(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(LanguageServersDir(), "go.yaml"))
	if string(data) != "custom" {
		t.Fatal("existing server definition was overwritten")
	}
}
