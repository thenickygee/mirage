package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/thenickygee/mirage/internal/agent"
	"github.com/thenickygee/mirage/internal/command"
	"github.com/thenickygee/mirage/internal/config"
	"github.com/thenickygee/mirage/internal/server"
	"github.com/thenickygee/mirage/internal/session"
	"github.com/thenickygee/mirage/internal/skill"
	"github.com/thenickygee/mirage/internal/stats"
	"github.com/thenickygee/mirage/internal/tool"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type tabID int

const (
	tabOverview tabID = iota
	tabAgents
	tabSkills
	tabCommands
	tabTools
	tabSessions
	tabDirs
	tabCount
)

var tabNames = map[tabID]string{
	tabOverview: " OVERVIEW ",
	tabAgents:   " AGENTS ",
	tabSkills:   " SKILLS ",
	tabCommands: " COMMANDS ",
	tabTools:    " TOOLS ",
	tabSessions: " SESSIONS ",
	tabDirs:     " DIRS ",
}

type paneState int

const (
	paneList paneState = iota
	paneDetail
	panePrompt
	paneForm
)

type App struct {
	tab              tabID
	pane             paneState
	list             agentList
	detail           detailPane
	promptView       viewport.Model
	promptLastKey    string
	form             formModel
	overviewTab      overviewModel
	sessionTab       sessionView
	dirsTab          dirsView
	skillTab         skillView
	cmdTab           commandView
	toolTab          toolView
	leader           leaderState
	finder           finderModel
	confirm          confirmModel
	dirPicker        dirPickerModel
	settings         settingsModel
	accentName       string
	instanceDisplay  config.InstanceDisplay
	width            int
	height           int
	statusMsg        string
	statusErr        bool
	showSplash       bool
	splashPhase      int // 0=text only, 1=robot only, 2=both (flash)
	splashFrame      int
	splashFlashing   bool
	splashFlashFrame int
	pool             *server.Pool
	activeAgents     map[string]bool
	spinFrame        int
	fetchingOutput   bool
	lastStateVersion uint64           // last pool.StateVersion() seen; gates expensive refresh work
	tabZones         [tabCount][2]int // [start, end] x-positions for each tab
}

type PermissionsChangedMsg struct{}

type UpdateAvailableMsg struct{ Version string }

type AgentChangedMsg struct{}
type SkillChangedMsg struct{}
type CommandChangedMsg struct{}
type ToolChangedMsg struct{}

type clearUpdateMsg struct{}

func (a *App) SetPool(pool *server.Pool) {
	a.pool = pool
	a.detail.pool = pool
	a.list.pool = pool
	a.overviewTab.pool = pool
}

func NewApp() (*App, error) {
	agents, agentErr := agent.LoadAll()
	skills, _ := skill.LoadAll()
	commands, _ := command.LoadAll()
	tools, _ := tool.LoadAll()
	sessions, _ := session.LoadAll()
	settings := config.LoadSettings()
	ApplyAccent(PaletteByName(settings.AccentColor))

	a := &App{
		tab:    tabOverview,
		pane:   paneList,
		list:   newAgentList(agents),
		detail: newDetailPane(),
		overviewTab: func() overviewModel {
			m := newOverviewModel()
			m.listOnly = settings.ListOnlyMode
			m.display = settings.InstanceDisplay
			return m
		}(),
		sessionTab:      newSessionView(sessions),
		dirsTab:         newDirsView(sessions),
		skillTab:        newSkillView(skills, agents),
		cmdTab:          newCommandView(commands),
		toolTab:         newToolView(tools),
		leader:          newLeaderState(),
		accentName:      settings.AccentColor,
		instanceDisplay: settings.InstanceDisplay,
		showSplash:      true,
	}
	if len(agents) > 0 {
		a.detail.setAgent(agents[0])
	}
	if agentErr != nil {
		a.statusMsg = agentErr.Error()
		a.statusErr = true
	}
	return a, nil
}

// sessionOutputMsg is returned by the async fetch command with parsed output lines.
type sessionOutputMsg struct {
	sessionID string
	lines     []server.OutputLine
	updated   bool // false if the fetch determined no update was needed
}

type sessionReloadMsg struct{}

func sessionReloadTick() tea.Cmd {
	return tea.Tick(10*time.Second, func(t time.Time) tea.Msg {
		return sessionReloadMsg{}
	})
}

type statsLoadedMsg struct {
	stats map[string]*stats.AgentStats
}

type statsTickMsg struct{}
type clearStatusMsg struct{}

func statsTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return statsTickMsg{}
	})
}

func clearStatusCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return clearStatusMsg{}
	})
}

func loadStatsCmd() tea.Cmd {
	return func() tea.Msg {
		s, _ := stats.LoadAll()
		return statsLoadedMsg{stats: s}
	}
}

func (a *App) Init() tea.Cmd {
	if a.showSplash {
		return tea.Batch(splashTick(), splashPhase1(), loadStatsCmd(), statsTickCmd(), sessionReloadTick())
	}
	return tea.Batch(loadStatsCmd(), statsTickCmd(), sessionReloadTick())
}

func (a *App) reloadAgents() {
	agents, err := agent.LoadAll()
	if err != nil {
		a.statusMsg = "reload error: " + err.Error()
		a.statusErr = true
		return
	}
	a.list.setAgents(agents)
	a.detail.setAgent(a.list.selected())
}

func (a *App) reloadSkills() {
	skills, err := skill.LoadAll()
	if err != nil {
		a.statusMsg = "reload error: " + err.Error()
		a.statusErr = true
		return
	}
	a.skillTab.skills = skills
}

func (a *App) reloadCommands() {
	commands, err := command.LoadAll()
	if err != nil {
		a.statusMsg = "reload error: " + err.Error()
		a.statusErr = true
		return
	}
	a.cmdTab.commands = commands
}

