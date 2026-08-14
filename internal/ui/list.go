package ui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/thenickygee/mirage/internal/agent"
	"github.com/thenickygee/mirage/internal/server"
)

// ─── Row model ───────────────────────────────────────────────────────────────

type rowKind int

const (
	rowKindAgent     rowKind = iota
	rowKindApprovals         // pending approvals row (shown when server connected with pending permissions)
	rowKindDivider           // visual divider before built-in agents
)

type listRow struct {
	kind  rowKind
	agent *agent.Agent // nil for the defaults row
}

// ─── agentList ───────────────────────────────────────────────────────────────

type agentList struct {
	agents     []*agent.Agent
	cachedRows []listRow
	cursor     int
	width      int
	height     int
	offset     int
	lastKey    string
	pool       *server.Pool
}

func newAgentList(agents []*agent.Agent) agentList {
	return agentList{agents: agents}
}

func (l *agentList) setSize(w, h int) {
	l.width = w
	l.height = h
}

func (l *agentList) setAgents(agents []*agent.Agent) {
	l.agents = agents
	l.cachedRows = nil // invalidate cache
	total := len(l.rows())
	if l.cursor >= total && total > 0 {
		l.cursor = total - 1
	}
}

// rows builds the flat list: approvals row (if pending), then all agents
// (file-based first, then built-in). Results are cached until invalidated.
func (l *agentList) rows() []listRow {
	if l.cachedRows != nil {
		return l.cachedRows
	}
	var out []listRow
	if l.pool != nil && l.pool.PendingCount() > 0 {
		out = append(out, listRow{kind: rowKindApprovals})
	}
	var builtins []listRow
	for _, a := range l.agents {
		if a.Source == agent.SourceBuiltin {
			builtins = append(builtins, listRow{kind: rowKindAgent, agent: a})
		} else {
			out = append(out, listRow{kind: rowKindAgent, agent: a})
		}
	}
	if len(builtins) > 0 {
		out = append(out, builtins...)
	}
	l.cachedRows = out
	return out
}

// selected returns the currently selected file agent, or nil when on the
// defaults row.
func (l *agentList) selected() *agent.Agent {
	rows := l.rows()
	if l.cursor < 0 || l.cursor >= len(rows) {
		return nil
	}
	return rows[l.cursor].agent
}

// onApprovals reports whether the cursor is on the "approvals" row.
func (l *agentList) onApprovals() bool {
	rows := l.rows()
	if l.cursor < 0 || l.cursor >= len(rows) {
		return false
	}
	return rows[l.cursor].kind == rowKindApprovals
}

func (l *agentList) moveUp() {
	if l.cursor > 0 {
		l.cursor--
		if l.cursor < l.offset {
			l.offset = l.cursor
		}
	}
}

func (l *agentList) moveDown() {
	total := len(l.rows())
	if l.cursor < total-1 {
		l.cursor++
		vh := l.visibleHeight()
		if l.cursor >= l.offset+vh {
			l.offset++
		}
	}
}

func (l *agentList) visibleHeight() int {
	// Each list item renders as 3 lines (1 content + Padding(1,1)).
	avail := l.height - 6
	vr := avail / 3
	if vr < 1 {
		vr = 1
	}
	return vr
}

func (l *agentList) clampOffset() {
	vr := l.visibleHeight()
	total := len(l.rows())
	if l.offset+vr > total {
		l.offset = total - vr
	}
	if l.offset < 0 {
		l.offset = 0
	}
}

func (l *agentList) halfPageDown() {
	step := l.visibleHeight() / 2
	if step < 1 {
		step = 1
	}
	total := len(l.rows())
	l.cursor += step
	if l.cursor >= total {
		l.cursor = total - 1
	}
	vh := l.visibleHeight()
	if l.cursor >= l.offset+vh {
		l.offset = l.cursor - vh + 1
	}
	l.clampOffset()
}

