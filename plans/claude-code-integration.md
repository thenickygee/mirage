> **Status update:** this plan predates the shipped design. Permissions are implemented without an MCP server: each turn runs `claude -p --input-format stream-json --output-format stream-json --permission-prompt-tool stdio`, the prompt is sent on stdin, `can_use_tool` `control_request` lines become pending `Permission`s, and `Respond` writes a `control_response` (`allow` / `allow` + `updatedPermissions` / `deny`) back on stdin. `--bare` is opt-in (`-claude-bare`) instead of the default, so subscription logins, hooks and CLAUDE.md work out of the box. See `internal/server/claude/{launch,permissions}.go`.

# Plan: Claude Code Integration (Option A — CLI Bridge)

## Goal

Let Mirage monitor and drive **Claude Code** sessions alongside OpenCode sessions, with no new runtime dependencies (pure Go). Claude Code has no HTTP/SSE server, so the bridge launches `claude -p` as a subprocess per session and parses its `--output-format stream-json` NDJSON stream into Mirage's existing session model.

## Constraints & non-goals

- **No Node/Python runtime** — pure Go, subprocess only.
- **No mid-run permission approval** — Claude Code's `-p` mode resolves permissions at launch via `--allowedTools` / `--permission-mode`. The in-TUI live approval flow cannot be a 1:1 port for Claude sessions. (See Phase 4 for the pragmatic workaround.)
- **No mDNS discovery** — `claude` is a CLI you invoke, not a service you discover. Sessions are launched from Mirage (or attached by PID/session-ID if we later add scanning).
- **OpenCode path is untouched** — the existing HTTP `Client` and mDNS discovery keep working. The Claude source is additive.

## Claude Code stream-json protocol (what we parse)

`claude -p "<prompt>" --output-format stream-json --verbose --include-partial-messages --bare` emits one JSON object per line. Each line has a top-level `type`. Event types relevant to Mirage:

| `type`            | Contains                                                                 | Mirage use                                           |
| ----------------- | ------------------------------------------------------------------------ | --------------------------------------------------- |
| `system/init`     | `model`, `tools`, `mcp_servers`, `plugins`, `session_id`, `capabilities`  | Seed `ModelID`, `ContextWindow` (from a static map), `session_id` |
| `assistant`       | `message.content[]` blocks: `text`, `tool_use` (id, name, input), `thinking` | Append OutputLines; set `ToolName`/`ToolState="running"`; bump `Busy=true` |
| `user`            | `message.content[]` blocks: `tool_result` (tool_use_id, content, is_error) | Mark matching tool call `ToolState="completed"`; append "✓ done" line |
| `stream_event`    | `.event.type` (`message_start`, `content_block_delta`, …), `.event.delta.type` (`text_delta`, `input_json_delta`) | Live text streaming (optional — Phase 3)            |
| `system/api_retry`| `attempt`, `error`, `error_status`                                       | Surface in status row                                |
| `result`          | `subtype` (`success`/`error`/`error_max_turns`), `result` (text), `session_id`, `total_cost_usd`, `usage` (input/output/cache tokens), `duration_ms` | Set `Busy=false`, finalize `Cost`, `InputTokens`, `OutputTokens`, `CacheRead`, `MessageCount` |

The final `result` line carries `total_cost_usd` and a per-model cost breakdown — this is the authoritative source for token/cost totals (OpenCode gets these per-message; Claude Code gives one summary at the end).

Sessions: each `claude -p` run returns a `session_id` in `system/init` and `result`. To send a follow-up, spawn another `claude -p "<text>" --resume <session_id> --output-format stream-json …`.

## Architecture

### Current shape (OpenCode only)

```
main.go
  └─ server.NewPool(onEvent)            pool.go
       └─ server.Discover(ctx, pool)    discovery.go   (mDNS -> pool.Add(url))
       └─ pool.Add(url) -> *Client      server.go      (HTTP + SSE)
            ├─ Client.Connect()         /global/health, /event SSE
            ├─ Client.handleEvent()     session/message/permission/question events
            └─ Client.TrackedSessions() -> []*TrackedSession
```