func (a *App) reloadTools() {
	tools, err := tool.LoadAll()
	if err != nil {
		a.statusMsg = "reload error: " + err.Error()
		a.statusErr = true
		return
	}
	a.toolTab.tools = tools
}

func (a *App) openForm(ag *agent.Agent, isNew bool) {
	a.form = newForm(ag, isNew)
	a.form.width = a.width
	a.form.height = a.height
	a.pane = paneForm
}

func (a *App) confirmToggle() {
	sel := a.list.selected()
	if sel == nil {
		return
	}
	action := "disable"
	if sel.Disable {
		action = "enable"
	}
	msg := "Are you sure you want to " + action + " @" + sel.ID + "?"
	title := "Confirm " + action
	a.confirm = newConfirm(title, msg, func() {
		a.toggleSelected()
	})
	a.confirm.width = a.width
	a.confirm.height = a.height
}

func (a *App) toggleSelected() {
	sel := a.list.selected()
	if sel == nil {
		return
	}
	sel.Disable = !sel.Disable
	if err := sel.Save(); err != nil {
		a.statusMsg = err.Error()
		a.statusErr = true
	} else {
		if sel.Disable {
			a.statusMsg = "@" + sel.ID + " disabled"
		} else {
			a.statusMsg = "@" + sel.ID + " enabled"
		}
		a.statusErr = false
	}
	a.reloadAgents()
}

func (a *App) contentHeight() int {
	// tabbar (1) + statusbar content (1) = 2; border is absorbed by lipgloss width constraint
	h := a.height - 2
	if h < 4 {
		h = 4
	}
	return h
}

