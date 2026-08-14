# AGENTS.md

## Project Overview

Mirage is a Go TUI application built with Bubble Tea (Elm architecture) + Lip Gloss for styling.
It connects to OpenCode server instances via SSE for real-time session monitoring and permission management.

Module: `github.com/thenickygee/mirage`

## Build / Lint / Test Commands

```bash
# Build
make build          # go build -o mirage .

# Run
make run            # go run main.go

# Format
make fmt            # goimports -w .

# Lint
make lint           # golangci-lint run ./...

# Test (no tests exist yet, but standard Go testing applies)
go test ./...                              # all tests
go test ./internal/agent                   # single package
go test ./internal/agent -run TestName     # single test function
go test ./internal/agent -run TestName -v  # verbose single test
```

## Project Structure

```
main.go                    # Entry point, CLI flags, BubbleTea program setup
internal/
├── agent/                 # Agent .md config load/save
├── command/               # Custom command definitions
├── config/                # XDG paths, user settings
├── server/                # SSE client, connection pool, mDNS discovery
├── session/               # Local session history
├── skill/                 # Skill definitions
├── stats/                 # Agent usage statistics
├── tool/                  # Tool definitions
├── update/                # Self-update mechanism
└── ui/                    # Bubble Tea UI (app.go is main model)
```

## Code Style

### Formatting & Linting

- Formatter: `goimports` (local prefix: `github.com/thenickygee/mirage`)
- Linters: errcheck, govet, ineffassign, staticcheck, unused, misspell, revive
- No doc comments required on exported symbols (revive `exported` rule disabled)

### Import Ordering

Three groups separated by blank lines:

```go
import (
    "context"
    "fmt"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss/v2"

    "github.com/thenickygee/mirage/internal/config"
    "github.com/thenickygee/mirage/internal/server"
)
```

### Naming Conventions

- **Packages:** short, lowercase, single word (`agent`, `server`, `ui`, `config`)
- **Files:** lowercase, underscore-separated (`command_view.go`, `dir_picker.go`)
- **Exported types/funcs:** PascalCase (`TrackedSession`, `LoadAll`)
- **Unexported types:** camelCase (`tabID`, `paneState`, `sseEvent`)
- **Constants:** PascalCase exported, camelCase unexported; use `iota` for enums

### Error Handling

- Wrap errors with context: `fmt.Errorf("loading config: %w", err)`
- Accumulate multiple errors in `[]error` slices when appropriate
- Use `_ =` for intentionally discarded errors (e.g., `_ = resp.Body.Close()`)
- Non-critical operations may silently discard errors: `skills, _ := skill.LoadAll()`
- Always close HTTP response bodies: `defer func() { _ = resp.Body.Close() }()`

### Type Patterns

- Pointer receivers for mutable state (`*Client`, `*Agent`)
- Bubble Tea models use pointer-based `App` as the root model
- `json.RawMessage` for deferred JSON parsing
- Custom enum types with `iota`
- Composition via fields, not struct embedding

### Concurrency

- `sync.Mutex` for shared state
- `context.Context` for cancellation
- Goroutines for background tasks (SSE streaming, update checks)
- Channel-based signal handling

### Highlight Styling

- When a row is selected/highlighted with a background color, any bare spaces between styled inline elements (e.g., between a dot/circle indicator and text) will show as unstyled gaps.
- Use `selSp(n)` (or equivalent styled-space helper) for gaps between inline styled elements on highlighted rows, not plain `" "` string literals.
- The outer `highlightStyle.Width(w).Render(...)` wrapper handles right-side padding, but does not fix gaps between ANSI-styled spans within the content.

### File Headers

Swift files should have: `// Created by Nick Gmitter on {date}`
Go files in this repo: only add header to new server/infrastructure files if appropriate.

## Architecture Notes

- **Bubble Tea (Elm) pattern:** single `App` model with `Init()`, `Update(msg)`, `View()`
- Sub-components have their own `Update`/`View` methods composed into the main app
- The app connects to one or more OpenCode servers via SSE
- mDNS auto-discovery for local servers
- Permission prompts forwarded from server to TUI for user approval

## Style Rules

- Use `bg-linear-to-br` instead of `bg-gradient-to-br` (Tailwind v4 syntax, if applicable)
- If code is self-documenting, do not add redundant comments
- Keep functions focused and short
- Prefer returning early on errors over deep nesting

## Dependencies

Key dependencies (see go.mod for full list):
- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/charmbracelet/lipgloss/v2` — Terminal styling
- `github.com/charmbracelet/glamour` — Markdown rendering
- `github.com/hashicorp/mdns` — Service discovery
- `github.com/r3labs/sse/v2` — SSE client

## Release

- GoReleaser builds cross-platform binaries (darwin/linux/windows, amd64/arm64)
- CGO_ENABLED=0
- Triggered by tag push via GitHub Actions