`Pool` holds `map[string]*Client` and reaches into `c.mu`, `c.pending`, `c.sessions`.

### Target shape (OpenCode + Claude)

```
server/
  pool.go              (refactored: holds map[string]Source)
  source.go            (NEW: Source interface)
  opencode/            (NEW subpackage — current HTTP client moved here)
    client.go          (was server.go, unchanged behavior)
    discovery.go       (was discovery.go, unchanged)
  claude/              (NEW subpackage)
    source.go          (implements Source; owns claude subprocess lifecycle)
    events.go          (stream-json event type definitions)
    parse.go           (NDJSON line -> Source state mutations)
    launch.go          (builds the `claude` argv, env, spawns the process)
    models.go          (static modelID -> context-window map)
```

### `Source` interface (the seam)

Extracted from the methods Pool currently calls on `*Client`. Internal-field access becomes methods:

```go
type Source interface {
    ID() string                         // baseURL for opencode, "claude:<pid>" for claude
    Kind() string                       // "opencode" | "claude"
    Connected() bool
    DisconnectedAt() *time.Time
    IsNewInstance() bool
    Connect() error
    Close()

    // Sessions
    TrackedSessions() []*TrackedSession
    ActiveAgents() map[string]bool
    ActiveSessions() map[string]string
    HasActiveAgents() bool
    OwnsSession(sessionID string) bool

    // Output
    FetchSessionOutput(sessionID string)
    FetchSessionOutputLines(sessionID string) ([]OutputLine, bool)
    SetSessionOutputLines(sessionID string, lines []OutputLine)

    // Permissions (no-op for Claude in Phase 1)
    Pending() []*Permission
    PendingForSession(sessionID string) []*Permission
    PendingCount() int
    HasPermission(permID string) bool
    Respond(sessionID, permissionID, response string) error

    // Messaging
    SendMessage(sessionID, text string) error
}
```

`TrackedSession`, `Permission`, `OutputLine` stay in package `server` (or a new `server/model` subpackage) so both sources share them.

`Pool` becomes `map[string]Source`. All `c.mu.Lock(); c.sessions[id]; c.mu.Unlock()` patterns in pool.go become `src.OwnsSession(id)` / `src.SetSessionOutputLines(...)` / `src.HasPermission(...)`.

## Claude source design (`internal/server/claude/`)

### Lifecycle

- `claude.Source` holds one long-lived **control process** per session: `claude -p <prompt> --output-format stream-json --verbose --include-partial-messages --bare`.
- A session starts Busy and stays Busy until the `result` event arrives (process exits).
- `SendMessage(sessionID, text)` spawns a new `claude -p "<text>" --resume <sessionID> …` process and streams it the same way; the session flips back to Busy until that run's `result`.
- `Connect()` does not make a network call — it verifies `claude` is on `$PATH` (`exec.LookPath("claude")`) and marks the source connected. There is no health endpoint.
- `DisconnectedAt()` is set if the subprocess dies unexpectedly (non-zero exit before `result`).

### Launch (`launch.go`)

Build argv from a `claude.SessionConfig`:

```
claude --bare -p "<prompt>"
  --output-format stream-json
  --verbose
  --include-partial-messages
  [--resume <sessionID>]
  [--allowedTools "Read,Edit,Bash"]
  [--permission-mode acceptEdits|dontAsk]
  [--model <id>]
  --append-system-prompt ""
```

Env passthrough: `ANTHROPIC_API_KEY` (required under `--bare`), plus `CLAUDE_CODE_*` overrides. Capture `stdout` (NDJSON stream) and `stderr` (diagnostics) via `os.Pipe`. Write stdin from a `strings.NewReader` so it closes EOF and the subprocess can exit.

### Parsing (`parse.go`)