func (a *App) layout() {
	ch := a.contentHeight()
	listW := a.width / 3
	if listW < 22 {
		listW = 22
	} else if listW > 40 {
		listW = 40
	}
	if a.width-listW < 10 {
		listW = a.width - 10
	}
	if listW < 1 {
		listW = 1
	}
	detailW := a.width - listW

	a.list.setSize(listW, ch)
	a.detail.setSize(detailW, ch)
	a.detail.refreshContent()
	a.overviewTab.setSize(a.width, ch)
	a.sessionTab.setSize(a.width, ch)
	a.skillTab.setSize(a.width, ch)
	a.cmdTab.setSize(a.width, ch)
	a.toolTab.setSize(a.width, ch)
	a.dirsTab.setSize(a.width, ch)
	a.promptView.Width = a.width - 4
	a.promptView.Height = ch - 4
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case splashTickMsg:
		a.splashFrame++
		return a, splashTick()

	case splashPhase1Msg:
		a.splashPhase = 1
		return a, splashPhase2()

	case splashPhase2Msg:
		a.showSplash = false
		return a, nil

	case splashDoneMsg:
		a.splashFlashing = true
		return a, tea.Batch(splashFlashTick(), splashFlashDone())

	case splashFlashTickMsg:
		if a.splashFlashing {
			a.splashFlashFrame++
			return a, splashFlashTick()
		}
		return a, nil

	case splashFlashDoneMsg:
		a.splashFlashing = false
		a.showSplash = false
		return a, nil

	case clearStatusMsg:
		a.statusMsg = ""
		a.statusErr = false
		return a, nil

	case statsLoadedMsg:
		a.detail.stats = msg.stats
		a.detail.refreshContent()
		return a, nil

	case statsTickMsg:
		needsTick := false
		if a.detail.stats == nil {
			a.detail.statsSpinFrame++
			a.detail.refreshContent()
			needsTick = true
		}
		var fetchCmd tea.Cmd
		if a.pool != nil {
			currentVersion := a.pool.StateVersion()
			stateChanged := currentVersion != a.lastStateVersion
			if stateChanged {
				a.lastStateVersion = currentVersion
			}

			if a.pool.HasActiveAgents() {
				if stateChanged {
					activeSessions := a.pool.ActiveSessions()
					a.activeAgents = a.pool.ActiveAgents()
					a.dirsTab.activeSessions = activeSessions
					a.dirsTab.rebuild()
				}
				a.spinFrame++
			} else if stateChanged {
				a.activeAgents = nil
				a.dirsTab.activeSessions = nil
				a.dirsTab.rebuild()
			}
			// Fetch live output for the selected overview session (async)
			if a.tab == tabOverview && !a.overviewTab.listOnly {
				if sel := a.overviewTab.selected(); sel != nil {
					needsFetch := sel.Busy || a.overviewTab.lastFetchedID != sel.ID
					if needsFetch && !a.fetchingOutput {
						a.fetchingOutput = true
						a.overviewTab.lastFetchedID = sel.ID
						pool := a.pool
						sid := sel.ID
						fetchCmd = func() tea.Msg {
							lines, updated := pool.FetchSessionOutputLines(sid)
							return sessionOutputMsg{sessionID: sid, lines: lines, updated: updated}
						}
					}
				}
			}
			// Auto-remove instances that have been disconnected for ≥ 5 seconds.
			// This must run every tick, not just on stateChanged, because the pool
			// state version doesn't increment while waiting for the timeout to expire.
			for _, srv := range a.pool.ConnectedServers() {
				if !srv.Connected && srv.DisconnectedAt != nil && time.Since(*srv.DisconnectedAt) >= 5*time.Second {
					a.pool.Remove(srv.URL)
					stateChanged = true
				}
			}
			if stateChanged {
				// Refresh overview and dirs when state actually changed.
				a.overviewTab.refresh(a.pool.ConnectedServers())
			}
			// Keep tick alive while pool is connected so state changes are caught promptly.
			needsTick = true
		}
		if needsTick {
			if fetchCmd != nil {
				return a, tea.Batch(statsTickCmd(), fetchCmd)
			}
			return a, statsTickCmd()
		}
		return a, nil

	case sessionOutputMsg:
		a.fetchingOutput = false
		if msg.updated && a.pool != nil {
			a.pool.SetSessionOutputLines(msg.sessionID, msg.lines)
			a.overviewTab.refresh(a.pool.ConnectedServers())
		}
		return a, nil

	case sessionReloadMsg:
		if sessions, err := session.LoadAll(); err == nil {
			a.sessionTab.setSessions(sessions)
			a.sessionTab.setSize(a.width, a.contentHeight())
			a.dirsTab.allSessions = sessions
			a.dirsTab.rebuild()
			var connectedServers []server.ConnectedServerInfo
			if a.pool != nil {
				connectedServers = a.pool.ConnectedServers()
			}
			a.overviewTab.refresh(connectedServers)
		}
		return a, sessionReloadTick()

	case AgentChangedMsg:
		a.reloadAgents()
		return a, nil

	case SkillChangedMsg:
		a.reloadSkills()
		return a, nil

	case CommandChangedMsg:
		a.reloadCommands()
		return a, nil

	case ToolChangedMsg:
		a.reloadTools()
		return a, nil

	case PermissionsChangedMsg:
		a.list.cachedRows = nil // invalidate: approval row may have changed
		a.detail.refreshContent()
		if a.pool != nil && a.pool.HasActiveAgents() {
			activeSessions := a.pool.ActiveSessions()
			a.activeAgents = a.pool.ActiveAgents()
			a.dirsTab.activeSessions = activeSessions
			a.overviewTab.refresh(a.pool.ConnectedServers())
		} else {
			a.activeAgents = nil
			a.dirsTab.activeSessions = nil
			connectedServers := []server.ConnectedServerInfo(nil)
			if a.pool != nil {
				connectedServers = a.pool.ConnectedServers()
			}
			a.overviewTab.refresh(connectedServers)
		}
		a.dirsTab.rebuild()
		return a, statsTickCmd()

	case UpdateAvailableMsg:
		a.statusMsg = fmt.Sprintf("Update available: %s — run 'mirage update'", msg.Version)
		a.statusErr = false
		return a, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearUpdateMsg{} })

	case clearUpdateMsg:
		if strings.HasPrefix(a.statusMsg, "Update available:") {
			a.statusMsg = ""
		}
		return a, nil

	case dirPickerSelectMsg:
		return a, NewSessionInDirCmd(msg.dir)

	case newSessionServeMsg:
		if msg.err != nil {
			a.statusMsg = "opencode error: " + msg.err.Error()
			a.statusErr = true
			return a, nil
		}
		a.statusMsg = ""
		a.statusErr = false
		// Add the new server to the pool if we got a URL.
		if msg.url != "" {
			_ = a.pool.Add(msg.url)
			a.overviewTab.selectByURL(msg.url)
		}
		// Refresh overview so the new instance appears.
		a.overviewTab.refresh(a.pool.ConnectedServers())
		a.tab = tabOverview
		// Attach to it so the user can interact with it.
		if msg.url != "" {
			// Create a lightweight tracked session to attach to.
			c := exec.Command("opencode", "attach", msg.url)
			return a, tea.ExecProcess(c, func(err error) tea.Msg {
				return sessionOpenDoneMsg{err: err}
			})
		}
		return a, nil

	case sessionOpenDoneMsg:
		if msg.err != nil {
			a.statusMsg = "opencode error: " + msg.err.Error()
			a.statusErr = true
		} else {
			a.statusMsg = ""
			a.statusErr = false
		}
		// Immediately reload sessions so newly created sessions appear
		if sessions, err := session.LoadAll(); err == nil {
			a.sessionTab.setSessions(sessions)
			a.sessionTab.setSize(a.width, a.contentHeight())
			a.dirsTab.allSessions = sessions
			a.dirsTab.rebuild()
		}
		// Switch to overview and auto-select the session that was just launched
		a.tab = tabOverview
		a.overviewTab.selectByURL(a.pool.LastAddedURL())
		return a, nil

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.layout()
		return a, nil

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			// Row 0 is the tab bar
			if msg.Y == 0 {
				for i := tabID(0); i < tabCount; i++ {
					if msg.X >= a.tabZones[i][0] && msg.X < a.tabZones[i][1] {
						a.tab = i
						a.pane = paneList
						return a, nil
					}
				}
			}
			// Left-pane list click (content starts at row 1)
			if msg.Y >= 1 && a.pane != panePrompt && a.pane != paneForm {
				a.handleListClick(msg.X, msg.Y)
			}
		}

		// Forward mouse events to sub-models for scroll support.
		if a.pane == panePrompt {
			var cmd tea.Cmd
			a.promptView, cmd = a.promptView.Update(msg)
			return a, cmd
		}

		var cmd tea.Cmd
		switch a.tab {
		case tabOverview:
			a.overviewTab, cmd = a.overviewTab.Update(msg)
		case tabSessions:
			a.sessionTab, cmd = a.sessionTab.Update(msg)
		case tabAgents:
			cmd = a.updateAgentsTab(msg)
		case tabSkills:
			a.skillTab, cmd = a.skillTab.Update(msg)
		case tabCommands:
			a.cmdTab, cmd = a.cmdTab.Update(msg)
		case tabTools:
			a.toolTab, cmd = a.toolTab.Update(msg)
		case tabDirs:
			a.dirsTab, cmd = a.dirsTab.Update(msg)
		}
		return a, cmd

	case tea.KeyMsg:
		// Splash screen — enter or space to continue
		if a.showSplash {
			if msg.String() == "enter" || msg.String() == " " {
				a.splashFlashing = false
				a.showSplash = false
			}
			return a, nil
		}

		// Leader menu intercepts all keys when active
		if a.leader.active {
			if key.Matches(msg, Keys.Esc) {
				a.leader.Dismiss()
				return a, nil
			}
			action, done := a.leader.Press(msg.String())
			if done {
				a.layout()
				if action != ActionNone {
					return a, a.execLeaderAction(action)
				}
			}
			return a, nil
		}

		// Confirm dialog intercepts all keys when active
		if a.confirm.active {
			switch msg.String() {
			case "y", "Y":
				a.confirm.confirm()
			case "n", "N", "esc":
				a.confirm.cancel()
			}
			return a, nil
		}

		// Settings dialog intercepts all keys when active
		if a.settings.active {
			switch msg.String() {
			case "k", "up":
				a.settings.moveUp()
			case "j", "down":
				a.settings.moveDown()
			case "enter":
				a.settings.selectCurrent()
			case "esc":
				a.settings.dismiss()
			}
			return a, nil
		}

		// Dir picker overlay intercepts all keys when active
		if a.dirPicker.active {
			var cmd tea.Cmd
			a.dirPicker, cmd = a.dirPicker.Update(msg)
			return a, cmd
		}

		// Finder overlay intercepts all keys when active
		if a.finder.active {
			if key.Matches(msg, Keys.Esc) {
				a.finder.active = false
				return a, nil
			}
			if msg.String() == "enter" {
				if sel := a.finder.selected(); sel != nil {
					a.finder.active = false
					a.tab = sel.tab
					a.pane = paneList
					switch sel.kind {
					case finderAgent:
						a.list.cursor = sel.index
						a.detail.setAgent(a.list.selected())
					case finderSkill:
						a.skillTab.cursor = sel.index
					case finderCommand:
						a.cmdTab.cursor = sel.index
					case finderTool:
						a.toolTab.cursor = sel.index
					}
				}
				return a, nil
			}
			var cmd tea.Cmd
			a.finder, cmd = a.finder.Update(msg)
			return a, cmd
		}

		// Global quit
		if key.Matches(msg, Keys.Quit) && a.pane != paneForm && !a.overviewTab.insertMode {
			return a, tea.Quit
		}

		// Leader activation — space, only when not in form/textinput
		if key.Matches(msg, Keys.Leader) && a.pane != paneForm && !a.overviewTab.insertMode {
			menu := rootMenu
			switch a.tab {
			case tabAgents:
				menu = agentMenu
			case tabOverview:
				menu = overviewMenu
			}
			a.leader.Activate(menu)
			return a, nil
		}

		// Direct find shortcut
		if key.Matches(msg, Keys.Find) && a.pane != paneForm && !a.overviewTab.insertMode && a.tab != tabSessions && a.tab != tabDirs {
			a.finder = newFinder(a.list.agents, a.skillTab.skills, a.cmdTab.commands, a.toolTab.tools)
			a.finder.width = a.width
			a.finder.height = a.height
			return a, nil
		}

		// Arrow keys switch tabs — only when not in form/prompt/insert
		if a.pane != paneForm && a.pane != panePrompt && !a.overviewTab.insertMode {
			if key.Matches(msg, Keys.NextTab) {
				a.tab = (a.tab + 1) % tabCount
				a.pane = paneList
				a.statusMsg = ""
				return a, nil
			}
			if key.Matches(msg, Keys.PrevTab) {
				a.tab = (a.tab + tabCount - 1) % tabCount
				a.pane = paneList
				a.statusMsg = ""
				return a, nil
			}
		}

		// l/h/enter/esc move focus between left and right pane within the same tab
		if a.pane != paneForm && a.pane != panePrompt && !a.overviewTab.insertMode {
			if key.Matches(msg, Keys.FocusPaneRight) && a.pane == paneList {
				if a.tab == tabAgents {
					a.pane = paneDetail
					a.detail.focused = true
					return a, nil
				}
				// skill/command/tool tabs handle their own focus via Update
			}
			if key.Matches(msg, Keys.FocusPaneLeft) && a.pane == paneDetail {
				if a.tab == tabAgents {
					a.pane = paneList
					a.detail.focused = false
					return a, nil
				}
			}
		}

		switch a.tab {
		case tabOverview:
			if !a.overviewTab.insertMode && key.Matches(msg, Keys.OpenSession) {
				if sel := a.overviewTab.selected(); sel != nil {
					serverURL := ""
					if srv := a.overviewTab.selectedServer(); srv != nil {
						serverURL = srv.URL
					}
					return a, OpenOverviewSessionCmd(sel, serverURL)
				}
			} else {
				var cmd tea.Cmd
				a.overviewTab, cmd = a.overviewTab.Update(msg)
				cmds = append(cmds, cmd)
			}

		case tabSessions:
			if key.Matches(msg, Keys.OpenSession) {
				if sel := a.sessionTab.selected(); sel != nil {
					return a, OpenSessionCmd(sel)
				}
			} else {
				var cmd tea.Cmd
				a.sessionTab, cmd = a.sessionTab.Update(msg)
				cmds = append(cmds, cmd)
			}

		case tabAgents:
			cmd := a.updateAgentsTab(msg)
			cmds = append(cmds, cmd)

		case tabSkills:
			var cmd tea.Cmd
			a.skillTab, cmd = a.skillTab.Update(msg)
			cmds = append(cmds, cmd)

		case tabCommands:
			var cmd tea.Cmd
			a.cmdTab, cmd = a.cmdTab.Update(msg)
			cmds = append(cmds, cmd)

		case tabTools:
			var cmd tea.Cmd
			a.toolTab, cmd = a.toolTab.Update(msg)
			cmds = append(cmds, cmd)

		case tabDirs:
			var cmd tea.Cmd
			a.dirsTab, cmd = a.dirsTab.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return a, tea.Batch(cmds...)
}

