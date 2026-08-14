package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/thenickygee/mirage/internal/config"
)

// ActionID identifies a leaf action the leader menu can trigger.
type ActionID int

const (
	ActionNone ActionID = iota
	ActionAgentNew
	ActionAgentEdit
	ActionAgentToggle
	ActionAgentPrompt
	ActionTabOverview
	ActionTabSessions
	ActionTabAgents
	ActionTabSkills
	ActionTabCommands
	ActionTabTools
	ActionTabDirs
	ActionQuit
	ActionFuzzyFind
	ActionSettings
	ActionNewSession
	ActionKillInstance
	ActionToggleListOnly
	ActionToggleShowCtx
	ActionToggleShowModel
	ActionToggleShowMsgs
	ActionToggleShowInOut
	ActionInstanceSettings
)

// menuEntry is either a group (has children) or a leaf (has an action).
type menuEntry struct {
	key      string
	label    string
	action   ActionID
	children []menuEntry
}

// menuSection groups related entries under a titled heading.
type menuSection struct {
	title   string
	entries []menuEntry
}

var navSection = menuSection{
	title: "NAVIGATION",
	entries: []menuEntry{
		{key: "o", label: "overview", action: ActionTabOverview},
		{key: "x", label: "sessions", action: ActionTabSessions},
		{key: "a", label: "agents", action: ActionTabAgents},
		{key: "s", label: "skills", action: ActionTabSkills},
		{key: "c", label: "commands", action: ActionTabCommands},
		{key: "t", label: "tools", action: ActionTabTools},
		{key: "d", label: "dirs", action: ActionTabDirs},
	},
}

var actionsSection = menuSection{
	title: "ACTIONS",
	entries: []menuEntry{
		{key: "n", label: "new session", action: ActionNewSession},
		{key: "f", label: "find", action: ActionFuzzyFind},
		{key: "O", label: "options", action: ActionSettings},
		{key: "q", label: "quit", action: ActionQuit},
	},
}

// rootMenu is the default which-key layout (no context-specific sections).
var rootMenu = []menuSection{navSection, actionsSection}

// overviewMenu adds an INSTANCE section above the standard navigation.
var overviewMenu = []menuSection{
	{
		title: "INSTANCE",
		entries: []menuEntry{
			{key: "k", label: "kill instance", action: ActionKillInstance},
			{key: "l", label: "toggle list-only", action: ActionToggleListOnly},
			{key: "I", label: "instance settings", action: ActionInstanceSettings},
		},
	},
	navSection,
	actionsSection,
}

// instanceSettingsMenu builds the instance settings pick-list with checkmarks
// reflecting current display state.
func instanceSettingsMenu(d config.InstanceDisplay) []menuSection {
	check := func(on bool, label string) string {
		if on {
			return "✓ " + label
		}
		return "  " + label
	}
	return []menuSection{
		{
			title: "INSTANCE DISPLAY",
			entries: []menuEntry{
				{key: "c", label: check(d.ShowCtx, "ctx"), action: ActionToggleShowCtx},
				{key: "m", label: check(d.ShowModel, "model"), action: ActionToggleShowModel},
				{key: "n", label: check(d.ShowMsgs, "msgs"), action: ActionToggleShowMsgs},
				{key: "i", label: check(d.ShowInOut, "in/out"), action: ActionToggleShowInOut},
			},
		},
	}
}

// agentMenu adds an AGENT section above the standard navigation.
var agentMenu = []menuSection{
	{
		title: "AGENT",
		entries: []menuEntry{
			{key: "n", label: "new agent", action: ActionAgentNew},
			{key: "e", label: "edit agent", action: ActionAgentEdit},
			{key: "d", label: "disable/enable", action: ActionAgentToggle},
		},
	},
	navSection,
	actionsSection,
}

// leaderState tracks the which-key overlay state.
type leaderState struct {
	active   bool
	prefix   string        // keys pressed so far
	sections []menuSection // sections shown at current level
}

func newLeaderState() leaderState {
	return leaderState{sections: rootMenu}
}

// Activate opens the menu with the given sections.
func (l *leaderState) Activate(sections []menuSection) {
	l.active = true
	l.prefix = ""
	l.sections = sections
}

// Dismiss closes the menu without action.
func (l *leaderState) Dismiss() {
	l.active = false
	l.prefix = ""
	l.sections = rootMenu
}

// Press handles a keypress while the leader menu is open.
// Returns (action, done). done=true means the menu should close.
func (l *leaderState) Press(k string) (ActionID, bool) {
	for _, sec := range l.sections {
		for _, entry := range sec.entries {
			if entry.key == k {
				if entry.action != ActionNone {
					l.Dismiss()
					return entry.action, true
				}
				// descend into sub-menu (future use)
				l.prefix += k
				l.sections = []menuSection{{entries: entry.children}}
				return ActionNone, false
			}
		}
	}
	l.Dismiss()
	return ActionNone, true
}

// View renders the which-key panel as a centered modal.
// Returns the panel string and its height in lines.
func (l leaderState) View(screenW int) string {
	if !l.active {
		return ""
	}

	// ── determine modal width ────────────────────────────────────────────────
	cols := 3
	cellW := 24
	sepW := 2
	innerW := cols*cellW + (cols+1)*sepW
	modalW := innerW + 4 // 2px padding each side
	if modalW > screenW-4 {
		modalW = screenW - 4
	}

	bg := StyleLeaderPanelBg.Width(modalW)

	// ── top bar ─────────────────────────────────────────────────────────────
	esc := StyleLeaderDismiss.Render("esc · close")
	sepFill := StyleLeaderPanelSep.Render(strings.Repeat("─", max(0, modalW-lipgloss.Width(esc)-3)))
	sepEdge := StyleLeaderPanelSep.Render(" ─")
	topBar := lipgloss.JoinHorizontal(lipgloss.Left, sepEdge, sepFill, esc, StyleLeaderPanelSep.Render("─ "))
	topBar = bg.Render(topBar)

	// ── sections ─────────────────────────────────────────────────────────────
	var bodyLines []string
	for _, sec := range l.sections {
		hdr := StyleLeaderSectionHeader.Render(sec.title)
		bodyLines = append(bodyLines, bg.Render("  "+hdr))

		for i := 0; i < len(sec.entries); i += cols {
			var cells []string
			for j := 0; j < cols && i+j < len(sec.entries); j++ {
				e := sec.entries[i+j]
				k := StyleLeaderKey.Render(e.key)
				var lbl string
				if e.action == ActionNone {
					lbl = StyleLeaderGroup.Render(e.label + " ›")
				} else {
					lbl = StyleLeaderLabel.Render(e.label)
				}
				spacer := lipgloss.NewStyle().Background(colorDeep).Render(" ")
				cell := k + spacer + lbl
				padded := lipgloss.NewStyle().Background(colorDeep).Width(cellW).Render(cell)
				cells = append(cells, padded)
			}
			sep := lipgloss.NewStyle().Background(colorDeep).Render("  ")
			row := bg.Render(sep + strings.Join(cells, sep))
			bodyLines = append(bodyLines, row)
		}

		bodyLines = append(bodyLines, bg.Render(""))
	}

	// Bottom padding line
	bodyLines = append(bodyLines, bg.Render(""))

	allLines := append([]string{topBar}, bodyLines...)

	return lipgloss.JoinVertical(lipgloss.Left, allLines...)
}
