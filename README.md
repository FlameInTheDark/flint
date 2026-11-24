# Flint

Flint is a desktop code editor built in Go with [go-gpui](https://github.com/FlameInTheDark/go-gpui). It runs on Windows and Linux. It has a project tree, syntax highlighting, project search, a minimap, an integrated terminal, and Markdown preview. Optional Go tools add completion, go-to-definition, and debugging.

## Run from source

Use Go 1.23 or newer. Flint uses the published go-gpui module; you don't need a separate checkout of the toolkit. Run these commands from the Flint repository root:

```sh
go run .               # open the welcome screen
go run . main.go       # open a file
go run . .             # open the current folder as a project
```

Run `go build .` for a binary, or `go build -ldflags "-H windowsgui" .` on Windows to launch it without a console.

## Optional tools

| Tool | What it enables |
| --- | --- |
| `go install golang.org/x/tools/gopls@latest` | Go completion (**Ctrl+Space**), definitions (**Ctrl+click**), and documentation (**Ctrl+Q**). |
| `go install github.com/go-delve/delve/cmd/dlv@latest` | Go debugging. Toggle a breakpoint with **F9** and start debugging with **Shift+F9**. |
| `git` on `PATH` | Staged and unstaged changes, side-by-side diffs, staging, commits, and push. |

**Ctrl+S** saves the active file, **Ctrl+F** finds text in it, and **Ctrl+P** searches the project. Use **Preview** on a Markdown tab to switch to CommonMark/GFM preview. The **Run** menu includes package-test debugging and saved launch configurations.

Flint keeps the former editor's settings and language-server configuration under `~/.go-gpui/editor`, so no settings migration is required. Set `GO_GPUI_CONFIG_DIR` to use another directory.

## Git

Open **Git Version Control** in the tool rail. Click a file to compare saved changes side by side, and use **+** or **−** to stage or unstage it. **Commit…** commits only staged changes. **Push** sends the current branch to its configured upstream; on a first push, Flint uses `origin` or the only configured remote and sets the upstream. **Commit & Push…** does both and keeps the local commit if the push fails.

Flint uses your Git configuration, credential helper, and SSH agent. Set up authentication outside Flint if a push cannot authenticate. A rejected push does not force-update the remote.

## Development

```sh
go test ./...
go run . --headless --out frames
go run ./bench -big 10000 -frames 1500 -cpuprofile frame-build.cpu
go test ./internal/workspace -run '^$' -bench '^BenchmarkSoftwarePaint$' -benchmem
```

The headless mode drives the editor without a display and writes PNGs. Terminal integration tests require `sh`; Windows terminal tests also exercise ConPTY.

If you're modifying both projects at once, keep them in sibling directories and create an optional local workspace from Flint:

```sh
go work init . ../go-gpui
```

Flint ignores workspace files. Set `GOWORK=off` to build against the published dependency, as CI does.

## Releases

Successful pushes to `main`, including merges, use semantic-release and [Conventional Commits](https://www.conventionalcommits.org/) to decide whether to tag and publish a release. The first release needs a release-triggering commit, such as `feat: launch Flint`.

After a release is published, the same workflow builds from the exact tag and attaches archives for **Linux and Windows** on **amd64 and arm64**, with SHA-256 checksums. macOS isn't packaged because go-gpui has no native macOS backend. If a packaging job fails after the release was created, rerun the failed jobs rather than starting a new release.

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