func (l *agentList) halfPageUp() {
	step := l.visibleHeight() / 2
	if step < 1 {
		step = 1
	}
	l.cursor -= step
	if l.cursor < 0 {
		l.cursor = 0
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	l.clampOffset()
}

func (l *agentList) pageDown() {
	step := l.visibleHeight()
	total := len(l.rows())
	l.cursor += step
	if l.cursor >= total {
		l.cursor = total - 1
	}
	vh := l.visibleHeight()
	if l.cursor >= l.offset+vh {
		l.offset = l.cursor - vh + 1
	}
	l.clampOffset()
}

func (l *agentList) pageUp() {
	step := l.visibleHeight()
	l.cursor -= step
	if l.cursor < 0 {
		l.cursor = 0
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	l.clampOffset()
}

func (l *agentList) jumpToBottom() {
	total := len(l.rows())
	if total == 0 {
		return
	}
	l.cursor = total - 1
	vh := l.visibleHeight()
	l.offset = l.cursor - vh + 1
	l.clampOffset()
}

func (l *agentList) jumpToTop() {
	l.cursor = 0
	l.offset = 0
}

// ─── Shared helpers (used by skillView / commandView / toolView) ──────────────

func listClampOffset(cursor, offset, total, visibleRows int) (int, int) {
	if offset+visibleRows > total {
		offset = total - visibleRows
	}
	if offset < 0 {
		offset = 0
	}
	return cursor, offset
}

func listHalfPageDown(cursor, offset, total, visibleRows int) (int, int) {
	step := visibleRows / 2
	if step < 1 {
		step = 1
	}
	cursor += step
	if cursor >= total {
		cursor = total - 1
	}
	if cursor >= offset+visibleRows {
		offset = cursor - visibleRows + 1
	}
	return listClampOffset(cursor, offset, total, visibleRows)
}

func listHalfPageUp(cursor, offset, _, visibleRows int) (int, int) {
	step := visibleRows / 2
	if step < 1 {
		step = 1
	}
	cursor -= step
	if cursor < 0 {
		cursor = 0
	}
	if cursor < offset {
		offset = cursor
	}
	return cursor, offset
}

func listPageDown(cursor, offset, total, visibleRows int) (int, int) {
	cursor += visibleRows
	if cursor >= total {
		cursor = total - 1
	}
	if cursor >= offset+visibleRows {
		offset = cursor - visibleRows + 1
	}
	return listClampOffset(cursor, offset, total, visibleRows)
}

func listPageUp(cursor, offset, _, visibleRows int) (int, int) {
	cursor -= visibleRows
	if cursor < 0 {
		cursor = 0
	}
	if cursor < offset {
		offset = cursor
	}
	return cursor, offset
}

func listJumpToBottom(total, visibleRows int) (int, int) {
	if total == 0 {
		return 0, 0
	}
	cursor := total - 1
	offset := cursor - visibleRows + 1
	_, offset = listClampOffset(cursor, offset, total, visibleRows)
	return cursor, offset
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (l agentList) Update(msg tea.Msg) (agentList, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			l.moveUp()
		case tea.MouseButtonWheelDown:
			l.moveDown()
		}
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, Keys.Up):
			l.moveUp()
			l.lastKey = ""
		case key.Matches(msg, Keys.Down):
			l.moveDown()
			l.lastKey = ""
		case key.Matches(msg, Keys.HalfPageDown):
			l.halfPageDown()
			l.lastKey = ""
		case key.Matches(msg, Keys.HalfPageUp):
			l.halfPageUp()
			l.lastKey = ""
		case key.Matches(msg, Keys.PageDown):
			l.pageDown()
			l.lastKey = ""
		case key.Matches(msg, Keys.PageUp):
			l.pageUp()
			l.lastKey = ""
		case key.Matches(msg, Keys.GoToBottom):
			l.jumpToBottom()
			l.lastKey = ""
		case key.Matches(msg, Keys.GoToTop):
			if l.lastKey == "g" {
				l.jumpToTop()
				l.lastKey = ""
			} else {
				l.lastKey = "g"
			}
		default:
			l.lastKey = ""
		}
	}
	return l, nil
}