func (a *App) updateAgentsTab(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if msg.X < a.list.width {
				a.list, _ = a.list.Update(msg)
				if a.list.onApprovals() {
					a.detail.setApprovals()
				} else {
					a.detail.setAgent(a.list.selected())
				}
			} else {
				var cmd tea.Cmd
				a.detail, cmd = a.detail.Update(msg)
				return cmd
			}
		}
		return nil

	case tea.KeyMsg:
		switch a.pane {
		case paneList:
			switch {
			case key.Matches(msg, Keys.Up), key.Matches(msg, Keys.Down),
				key.Matches(msg, Keys.HalfPageDown), key.Matches(msg, Keys.HalfPageUp),
				key.Matches(msg, Keys.PageDown), key.Matches(msg, Keys.PageUp),
				key.Matches(msg, Keys.GoToBottom), key.Matches(msg, Keys.GoToTop):
				var cmd tea.Cmd
				a.list, cmd = a.list.Update(msg)
				cmds = append(cmds, cmd)
				if a.list.onApprovals() {
					a.detail.setApprovals()
				} else {
					a.detail.setAgent(a.list.selected())
				}
				a.statusMsg = ""

			case key.Matches(msg, Keys.Enter):
				if a.list.onApprovals() || a.list.selected() != nil {
					a.pane = paneDetail
					a.detail.focused = true
				}

			case key.Matches(msg, Keys.New):
				a.openForm(nil, true)

			case key.Matches(msg, Keys.Edit):
				a.openForm(a.list.selected(), false)

			case key.Matches(msg, Keys.Toggle):
				a.confirmToggle()

			case key.Matches(msg, Keys.Prompt):
				if sel := a.list.selected(); sel != nil {
					a.openPromptView(sel)
				}
			}

		case paneDetail:
			switch {
			case key.Matches(msg, Keys.Esc):
				a.pane = paneList
				a.detail.focused = false

			case a.detail.showApprovals && msg.String() == "y":
				a.respondToSelectedApproval("once")

			case a.detail.showApprovals && msg.String() == "Y":
				a.respondToSelectedApproval("always")

			case a.detail.showApprovals && msg.String() == "n":
				a.respondToSelectedApproval("reject")

			case a.detail.showApprovals && (msg.String() == "j" || msg.String() == "down"):
				a.detail.approvalMoveDown()

			case a.detail.showApprovals && (msg.String() == "k" || msg.String() == "up"):
				a.detail.approvalMoveUp()

			case key.Matches(msg, Keys.New):
				a.openForm(nil, true)

			case key.Matches(msg, Keys.Edit):
				a.openForm(a.list.selected(), false)

			case key.Matches(msg, Keys.Toggle):
				a.confirmToggle()

			case key.Matches(msg, Keys.Prompt):
				if sel := a.list.selected(); sel != nil {
					a.openPromptView(sel)
				}

			default:
				var cmd tea.Cmd
				a.detail, cmd = a.detail.Update(msg)
				cmds = append(cmds, cmd)
			}

		case panePrompt:
			switch {
			case key.Matches(msg, Keys.Esc):
				a.pane = paneDetail
				a.promptLastKey = ""
			case key.Matches(msg, Keys.GoToBottom):
				a.promptView.GotoBottom()
				a.promptLastKey = ""
			case key.Matches(msg, Keys.GoToTop):
				if a.promptLastKey == "g" {
					a.promptView.GotoTop()
					a.promptLastKey = ""
				} else {
					a.promptLastKey = "g"
				}
			default:
				a.promptLastKey = ""
				var cmd tea.Cmd
				a.promptView, cmd = a.promptView.Update(msg)
				cmds = append(cmds, cmd)
			}

		case paneForm:
			switch {
			case key.Matches(msg, Keys.Esc):
				a.pane = paneList
				a.statusMsg = ""

			case key.Matches(msg, Keys.Save):
				built, err := a.form.build()
				if err != nil {
					a.form.err = err.Error()
				} else {
					if saveErr := built.Save(); saveErr != nil {
						a.form.err = saveErr.Error()
					} else {
						a.statusMsg = "Saved @" + built.ID
						a.statusErr = false
						a.reloadAgents()
						for i, ag := range a.list.agents {
							if ag.ID == built.ID {
								a.list.cursor = i
								a.detail.setAgent(ag)
								break
							}
						}
						a.pane = paneList
					}
				}

			default:
				var cmd tea.Cmd
				a.form, cmd = a.form.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
	}

	return tea.Batch(cmds...)
}

