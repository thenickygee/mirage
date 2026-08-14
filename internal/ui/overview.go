
package ui

import (
	"fmt"
	"image/color"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/thenickygee/mirage/internal/config"
	"github.com/thenickygee/mirage/internal/server"
)

type sendResultMsg struct {
	err error
}

type overviewModel struct {
	connectedServers []server.ConnectedServerInfo
	cursor           int
	offset           int
	lastKey          string
	listW            int
	detailW          int
	height           int
	detail           viewport.Model
	detailHeader     string // fixed header above scrollable content
	detailFocused    bool
	detailLastKey    string
	lastSelectedID   string // track cursor changes to refresh detail
	lastFetchedID    string // track which session output was last fetched
	insertMode       bool
	inputBuf         string
	inputCursor      int
	pool             *server.Pool
	sendErr          string
	pendingSelectURL string // URL to auto-select once it appears in connectedServers
	listOnly         bool   // when true, only show list pane — skip detail fetching/rendering
	display          config.InstanceDisplay

	// Markdown render cache: skip expensive re-render when output hasn't changed.
	cachedOutputLen     int
	cachedOutputLastTxt string
	cachedRenderedBody  string
	cachedRenderWidth   int
	cachedRenderSession string
	rowToCursor         []int // maps visible content row to cursor index (-1 for non-item rows)
}

func newOverviewModel() overviewModel {
	vp := viewport.New(0, 0)
	return overviewModel{detail: vp}
}

func (m *overviewModel) setSize(w, h int) {
	if m.listOnly {
		m.listW = w
		m.detailW = 0
	} else {
		m.listW = w / 3
		if m.listW < 22 {
			m.listW = 22
		} else if m.listW > 40 {
			m.listW = 40
		}
		m.detailW = w - m.listW
	}
	m.height = h
	if m.listOnly {
		return
	}
	m.detail.Width = m.detailW - 4
	headerLines := 0
	if m.detailHeader != "" {
		headerLines = strings.Count(m.detailHeader, "\n") + 3 // +1 for last line, +1 for bottom border, +1 for gap
	}
	inputHeight := 0
	if m.insertMode {
		inputHeight = 3 // input box height
	}
	m.detail.Height = h - 4 - headerLines - inputHeight
	m.refreshDetail()
}

func (m *overviewModel) refresh(connectedServers []server.ConnectedServerInfo) {
	m.connectedServers = connectedServers
	ordered := orderedServers(m.connectedServers)
	if m.pendingSelectURL != "" {
		for i, srv := range ordered {
			if srv.URL == m.pendingSelectURL {
				m.cursor = i
				m.pendingSelectURL = ""
				break
			}
		}
	}
	if m.cursor >= len(ordered) {
		m.cursor = max(0, len(ordered)-1)
	}
	m.refreshDetail()
}

// selectByURL sets the cursor to the entry matching url, or stores it as
// pending if the entry hasn't appeared in connectedServers yet.
func (m *overviewModel) selectByURL(url string) {
	ordered := orderedServers(m.connectedServers)
	for i, srv := range ordered {
		if srv.URL == url {
			m.cursor = i
			m.pendingSelectURL = ""
			return
		}
	}
	m.pendingSelectURL = url
}

func (m *overviewModel) selectedServer() *server.ConnectedServerInfo {
	ordered := orderedServers(m.connectedServers)
	if len(ordered) == 0 || m.cursor >= len(ordered) {
		return nil
	}
	srv := ordered[m.cursor]
	return &srv
}

func (m *overviewModel) selected() *server.TrackedSession {
	ordered := orderedServers(m.connectedServers)
	if len(ordered) == 0 || m.cursor >= len(ordered) {
		return nil
	}
	srv := ordered[m.cursor]
	if len(srv.Sessions) == 0 {
		return nil
	}
	ts := srv.Sessions[0]
	for _, s := range srv.Sessions {
		if s.Busy {
			ts = s
			break
		}
	}
	return ts
}

