package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/thenickygee/mirage/internal/agent"
	"github.com/thenickygee/mirage/internal/command"
	"github.com/thenickygee/mirage/internal/server"
	"github.com/thenickygee/mirage/internal/server/claude"
	"github.com/thenickygee/mirage/internal/server/opencode"
	"github.com/thenickygee/mirage/internal/skill"
	"github.com/thenickygee/mirage/internal/tool"
	"github.com/thenickygee/mirage/internal/ui"
	"github.com/thenickygee/mirage/internal/update"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

type multiFlag []string

func (m *multiFlag) String() string     { return fmt.Sprintf("%v", *m) }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func printHelp() {
	fmt.Printf(`mirage %s — TUI monitor for OpenCode server instances

Usage:
  mirage [flags]
  mirage <command>

Commands:
  version          Print the current version
  update, upgrade  Update mirage to the latest version
  help             Show this help message

Flags:
  -server <url>    OpenCode server URL (may be repeated)
  -no-mdns         Disable mDNS auto-discovery of OpenCode instances
  -claude          Force-enable the Claude Code source (otherwise auto-enabled
                   when claude is on $PATH)
  -claude-bare     Run claude with --bare: API-key auth only, and skips hooks,
                   plugins, CLAUDE.md and auto-memory. Needs -claude-key or
                   $ANTHROPIC_API_KEY. Without it, claude uses its own login
                   (subscription or API key) and your normal configuration.
  -claude-key <key>    ANTHROPIC_API_KEY to pass to claude (with -claude-bare,
                       falls back to $ANTHROPIC_API_KEY)
  -claude-model <id>   Default model (else claude's default)
  -claude-mode <mode>  Permission mode: acceptEdits (default) | auto | dontAsk |
                       plan | default
  -claude-tools <list> Comma-separated --allowedTools that run without asking
                       (default "Read,Glob,Grep"); other tools prompt for
                       approval in the Approvals view
  -claude-prompt <text>  Initial prompt to launch a Claude session at startup

Examples:
  mirage
  mirage -server http://localhost:4096
  mirage -no-mdns -server http://localhost:4096
  mirage -claude -claude-prompt "summarize this folder"
  mirage -claude-bare -claude-key $ANTHROPIC_API_KEY
`, version)
}

func main() {
	// Handle subcommands before flag parsing.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "update", "upgrade":
			update.Run(version)
			return
		case "version", "-v", "-version", "--version":
			fmt.Printf("mirage %s\n", version)
			return
		case "help", "-h", "--help":
			printHelp()
			return
		}
	}

	var serverURLs multiFlag
	flag.Var(&serverURLs, "server", "opencode server URL (may be repeated, e.g. --server http://localhost:4096)")
	noMDNS := flag.Bool("no-mdns", false, "disable mDNS auto-discovery of opencode instances")
	claudeEnabled := flag.Bool("claude", false, "enable the Claude Code source")
	claudeBare := flag.Bool("claude-bare", false, "run claude with --bare (API-key auth, no hooks/plugins/CLAUDE.md)")
	claudeKey := flag.String("claude-key", "", "ANTHROPIC_API_KEY to pass to claude (with -claude-bare, else read from $ANTHROPIC_API_KEY)")
	claudeModel := flag.String("claude-model", "", "default model for claude sessions (else claude's default)")
	claudeMode := flag.String("claude-mode", "acceptEdits", "permission mode for claude sessions: acceptEdits | auto | dontAsk | plan | default")
	claudeTools := flag.String("claude-tools", "Read,Glob,Grep", "comma-separated --allowedTools that run without approval; other tools prompt in the Approvals view")
	claudePrompt := flag.String("claude-prompt", "", "initial prompt to launch a Claude session at startup")
	flag.Usage = printHelp
	flag.Parse()

	app, err := ui.NewApp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading agents: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	pool := server.NewPool(func() {
		p.Send(ui.PermissionsChangedMsg{})
	})
	pool.SetSourceFactory(opencode.NewSource)

	// Add any explicitly specified servers.
	for _, u := range serverURLs {
		if err := pool.Add(u); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not connect to %s: %v\n", u, err)
		}
	}

	// Register the Claude Code source. It launches sessions as `claude -p`
	// subprocesses, parses the stream-json output, and answers tool permission
	// prompts over stdin. See plans/claude-code-integration.md.
	//
	// Auto-enable whenever the `claude` CLI is on $PATH so mirage works with
	// Claude and OpenCode simultaneously out of the box — the source stays
	// out of the overview until it owns a session, so OpenCode-only users
	// see no clutter. The -claude flag forces enable even when `claude`
	// isn't on $PATH (to surface the missing-binary error explicitly).
	// Auth is claude's own login unless -claude-bare is set, in which case a
	// missing key fails each launch with a clear error.
	_, claudeLookErr := exec.LookPath("claude")
	claudeOnPath := claudeLookErr == nil
	if *claudeEnabled || claudeOnPath {
		src := claude.NewSource(claude.Config{
			Bare:           *claudeBare,
			APIKey:         *claudeKey,
			Model:          *claudeModel,
			PermissionMode: *claudeMode,
			AllowedTools:   splitCSV(*claudeTools),
			InitialPrompt:  *claudePrompt,
		}, pool.NotifyChange)
		if err := pool.AddSource(src); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not enable Claude source: %v\n", err)
		}
	}

	// Start mDNS discovery by default.
	var cancel context.CancelFunc
	if !*noMDNS {
		ctx, c := context.WithCancel(context.Background())
		cancel = c
		go opencode.Discover(ctx, pool)
	}

	// Watch agent config files for changes and hot-reload the agents list.
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	go func() {
		_ = agent.Watch(watchCtx, func() {
			p.Send(ui.AgentChangedMsg{})
		})
	}()
	go func() {
		_ = skill.Watch(watchCtx, func() {
			p.Send(ui.SkillChangedMsg{})
		})
	}()
	go func() {
		_ = command.Watch(watchCtx, func() {
			p.Send(ui.CommandChangedMsg{})
		})
	}()
	go func() {
		_ = tool.Watch(watchCtx, func() {
			p.Send(ui.ToolChangedMsg{})
		})
	}()

	// Check for updates in the background.
	go func() {
		if latest, available := update.CheckForUpdate(version); available {
			p.Send(ui.UpdateAvailableMsg{Version: latest})
		}
	}()

	// Always give the app the pool so it can show connection status.
	app.SetPool(pool)

	// Handle OS signals for clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		p.Quit()
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}

	if cancel != nil {
		cancel()
	}
	pool.Close()
}

// splitCSV splits a comma-separated string into trimmed fields, dropping
// empty entries. Used to parse -claude-tools.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
