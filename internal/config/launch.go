package config

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LaunchProfile identifies a Go main package by directory and passes Args
// verbatim to the executable, without going through a shell.
type LaunchProfile struct {
	Name       string   `json:"name"`
	PackageDir string   `json:"package_dir"`
	Args       []string `json:"args,omitempty"`
}

type LaunchProfiles struct {
	Active   string          `json:"active,omitempty"`
	Profiles []LaunchProfile `json:"profiles"`
}

func (p LaunchProfile) Directory(root string) (string, error) {
	dir := filepath.FromSlash(p.PackageDir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("package directory does not exist: %s", dir)
	}
	return dir, nil
}

func launchPath(root string) string {
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	key := sha256.Sum256([]byte(root))
	return filepath.Join(Dir(), "projects", fmt.Sprintf("%x.json", key))
}

func LoadLaunchProfiles(root string) (LaunchProfiles, error) {
	var cfg LaunchProfiles
	data, err := os.ReadFile(launchPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(data, &cfg)
	return cfg, err
}

func (p LaunchProfiles) Save(root string) error {
	path := launchPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "run-config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(append(data, '\n'))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