func (m *overviewModel) refreshDetail() {
	if m.listOnly {
		return
	}
	sel := m.selected()
	if sel == nil {
		m.detailHeader = ""
		m.detail.SetContent(StyleMuted.Render("  no session selected"))
		m.lastSelectedID = ""
		return
	}

	// Build fixed header (agent/model/tool + stats) that stays pinned above the viewport.
	var hdr strings.Builder
	srv := m.selectedServer()
	title := StyleDetailTitle.Background(colorShadow).Render("◈ " + strings.ToUpper(projectName(*srv)))
	hdr.WriteString(title + "\n")

	// Two-column grid: left column = agent/model/tool, right column = stats.
	halfW := m.detail.Width / 2
	if halfW < 20 {
		halfW = 20
	}

	// Left column lines.
	lbl := StyleLabel.Background(colorShadow)
	val := StyleValue.Background(colorShadow)
	var leftLines []string
	if sel.AgentName != "" {
		leftLines = append(leftLines, lbl.Render("AGENT")+" "+val.Render("@"+sel.AgentName))
	}
	if sel.ModelID != "" {
		leftLines = append(leftLines, lbl.Render("MODEL")+" "+val.Render(sel.ModelID))
	}
	if sel.ToolName != "" {
		leftLines = append(leftLines, lbl.Render("TOOL ")+" "+val.Render(sel.ToolName))
	}

	// Right column lines.
	var rightLines []string
	rightLines = append(rightLines, lbl.Render("MSGS")+" "+val.Render(strconv.Itoa(sel.MessageCount)))
	rightLines = append(rightLines, lbl.Render("IN")+" "+val.Render(formatTokens(sel.InputTokens))+" "+lbl.Render("OUT")+" "+val.Render(formatTokens(sel.OutputTokens)))
	if sel.ContextWindow > 0 {
		rightLines = append(rightLines, lbl.Render("CTX")+" "+val.Render(renderContextBar(sel.LastInputTokens+sel.LastCacheRead, sel.ContextWindow, 8)))
	}

	// Merge columns side by side.
	rows := len(leftLines)
	if len(rightLines) > rows {
		rows = len(rightLines)
	}
	leftStyle := lipgloss.NewStyle().Width(halfW).Background(colorShadow)
	pad := lipgloss.NewStyle().Background(colorShadow).Render(" ")
	for i := 0; i < rows; i++ {
		left := ""
		if i < len(leftLines) {
			left = leftLines[i]
		}
		right := ""
		if i < len(rightLines) {
			right = rightLines[i]
		}
		hdr.WriteString(pad + leftStyle.Render(left) + right + "\n")
	}

	m.detailHeader = strings.TrimRight(hdr.String(), "\n")

	// Recalculate viewport height to account for header lines.
	headerLines := strings.Count(m.detailHeader, "\n") + 3 // +1 for last line, +1 for bottom border, +1 for gap
	inputHeight := 0
	if m.insertMode {
		inputHeight = 3
	}
	m.detail.Height = m.height - 4 - headerLines - inputHeight

	// Build scrollable content (output only).
	// Use cached rendered markdown if output hasn't changed.
	outputLen := len(sel.OutputLines)
	detailW := m.detail.Width
	if detailW < 10 {
		detailW = 60
	}
	lastTxt := ""
	if outputLen > 0 {
		lastTxt = sel.OutputLines[outputLen-1].Text
	}
	cacheHit := m.cachedRenderSession == sel.ID &&
		m.cachedOutputLen == outputLen &&
		m.cachedOutputLastTxt == lastTxt &&
		m.cachedRenderWidth == detailW &&
		outputLen > 0

	var sb strings.Builder
	sb.WriteString(StyleSectionHeader.Render("OUTPUT") + "\n")
	sb.WriteString(StyleSeparator.Render(strings.Repeat("╌", m.detail.Width)) + "\n")

	if cacheHit {
		sb.WriteString(m.cachedRenderedBody)
	} else if len(sel.OutputLines) > 0 {
		// Group consecutive lines by role and render each block with role-appropriate styling.
		type block struct {
			role  string
			lines []string
		}
		var blocks []block
		for _, ol := range sel.OutputLines {
			if len(blocks) == 0 || blocks[len(blocks)-1].role != ol.Role {
				blocks = append(blocks, block{role: ol.Role, lines: []string{ol.Text}})
			} else {
				blocks[len(blocks)-1].lines = append(blocks[len(blocks)-1].lines, ol.Text)
			}
		}

		var body strings.Builder
		for _, b := range blocks {
			content := strings.Join(b.lines, "\n")
			rendered := renderMarkdown(content, detailW)
			if b.role == "user" {
				// User messages: left border divider to distinguish from assistant
				rendered = strings.TrimSpace(rendered)
				bordered := lipgloss.NewStyle().
					BorderStyle(lipgloss.ThickBorder()).
					BorderLeft(true).
					BorderForeground(colorAmberMid).
					PaddingLeft(1).
					Render(rendered)
				body.WriteString(bordered + "\n\n")
			} else {
				// Assistant messages: no decoration
				body.WriteString(rendered + "\n")
			}
		}
		m.cachedRenderedBody = body.String()
		m.cachedRenderSession = sel.ID
		m.cachedOutputLen = outputLen
		m.cachedOutputLastTxt = lastTxt
		m.cachedRenderWidth = detailW
		sb.WriteString(m.cachedRenderedBody)
	} else {
		if sel.Busy {
			sb.WriteString(StyleDim.Render("  waiting for output...") + "\n")
		} else {
			sb.WriteString(StyleDim.Render("  idle") + "\n")
		}
	}

	wasBottom := m.detail.AtBottom() || m.lastSelectedID != sel.ID
	m.detail.SetContent(sb.String())
	if wasBottom {
		m.detail.GotoBottom()
	}
	m.lastSelectedID = sel.ID
}