func (a *App) execLeaderAction(action ActionID) tea.Cmd {
	switch action {
	case ActionAgentNew:
		a.tab = tabAgents
		a.openForm(nil, true)

	case ActionAgentEdit:
		a.tab = tabAgents
		a.openForm(a.list.selected(), false)

	case ActionAgentToggle:
		a.tab = tabAgents
		a.confirmToggle()

	case ActionAgentPrompt:
		a.tab = tabAgents
		if sel := a.list.selected(); sel != nil {
			a.openPromptView(sel)
		}

	case ActionTabOverview:
		a.tab = tabOverview
		a.pane = paneList

	case ActionTabSessions:
		a.tab = tabSessions
		a.pane = paneList

	case ActionTabAgents:
		a.tab = tabAgents
		a.pane = paneList

	case ActionTabSkills:
		a.tab = tabSkills
		a.pane = paneList

	case ActionTabCommands:
		a.tab = tabCommands
		a.pane = paneList

	case ActionTabTools:
		a.tab = tabTools
		a.pane = paneList

	case ActionTabDirs:
		a.tab = tabDirs
		a.pane = paneList

	case ActionQuit:
		return tea.Quit

	case ActionFuzzyFind:
		a.finder = newFinder(a.list.agents, a.skillTab.skills, a.cmdTab.commands, a.toolTab.tools)
		a.finder.width = a.width
		a.finder.height = a.height

	case ActionNewSession:
		a.dirPicker = newDirPicker(a.dirsTab.projects)
		a.dirPicker.width = a.width
		a.dirPicker.height = a.height

	case ActionSettings:
		a.settings = newSettings(a.accentName, func(p AccentPalette) {
			ApplyAccent(p)
			a.accentName = p.Name
			_ = a.saveSettings()
			a.statusMsg = "accent: " + p.Label
			a.statusErr = false
		})
		a.settings.width = a.width
		a.settings.height = a.height

	case ActionKillInstance:
		a.tab = tabOverview
		a.pane = paneList
		if sel := a.overviewTab.selectedServer(); sel != nil {
			a.pool.Remove(sel.URL)
			a.overviewTab.connectedServers = a.pool.ConnectedServers()
			if a.overviewTab.cursor >= len(a.overviewTab.connectedServers) {
				a.overviewTab.cursor = max(0, len(a.overviewTab.connectedServers)-1)
			}
			a.statusMsg = "removed " + sel.URL
			a.statusErr = false
			return clearStatusCmd()
		}

	case ActionToggleListOnly:
		a.tab = tabOverview
		a.pane = paneList
		a.overviewTab.listOnly = !a.overviewTab.listOnly
		a.overviewTab.detailFocused = false
		a.layout()
		_ = a.saveSettings()
		if a.overviewTab.listOnly {
			a.statusMsg = "list-only mode on"
		} else {
			a.statusMsg = "list-only mode off"
		}
		a.statusErr = false
		return clearStatusCmd()

	case ActionInstanceSettings:
		a.leader.Activate(instanceSettingsMenu(a.instanceDisplay))
		return nil

	case ActionToggleShowCtx:
		a.instanceDisplay.ShowCtx = !a.instanceDisplay.ShowCtx
		a.overviewTab.display = a.instanceDisplay
		_ = a.saveSettings()
		a.leader.Activate(instanceSettingsMenu(a.instanceDisplay))
		return nil

	case ActionToggleShowModel:
		a.instanceDisplay.ShowModel = !a.instanceDisplay.ShowModel
		a.overviewTab.display = a.instanceDisplay
		_ = a.saveSettings()
		a.leader.Activate(instanceSettingsMenu(a.instanceDisplay))
		return nil

	case ActionToggleShowMsgs:
		a.instanceDisplay.ShowMsgs = !a.instanceDisplay.ShowMsgs
		a.overviewTab.display = a.instanceDisplay
		_ = a.saveSettings()
		a.leader.Activate(instanceSettingsMenu(a.instanceDisplay))
		return nil

	case ActionToggleShowInOut:
		a.instanceDisplay.ShowInOut = !a.instanceDisplay.ShowInOut
		a.overviewTab.display = a.instanceDisplay
		_ = a.saveSettings()
		a.leader.Activate(instanceSettingsMenu(a.instanceDisplay))
		return nil
	}
	return nil
}

