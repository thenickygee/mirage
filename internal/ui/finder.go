package ui

import (
	"fmt"
	"strings"

	"github.com/thenickygee/mirage/internal/agent"
	"github.com/thenickygee/mirage/internal/command"
	"github.com/thenickygee/mirage/internal/skill"
	"github.com/thenickygee/mirage/internal/tool"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	lgv1 "github.com/charmbracelet/lipgloss"
)

type finderItemKind int

const (
	finderAgent finderItemKind = iota
	finderSkill
	finderCommand
	finderTool
)

type finderItem struct {
	kind  finderItemKind
	id    string
	label string
	desc  string
	tab   tabID
	index int
}

type finderModel struct {
	active   bool
	input    textinput.Model
	items    []finderItem
	filtered []finderItem
	cursor   int
	width    int
	height   int
}

func newFinder(agents []*agent.Agent, skills []*skill.Skill, commands []*command.Command, tools []*tool.Tool) finderModel {
	ti := textinput.New()
	ti.Placeholder = "type to search..."
	ti.Prompt = "  › "
	ti.Focus()
	ti.CharLimit = 100
	ti.TextStyle = lgv1.NewStyle().Background(lgv1.Color("#141414"))
	ti.PromptStyle = lgv1.NewStyle().Foreground(lgv1.Color("#3A5200")).Background(lgv1.Color("#141414"))
	ti.PlaceholderStyle = lgv1.NewStyle().Foreground(lgv1.Color("#555555")).Background(lgv1.Color("#141414"))
	ti.Cursor.Style = lgv1.NewStyle().Background(lgv1.Color("#141414"))

	var items []finderItem

	for i, a := range agents {
		items = append(items, finderItem{
			kind:  finderAgent,
			id:    a.ID,
			label: a.ID,
			desc:  a.Description,
			tab:   tabAgents,
			index: i,
		})
	}
	for i, s := range skills {
		name := s.Name
		if name == "" {
			name = s.ID
		}
		items = append(items, finderItem{
			kind:  finderSkill,
			id:    s.ID,
			label: name,
			desc:  s.Description,
			tab:   tabSkills,
			index: i,
		})
	}
	for i, c := range commands {
		items = append(items, finderItem{
			kind:  finderCommand,
			id:    c.ID,
			label: c.ID,
			desc:  c.Description,
			tab:   tabCommands,
			index: i,
		})
	}
	for i, t := range tools {
		items = append(items, finderItem{
			kind:  finderTool,
			id:    t.ID,
			label: t.ID + t.Ext,
			desc:  t.Description(),
			tab:   tabTools,
			index: i,
		})
	}

	return finderModel{
		active:   true,
		input:    ti,
		items:    items,
		filtered: items,
	}
}

func (f *finderModel) filter() {
	query := strings.ToLower(f.input.Value())
	if query == "" {
		f.filtered = f.items
		f.cursor = 0
		return
	}

	var matches []finderItem
	for _, item := range f.items {
		if fuzzyMatch(strings.ToLower(item.label), query) ||
			fuzzyMatch(strings.ToLower(item.desc), query) ||
			fuzzyMatch(strings.ToLower(item.id), query) {
			matches = append(matches, item)
		}
	}
	f.filtered = matches
	if f.cursor >= len(f.filtered) {
		f.cursor = max(0, len(f.filtered)-1)
	}
}

func fuzzyMatch(s, pattern string) bool {
	pi := 0
	for i := 0; i < len(s) && pi < len(pattern); i++ {
		if s[i] == pattern[pi] {
			pi++
		}
	}
	return pi == len(pattern)
}

func (f *finderModel) selected() *finderItem {
	if len(f.filtered) == 0 {
		return nil
	}
	return &f.filtered[f.cursor]
}

func (f finderModel) Update(msg tea.Msg) (finderModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, Keys.Up), key.Matches(msg, Keys.CtrlP):
			if f.cursor > 0 {
				f.cursor--
			}
			return f, nil
		case key.Matches(msg, Keys.Down), key.Matches(msg, Keys.CtrlN):
			if f.cursor < len(f.filtered)-1 {
				f.cursor++
			}
			return f, nil
		}
	}

	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	f.filter()
	return f, cmd
}