func (m overviewModel) Update(msg tea.Msg) (overviewModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if !m.listOnly && msg.X >= m.listW {
				var cmd tea.Cmd
				m.detail, cmd = m.detail.Update(msg)
				return m, cmd
			}
			ordered := orderedServers(m.connectedServers)
			n := len(ordered)
			if msg.Button == tea.MouseButtonWheelUp && m.cursor > 0 {
				m.cursor--
				m.refreshDetail()
			} else if msg.Button == tea.MouseButtonWheelDown && m.cursor < n-1 {
				m.cursor++
				m.refreshDetail()
			}
		}
		return m, nil
	case sendResultMsg:
		if msg.err != nil {
			m.sendErr = msg.err.Error()
		}
		return m, nil
	case tea.KeyMsg:
		// Insert mode: custom input captures all keys
		if m.insertMode {
			switch {
			case key.Matches(msg, Keys.Esc):
				m.insertMode = false
				m.inputBuf = ""
				m.inputCursor = 0
				m.refreshDetail()
				return m, nil
			case msg.String() == "enter":
				text := strings.TrimSpace(m.inputBuf)
				m.inputBuf = ""
				m.inputCursor = 0
				m.sendErr = ""
				m.refreshDetail()
				if text != "" && m.pool != nil {
					if sel := m.selected(); sel != nil {
						pool := m.pool
						sid := sel.ID
						return m, func() tea.Msg {
							return sendResultMsg{err: pool.SendMessage(sid, text)}
						}
					}
				}
				return m, nil
			case msg.String() == "shift+enter":
				m.inputBuf = m.inputBuf[:m.inputCursor] + "\n" + m.inputBuf[m.inputCursor:]
				m.inputCursor++
				return m, nil
			case msg.String() == "backspace":
				if m.inputCursor > 0 {
					m.inputBuf = m.inputBuf[:m.inputCursor-1] + m.inputBuf[m.inputCursor:]
					m.inputCursor--
				}
				return m, nil
			case msg.String() == "delete":
				if m.inputCursor < len(m.inputBuf) {
					m.inputBuf = m.inputBuf[:m.inputCursor] + m.inputBuf[m.inputCursor+1:]
				}
				return m, nil
			case msg.String() == "left":
				if m.inputCursor > 0 {
					m.inputCursor--
				}
				return m, nil
			case msg.String() == "right":
				if m.inputCursor < len(m.inputBuf) {
					m.inputCursor++
				}
				return m, nil
			case msg.String() == "home", msg.String() == "ctrl+a":
				m.inputCursor = 0
				return m, nil
			case msg.String() == "end", msg.String() == "ctrl+e":
				m.inputCursor = len(m.inputBuf)
				return m, nil
			case msg.String() == "ctrl+u":
				m.inputBuf = m.inputBuf[m.inputCursor:]
				m.inputCursor = 0
				return m, nil
			case msg.String() == "ctrl+k":
				m.inputBuf = m.inputBuf[:m.inputCursor]
				return m, nil
			case msg.String() == "ctrl+d":
				m.detail.HalfPageDown()
				return m, nil
			case msg.String() == "pgdown":
				m.detail.PageDown()
				return m, nil
			case msg.String() == "pgup":
				m.detail.PageUp()
				return m, nil
			case msg.String() == "down":
				m.detail.ScrollDown(3)
				return m, nil
			case msg.String() == "up":
				m.detail.ScrollUp(3)
				return m, nil
			default:
				// Insert printable runes
				r := msg.Runes
				if len(r) > 0 {
					ch := string(r)
					m.inputBuf = m.inputBuf[:m.inputCursor] + ch + m.inputBuf[m.inputCursor:]
					m.inputCursor += len(ch)
				}
				return m, nil
			}
		}

		if m.detailFocused {
			switch {
			case key.Matches(msg, Keys.Esc), key.Matches(msg, Keys.FocusPaneLeft):
				m.detailFocused = false
				m.detailLastKey = ""
				return m, nil
			case key.Matches(msg, Keys.InsertMode):
				if m.selected() != nil {
					m.insertMode = true
					m.inputBuf = ""
					m.inputCursor = 0
					m.refreshDetail()
					return m, nil
				}
			case key.Matches(msg, Keys.GoToBottom):
				m.detail.GotoBottom()
				m.detailLastKey = ""
				return m, nil
			case key.Matches(msg, Keys.GoToTop):
				if m.detailLastKey == "g" {
					m.detail.GotoTop()
					m.detailLastKey = ""
					return m, nil
				}
				m.detailLastKey = "g"
				return m, nil
			default:
				m.detailLastKey = ""
				var cmd tea.Cmd
				m.detail, cmd = m.detail.Update(msg)
				return m, cmd
			}
		}

		n := len(orderedServers(m.connectedServers))
		visibleRows := (m.height - 6) / 6
		if visibleRows < 1 {
			visibleRows = 1
		}
		switch {
		case key.Matches(msg, Keys.Up):
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.offset {
					m.offset--
				}
				m.refreshDetail()
			}
			m.lastKey = ""
		case key.Matches(msg, Keys.Down):
			if m.cursor < n-1 {
				m.cursor++
				if m.cursor >= m.offset+visibleRows {
					m.offset++
				}
				m.refreshDetail()
			}
			m.lastKey = ""
		case key.Matches(msg, Keys.GoToBottom):
			if n > 0 {
				m.cursor = n - 1
				m.offset = max(0, n-visibleRows)
				m.refreshDetail()
			}
			m.lastKey = ""
		case key.Matches(msg, Keys.GoToTop):
			if m.lastKey == "g" {
				m.cursor = 0
				m.offset = 0
				m.refreshDetail()
				m.lastKey = ""
			} else {
				m.lastKey = "g"
			}
		case key.Matches(msg, Keys.FocusPaneRight):
			if !m.listOnly {
				m.detailFocused = true
			}
			m.lastKey = ""
		default:
			m.lastKey = ""
		}
	}
	return m, nil
}

