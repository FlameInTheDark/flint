package workspace_test

import (
	"os"
	"testing"
)

// TestMain pins the settings directory to a temp dir so the suite never
// reads (or writes) the developer's real ~/.go-gpui/editor settings.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "go-gpui-test-config")
	if err != nil {
		panic(err)
	}
	os.Setenv("GO_GPUI_CONFIG_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