A goroutine reads `stdout` line-by-line with `bufio.Scanner` (reuse the 1 MB buffer pattern from `connectSSE`). For each line:

1. `json.Unmarshal` into `events.StreamEvent` (discriminated by `type`).
2. Dispatch to a handler that mutates `Source` state under `Source.mu` (mirrors `Client.handleEvent`).
3. After each mutation, call `s.onEvent()` (which bumps Pool's `stateVersion` and triggers a UI redraw).

### State mapping (`events.go` + `parse.go`)

| stream-json event                                  | `TrackedSession` mutation                                                                  |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| `system/init` (first)                              | `ModelID = e.Model`; `ContextWindow` from static map; `ID = e.SessionID`; `Busy = true`    |
| `assistant` content block `text`                  | append `OutputLine{Role:"assistant", Text:...}`; `Busy = true`                             |
| `assistant` content block `tool_use`               | `ToolName = block.Name`; `ToolState = "running"`; append `"▸ "+name` line; record `toolUseID -> name` |
| `user` content block `tool_result`                 | look up `toolUseID`; mark its line `"  ✓ done"`; `ToolState = "completed"`                 |
| `stream_event` with `delta.type == "text_delta"`   | append to the in-progress assistant text line (Phase 3 polish; Phase 1 can skip and rely on full `assistant` blocks) |
| `system/api_retry`                                 | set a transient status string on the session (cleared on next `assistant` block)            |
| `result` (subtype `success`)                       | `Busy = false`; `WaitingForInput = false`; `Cost = e.TotalCostUSD`; `InputTokens = e.Usage.Input`; `OutputTokens = e.Usage.Output`; `CacheRead = e.Usage.Cache.Read`; `MessageCount++`; mark disconnected |
| `result` (subtype `error*`)                        | same as success plus set an error line and `DisconnectedAt`                                |

Because the stream is the **authoritative** transcript, `FetchSessionOutputLines` for a Claude source just returns the in-memory `OutputLines` (no HTTP fetch). `outputDirty` is set whenever a new `assistant`/`tool_result` block arrives.

### Models / context window (`models.go`)

OpenCode learns context windows from `/provider`. Claude Code's `system/init` reports the model ID but not its context limit. Ship a small static map (mirrors `modelLimits`):

```go
var contextWindows = map[string]int64{
    "claude-sonnet-4-5": 200000,
    "claude-opus-4-1":   200000,
    "claude-haiku-4-5":  200000,
    // update as Anthropic ships models
}
```

Fallback: 200000 if unknown. This is good enough for the context-usage bar.

### Permissions (Phase 1: none live)

Claude sessions expose **no pending permissions**. `Pending()` / `PendingForSession()` / `PendingCount()` / `HasPermission()` return zero/empty. `Respond()` returns `nil`. The UI's permission pane simply shows nothing for Claude sources.

Phase 4 implements live approval via a `--permission-prompt-tool` MCP server (the verified mechanism — see "Phased implementation > Phase 4"). Until then, the launcher picks a non-interactive permission mode up front (see CLI flags below).

## Wiring into `main.go`

New flags:

```
-claude                  enable the Claude Code source (launches sessions from the TUI)
-claude-key <key>        ANTHROPIC_API_KEY for `claude --bare` (else read from env)
-claude-model <id>       default model (else claude's default)
-claude-mode <mode>      permission mode: acceptEdits (default) | dontAsk | default
-claude-tools <list>     comma-separated --allowedTools (default "Read,Edit,Bash,Glob,Grep")
```

When `-claude` is set, `main.go`:

1. Calls `claude.NewSource(cfg, onEvent)` and `pool.AddSource(src)` immediately (no discovery loop).
2. Skips mDNS for the Claude source (OpenCode mDNS still runs unless `-no-mdns`).
3. Sends a `ui.ClaudeSourceReadyMsg{}` so the UI can show a "New Claude session" entry point.

`Pool.AddSource(src Source)` replaces the implicit `Add(url)` path for non-HTTP sources. `Add(url string)` stays for OpenCode URLs.

## UI changes (`internal/ui/`)

Minimal in Phase 1 — Claude sessions flow through the existing `ConnectedServers()` / `TrackedSessions()` machinery and render identically. Additions:

1. **Source kind badge** — `ConnectedServerInfo` gains a `Kind string` field; the overview row shows an "OC" or "CC" tag so users can tell sources apart.
2. **New Claude session launcher** — a keybind (e.g. `N`) opens a prompt for the initial message + working directory, then calls `pool.SendClaudeMessage(text)` which routes to the Claude source's `SendMessage` with no `--resume` (fresh session).
3. **SendMessage for Claude** — the existing message-send path (whatever triggers `pool.SendMessage`) detects a Claude-owned session and spawns a new `--resume` run. No behavior change from the user's perspective.
4. **No permission pane for CC** — the permission pane auto-hides when the focused session is Claude-owned. (Phase 1: just render empty.)

## Progress tracking

- [x] **Phase 1 — Refactor + stub (no Claude yet)**: `Source` interface, `Pool` refactor, move OpenCode client into `opencode/`. ✅ build + lint clean, OpenCode path unchanged.
- [x] **Phase 2 — Claude source core**: `claude/` package, subprocess launch, NDJSON parse, `Source` impl, `-claude` flags wired. ✅ build + lint clean.
- [x] **Phase 3 — Polish & follow-ups**: `--resume` send, `stream_event` text-delta streaming, `system/api_retry` status, UI badge + "New Claude session" launcher, model context-window map.
- [ ] **Phase 4 — Permissions (live approval)**: MCP-prompt-tool server so Claude tool calls surface in Mirage's permission pane.

(Update these checkboxes as each phase lands. See "Phased implementation" below for scope.)

### Phase 1 — implementation notes (what actually shipped)

- `internal/server/source.go` (NEW): `Source` interface + shared types (`TrackedSession`, `Permission`, `OutputLine`) moved out of `server.go`; pure helpers `DisplaySessionTitle` + newly-exported `IsDefaultTitle` + `defaultSessionTitleRe` kept in `server` (used by UI and opencode).
- `internal/server/opencode/client.go` (NEW, moved from `server.go`): package `opencode`, `Client` implements `server.Source` via type aliases (`TrackedSession = server.TrackedSession`, etc.) so the existing code compiles with minimal churn. Added `ID()`, `Kind()` ("opencode"), `OwnsSession()`, `HasPermission()`, `SetSessionOutputLines()`, and a `NewSource` factory wrapper.
- `internal/server/opencode/discovery.go` (NEW, moved from `discovery.go`): package `opencode`, `Discover` takes `*server.Pool`.
- `internal/server/pool.go`: `Pool` now holds `map[string]Source`; added `AddSource(Source)` and `SetSourceFactory(func(url, onEvent) Source)`. `Add(url)` (OpenCode HTTP path) is preserved unchanged from callers' perspective — it delegates to the injected factory so `server` never imports `opencode` (avoids the import cycle). All `c.mu`/`c.pending`/`c.sessions` direct field access replaced with `Source.HasPermission` / `Source.OwnsSession` / `Source.SetSessionOutputLines`. `ConnectedServerInfo` gained `Kind string`, populated from `Source.Kind()`. `URLs()` now filters to `Kind()=="opencode"` for discovery-caller compatibility.
- `main.go`: imports `internal/server/opencode`; calls `pool.SetSourceFactory(opencode.NewSource)` before any `Add`; mDNS discovery via `opencode.Discover`.
- `internal/ui/detail.go`: removed dead `srv *server.Client` field and its branches (never assigned). UI imports unchanged (`server.*` types still live in package `server`).
- **Import cycle avoided**: `opencode` imports `server` (shared types + Source); `server` does not import `opencode` (factory injection breaks the cycle). `main.go` is the composition root.
- **No behavior change**: OpenCode connect/SSE/permissions/mDNS all run through the same `Client` code, now exposed via `Source`. `make build` + `make fmt` + `make lint` (golangci-lint, 0 issues) pass.

### Phase 2 — implementation notes (what actually shipped)

- `internal/server/claude/` (NEW subpackage): `models.go` (static `contextWindows` map + `contextWindowFor` fallback), `events.go` (`streamEvent`/`messageEnvelope`/`contentBlock`/`resultUsage` structs; `streamEventEnvelope` deferred to Phase 3 to satisfy the `unused` linter), `launch.go` (`buildArgv` + `launch` spawning `claude --bare -p … --output-format stream-json --verbose --include-partial-messages --session-id <uuid>` + `newUUID` via `crypto/rand`), `parse.go` (`handleLine` dispatch + per-type handlers mutating `*Source` state under `s.mu`, firing `onEvent` outside the lock), `source.go` (`Source` implementing `server.Source`, single source managing multiple subprocesses, ID = `"claude"`).
- **Source identity**: a single `claude.Source` (ID `"claude"`) owns all launched subprocesses, sidestepping the "ID needed before PID exists" problem. Multiple concurrent sessions = multiple subprocesses in `s.procs`; the per-source cap from the risks list is enforceable here later.
- **Session ID stability**: `--session-id <uuid>` (pre-generated with `newUUID`) is passed at launch so the UI can select the session before `system/init` arrives; `handleInit` re-keys defensively if claude ever returns a different `session_id` (no-op under current CLI).
- **Parsing**: `system/init` seeds model/context-window/cwd; `assistant` text→OutputLines, `tool_use`→`ToolName`/`ToolState="running"` + `▸ name` line + `toolUseID→name` map; `user` `tool_result`→`  ✓ done`/`  ✗ error` line + `ToolState="completed"`; `result` finalizes Busy=false + token/cost totals from the authoritative `usage`/`total_cost_usd` + error line on non-success; `stream_event` deferred to Phase 3; `system/api_retry` sets a transient `ToolState="retrying"` (UI surfacing is Phase 3). Unknown `type`/`subtype` ignored (schema-drift tolerance).
- **Output contract**: the stream IS the authoritative transcript, so parse.go writes `OutputLines` live and sets `outputDirty`; `FetchSessionOutputLines` returns the in-memory copy + dirty flag (clearing it); `SetSessionOutputLines` is a no-op (parser owns the lines, UI must not overwrite with a stale snapshot); `FetchSessionOutput` is a no-op.
- **Permissions**: no-op (Phase 4). `Pending*`/`HasPermission`/`Respond` return zero/empty/nil.
- **Messaging**: `SendMessage` returns a clear "not yet supported (Phase 3)" error — `--resume` follow-ups are deliberately deferred to Phase 3 to keep the phase boundary clean.
- **Subprocess lifecycle**: `Connect()` does `exec.LookPath("claude")` (fail fast with a helpful error if missing) + marks connected + launches `cfg.InitialPrompt` if set; per-session subprocess stdout is scanned by a 1 MiB-buffered `bufio.Scanner` goroutine and stderr is drained into a buffer; unexpected exit before `result` marks the session idle with a `✗ <err>` line and surfaces captured stderr. `Close()` kills and waits all subprocesses.
- **Pool wiring**: `pool.AddSource(src)` registers the Claude source; because `AddSource` (unlike `Add`) doesn't wrap the source's `onEvent`, added `Pool.NotifyChange()` (public `bumpVersion` + `onEvent`) so the Claude source's `onEvent` bumps the pool state version — without it the overview *list* wouldn't refresh on busy/idle transitions (the detail pane still refreshes via the busy-gated fetch path). `main.go` passes `pool.NotifyChange` as the Claude source's `onEvent`.
- **Flags**: `-claude`, `-claude-key`, `-claude-model`, `-claude-mode` (default `acceptEdits`), `-claude-tools` (default `Read,Edit,Bash,Glob,Grep`), plus `-claude-prompt <text>` (not in the original flag list; added so Phase 2 is independently smoke-testable without the Phase 3 UI launcher keybind). `main.go` parses tools via `splitCSV`.
- **API key**: resolved as `-claude-key` → `$ANTHROPIC_API_KEY`; `launch()` errors fast with a helpful message if the key is absent (required under `--bare`).
- **Build/lint**: `make build` + `make fmt` + `make lint` (golangci-lint, 0 issues) pass. No Go tests added yet (the plan's optional NDJSON-fixture test is still open). Regression: `mirage` with no `-claude` flag is unchanged (OpenCode path untouched; the Claude source is purely additive).

### Phase 3 — implementation notes (what actually shipped)

- **`--resume` follow-ups (`source.go`)**: `SendMessage(sessionID, text)` now spawns a follow-up `claude -p "<text>" --resume <sessionID>` run instead of returning the Phase 2 not-yet-supported error. It guards against unknown sessions and double-launches (a claude subprocess can't service two concurrent turns on one session), echoes the user's message into the transcript, and flips the session back to Busy until the new `result` event. `launchSession` was refactored to take an explicit `workingDir` override and to branch on `resumeID`: fresh sessions pre-register with a generated UUID + `--session-id`; resume reuses the existing ID + `--resume` (no `--session-id`), and on launch failure surfaces the error on the existing session rather than creating a phantom new one.
- **Per-session event routing (`parse.go`)**: Phase 2's handlers iterated *every busy session* and applied each line to all of them — fine for one subprocess, but `--resume` makes concurrent sessions common, so that would cross-contaminate. `handleLine` now takes the scanner's known `sessionID` and routes every mutation (`handleAssistant`/`handleUser`/`handleStreamEvent`/`handleResult`/`handleAPIRetry`) to that one `TrackedSession`; `handleInit` returns the resolved ID (re-keyed if claude ever returns a different `session_id` than we launched with) so the scanner updates its routing key. `scanStdout` threads the possibly-rekeyed `sid` through the loop and into its finalize-on-unexpected-exit path.
- **`stream_event` text-delta streaming (`events.go` + `parse.go`)**: defined `streamEventEnvelope`, `contentBlockStart`, and `streamDelta` (the `streamEventEnvelope` placeholder from Phase 2 is now real). `handleStreamEvent` handles `content_block_start` (text) → append an empty assistant line + record `streamingIdx`; `content_block_delta` (`text_delta`) → append the delta to that line; `content_block_stop`/`message_stop` → leave the index for the assistant block to reconcile. `handleAssistant` reconciles: if a streaming line exists for the session, the final `assistant` text block *replaces* it with the authoritative text (deltas could be dropped/truncated on error); otherwise it appends as before (fallback when stream-json omits deltas). `streamingIdx` is cleared on `result` and on re-key. `--include-partial-messages` (already set in `buildArgv`) is what makes claude emit the deltas.
- **`system/api_retry` status (`parse.go` + `overview.go`)**: `handleAPIRetry` now targets the specific session (sets `ToolName="api_retry"`, `ToolState="retrying"`); the overview busy row surfaces it as `↻ @agent (retrying)` instead of the spinner `(running)`, cleared on the next assistant block.
- **UI source-kind badge (`overview.go` + `styles.go`)**: `ConnectedServerInfo.Kind` (added in Phase 1) is now rendered as a small `OC`/`CC` tag in the instance header row (`sourceKindBadge` + `StyleBadgeSourceOC`/`StyleBadgeSourceCC`), placed after the project name with the ctx bar still pinned right. The name truncation width was reduced to make room.
- **"New Claude session" launcher (`claude_launcher.go` + `app.go` + `leader.go` + `pool.go`)**: new `ActionNewClaudeSession` bound to `N` in the ACTIONS section opens a `claudeLauncherModel` modal (prompt textarea + working-directory field, `ctrl+s` to launch / `esc` to close / `tab` to switch fields). Submit emits `claudeLauncherSubmitMsg`, which calls `pool.LaunchClaudeSession(prompt, dir)`. The pool exposes `ClaudeLauncher` (interface with `Launch(prompt, dir) error`) and `LaunchClaudeSession`, which routes to the first source implementing it (the Claude source's `Launch` starts a fresh session via `launchSession` with no `--resume`). Errors (e.g. no `-claude` flag) surface in the status bar.
- **Models map (`models.go`)**: unchanged from Phase 2 (already ships the static `contextWindows` map + fallback). Phase 3's optional fetch-from-Anthropic-API path is still open (risk #8).
- **Build/lint**: `make build` + `make fmt` + `make lint` (golangci-lint, 0 issues) pass. OpenCode-only regression unchanged (no `-claude` flag → no Claude source, no launcher route, badge never renders `CC`).

## Phased implementation

### Phase 1 — Refactor + stub (no Claude yet)
- Introduce `server.Source` interface and `server/model` (move `TrackedSession`, `Permission`, `OutputLine` if needed).
- Refactor `Pool` to hold `map[string]Source`; replace direct field access with interface methods.
- Move existing HTTP client into `internal/server/opencode/` implementing `Source`.
- All OpenCode behavior unchanged; `make build` + manual smoke test pass.

### Phase 2 — Claude source core
- `internal/server/claude/`: `events.go`, `parse.go`, `launch.go`, `models.go`, `source.go`.
- Implement `Source` for a single launched session: spawn `claude --bare -p ... --output-format stream-json --verbose`, parse the stream, populate `TrackedSession`, fire `onEvent`.
- Wire `-claude` flag in `main.go` and `Pool.AddSource`.
- Smoke test: launch one session from the TUI, watch it go Busy -> idle with token/cost totals.

### Phase 3 — Polish & follow-ups
- `--resume` support: `SendMessage` spawns a follow-up run; session flips Busy/idle correctly.
- `stream_event` text-delta streaming for live assistant text (instead of waiting for full `assistant` blocks).
- `system/api_retry` surfacing in the status row.
- UI source-kind badge + "New Claude session" launcher keybind.
- Static model context-window map maintained.

### Phase 4 — Permissions (live approval, `--permission-prompt-tool`)
- **Verified mechanism.** Claude Code's CLI (v2.1.199+) supports live approval in non-interactive mode via `--permission-prompt-tool`, not via stdin. The `--input-format stream-json` stdin channel accepts user messages only — it cannot return permission decisions.
- **How it works.** Mirage runs a minimal **MCP server** (stdio JSON-RPC) per Claude session that exposes one tool, e.g. `mirage_approve`. The session is launched with `--permission-prompt-tool mirage_approve --mcp-config <config> --permission-mode default`. When Claude wants a tool that needs approval, it **calls the `mirage_approve` MCP tool** (tool name + input as call params). Mirage's MCP server receives the `tools/call` request, surfaces it to the TUI as a `Permission`, and **blocks the MCP call** until the user approves/denies in the permission pane. The MCP tool's return value becomes the allow/deny decision.
- **Why this fits the constraints.** MCP over stdio is plain JSON-RPC — trivial to implement in pure Go, no Node/Python runtime. This is the SDK's `canUseTool` callback flow, routed to an MCP tool so it works from a subprocess host in any language. This is the only supported way to do interactive approval from `-p` mode.
- **Known limit.** The prompt tool cannot approve MCP tools marked `requiresUserInteraction` (Claude Code auto-denies those). Built-in tools (Bash/Write/Edit/…) work fine.
- **New files.** `internal/server/claude/mcpserver.go` (stdio MCP server implementing `initialize`, `tools/list`, `tools/call` for `mirage_approve`) and launch wiring that writes a temp `--mcp-config` and passes `--permission-prompt-tool`.
- **Scope guard.** Until Phase 4 lands, the launcher picks a non-interactive permission mode up front (`--permission-mode acceptEdits|dontAsk`) and the `Respond` method is a no-op. See "CLI flags" below.

## File-by-file change list

| File | Change |
| --- | --- |
| `internal/server/source.go` (NEW) | `Source` interface + shared `TrackedSession`/`Permission`/`OutputLine` (moved from server.go if cleaner) |
| `internal/server/pool.go` | Hold `map[string]Source`; add `AddSource`; replace `c.mu`/`c.sessions`/`c.pending` access with interface calls; `Kind` on `ConnectedServerInfo` |
| `internal/server/opencode/client.go` | Renamed from `server.go`; implements `Source`; add `ID()`, `Kind()`, `OwnsSession()`, `HasPermission()` methods |
| `internal/server/opencode/discovery.go` | Renamed from `discovery.go`; unchanged behavior, calls `pool.Add` (HTTP path) |
| `internal/server/claude/source.go` (NEW) | `Source` implementation; subprocess lifecycle; state map |
| `internal/server/claude/events.go` (NEW) | stream-json event struct definitions |
| `internal/server/claude/parse.go` (NEW) | line-to-state-mutation dispatch |
| `internal/server/claude/launch.go` (NEW) | argv builder + `exec.Cmd` spawning + stdin/stdout/stdout pipe wiring |
| `internal/server/claude/models.go` (NEW) | static `modelID -> contextWindow` map |
| `main.go` | `-claude*` flags; `claude.NewSource` + `pool.AddSource` when enabled |
| `internal/server/claude/mcpserver.go` (NEW, Phase 4) | Stdio MCP server exposing `mirage_approve`; bridges a blocking `tools/call` to a `Permission` and the user's allow/deny back to the MCP return value |
| `internal/ui/app.go` | Source-kind badge; `New Claude session` keybind; route send-message to Claude source |
| `AGENTS.md` | Note the `server.Source` interface and the `opencode`/`claude` subpackage split |

## Risks & open questions

1. **`--bare` requires `ANTHROPIC_API_KEY`** — no OAuth/subscription login in bare mode. The flag help must make this clear, and we should fail fast with a helpful error if the key is missing.
2. **Stream-json schema drift** — Claude Code is actively evolving (`system/api_retry`, `system/plugin_install`, `mcp_server_errors` are all recent additions). The parser must ignore unknown `type` values gracefully (log at debug, never error).
3. **Long-running tools** — a `claude -p` run can take minutes. The subprocess must survive Bubble Tea redraws and only be killed on `Close()` or explicit session termination. Confirm `exec.Cmd` isn't tied to the TUI's process group.
4. **Multiple concurrent Claude sessions** — each is a separate subprocess. Need a per-source cap (e.g. 10) to avoid runaway process spawning.
5. **Cost accuracy** — `total_cost_usd` is a client-side estimate and can differ from the bill. Reuse the existing "client-side estimate" wording in any cost UI.
6. **Session ID stability across `--resume`** — confirm the `session_id` returned by a resumed run matches the original (docs imply yes). If it differs, we need to remap.
7. **No live permission approval in Phase 1** — the biggest UX gap. Phase 1 ships without it (launcher uses `--permission-mode acceptEdits|dontAsk`). Phase 4 adds live approval via the `--permission-prompt-tool` MCP-server mechanism (verified against Claude Code CLI v2.1.199+ docs); the plan's earlier "stdin/named-pipe" idea was incorrect and has been corrected.
8. **Context window map maintenance** — a hardcoded map goes stale. Consider fetching from Anthropic's models API at startup if a key is present, falling back to the static map.

## Verification

- `make build` after each phase.
- `make lint` (golangci-lint with the existing linters).
- Manual smoke: `-claude -claude-key $ANTHROPIC_API_KEY`, launch "summarize this folder", watch Busy -> idle, confirm token/cost populated.
- Regression: run with no `-claude` flag and confirm OpenCode-only path is unchanged.
- (No Go tests exist yet; if desired, add a `claude` package test that feeds a recorded NDJSON fixture through `parse.go` and asserts `TrackedSession` state.)