// OpenOverviewSessionCmd suspends the TUI and attaches to the running opencode
// server at serverURL. If serverURL is empty it falls back to launching a new
// opencode process for the given tracked session.
func OpenOverviewSessionCmd(ts *server.TrackedSession, serverURL string) tea.Cmd {
	var c *exec.Cmd
	if serverURL != "" {
		c = exec.Command("opencode", "attach", serverURL, "--session", ts.ID)
	} else {
		dir := ts.Directory
		if dir == "" {
			dir = "."
		}
		c = exec.Command("opencode", "--mdns", "--session", ts.ID)
		c.Dir = filepath.Clean(dir)
	}
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return sessionOpenDoneMsg{err: err}
	})
}

func projectName(srv server.ConnectedServerInfo) string {
	for _, ts := range srv.Sessions {
		if ts.Directory != "" {
			name := filepath.Base(ts.Directory)
			if name != "." && name != "/" {
				return name
			}
			return ts.Directory
		}
	}
	return srv.URL
}

// projectDir returns the cleaned project directory for a server, or "" if none.
func projectDir(srv server.ConnectedServerInfo) string {
	for _, ts := range srv.Sessions {
		if ts.Directory != "" {
			return filepath.Clean(ts.Directory)
		}
	}
	return ""
}

// serverGroup represents a set of instances sharing the same project directory.
type serverGroup struct {
	dir     string // "" means ungrouped
	name    string // display name (filepath.Base of dir)
	servers []server.ConnectedServerInfo
}

