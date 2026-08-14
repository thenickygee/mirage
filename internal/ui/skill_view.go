package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/thenickygee/mirage/internal/agent"
	"github.com/thenickygee/mirage/internal/skill"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type skillView struct {
	skills        []*skill.Skill
	agents        []*agent.Agent
	cursor        int
	offset        int
	lastKey       string
	listW         int
	detailW       int
	height        int
	detail        viewport.Model
	detailFocused bool
	detailLastKey string
}

func newSkillView(skills []*skill.Skill, agents []*agent.Agent) skillView {
	vp := viewport.New(0, 0)
	return skillView{
		skills: skills,
		agents: agents,
		detail: vp,
	}
}

func (v *skillView) setSize(w, h int) {
	v.listW = w / 3
	if v.listW < 22 {
		v.listW = 22
	} else if v.listW > 40 {
		v.listW = 40
	}
	if w-v.listW < 10 {
		v.listW = w - 10
	}
	if v.listW < 1 {
		v.listW = 1
	}
	v.detailW = w - v.listW
	v.height = h
	dw := v.detailW - 4
	if dw < 1 {
		dw = 1
	}
	v.detail.Width = dw
	dh := h - 4
	if dh < 1 {
		dh = 1
	}
	v.detail.Height = dh
	v.refreshDetail()
}

func (v *skillView) selected() *skill.Skill {
	if len(v.skills) == 0 {
		return nil
	}
	return v.skills[v.cursor]
}

func (v *skillView) refreshDetail() {
	s := v.selected()
	if s == nil {
		v.detail.SetContent(StyleMuted.Render("  no skill selected"))
		return
	}
	v.detail.SetContent(v.renderSkill(s))
}

func (v *skillView) renderSkill(s *skill.Skill) string {
	var sb strings.Builder

	title := StyleDetailTitle.Render("◈ " + strings.ToUpper(s.Name))
	sb.WriteString(title + "\n")
	sb.WriteString(StyleSeparator.Render(strings.Repeat("─", v.detail.Width)) + "\n\n")

	sb.WriteString(StyleBadgeSkill.Render(" SKILL ") + "\n\n")

	field := func(label, value string) string {
		if value == "" {
			return fmt.Sprintf("  %s  %s\n",
				StyleLabel.Render(fmt.Sprintf("%-14s", label)),
				StyleDim.Render("—"),
			)
		}
		return fmt.Sprintf("  %s  %s\n",
			StyleLabel.Render(fmt.Sprintf("%-14s", label)),
			StyleValue.Render(value),
		)
	}

	if s.Compatibility != "" {
		sb.WriteString(field("COMPATIBILITY", s.Compatibility))
	}
	if s.License != "" {
		sb.WriteString(field("LICENSE", s.License))
	}
	if s.AllowedTools != "" {
		sb.WriteString(field("ALLOWED TOOLS", s.AllowedTools))
	}

	if len(s.Metadata) > 0 {
		sb.WriteString("\n")
		sb.WriteString(StyleSectionHeader.Render("METADATA") + "\n")
		sb.WriteString(StyleSeparator.Render(strings.Repeat("╌", v.detail.Width)) + "\n")
		metaKeys := make([]string, 0, len(s.Metadata))
		for k := range s.Metadata {
			metaKeys = append(metaKeys, k)
		}
		sort.Strings(metaKeys)
		for _, k := range metaKeys {
			fmt.Fprintf(&sb, "  %s  %s\n",
				StyleMuted.Render(fmt.Sprintf("%-12s", k)),
				StyleValue.Render(s.Metadata[k]),
			)
		}
	}

	sb.WriteString("\n")
	sb.WriteString(StyleSectionHeader.Render("DESCRIPTION") + "\n")
	sb.WriteString(StyleSeparator.Render(strings.Repeat("╌", v.detail.Width)) + "\n")
	if s.Description != "" {
		sb.WriteString(renderMarkdown(s.Description, v.detail.Width) + "\n")
	} else {
		sb.WriteString(StyleDim.Render("  —") + "\n")
	}

	if s.Content != "" {
		sb.WriteString("\n")
		sb.WriteString(StyleSectionHeader.Render("CONTENT") + "\n")
		sb.WriteString(StyleSeparator.Render(strings.Repeat("╌", v.detail.Width)) + "\n")
		sb.WriteString(renderMarkdown(s.Content, v.detail.Width) + "\n")
	}

	// Show which agents use this skill
	var usingAgents []string
	for _, a := range v.agents {
		for _, sk := range a.Skills {
			if strings.EqualFold(sk, s.ID) || strings.EqualFold(sk, s.Name) {
				usingAgents = append(usingAgents, "@"+strings.ToLower(a.ID))
				break
			}
		}
	}
	if len(usingAgents) > 0 {
		sb.WriteString("\n")
		sb.WriteString(StyleSectionHeader.Render("USED BY AGENTS") + "\n")
		sb.WriteString(StyleSeparator.Render(strings.Repeat("╌", v.detail.Width)) + "\n")
		for _, name := range usingAgents {
			sb.WriteString("  " + StyleValue.Render(name) + "\n")
		}
	}

	return sb.String()
}