func (a *App) saveSettings() error {
	return config.SaveSettings(config.Settings{
		AccentColor:     a.accentName,
		ListOnlyMode:    a.overviewTab.listOnly,
		InstanceDisplay: a.instanceDisplay,
	})
}

func (a *App) respondToSelectedApproval(response string) {
	if a.pool == nil {
		return
	}
	p := a.detail.selectedApproval()
	if p == nil {
		return
	}
	if err := a.pool.Respond(p.SessionID, p.ID, response); err != nil {
		a.statusMsg = "error: " + err.Error()
		a.statusErr = true
	} else {
		a.statusMsg = p.Type + " " + response + "d"
		a.statusErr = false
	}
	a.detail.refreshContent()
}

func (a *App) openPromptView(ag *agent.Agent) {
	vp := viewport.New(a.width-4, a.contentHeight()-4)
	vp.KeyMap = viewportKeyMap()
	prompt := ag.Prompt
	if prompt == "" {
		prompt = StyleMuted.Render("  (no system prompt defined)")
	}
	vp.SetContent(prompt)
	a.promptView = vp
	a.promptLastKey = ""
	a.pane = panePrompt
}

// ─── Rendering ───────────────────────────────────────────────────────────────

func (a *App) handleListClick(x, y int) {
	// relY is relative to the first row inside the border (title row)
	// Screen: row 0 = tab bar, row 1 = top border, row 2+ = inner content
	relY := y - 2
	if relY < 0 {
		return
	}

	switch a.tab {
	case tabAgents:
		if x >= a.list.width {
			return
		}
		// Inner rows: 0=title, 1=separator, 2+=items (3 lines each)
		itemRelY := relY - 2
		if itemRelY < 0 {
			return
		}
		idx := itemRelY/3 + a.list.offset
		rows := a.list.rows()
		if idx >= 0 && idx < len(rows) {
			a.list.cursor = idx
			a.detail.setAgent(a.list.selected())
			a.pane = paneList
		}

	case tabSkills:
		if x >= a.skillTab.listW {
			return
		}
		itemRelY := relY - 2
		if itemRelY < 0 {
			return
		}
		idx := itemRelY/3 + a.skillTab.offset
		if idx >= 0 && idx < len(a.skillTab.skills) {
			a.skillTab.cursor = idx
			a.skillTab.refreshDetail()
			a.pane = paneList
		}

	case tabCommands:
		if x >= a.cmdTab.listW {
			return
		}
		itemRelY := relY - 2
		if itemRelY < 0 {
			return
		}
		idx := itemRelY/3 + a.cmdTab.offset
		if idx >= 0 && idx < len(a.cmdTab.commands) {
			a.cmdTab.cursor = idx
			a.cmdTab.refreshDetail()
			a.pane = paneList
		}

	case tabTools:
		if x >= a.toolTab.listW {
			return
		}
		itemRelY := relY - 2
		if itemRelY < 0 {
			return
		}
		idx := itemRelY/3 + a.toolTab.offset
		if idx >= 0 && idx < len(a.toolTab.tools) {
			a.toolTab.cursor = idx
			a.toolTab.refreshDetail()
			a.pane = paneList
		}

	case tabOverview:
		if !a.overviewTab.listOnly && x >= a.overviewTab.listW {
			return
		}
		if relY >= 0 && relY < len(a.overviewTab.rowToCursor) {
			idx := a.overviewTab.rowToCursor[relY]
			if idx >= 0 {
				ordered := orderedServers(a.overviewTab.connectedServers)
				if idx < len(ordered) {
					a.overviewTab.cursor = idx
					a.overviewTab.refreshDetail()
					a.pane = paneList
				}
			}
		}

	case tabSessions:
		if relY >= 0 && relY < len(a.sessionTab.rowToCursor) {
			idx := a.sessionTab.rowToCursor[relY]
			if idx >= 0 {
				a.sessionTab.cursor = idx
				a.pane = paneList
			}
		}

	case tabDirs:
		if relY >= 0 && relY < len(a.dirsTab.rowToCursor) {
			idx := a.dirsTab.rowToCursor[relY]
			if idx >= 0 {
				a.dirsTab.cursor = idx
				a.pane = paneList
			}
		}
	}
}