// buildServerGroups returns an ordered list of groups derived from servers.
// Groups with 2+ instances sharing a directory come first (sorted by dir),
// then solo instances (no dir match), then no-dir instances — all alphabetically.
func buildServerGroups(servers []server.ConnectedServerInfo) []serverGroup {
	// Map dir -> servers
	dirMap := make(map[string][]server.ConnectedServerInfo)
	var noDir []server.ConnectedServerInfo
	for _, srv := range servers {
		d := projectDir(srv)
		if d == "" {
			noDir = append(noDir, srv)
		} else {
			dirMap[d] = append(dirMap[d], srv)
		}
	}

	var groups []serverGroup

	// Multi-instance groups first, sorted by dir.
	var multiDirs []string
	var soloDirs []string
	for d, srvs := range dirMap {
		if len(srvs) >= 2 {
			multiDirs = append(multiDirs, d)
		} else {
			soloDirs = append(soloDirs, d)
		}
	}
	// Sort for stable ordering.
	for i := 1; i < len(multiDirs); i++ {
		for j := i; j > 0 && multiDirs[j] < multiDirs[j-1]; j-- {
			multiDirs[j], multiDirs[j-1] = multiDirs[j-1], multiDirs[j]
		}
	}
	for i := 1; i < len(soloDirs); i++ {
		for j := i; j > 0 && soloDirs[j] < soloDirs[j-1]; j-- {
			soloDirs[j], soloDirs[j-1] = soloDirs[j-1], soloDirs[j]
		}
	}

	for _, d := range multiDirs {
		groups = append(groups, serverGroup{
			dir:     d,
			name:    filepath.Base(d),
			servers: dirMap[d],
		})
	}
	for _, d := range soloDirs {
		groups = append(groups, serverGroup{
			dir:     d,
			name:    filepath.Base(d),
			servers: dirMap[d],
		})
	}
	for _, srv := range noDir {
		groups = append(groups, serverGroup{
			servers: []server.ConnectedServerInfo{srv},
		})
	}
	return groups
}

// orderedServers returns the flat list of servers in grouped display order.
func orderedServers(servers []server.ConnectedServerInfo) []server.ConnectedServerInfo {
	groups := buildServerGroups(servers)
	var out []server.ConnectedServerInfo
	for _, g := range groups {
		out = append(out, g.servers...)
	}
	return out
}

