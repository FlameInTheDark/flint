// Command flint is a desktop code editor built with go-gpui.
//
// By default it opens an interactive native window (win32 on Windows,
// X11/Wayland on Linux) and runs until closed. Positional arguments open
// files and folders:
//
//	go run . main.go   # open a file
//	go run . .         # open a folder as a project
//
// Debug mode renders frames to PNGs and self-verifies the interactions:
//
//	go run . --headless --out frames
//
// The application lives in internal/ (workspace, editor, terminal, ui);
// this file is only the entry point.
package main

import (
	"github.com/FlameInTheDark/flint/internal/workspace"
)

func main() {
	workspace.Run()
}