func (a *App) renderTabBar() string {
	sep := StyleDim.Render("·")
	joinSep := "  " + sep + "  "

	// Pass 0: full form
	var tabs []string
	for i := tabID(0); i < tabCount; i++ {
		label := strings.TrimSpace(tabNames[i])
		if i == a.tab {
			tabs = append(tabs, StyleTabActive.Render("["+label+"]"))
		} else {
			tabs = append(tabs, StyleTabInactive.Render(label))
		}
	}
	bar := strings.Join(tabs, joinSep)

	// Pass 1: shrink inactive tabs to first char only
	if lipgloss.Width(bar) > a.width {
		tabs = tabs[:0]
		for i := tabID(0); i < tabCount; i++ {
			label := strings.TrimSpace(tabNames[i])
			if i == a.tab {
				tabs = append(tabs, StyleTabActive.Render("["+label+"]"))
			} else {
				abbrev := string([]rune(label)[0:1])
				tabs = append(tabs, StyleTabInactiveHotkey.Render(abbrev))
			}
		}
		bar = strings.Join(tabs, joinSep)
	}

	// Pass 2: shrink active tab to [first char] only
	if lipgloss.Width(bar) > a.width {
		tabs = tabs[:0]
		for i := tabID(0); i < tabCount; i++ {
			label := strings.TrimSpace(tabNames[i])
			abbrev := string([]rune(label)[0:1])
			if i == a.tab {
				tabs = append(tabs, StyleTabActive.Render("["+abbrev+"]"))
			} else {
				tabs = append(tabs, StyleTabInactiveHotkey.Render(abbrev))
			}
		}
		bar = strings.Join(tabs, joinSep)
	}

	// Pass 3: hard truncate if still too wide
	if lipgloss.Width(bar) > a.width {
		bar = lipgloss.NewStyle().MaxWidth(a.width).Render(bar)
	}

	// Compute tab click zones from the final tabs slice
	sepW := lipgloss.Width(joinSep)
	x := 0
	for i := tabID(0); i < tabCount; i++ {
		w := lipgloss.Width(tabs[int(i)])
		a.tabZones[i] = [2]int{x, x + w}
		x += w + sepW
	}

	padW := a.width - lipgloss.Width(bar)
	if padW > 0 {
		bar += strings.Repeat(" ", padW)
	}
	return bar
}

func (a *App) View() string {
	if a.showSplash {
		return renderSplash(a.splashPhase, a.splashFrame, a.splashFlashFrame, a.splashFlashing, a.width, a.height)
	}

	// Confirm dialog takes over the full screen.
	if a.confirm.active {
		return a.confirm.View(a.width, a.height)
	}

	// Settings will be overlaid at the end if active.

	tabBar := a.renderTabBar()
	statusBar := a.buildStatusBar()
	ch := a.contentHeight()

	var base string

	switch a.pane {
	case panePrompt:
		sel := a.list.selected()
		title := "SYSTEM PROMPT"
		if sel != nil {
			title = "prompt  @" + strings.ToUpper(sel.ID)
		}
		ph := StylePromptHeader.Render(title)
		h := ch - lipgloss.Height(ph) - 2
		if h < 1 {
			h = 1
		}
		content := StyleActiveBorder.Width(a.width).Height(h).Render(a.promptView.View())
		base = lipgloss.JoinVertical(lipgloss.Left, tabBar, ph, content, statusBar)

	case paneForm:
		inner := a.form.View()
		available := ch - 2
		lines := strings.Split(inner, "\n")
		scrollY := a.form.scrollY
		if scrollY > len(lines)-available {
			scrollY = len(lines) - available
		}
		if scrollY < 0 {
			scrollY = 0
		}
		end := scrollY + available
		if end > len(lines) {
			end = len(lines)
		}
		visible := strings.Join(lines[scrollY:end], "\n")
		body := StyleBorder.Width(a.width).Height(ch - 2).Render(visible)
		base = lipgloss.JoinVertical(lipgloss.Left, tabBar, body, statusBar)

	default:
		var content string
		switch a.tab {
		case tabOverview:
			if a.overviewTab.listOnly {
				content = a.overviewTab.renderList(a.spinFrame)
			} else {
				content = lipgloss.JoinHorizontal(lipgloss.Top,
					a.overviewTab.renderList(a.spinFrame),
					a.overviewTab.renderDetailPane(),
				)
			}
		case tabSessions:
			content = a.sessionTab.View()
		case tabAgents:
			listFocused := a.pane == paneList
			detailFocused := a.pane == paneDetail
			content = lipgloss.JoinHorizontal(lipgloss.Top,
				a.list.View(listFocused, a.activeAgents, a.spinFrame),
				a.detail.View(detailFocused),
			)
		case tabSkills:
			content = lipgloss.JoinHorizontal(lipgloss.Top,
				a.skillTab.renderList(),
				a.skillTab.renderDetailPane(),
			)
		case tabCommands:
			content = lipgloss.JoinHorizontal(lipgloss.Top,
				a.cmdTab.renderList(),
				a.cmdTab.renderDetailPane(),
			)
		case tabTools:
			content = lipgloss.JoinHorizontal(lipgloss.Top,
				a.toolTab.renderList(),
				a.toolTab.renderDetailPane(),
			)
		case tabDirs:
			content = a.dirsTab.View()
		}
		base = lipgloss.JoinVertical(lipgloss.Left, tabBar, content, statusBar)
	}

	// Clamp final output to terminal dimensions to prevent overflow.
	base = lipgloss.NewStyle().MaxWidth(a.width).MaxHeight(a.height).Render(base)

	// Overlay dir picker modal when active.
	if a.dirPicker.active {
		return overlayModal(base, a.dirPicker.View(), a.width, 3)
	}

	// Overlay finder modal when active.
	if a.finder.active {
		return overlayModal(base, a.finder.View(a.width, a.height), a.width, 3)
	}

	// Overlay settings modal when active.
	if a.settings.active {
		return overlayModal(base, a.settings.View(a.width), a.width, 3)
	}

	// Overlay leader panel as a centered modal when active.
	if a.leader.active {
		return overlayModal(base, a.leader.View(a.width), a.width, 2)
	}

	return base
}