func (m *overviewModel) renderList(spinFrame int) string {
	borderStyle := StyleBorder
	titleStyle := StylePaneTitle
	if !m.detailFocused {
		borderStyle = StyleActiveBorder
		titleStyle = StylePaneTitleActive
	}

	innerW := m.listW - 4
	innerH := m.height - 4

	countStr := strconv.Itoa(len(m.connectedServers))
	title := titleStyle.Render("◈ INSTANCES")
	gap := innerW - len("◈ INSTANCES") - len(countStr)
	if gap < 1 {
		gap = 1
	}
	titleLine := title + strings.Repeat(" ", gap) + StyleMuted.Render(countStr)

	var rows []string
	rows = append(rows, titleLine)
	rows = append(rows, StyleSeparator.Render(strings.Repeat("─", innerW)))
	// Reset rowToCursor: first 2 rows are title+separator (non-clickable)
	m.rowToCursor = []int{-1, -1}

	ordered := orderedServers(m.connectedServers)

	if len(ordered) > 0 {
		groups := buildServerGroups(m.connectedServers)

		// Build a flat rendering plan: items are either group headers or instance entries.
		// We also need to map flat cursor index -> group/server.
		type renderItem struct {
			isHeader bool
			label    string // for headers
			srv      *server.ConnectedServerInfo
			idx      int  // cursor index (only for non-header items)
			grouped  bool // is this instance inside a multi-instance group?
		}

		var items []renderItem
		cursorIdx := 0
		for _, g := range groups {
			isGrouped := len(g.servers) >= 2
			if isGrouped {
				items = append(items, renderItem{isHeader: true, label: g.name})
			}
			for si := range g.servers {
				srv := g.servers[si]
				items = append(items, renderItem{
					isHeader: false,
					srv:      &srv,
					idx:      cursorIdx,
					grouped:  isGrouped,
				})
				cursorIdx++
			}
		}

		// Estimate visible rows (each instance takes ~5 rows + header takes 1).
		visibleRows := (innerH - 2) / 6
		if visibleRows < 1 {
			visibleRows = 1
		}
		end := m.offset + visibleRows
		if end > len(ordered) {
			end = len(ordered)
		}

		// Render items in range [offset, end) for instance entries.
		for _, item := range items {
			if item.isHeader {
				// Only render the header if at least one instance in this group is visible.
				rows = append(rows, "  "+StyleSectionHeader.Render(item.label))
				m.rowToCursor = append(m.rowToCursor, -1)
				continue
			}
			if item.idx < m.offset || item.idx >= end {
				continue
			}

			srv := item.srv
			indent := "  "
			if item.grouped {
				indent = "    "
			}
			innerIndent := indent + "  "

			name := truncate(projectName(*srv), innerW-6)
			selected := item.idx == m.cursor

			// Determine the active session for this server.
			var activeTS *server.TrackedSession
			if len(srv.Sessions) > 0 {
				activeTS = srv.Sessions[0]
				for _, s := range srv.Sessions {
					if s.Busy {
						activeTS = s
						break
					}
				}
				for _, s := range srv.Sessions {
					if s.WaitingForInput || s.PendingCount > 0 {
						activeTS = s
						break
					}
				}
			}

			waiting := !srv.IsNewInstance && activeTS != nil && (activeTS.WaitingForInput || activeTS.PendingCount > 0 || (activeTS.Busy && activeTS.ToolName == "question"))
			busy := !srv.IsNewInstance && activeTS != nil && activeTS.Busy

			yellowDot := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAA00"))

			// When this row is selected, use bg-aware style variants so every
			// inline render matches the highlight strip background (colorDark).
			mutedStyle := StyleMuted
			spinnerStyle := StyleOverviewSpinner
			agentActiveStyle := StyleOverviewAgentActive
			agentIdleStyle := StyleOverviewAgentIdle
			yellowDotStyle := yellowDot
			if selected {
				mutedStyle = StyleOverviewSelectedMuted
				spinnerStyle = StyleOverviewSelectedSpinner
				agentActiveStyle = StyleOverviewSelectedAgentActive
				agentIdleStyle = StyleOverviewSelectedAgentIdle
				yellowDotStyle = StyleOverviewSelectedYellowDot
			}

			dot := agentIdleStyle.Render("○")
			if !srv.Connected {
				dot = mutedStyle.Render("○")
			} else if waiting {
				if (spinFrame/2)%2 == 0 {
					dot = yellowDotStyle.Render("●")
				} else {
					dot = mutedStyle.Render("●")
				}
			} else if busy {
				dot = agentActiveStyle.Render("●")
			}

			var sectionRows []string

			// Build optional CTX bar for top-right corner in list-only mode.
			ctxBar := ""
			if m.display.ShowCtx && m.listOnly && activeTS != nil && activeTS.ContextWindow > 0 {
				var ctxBg []color.Color
				if selected {
					ctxBg = []color.Color{lipgloss.Color("#2A2A2A")}
				}
				ctxBar = mutedStyle.Render("ctx:") + renderContextBar(activeTS.LastInputTokens+activeTS.LastCacheRead, activeTS.ContextWindow, 6, ctxBg...)
			}

			// headerRow builds the instance name row with the CTX bar pinned to the
			// right border wall. When selected, every space segment is explicitly
			// styled with colorDark so no bare gaps break the highlight strip.
			selSp := func(n int) string {
				if selected {
					return StyleOverviewSelectedRow.Render(strings.Repeat(" ", n))
				}
				return strings.Repeat(" ", n)
			}
			headerRow := func(dotStr, labelStr string) string {
				if ctxBar == "" {
					return selSp(len(indent)) + dotStr + selSp(1) + labelStr
				}
				gap := innerW - len(indent) - 1 /*dot*/ - 1 /*space*/ - lipgloss.Width(labelStr) - lipgloss.Width(ctxBar)
				if gap < 1 {
					gap = 1
				}
				return selSp(len(indent)) + dotStr + selSp(1) + labelStr + selSp(gap) + ctxBar
			}

			if selected {
				label := agentActiveStyle.Render(name)
				sectionRows = append(sectionRows, "")
				sectionRows = append(sectionRows, headerRow(dot, label))
			} else if !srv.Connected {
				label := mutedStyle.Render(name)
				sectionRows = append(sectionRows, "")
				sectionRows = append(sectionRows, headerRow(dot, label))
			} else {
				label := agentActiveStyle.Render(name)
				sectionRows = append(sectionRows, "")
				sectionRows = append(sectionRows, headerRow(dot, label))
			}

			if !srv.Connected {
				statusText := "disconnected"
				if srv.DisconnectedAt != nil {
					remaining := 5 - int(time.Since(*srv.DisconnectedAt).Seconds())
					if remaining > 0 {
						statusText = fmt.Sprintf("instance disconnected · removing in %ds...", remaining)
					}
				}
				sectionRows = append(sectionRows, innerIndent+mutedStyle.Render(statusText))
			} else if srv.IsNewInstance {
				sectionRows = append(sectionRows, innerIndent+mutedStyle.Render("New Session"))
			} else if activeTS != nil {
				if waiting {
					agent := activeTS.AgentName
					if agent == "" {
						agent = "agent"
					}
					// Pulse only the "(waiting)" text: alternate between bright and dim every 2 frames.
					pulseOn := (spinFrame/2)%2 == 0
					waitTextStyle := yellowDotStyle
					if !pulseOn {
						waitTextStyle = mutedStyle
					}
					statusStr := waitTextStyle.Render(" (waiting)")
					agentStr := agentActiveStyle.Render(truncate("@"+agent, innerW-8))
					sectionRows = append(sectionRows, innerIndent+waitTextStyle.Render("◆")+selSp(1)+agentStr+statusStr)
					if activeTS.Title != "" {
						sectionRows = append(sectionRows, innerIndent+mutedStyle.Render(truncate(server.DisplaySessionTitle(activeTS.Title), innerW-6)))
					}
				} else if activeTS.Busy {
					agent := activeTS.AgentName
					if agent == "" {
						agent = "agent"
					}
					statusStr := spinnerStyle.Render(" (running)")
					spin := circleGlyphs[spinFrame%len(circleGlyphs)]
					indicator := spinnerStyle.Render(spin)
					agentStr := agentActiveStyle.Render(truncate("@"+agent, innerW-8))
					sectionRows = append(sectionRows, innerIndent+indicator+selSp(1)+agentStr+statusStr)
					if activeTS.Title != "" {
						sectionRows = append(sectionRows, innerIndent+mutedStyle.Render(truncate(server.DisplaySessionTitle(activeTS.Title), innerW-6)))
					}
				} else {
					agent := activeTS.AgentName
					if agent == "" {
						agent = "agent"
					}
					idleStatus := mutedStyle.Render(" (idle)")
					agentStr := mutedStyle.Render(truncate("@"+agent, innerW-8))
					sectionRows = append(sectionRows, innerIndent+agentStr+idleStatus)
					t := server.DisplaySessionTitle(activeTS.Title)
					if t == "" && len(activeTS.ID) > 0 {
						t = activeTS.ID[:min(12, len(activeTS.ID))]
					}
					sectionRows = append(sectionRows, innerIndent+mutedStyle.Render(truncate(t, innerW-6)))
				}
			}

			if srv.PendingCount > 0 {
				warn := fmt.Sprintf("⚠ %d pending", srv.PendingCount)
				sectionRows = append(sectionRows, innerIndent+StyleBadgeDisabled.Render(warn))
			}

			if m.listOnly && activeTS != nil {
				var statsParts []string
				if m.display.ShowModel && activeTS.ModelID != "" {
					statsParts = append(statsParts, activeTS.ModelID)
				}
				if m.display.ShowMsgs {
					statsParts = append(statsParts, fmt.Sprintf("msgs:%d", activeTS.MessageCount))
				}
				if m.display.ShowInOut {
					statsParts = append(statsParts,
						fmt.Sprintf("in:%s", formatTokens(activeTS.InputTokens)),
						fmt.Sprintf("out:%s", formatTokens(activeTS.OutputTokens)),
					)
				}
				if len(statsParts) > 0 {
					statsStr := strings.Join(statsParts, "  ")
					statsW := innerW - len(innerIndent)
					statsLine := lipgloss.NewStyle().Width(statsW).Align(lipgloss.Right).Render(mutedStyle.Render(statsStr))
					sectionRows = append(sectionRows, innerIndent+statsLine)
				}
			}

			sectionRows = append(sectionRows, innerIndent+StyleSeparator.Render(strings.Repeat("─", innerW-6)))

			if selected {
				highlightStyle := StyleOverviewSelectedRow.Width(innerW)
				for _, r := range sectionRows {
					rows = append(rows, highlightStyle.Render(r))
				}
			} else {
				rows = append(rows, sectionRows...)
			}
			// Mark these rows as belonging to this cursor index.
			for range sectionRows {
				m.rowToCursor = append(m.rowToCursor, item.idx)
			}
		}
	} else {
		rows = append(rows, "")
		rows = append(rows, StyleMuted.Render("  not connected"))
		m.rowToCursor = append(m.rowToCursor, -1, -1)
	}

	content := strings.Join(rows, "\n")
	// Populate rowToCursor for mouse click support.
	return borderStyle.Width(m.listW).Height(m.height - 2).Render(content)
}

