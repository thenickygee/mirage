package ui

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/thenickygee/mirage/internal/tool"
)

type toolView struct {
	tools         []*tool.Tool
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

func newToolView(tools []*tool.Tool) toolView {
	vp := viewport.New(0, 0)
	vp.KeyMap = viewportKeyMap()
	return toolView{
		tools:  tools,
		detail: vp,
	}
}

func (v *toolView) setSize(w, h int) {
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

func (v *toolView) selected() *tool.Tool {
	if len(v.tools) == 0 {
		return nil
	}
	return v.tools[v.cursor]
}

func (v *toolView) refreshDetail() {
	t := v.selected()
	if t == nil {
		v.detail.SetContent(StyleMuted.Render("  no tool selected"))
		return
	}
	v.detail.SetContent(v.renderTool(t))
}

func (v *toolView) renderTool(t *tool.Tool) string {
	var sb strings.Builder

	title := StyleDetailTitle.Render("⚙ " + strings.ToUpper(t.ID))
	sb.WriteString(title + "\n")
	sb.WriteString(StyleSeparator.Render(strings.Repeat("─", v.detail.Width)) + "\n\n")

	// Badge
	sb.WriteString(StyleBadgeTool.Render(" TOOL ") + "  " + StyleBadgeMode.Render(t.Ext) + "\n\n")

	// Description (extracted from source)
	desc := t.Description()
	sb.WriteString(StyleSectionHeader.Render("DESCRIPTION") + "\n")
	sb.WriteString(StyleSeparator.Render(strings.Repeat("╌", v.detail.Width)) + "\n")
	if desc != "" {
		wrapped := wordWrap(desc, v.detail.Width-2)
		sb.WriteString(StyleValue.Render("  "+strings.ReplaceAll(wrapped, "\n", "\n  ")) + "\n")
	} else {
		sb.WriteString(StyleDim.Render("  —") + "\n")
	}

	// Source
	if t.Content != "" {
		sb.WriteString("\n")
		sb.WriteString(StyleSectionHeader.Render("SOURCE") + "\n")
		sb.WriteString(StyleSeparator.Render(strings.Repeat("╌", v.detail.Width)) + "\n")
		highlighted := highlightSource(t.Content, t.Ext)
		if highlighted != "" {
			for _, line := range strings.Split(highlighted, "\n") {
				sb.WriteString("  " + line + "\n")
			}
		}
	}

	return sb.String()
}

func (v toolView) Update(msg tea.Msg) (toolView, tea.Cmd) {
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
			} else if msg.Button == tea.MouseButtonWheelDown && v.cursor < len(v.tools)-1 {
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
			if v.cursor < len(v.tools)-1 {
				v.cursor++
				if v.cursor >= v.offset+visibleRows {
					v.offset++
				}
				v.refreshDetail()
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.HalfPageDown):
			v.cursor, v.offset = listHalfPageDown(v.cursor, v.offset, len(v.tools), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.HalfPageUp):
			v.cursor, v.offset = listHalfPageUp(v.cursor, v.offset, len(v.tools), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.PageDown):
			v.cursor, v.offset = listPageDown(v.cursor, v.offset, len(v.tools), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.PageUp):
			v.cursor, v.offset = listPageUp(v.cursor, v.offset, len(v.tools), visibleRows)
			v.refreshDetail()
			v.lastKey = ""
		case key.Matches(msg, Keys.GoToBottom):
			v.cursor, v.offset = listJumpToBottom(len(v.tools), visibleRows)
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

func (v toolView) renderList() string {
	borderStyle := StyleBorder
	titleStyle := StylePaneTitle
	if !v.detailFocused {
		borderStyle = StyleActiveBorder
		titleStyle = StylePaneTitleActive
	}

	innerW := v.listW - 4
	innerH := v.height - 4

	countStr := strconv.Itoa(len(v.tools))
	title := titleStyle.Render("⚙ TOOLS")
	gap := innerW - len("⚙ TOOLS") - len(countStr)
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
	if end > len(v.tools) {
		end = len(v.tools)
	}

	for i := v.offset; i < end; i++ {
		t := v.tools[i]
		var prefix string
		rowStyle := StyleListItem
		if i == v.cursor {
			prefix = "▶ "
			rowStyle = StyleListItemSelected
		} else {
			prefix = "  "
		}
		label := fmt.Sprintf("%s%s%s", prefix, t.ID, StyleDim.Render(t.Ext))
		rows = append(rows, rowStyle.Width(innerW).Render(label))
	}

	content := strings.Join(rows, "\n")
	return borderStyle.Width(v.listW).Height(v.height - 2).Render(content)
}

func (v toolView) renderDetailPane() string {
	borderStyle := StyleBorder
	if v.detailFocused {
		borderStyle = StyleActiveBorder
	}
	return borderStyle.Width(v.detailW).Height(v.height - 2).Render(v.detail.View())
}

func highlightSource(content, ext string) string {
	var lexer chroma.Lexer
	switch ext {
	case ".ts", ".tsx":
		lexer = lexers.Get("typescript")
	case ".py":
		lexer = lexers.Get("python")
	default:
		return StyleMuted.Render(content)
	}
	if lexer == nil {
		return StyleMuted.Render(content)
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get("dracula")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}
	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return StyleMuted.Render(content)
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return StyleMuted.Render(content)
	}
	return buf.String()
}