// overlayModal composites a modal panel onto a base screen. The modal is
// centered horizontally; vPos controls vertical placement (2=center, 3=upper third).
func overlayModal(base, modal string, screenW, vPos int) string {
	modalLines := strings.Split(modal, "\n")
	baseLines := strings.Split(base, "\n")
	modalH := len(modalLines)
	modalW := lipgloss.Width(modalLines[0])

	startY := (len(baseLines) - modalH) / vPos
	startX := (screenW - modalW) / 2
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
	}

	for i, mLine := range modalLines {
		row := startY + i
		if row >= len(baseLines) {
			break
		}
		bLine := baseLines[row]
		left := ansi.Truncate(bLine, startX, "")
		right := ansi.TruncateLeft(bLine, startX+modalW, "")
		baseLines[row] = left + mLine + right
	}
	return strings.Join(baseLines, "\n")
}

func (a *App) buildStatusBar() string {
	type hint struct{ k, d string }

	var hints []hint
	switch a.tab {
	case tabOverview:
		if a.overviewTab.insertMode {
			hints = []hint{{"enter", "send"}, {"shift+enter", "newline"}, {"esc", "cancel"}}
		} else if a.overviewTab.detailFocused {
			hints = []hint{{"↑↓", "scroll"}, {"i", "chat"}, {"esc", "back"}}
		} else {
			hints = []hint{{"↑↓", "nav"}, {"l/h", "pane"}, {"enter", "opencode"}, {"←/→", "tab"}, {"spc", "leader"}, {"q", "quit"}}
		}
	case tabSessions:
		hints = []hint{{"↑↓", "nav"}, {"←/→", "tab"}, {"enter", "open"}, {"spc", "leader"}, {"q", "quit"}}
	case tabAgents:
		switch a.pane {
		case paneList:
			hints = []hint{{"↑↓", "nav"}, {"←/→", "tab"}, {"enter", "focus"}, {"spc", "leader"}, {"q", "quit"}}
		case paneDetail:
			hints = []hint{{"↑↓", "scroll"}, {"spc", "leader"}, {"esc", "back"}}
		case panePrompt:
			hints = []hint{{"↑↓", "scroll"}, {"esc", "back"}}
		case paneForm:
			hints = []hint{{"tab", "next"}, {"shift+tab", "prev"}, {"ctrl+s", "save"}, {"esc", "cancel"}}
		}
	case tabSkills:
		if a.skillTab.detailFocused {
			hints = []hint{{"↑↓", "scroll"}, {"esc", "back"}}
		} else {
			hints = []hint{{"↑↓", "nav"}, {"←/→", "tab"}, {"spc", "leader"}, {"q", "quit"}}
		}
	case tabCommands:
		if a.cmdTab.detailFocused {
			hints = []hint{{"↑↓", "scroll"}, {"esc", "back"}}
		} else {
			hints = []hint{{"↑↓", "nav"}, {"←/→", "tab"}, {"spc", "leader"}, {"q", "quit"}}
		}
	case tabTools:
		if a.toolTab.detailFocused {
			hints = []hint{{"↑↓", "scroll"}, {"esc", "back"}}
		} else {
			hints = []hint{{"↑↓", "nav"}, {"←/→", "tab"}, {"spc", "leader"}, {"q", "quit"}}
		}
	case tabDirs:
		hints = []hint{{"↑↓", "nav"}, {"←/→", "tab"}, {"enter", "open"}, {"spc", "leader"}, {"q", "quit"}}
	}

	var parts []string
	for _, h := range hints {
		parts = append(parts, StyleStatusKey.Render(h.k)+" "+StyleStatusDesc.Render(h.d))
	}
	sep := StyleDim.Render("·")
	keysStr := strings.Join(parts, "  "+sep+"  ")

	var msgStr string
	if a.statusMsg != "" {
		if a.statusErr {
			msgStr = StyleStatusErr.Render("["+a.statusMsg+"]") + "  "
		} else {
			msgStr = StyleStatusMsg.Render("["+a.statusMsg+"]") + "  "
		}
	}

	var srvStr string
	if a.pool != nil {
		if a.pool.Connected() {
			n := a.pool.ConnectedCount()
			if n > 1 {
				srvStr = StyleBadgeEnabled.Render(fmt.Sprintf("[CONN:%d]", n)) + "  "
			} else {
				srvStr = StyleBadgeEnabled.Render("[CONN]") + "  "
			}
		} else {
			srvStr = StyleBadgeDisabled.Render("[DISC]") + "  "
		}
	}

	bar := srvStr + msgStr + keysStr
	return StyleStatusBar.Width(a.width).Render(bar)
}