func (m overviewModel) renderDetailPane() string {
	borderStyle := StyleBorder
	if m.detailFocused || m.insertMode {
		borderStyle = StyleActiveBorder
	}
	// Render fixed header with dark background above scrollable viewport content.
	var content string
	if m.detailHeader != "" {
		styledHeader := lipgloss.NewStyle().
			Background(colorShadow).
			Width(m.detailW - 4).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderTop(false).
			BorderLeft(false).
			BorderRight(false).
			BorderForeground(colorMid).
			Render(m.detailHeader)
		content = styledHeader + "\n" + m.detail.View()
	} else {
		content = m.detail.View()
	}
	if m.insertMode {
		content = content + "\n" + m.renderInput()
	}
	return borderStyle.Width(m.detailW).Height(m.height - 2).Render(content)
}

func (m overviewModel) renderInput() string {
	w := m.detailW - 6
	if w < 10 {
		w = 10
	}

	// Build the display text with a block cursor
	buf := m.inputBuf
	var display string
	if len(buf) == 0 {
		// Show placeholder with cursor at start
		cursor := lipgloss.NewStyle().
			Background(colorAmberMid).
			Foreground(colorWhite).
			Render(" ")
		placeholder := StyleMuted.Render("send a message...")
		display = cursor + placeholder
	} else {
		// Render text with cursor
		before := buf[:m.inputCursor]
		after := buf[m.inputCursor:]
		if len(after) > 0 {
			// Cursor on the next character
			_, size := utf8.DecodeRuneInString(after)
			cursorChar := after[:size]
			rest := after[size:]
			cursor := lipgloss.NewStyle().
				Background(colorAmberMid).
				Foreground(colorWhite).
				Render(cursorChar)
			display = before + cursor + rest
		} else {
			// Cursor at end — show block
			cursor := lipgloss.NewStyle().
				Background(colorAmberMid).
				Foreground(colorWhite).
				Render(" ")
			display = before + cursor
		}
	}

	inputBox := lipgloss.NewStyle().
		Width(w).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderForeground(colorAmberMid).
		Render(display)

	return inputBox
}
