package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// LanguageServer describes one stdio Language Server Protocol process. Each
// definition lives in language-servers/<language>.yaml under the app settings
// directory so users can add servers without recompiling the editor.
//
// Supported YAML is intentionally small and human-editable: scalar keys plus
// indented lists. This avoids making the example depend on a YAML library.
type LanguageServer struct {
	Language    string
	Command     string
	Args        []string
	Extensions  []string
	RootMarkers []string
	Enabled     bool
	Source      string // populated by LoadLanguageServers for diagnostics
}

// LanguageServersDir is the directory holding one YAML file per language.
func LanguageServersDir() string { return filepath.Join(Dir(), "language-servers") }

// EnsureLanguageServerDefaults creates the editable Go starter definition on
// first launch. Existing YAML files are never changed or overwritten.
func EnsureLanguageServerDefaults() error {
	dir := LanguageServersDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "go.yaml")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	const goServer = "# Go language server (LSP)\n# `gopls` is resolved from PATH or Go's GOPATH/bin automatically.\nlanguage: go\ncommand: gopls\nargs:\n  - serve\nextensions:\n  - .go\nroot_markers:\n  - go.mod\n  - go.work\n"
	return os.WriteFile(path, []byte(goServer), 0o644)
}

// LoadLanguageServers reads every .yaml/.yml definition, ordered by filename.
// Invalid files are returned as errors rather than silently starting the wrong
// executable; valid sibling definitions remain available.
func LoadLanguageServers() ([]LanguageServer, []error) {
	entries, err := os.ReadDir(LanguageServersDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var servers []LanguageServer
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yaml" && filepath.Ext(entry.Name()) != ".yml") {
			continue
		}
		path := filepath.Join(LanguageServersDir(), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s, err := parseLanguageServerYAML(string(data))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		s.Source = path
		servers = append(servers, s)
	}
	return servers, errs
}

// ServerForPath resolves a file extension to its configured server.
func ServerForPath(servers []LanguageServer, path string) (LanguageServer, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	for _, server := range servers {
		if !server.Enabled {
			continue
		}
		for _, candidate := range server.Extensions {
			if strings.EqualFold(candidate, ext) {
				return server, true
			}
		}
	}
	return LanguageServer{}, false
}

// ResolveLanguageServerCommand resolves a configured command without forcing
// users to put Go's bin directory on PATH. Absolute commands are preserved;
// bare commands try PATH first, then GOBIN and each GOPATH/bin.
func ResolveLanguageServerCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("empty language-server command")
	}
	if filepath.IsAbs(command) || strings.ContainsAny(command, `\\/`) {
		if info, err := os.Stat(command); err == nil && !info.IsDir() {
			return command, nil
		}
		return "", fmt.Errorf("language-server executable not found: %s", command)
	}
	if path, err := exec.LookPath(command); err == nil {
		return path, nil
	}
	goCmd := exec.Command("go", "env", "GOBIN", "GOPATH")
	hideHelperWindow(goCmd)
	goenv, err := goCmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve %q: not on PATH and go env failed", command)
	}
	name := command
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		name += ".exe"
	}
	lines := strings.Split(strings.ReplaceAll(string(goenv), "\r\n", "\n"), "\n")
	var dirs []string
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		dirs = append(dirs, strings.TrimSpace(lines[0]))
	}
	if len(lines) > 1 {
		for _, path := range filepath.SplitList(strings.TrimSpace(lines[1])) {
			dirs = append(dirs, filepath.Join(path, "bin"))
		}
	}
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("language-server executable not found: %s", command)
}

func parseLanguageServerYAML(raw string) (LanguageServer, error) {
	s := LanguageServer{Enabled: true}
	var list *[]string
	for n, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(strings.SplitN(rawLine, "#", 2)[0])
		if line == "" || line == "---" {
			continue
		}
		if strings.HasPrefix(line, "- ") {
			if list == nil {
				return s, fmt.Errorf("line %d: list item without a list key", n+1)
			}
			*list = append(*list, unquote(strings.TrimSpace(strings.TrimPrefix(line, "- "))))
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return s, fmt.Errorf("line %d: expected key: value", n+1)
		}
		key, value := strings.TrimSpace(parts[0]), unquote(strings.TrimSpace(parts[1]))
		list = nil
		switch key {
		case "language":
			s.Language = value
		case "command":
			s.Command = value
		case "enabled":
			s.Enabled = !strings.EqualFold(value, "false")
		case "args":
			list = &s.Args
		case "extensions":
			list = &s.Extensions
		case "root_markers":
			list = &s.RootMarkers
		default:
			return s, fmt.Errorf("line %d: unknown key %q", n+1, key)
		}
	}
	if s.Language == "" || s.Command == "" || len(s.Extensions) == 0 {
		return s, fmt.Errorf("language, command and extensions are required")
	}
	return s, nil
}

func unquote(s string) string { return strings.Trim(strings.TrimSpace(s), "\"'") }
