<img width="986" height="197" alt="mirage-wordstamp" src="https://github.com/user-attachments/assets/134c9746-ee95-49e2-937e-ff5632186b60" />

# Mirage

A terminal UI for managing and browsing your [opencode](https://opencode.ai) configuration — agents, skills, commands, tools, and sessions — from a keyboard-driven interface.


## Install

### Prerequisites

- [GitHub CLI](https://cli.github.com) (`gh`) installed and authenticated

### Setup

1. Clone the repo:

```sh
gh repo clone thenickygee/mirage && cd mirage
```

2. Install the binary:

```sh
make install
```

This downloads the latest release binary to `~/.local/bin/mirage`.

3. Add `~/.local/bin` to your PATH (if not already):

```sh
# Add to your ~/.zshrc or ~/.bashrc
export PATH="$HOME/.local/bin:$PATH"
```

4. Verify it works:

```sh
mirage version
```

### Updating

Run:

```sh
mirage update
```

The app also checks for updates on launch and notifies you if a new version is available.

## Usage

```sh
mirage
```

The app reads configuration from the standard opencode directories:

| Data | Path |
|---|---|
| Agents | `~/.config/opencode/agents/*.md` |
| Skills | `~/.config/opencode/skills/<name>/SKILL.md` |
| Commands | `~/.config/opencode/commands/*.md` |
| Tools | `~/.config/opencode/tools/*.{ts,js,py,sh}` |
| Sessions | `~/.local/share/opencode/storage/session/` |

## Features

- **Agents** — View custom and built-in agents with usage stats (token counts, cost, run history), enable/disable, and create or edit agent files
- **Skills** — Browse skills with descriptions, metadata, and full markdown body
- **Commands** — Inspect slash-commands with their template bodies and associated agents
- **Tools** — View custom tool scripts (TypeScript, JavaScript, Python, Shell) with descriptions and source
- **Sessions** — Browse chat sessions grouped by date, open any session directly in `opencode`
- Fuzzy finder across all entity types (`/`)
- Which-key leader menu (`space`)
- Markdown rendering in detail panes via glamour

## Server Mode

Mirage can connect to one or more running OpenCode instances to show live agent activity and handle permission requests in real time.

### Auto-discovery with mDNS (recommended)

Launch each OpenCode session with the `--mdns` flag:

```sh
opencode --mdns
```

Then launch Mirage — it auto-discovers running OpenCode instances by default:

```sh
mirage
```

No port configuration required. Mirage re-scans every 5 seconds, so new sessions are picked up automatically.

> **Note:** mDNS requires OpenCode to bind to a non-loopback address. The `--mdns` flag automatically sets hostname to `0.0.0.0`.

### Manual connection

If you prefer to connect to a specific instance, pin it to a known port with `--port` and connect Mirage with `--server`:

```sh
# Terminal 1 — start OpenCode on a fixed port
opencode --port 4096

# Terminal 2 — connect Mirage
mirage --no-mdns --server http://localhost:4096
```

You can connect to multiple instances simultaneously by repeating the flag:

```sh
mirage --no-mdns --server http://localhost:4096 --server http://localhost:4097
```

### What you'll see

The **Overview** tab (`o`) is the live monitoring view. Instances are grouped by project directory, with multi-instance projects appearing first.

#### Instance list (left pane)

Each instance entry shows:

- **Status dot** — color and animation indicate state:
  - `●` green = running/busy
  - `●` pulsing yellow = waiting for user input or pending permissions
  - `○` dim = connected but idle
  - `○` gray = disconnected (removed automatically after 5 seconds)
- **Project name** — derived from the working directory
- **Agent + state** — e.g. `◆ @explore (running)` with an animated spinner when busy
- **Session title** — the active session title, truncated to fit
- **`⚠ N pending`** — badge shown when there are unresolved permission requests
- **Stats line** (configurable) — model name, message count, and cumulative `in`/`out` token counts
- **CTX bar** — a mini fill bar showing context window usage for the most recent turn

Example pending questions status:


#### Instance detail (right pane)


Selecting an instance opens the detail pane:

| Field | Description |
|---|---|
| Project | Working directory name |
| Agent | Active agent name |
| Model | LLM model ID |
| Tool | Current tool being executed (shown while active) |
| Msgs | Total message count for the session |
| In / Out | Cumulative input and output token counts |
| CTX | Visual fill bar: `(last input tokens + cache read) / context window` |

Below the header, all session messages are rendered in a scrollable viewport — user turns with an amber left-border quote style, assistant turns as markdown, and tool calls/results as inline lines.

Press `i` to enter insert mode and send a message directly to the selected instance without switching to the terminal running OpenCode.

#### Status bar

- **`[CONN]` / `[DISC]`** — connection state; shows `(N)` count when more than one instance is connected

### Important: `opencode serve` vs `opencode`

`opencode serve` starts a **headless** standalone server with no interactive session. Active agents running in a separate `opencode` TUI will **not** be visible through a `serve` instance. Use `opencode --mdns` or `opencode --port` instead.

## Keybindings

### Global

| Key | Action |
|---|---|
| `q` / `ctrl+c` | Quit |
| `space` | Leader menu |
| `/` | Fuzzy finder |
| `o` | Overview tab |
| `a` | Agents tab |
| `s` | Skills tab |
| `c` | Commands tab |
| `t` | Tools tab |
| `x` | Sessions tab |
| `←` / `→` | Previous / next tab |

### Navigation

| Key | Action |
|---|---|
| `↑` / `k` | Up |
| `↓` / `j` | Down |
| `ctrl+d` / `ctrl+u` | Half page down / up |
| `G` | Go to bottom |
| `g g` | Go to top |
| `l` / `enter` | Focus detail pane |
| `h` / `esc` | Focus list pane / back |

### Agents

| Key | Action |
|---|---|
| `n` | New agent |
| `e` | Edit agent |
| `d` | Toggle enable/disable |
| `p` | View system prompt |

### Sessions

| Key | Action |
|---|---|
| `o` | Open session in `opencode` |

### Leader Menu (`space`)

The leader menu is a which-key style overlay activated by pressing `space`. It shows context-sensitive shortcuts depending on the active tab.


#### Navigation

| Key | Action |
|---|---|
| `o` | Overview tab |
| `x` | Sessions tab |
| `a` | Agents tab |
| `s` | Skills tab |
| `c` | Commands tab |
| `t` | Tools tab |
| `d` | Dirs tab |

#### Actions

| Key | Action |
|---|---|
| `n` | New session |
| `f` | Find (fuzzy finder) |
| `O` | Options / settings |
| `q` | Quit |

#### Agent Context (when on Agents tab)

| Key | Action |
|---|---|
| `n` | New agent |
| `e` | Edit agent |
| `d` | Disable / enable agent |

#### Overview Context (when on Overview tab)

| Key | Action |
|---|---|
| `k` | Kill instance |
| `l` | Toggle list-only view |
| `I` | Instance settings |
| `i` | Send message to selected instance |

##### Instance Settings (`I`)

Opens a pick-list to toggle which fields are shown on each instance in the overview list. Options:

| Key | Field | Default |
|---|---|---|
| `c` | ctx (context window bar) | on |
| `m` | model | on |
| `n` | msgs (message count) | on |
| `i` | in/out (token counts) | on |

Toggling is immediate and persisted to `agent-view.yaml`.

## Releasing

To publish a new release with pre-built binaries, tag the latest commit with an incremented version number and push:

```sh
git tag v0.0.X   # increment from the latest existing tag
git push --tags
```

This triggers a GitHub Actions workflow that builds and publishes binaries to the [Releases page](https://github.com/thenickygee/mirage/releases) automatically.

## Development

```sh
make fmt    # format code with goimports
make lint   # run golangci-lint
make build  # compile binary
```

## Agent File Format

Agents are stored as markdown files with YAML frontmatter at `~/.config/opencode/agents/<name>.md`:

```markdown
---
description: "What this agent does"
mode: subagent        # or: primary
model: claude-sonnet-4-5
temperature: 0.7
color: "#ff5f00"
disable: false
permission:
  read: allow
  write: deny
---

Your system prompt goes here...
```