func (v skillView) Update(msg tea.Msg) (skillView, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if msg.X >= v.listW {
				var cmd tea.Cmd
				v.detail, cmd = v.detail.Update(msg)
				return v, cmd
			}
			if msg.Button == tea.MouseButtonWheelUp && v.cursor > 0 {
				v.cursor--
				if v.cursor < v.offset {
					v.offset = v.cursor
				}
				v.refreshDetail()
			} else if msg.Button == tea.MouseButtonWheelDown && v.cursor < len(v.skills)-1 {
				v.cursor++
				visibleRows := (v.height - 6) / 3
				if visibleRows < 1 {
					visibleRows = 1
				}
				if v.cursor >= v.offset+visibleRows {
					v.offset = v.cursor - visibleRows + 1
				}
				v.refreshDetail()
			}
		}
		return v, nil
	case tea.KeyMsg:
		if v.detailFocused {
			switch {
			case key.Matches(msg, Keys.Esc), key.Matches(msg, Keys.FocusPaneLeft):
				v.detailFocused = false
				v.detailLastKey = ""
				return v, nil
			case key.Matches(msg, Keys.GoToBottom):
				v.detail.GotoBottom()
				v.detailLastKey = ""
				return v, nil
			case key.Matches(msg, Keys.GoToTop):
				if v.detailLastKey == "g" {
					v.detail.GotoTop()
					v.detailLastKey = ""
					return v, nil
				}
				v.detailLastKey = "g"
				return v, nil
			default:
				v.detailLastKey = ""
				var cmd tea.Cmd
				v.detail, cmd = v.detail.Update(msg)
				return v, cmd
			}
		}
		visibleRows := (v.height - 6) / 3 // each padded item is 3 lines
		if visibleRows < 1 {
			visibleRows = 1
		}
		switch {
		case key.Matches(msg, Keys.Up):
			if v.cursor > 0 {
				v.cursor--
				if v.cursor < v.offset {
					v.offset--
				}
				v.refreshDetail()
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.Down):
			if v.cursor < len(v.skills)-1 {
				v.cursor++
				if v.cursor >= v.offset+visibleRows {
					v.offset++
				}
				v.refreshDetail()
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.HalfPageDown):
			v.cursor, v.offset = listHalfPageDown(v.cursor, v.offset, len(v.skills), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.HalfPageUp):
			v.cursor, v.offset = listHalfPageUp(v.cursor, v.offset, len(v.skills), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.PageDown):
			v.cursor, v.offset = listPageDown(v.cursor, v.offset, len(v.skills), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.PageUp):
			v.cursor, v.offset = listPageUp(v.cursor, v.offset, len(v.skills), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.GoToBottom):
			v.cursor, v.offset = listJumpToBottom(len(v.skills), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.GoToTop):
			if v.lastKey == "g" {
				v.cursor = 0
				v.offset = 0
				v.refreshDetail()
				v.lastKey = ""
			} else {
				v.lastKey = "g"
			}
		case key.Matches(msg, Keys.Enter), key.Matches(msg, Keys.FocusPaneRight):
			v.detailFocused = true
			v.lastKey = ""
		default:
			v.lastKey = ""
		}
	}
	return v, nil
}

func (v skillView) renderList() string {
	borderStyle := StyleBorder
	titleStyle := StylePaneTitle
	if !v.detailFocused {
		borderStyle = StyleActiveBorder
		titleStyle = StylePaneTitleActive
	}

	innerW := v.listW - 4
	innerH := v.height - 4

	countStr := strconv.Itoa(len(v.skills))
	title := titleStyle.Render("◈ SKILLS")
	gap := innerW - len("◈ SKILLS") - len(countStr)
	if gap < 1 {
		gap = 1
	}
	titleLine := title + strings.Repeat(" ", gap) + StyleMuted.Render(countStr)

	var rows []string
	rows = append(rows, titleLine)
	rows = append(rows, StyleSeparator.Render(strings.Repeat("─", innerW)))

	visibleRows := (innerH - 2) / 3 // each padded item is 3 lines
	if visibleRows < 1 {
		visibleRows = 1
	}
	end := v.offset + visibleRows
	if end > len(v.skills) {
		end = len(v.skills)
	}

	for i := v.offset; i < end; i++ {
		s := v.skills[i]
		var prefix string
		var rowStyle = StyleListItem
		if i == v.cursor {
			prefix = "▶ "
			rowStyle = StyleListItemSelected
		} else {
			prefix = "  "
		}
		rows = append(rows, rowStyle.Width(innerW).Render(prefix+s.Name))
	}

	content := strings.Join(rows, "\n")
	return borderStyle.Width(v.listW).Height(v.height - 2).Render(content)
}

func (v skillView) renderDetailPane() string {
	borderStyle := StyleBorder
	if v.detailFocused {
		borderStyle = StyleActiveBorder
	}
	return borderStyle.Width(v.detailW).Height(v.height - 2).Render(v.detail.View())
}
