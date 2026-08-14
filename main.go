package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/thenickygee/mirage/internal/agent"
	"github.com/thenickygee/mirage/internal/command"
	"github.com/thenickygee/mirage/internal/server"
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

Examples:
  mirage
  mirage -server http://localhost:4096
  mirage -no-mdns -server http://localhost:4096
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

	// Add any explicitly specified servers.
	for _, u := range serverURLs {
		if err := pool.Add(u); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not connect to %s: %v\n", u, err)
		}
	}

	// Start mDNS discovery by default.
	var cancel context.CancelFunc
	if !*noMDNS {
		ctx, c := context.WithCancel(context.Background())
		cancel = c
		go server.Discover(ctx, pool)
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