func (f finderModel) View(screenW, screenH int) string {
	if !f.active {
		return ""
	}

	// Box sizing
	boxW := screenW * 55 / 100
	if boxW < 52 {
		boxW = 52
	}
	if boxW > 96 {
		boxW = 96
	}
	innerW := boxW - 6 // account for border (2) + padding (2*2)

	var rows []string

	// ── Input row ────────────────────────────────────────────────
	f.input.Width = 0
	inputLine := lipgloss.NewStyle().Background(colorDeep).Width(innerW).Render(f.input.View())
	rows = append(rows, inputLine)

	// ── Separator ────────────────────────────────────────────────
	rows = append(rows, StyleFinderSeparator.Render(strings.Repeat("─", innerW)))

	// ── Results ──────────────────────────────────────────────────
	maxVisible := (screenH * 40 / 100)
	if maxVisible < 5 {
		maxVisible = 5
	}
	if maxVisible > 16 {
		maxVisible = 16
	}
	if maxVisible > len(f.filtered) {
		maxVisible = len(f.filtered)
	}

	start := 0
	if f.cursor >= maxVisible {
		start = f.cursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(f.filtered) {
		end = len(f.filtered)
		start = max(0, end-maxVisible)
	}

	if len(f.filtered) == 0 {
		rows = append(rows, "")
		rows = append(rows, StyleMuted.Render("  no results"))
		rows = append(rows, "")
	} else {
		rows = append(rows, "")
		for i := start; i < end; i++ {
			item := f.filtered[i]
			rows = append(rows, f.renderItem(item, i == f.cursor, innerW))
		}
		rows = append(rows, "")
	}

	// ── Footer ───────────────────────────────────────────────────
	rows = append(rows, StyleFinderSeparator.Render(strings.Repeat("─", innerW)))
	rows = append(rows, f.renderFooter(innerW))

	inner := strings.Join(rows, "\n")
	return StyleFinderBox.Width(boxW).Render(inner)
}

func (f finderModel) renderItem(item finderItem, selected bool, innerW int) string {
	badge := finderKindBadge(item.kind, selected)
	badgeW := lipgloss.Width(badge)

	// 1 = indicator, 1 = space after indicator, 2 = sp2 after badge, 2 = sp2 after label
	available := innerW - badgeW - 6
	labelMax := available / 3
	if labelMax < 12 {
		labelMax = 12
	}
	descMax := available - labelMax
	if descMax < 0 {
		descMax = 0
	}

	label := truncateRunes(item.label, labelMax)
	descOneLine := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, item.desc)
	desc := truncateRunes(descOneLine, descMax)

	var labelStyle, descStyle lipgloss.Style
	var indicator string
	if selected {
		labelStyle = StyleFinderSelectedLabel
		descStyle = StyleFinderSelectedDesc
		indicator = StyleFinderSelectedLabel.Render("▶")
	} else {
		labelStyle = StyleFinderItemLabel
		descStyle = StyleFinderItemDesc
		indicator = StyleFinderItem.Render(" ")
	}

	labelStr := labelStyle.Render(padRight(label, labelMax))
	descStr := descStyle.Render(desc)

	bgStyle := lipgloss.NewStyle().Background(colorDeep)
	if selected {
		bgStyle = lipgloss.NewStyle().Background(colorShadow)
	}
	sp := bgStyle.Render(" ")
	sp2 := bgStyle.Render("  ")

	line := indicator + sp + badge + sp2 + labelStr + sp2 + descStr

	if selected {
		return StyleFinderSelected.Width(innerW).MaxWidth(innerW).Render(line)
	}
	return StyleFinderItem.Width(innerW).MaxWidth(innerW).Render(line)
}

func (f finderModel) renderFooter(innerW int) string {
	count := fmt.Sprintf("%d/%d", len(f.filtered), len(f.items))
	countStr := StyleFinderCount.Render(count)

	finderMuted := lipgloss.NewStyle().Foreground(colorStone).Background(colorDeep)
	hints := StyleFinderHint.Render("↑↓") + finderMuted.Render(" navigate  ") +
		StyleFinderHint.Render("enter") + finderMuted.Render(" open  ") +
		StyleFinderHint.Render("esc") + finderMuted.Render(" close")

	countW := lipgloss.Width(countStr)
	hintsW := lipgloss.Width(hints)
	gap := innerW - countW - hintsW
	if gap < 1 {
		gap = 1
	}

	return countStr + lipgloss.NewStyle().Background(colorDeep).Render(strings.Repeat(" ", gap)) + hints
}

func finderKindBadge(kind finderItemKind, selected bool) string {
	if selected {
		switch kind {
		case finderAgent:
			return StyleBadgeFinderAgentSel.Render("agent")
		case finderSkill:
			return StyleBadgeFinderSkillSel.Render("skill")
		case finderCommand:
			return StyleBadgeFinderCommandSel.Render("cmd")
		case finderTool:
			return StyleBadgeFinderToolSel.Render("tool")
		}
		return ""
	}
	switch kind {
	case finderAgent:
		return StyleBadgeFinderAgent.Render("agent")
	case finderSkill:
		return StyleBadgeFinderSkill.Render("skill")
	case finderCommand:
		return StyleBadgeFinderCommand.Render("cmd")
	case finderTool:
		return StyleBadgeFinderTool.Render("tool")
	}
	return ""
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if max <= 0 {
		return ""
	}
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func padRight(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(r))
}
