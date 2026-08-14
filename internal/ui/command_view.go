package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/thenickygee/mirage/internal/command"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type commandView struct {
	commands      []*command.Command
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

func newCommandView(commands []*command.Command) commandView {
	vp := viewport.New(0, 0)
	vp.KeyMap = viewportKeyMap()
	return commandView{
		commands: commands,
		detail:   vp,
	}
}

func (v *commandView) setSize(w, h int) {
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

func (v *commandView) selected() *command.Command {
	if len(v.commands) == 0 {
		return nil
	}
	return v.commands[v.cursor]
}

func (v *commandView) refreshDetail() {
	c := v.selected()
	if c == nil {
		v.detail.SetContent(StyleMuted.Render("  no command selected"))
		return
	}
	v.detail.SetContent(v.renderCommand(c))
}

func (v *commandView) renderCommand(c *command.Command) string {
	var sb strings.Builder

	title := StyleDetailTitle.Render("◈ /" + strings.ToUpper(c.ID))
	sb.WriteString(title + "\n")
	sb.WriteString(StyleSeparator.Render(strings.Repeat("─", v.detail.Width)) + "\n\n")

	// Badges
	badges := []string{StyleBadgeCommand.Render(" COMMAND ")}
	if c.Subtask {
		badges = append(badges, StyleBadgeSubtask.Render(" SUBTASK "))
	}
	sb.WriteString(strings.Join(badges, "  ") + "\n\n")

	field := func(label, value string) string {
		if value == "" {
			return fmt.Sprintf("  %s  %s\n",
				StyleLabel.Render(fmt.Sprintf("%-12s", label)),
				StyleDim.Render("—"),
			)
		}
		return fmt.Sprintf("  %s  %s\n",
			StyleLabel.Render(fmt.Sprintf("%-12s", label)),
			StyleValue.Render(value),
		)
	}

	if c.Agent != "" {
		sb.WriteString(field("AGENT", "@"+c.Agent))
	}
	if c.Model != "" {
		sb.WriteString(field("MODEL", c.Model))
	}

	sb.WriteString("\n")
	sb.WriteString(StyleSectionHeader.Render("DESCRIPTION") + "\n")
	sb.WriteString(StyleSeparator.Render(strings.Repeat("╌", v.detail.Width)) + "\n")
	if c.Description != "" {
		sb.WriteString(renderMarkdown(c.Description, v.detail.Width) + "\n")
	} else {
		sb.WriteString(StyleDim.Render("  —") + "\n")
	}

	if c.Template != "" {
		sb.WriteString("\n")
		sb.WriteString(StyleSectionHeader.Render("TEMPLATE") + "\n")
		sb.WriteString(StyleSeparator.Render(strings.Repeat("╌", v.detail.Width)) + "\n")
		sb.WriteString(renderMarkdown(c.Template, v.detail.Width) + "\n")
	}

	return sb.String()
}

func (v commandView) Update(msg tea.Msg) (commandView, tea.Cmd) {
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
			} else if msg.Button == tea.MouseButtonWheelDown && v.cursor < len(v.commands)-1 {
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
			if v.cursor < len(v.commands)-1 {
				v.cursor++
				if v.cursor >= v.offset+visibleRows {
					v.offset++
				}
				v.refreshDetail()
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.HalfPageDown):
			v.cursor, v.offset = listHalfPageDown(v.cursor, v.offset, len(v.commands), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.HalfPageUp):
			v.cursor, v.offset = listHalfPageUp(v.cursor, v.offset, len(v.commands), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.PageDown):
			v.cursor, v.offset = listPageDown(v.cursor, v.offset, len(v.commands), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.PageUp):
			v.cursor, v.offset = listPageUp(v.cursor, v.offset, len(v.commands), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.GoToBottom):
			v.cursor, v.offset = listJumpToBottom(len(v.commands), visibleRows)
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

func (v commandView) renderList() string {
	borderStyle := StyleBorder
	titleStyle := StylePaneTitle
	if !v.detailFocused {
		borderStyle = StyleActiveBorder
		titleStyle = StylePaneTitleActive
	}

	innerW := v.listW - 4
	innerH := v.height - 4

	countStr := strconv.Itoa(len(v.commands))
	title := titleStyle.Render("◈ COMMANDS")
	gap := innerW - len("◈ COMMANDS") - len(countStr)
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
	if end > len(v.commands) {
		end = len(v.commands)
	}

	for i := v.offset; i < end; i++ {
		c := v.commands[i]
		var prefix string
		rowStyle := StyleListItem
		if i == v.cursor {
			prefix = "▶ "
			rowStyle = StyleListItemSelected
		} else {
			prefix = "  "
		}
		label := prefix + "/" + c.ID
		if c.Subtask {
			label += "  " + StyleDim.Render("↪")
		}
		rows = append(rows, rowStyle.Width(innerW).Render(label))
	}

	content := strings.Join(rows, "\n")
	return borderStyle.Width(v.listW).Height(v.height - 2).Render(content)
}

func (v commandView) renderDetailPane() string {
	borderStyle := StyleBorder
	if v.detailFocused {
		borderStyle = StyleActiveBorder
	}
	return borderStyle.Width(v.detailW).Height(v.height - 2).Render(v.detail.View())
}