// ─── View ────────────────────────────────────────────────────────────────────

var circleGlyphs = []string{"◌", "○", "◎", "◉"}

func (l agentList) View(focused bool, activeAgents map[string]bool, spinFrame int) string {
	borderStyle := StyleBorder
	titleStyle := StylePaneTitle
	if focused {
		borderStyle = StyleActiveBorder
		titleStyle = StylePaneTitleActive
	}

	innerW := l.width - 4
	innerH := l.height - 4

	rows := l.rows()
	total := len(rows)
	countStr := strconv.Itoa(len(l.agents))

	// Check if there are any busy sessions whose agent isn't in our list yet
	hasUnknownActive := false
	for k := range activeAgents {
		if strings.HasPrefix(k, "ses:") {
			hasUnknownActive = true
			break
		}
	}

	title := titleStyle.Render("AGENTS")
	var titleRight string
	if hasUnknownActive {
		glyph := circleGlyphs[spinFrame%len(circleGlyphs)]
		titleRight = StylePaneTitleActive.Render(glyph) + " " + StyleMuted.Render(countStr)
	} else {
		titleRight = StyleMuted.Render(countStr)
	}
	gap := innerW - len("AGENTS") - len(countStr)
	if hasUnknownActive {
		gap -= 2 // account for glyph + space
	}
	if gap < 1 {
		gap = 1
	}
	titleLine := title + strings.Repeat(" ", gap) + titleRight
	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, StyleSeparator.Render(strings.Repeat("─", innerW)))

	vh := (innerH - 2) / 3 // each padded item is 3 lines
	if vh < 1 {
		vh = 1
	}

	end := l.offset + vh
	if end > total {
		end = total
	}

	for i := l.offset; i < end; i++ {
		row := rows[i]

		if row.kind == rowKindApprovals {
			count := 0
			if l.pool != nil {
				count = l.pool.PendingCount()
			}
			var style lipgloss.Style
			if i == l.cursor {
				style = StyleListItemSelected
			} else {
				style = StyleListItem
			}
			prefix := "  "
			if i == l.cursor {
				prefix = "▶ "
			}
			badge := StyleBadgeDisabled.Render(fmt.Sprintf(" (%d)", count))
			lines = append(lines, style.Width(innerW).Render(prefix+"approvals"+badge))
			continue
		}

		a := row.agent
		var rowStyle lipgloss.Style
		var prefix string

		if i == l.cursor {
			prefix = "▶ "
			rowStyle = StyleListItemSelected
		} else if a.Disable {
			prefix = "  "
			rowStyle = StyleListItemDisabled
		} else {
			prefix = "  "
			rowStyle = StyleListItem
		}

		label := prefix + a.ID
		if a.Source == agent.SourceBuiltin {
			label += " " + StyleMuted.Render("❖")
		}
		if activeAgents[a.ID] {
			const pongWidth = 6
			period := 2 * (pongWidth - 1)
			f := spinFrame % period
			ballPos := f
			if f >= pongWidth {
				ballPos = period - f
			}
			var fill strings.Builder
			for i := 0; i < pongWidth; i++ {
				if i == ballPos {
					fill.WriteString(circleGlyphs[spinFrame%len(circleGlyphs)])
				} else {
					fill.WriteString("◌")
				}
				if i < pongWidth-1 {
					fill.WriteString(" ")
				}
			}
			label += " " + StylePaneTitleActive.Render(fill.String())
		}
		if a.Disable {
			label += "  " + StyleDim.Render("[off]")
		}
		lines = append(lines, rowStyle.Width(innerW).Render(label))
	}

	content := strings.Join(lines, "\n")
	return borderStyle.Width(l.width).Height(l.height - 2).Render(content)
}
