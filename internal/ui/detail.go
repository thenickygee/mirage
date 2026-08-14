package ui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"

	"github.com/thenickygee/mirage/internal/agent"
	"github.com/thenickygee/mirage/internal/server"
	"github.com/thenickygee/mirage/internal/stats"
)

var statsSpinnerFrames = []string{"•", "◦", "●", "◎", "⦿"}

type detailPane struct {
	viewport       viewport.Model
	agent          *agent.Agent
	showApprovals  bool
	approvalCursor int
	stats          map[string]*stats.AgentStats
	statsSpinFrame int
	width          int
	height         int
	focused        bool
	lastKey        string
	srv            *server.Client
	pool           *server.Pool
}

func (d *detailPane) pending() []*server.Permission {
	if d.pool != nil {
		return d.pool.Pending()
	}
	if d.srv != nil {
		return d.srv.Pending()
	}
	return nil
}

func newDetailPane() detailPane {
	vp := viewport.New(0, 0)
	vp.KeyMap = viewportKeyMap()
	return detailPane{viewport: vp}
}

func (d *detailPane) setAgent(a *agent.Agent) {
	d.agent = a
	d.showApprovals = false
	d.refreshContent()
}

func (d *detailPane) setApprovals() {
	d.agent = nil
	d.showApprovals = true
	d.approvalCursor = 0
	d.refreshContent()
}

func (d *detailPane) approvalMoveUp() {
	if d.approvalCursor > 0 {
		d.approvalCursor--
		d.refreshContent()
	}
}

func (d *detailPane) approvalMoveDown() {
	if d.pool == nil && d.srv == nil {
		return
	}
	pending := d.pending()
	if d.approvalCursor < len(pending)-1 {
		d.approvalCursor++
		d.refreshContent()
	}
}

func (d *detailPane) selectedApproval() *server.Permission {
	if d.pool == nil && d.srv == nil {
		return nil
	}
	pending := d.sortedPending()
	if d.approvalCursor < 0 || d.approvalCursor >= len(pending) {
		return nil
	}
	return pending[d.approvalCursor]
}

func (d *detailPane) sortedPending() []*server.Permission {
	pending := d.pending()
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].Time.Created < pending[j].Time.Created
	})
	return pending
}

func (d *detailPane) setSize(w, h int) {
	d.width = w
	d.height = h
	d.viewport.Width = w - 4
	d.viewport.Height = h - 4
	d.refreshContent()
}

func (d *detailPane) refreshContent() {
	if d.showApprovals {
		d.viewport.SetContent(d.renderApprovals())
		return
	}
	if d.agent == nil {
		d.viewport.SetContent(StyleMuted.Render("  no agent selected"))
		return
	}
	d.viewport.SetContent(d.renderAgent())
}

// renderTable renders a two-column key/value table with box-drawing borders.
// rows is a slice of [2]string{label, value}.
func (d *detailPane) renderTable(rows [][2]string) string {
	if len(rows) == 0 {
		return ""
	}
	w := d.viewport.Width
	if w <= 0 {
		w = 60
	}

	// Compute label column width from longest label (min 10, max 20)
	labelW := 10
	for _, r := range rows {
		if len(r[0]) > labelW {
			labelW = len(r[0])
		}
	}
	if labelW > 20 {
		labelW = 20
	}
	labelW += 2 // padding

	// Inner width: total - 2 border chars - 3 separators (│·label·│·value·│)
	innerW := w - 2
	valueW := innerW - labelW - 3 // 3 = "│" separators on each side + mid
	if valueW < 10 {
		valueW = 10
	}

	dim := StyleTableCellDim.Render
	muted := StyleTableCellMuted.Render
	cell := StyleTableCell.Render

	top := dim("┌") + dim(strings.Repeat("─", labelW)) + dim("┬") + dim(strings.Repeat("─", valueW)) + dim("┐")
	mid := dim("├") + dim(strings.Repeat("─", labelW)) + dim("┼") + dim(strings.Repeat("─", valueW)) + dim("┤")
	bot := dim("└") + dim(strings.Repeat("─", labelW)) + dim("┴") + dim(strings.Repeat("─", valueW)) + dim("┘")

	var sb strings.Builder
	sb.WriteString(top + "\n")
	for i, r := range rows {
		label := r[0]
		value := r[1]

		paddedLabel := fmt.Sprintf(" %-*s", labelW-1, label)
		paddedValue := truncate(value, valueW-1)
		paddedValue = fmt.Sprintf(" %-*s", valueW-1, paddedValue)

		var renderedValue string
		if value == "" || value == "—" {
			renderedValue = dim(paddedValue)
		} else {
			renderedValue = cell(paddedValue)
		}

		sb.WriteString(dim("│") + muted(paddedLabel) + dim("│") + renderedValue + dim("│") + "\n")

		if i < len(rows)-1 {
			sb.WriteString(mid + "\n")
		}
	}
	sb.WriteString(bot)
	return sb.String()
}

// renderPermTable renders a permissions table with allow/deny colored values.
func (d *detailPane) renderPermTable(perms map[string]interface{}) string {
	if len(perms) == 0 {
		return ""
	}
	w := d.viewport.Width
	if w <= 0 {
		w = 60
	}

	keys := make([]string, 0, len(perms))
	for k := range perms {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	labelW := 14
	for _, k := range keys {
		if len(k) > labelW {
			labelW = len(k)
		}
	}
	labelW += 2

	badgeW := 8
	innerW := w - 2
	valueW := innerW - labelW - 3
	if valueW < badgeW {
		valueW = badgeW
	}

	dim := StyleTableCellDim.Render
	muted := StyleTableCellMuted.Render

	top := dim("┌") + dim(strings.Repeat("─", labelW)) + dim("┬") + dim(strings.Repeat("─", valueW)) + dim("┐")
	// header row
	hLabel := fmt.Sprintf(" %-*s", labelW-1, "permission")
	hValue := fmt.Sprintf(" %-*s", valueW-1, "access")
	headerSep := dim("├") + dim(strings.Repeat("─", labelW)) + dim("┼") + dim(strings.Repeat("─", valueW)) + dim("┤")
	mid := dim("├") + dim(strings.Repeat("─", labelW)) + dim("┼") + dim(strings.Repeat("─", valueW)) + dim("┤")
	bot := dim("└") + dim(strings.Repeat("─", labelW)) + dim("┴") + dim(strings.Repeat("─", valueW)) + dim("┘")

	var sb strings.Builder
	sb.WriteString(top + "\n")
	sb.WriteString(dim("│") + StyleTableHeader.Render(hLabel) + dim("│") + StyleTableHeader.Render(hValue) + dim("│") + "\n")
	sb.WriteString(headerSep + "\n")

	for i, k := range keys {
		v := fmt.Sprintf("%v", perms[k])
		paddedLabel := fmt.Sprintf(" %-*s", labelW-1, k)

		var badge string
		switch v {
		case "allow":
			badge = StyleBadgeEnabled.Render("● allow")
		case "deny":
			badge = StyleBadgeDisabled.Render("● deny")
		default:
			badge = StyleBadgeMode.Render(v)
		}
		paddedBadge := " " + badge + strings.Repeat(" ", max(0, valueW-1-len(v)-3))

		sb.WriteString(dim("│") + muted(paddedLabel) + dim("│") + paddedBadge + dim("│") + "\n")

		if i < len(keys)-1 {
			sb.WriteString(mid + "\n")
		}
	}
	sb.WriteString(bot)
	return sb.String()
}

// renderBar renders a single horizontal bar graph row.
// label is left-padded to labelW, barW is the total bar area width.
func (d *detailPane) renderBar(label string, value int64, maxVal int64, labelW int, barW int) string {
	filled := 0
	if maxVal > 0 && barW > 0 {
		filled = int(float64(value) / float64(maxVal) * float64(barW))
		if filled > barW {
			filled = barW
		}
	}
	empty := barW - filled

	paddedLabel := fmt.Sprintf(" %-*s", labelW-1, label)
	bar := StyleBarFilled.Render(strings.Repeat("█", filled)) +
		StyleBarEmpty.Render(strings.Repeat("░", empty))
	valueStr := StyleTableCell.Render(" " + formatTokens(value))

	dim := StyleTableCellDim.Render
	muted := StyleTableCellMuted.Render

	return dim("│") + muted(paddedLabel) + dim("│") + bar + valueStr + dim("│")
}

// renderStatsTable renders the usage stats: a summary table plus token bar graphs.
func (d *detailPane) renderStatsTable(s *stats.AgentStats) string {
	w := d.viewport.Width
	if w <= 0 {
		w = 60
	}

	// Summary rows (non-token stats)
	type statKV struct{ k, v string }
	summary := []statKV{
		{"runs", fmt.Sprintf("%d", s.Runs)},
	}
	if s.Cost > 0 {
		summary = append(summary, statKV{"cost", fmt.Sprintf("$%.4f", s.Cost)})
	}
	if !s.LastUsed.IsZero() {
		summary = append(summary, statKV{"last used", s.LastUsed.Format("2006-01-02")})
	}
	rows := make([][2]string, len(summary))
	for i, f := range summary {
		rows[i] = [2]string{f.k, f.v}
	}

	var sb strings.Builder
	sb.WriteString(d.renderTable(rows))
	sb.WriteString("\n")

	// Bar graph section for token metrics
	type barEntry struct {
		label string
		value int64
	}
	bars := []barEntry{
		{"input tokens", s.InputTokens},
		{"output tokens", s.OutputTokens},
		{"cache read", s.CacheRead},
	}

	labelW := 14
	for _, b := range bars {
		if len(b.label) > labelW {
			labelW = len(b.label)
		}
	}
	labelW += 2

	// valueStr width: longest formatted token value + 1 space prefix
	valDisplayW := 8
	maxVal := s.InputTokens
	if s.OutputTokens > maxVal {
		maxVal = s.OutputTokens
	}
	if s.CacheRead > maxVal {
		maxVal = s.CacheRead
	}

	// total inner width = labelW + "│" + barW + valueStr + "│"
	// barW = w - 2(borders) - labelW - 1(mid sep) - valDisplayW - 1(right border)
	barW := w - 2 - labelW - 1 - valDisplayW - 1
	if barW < 4 {
		barW = 4
	}

	dim := StyleTableCellDim.Render
	topBar := dim("┌") + dim(strings.Repeat("─", labelW)) + dim("┬") + dim(strings.Repeat("─", barW+valDisplayW)) + dim("┐")
	midBar := dim("├") + dim(strings.Repeat("─", labelW)) + dim("┼") + dim(strings.Repeat("─", barW+valDisplayW)) + dim("┤")
	botBar := dim("└") + dim(strings.Repeat("─", labelW)) + dim("┴") + dim(strings.Repeat("─", barW+valDisplayW)) + dim("┘")

	sb.WriteString(topBar + "\n")
	for i, b := range bars {
		sb.WriteString(d.renderBar(b.label, b.value, maxVal, labelW, barW))
		sb.WriteString("\n")
		if i < len(bars)-1 {
			sb.WriteString(midBar + "\n")
		}
	}
	sb.WriteString(botBar)

	return sb.String()
}

func (d *detailPane) renderApprovals() string {
	if d.pool == nil && d.srv == nil {
		return StyleMuted.Render("  no server connected")
	}
	pending := d.sortedPending()
	if len(pending) == 0 {
		return StyleMuted.Render("  no pending approvals")
	}

	// Clamp cursor
	if d.approvalCursor >= len(pending) {
		d.approvalCursor = len(pending) - 1
	}

	var sb strings.Builder
	sep := StyleSeparator.Render(strings.Repeat("─", d.viewport.Width))

	title := StyleDetailTitle.Render("pending approvals")
	badge := "  " + StyleBadgeDisabled.Render(fmt.Sprintf("(%d)", len(pending)))
	sb.WriteString(title + badge + "\n")
	sb.WriteString(sep + "\n\n")

	for i, p := range pending {
		prefix := "  "
		if i == d.approvalCursor {
			prefix = "▶ "
		}
		typeLabel := StyleLabel.Render(p.Type)
		titleStr := StyleValue.Render(p.Title)
		sb.WriteString(prefix + fmt.Sprintf("%d. ", i+1) + typeLabel + "\n")
		sb.WriteString("     " + titleStr + "\n")
		if pat := p.PatternString(); pat != "" {
			sb.WriteString("     " + StyleMuted.Render("pattern: "+pat) + "\n")
		}
		sb.WriteString("     " + StyleMuted.Render("session: "+p.SessionID[:min(12, len(p.SessionID))]+"…") + "\n")
		sb.WriteString("\n")
	}

	sb.WriteString(sep + "\n")
	hints := []string{
		StyleStatusKey.Render("j/k") + " " + StyleMuted.Render("navigate"),
		StyleStatusKey.Render("y") + " " + StyleMuted.Render("approve once"),
		StyleStatusKey.Render("Y") + " " + StyleMuted.Render("approve always"),
		StyleStatusKey.Render("n") + " " + StyleMuted.Render("reject"),
	}
	sb.WriteString(strings.Join(hints, "  "))

	return sb.String()
}

func (d *detailPane) renderAgent() string {
	a := d.agent
	var sb strings.Builder

	sep := StyleSeparator.Render(strings.Repeat("─", d.viewport.Width))

	// Title row: @id  [status]  [mode]  [builtin]
	title := StyleDetailTitle.Render("@" + strings.ToLower(a.ID))
	statusBadge := StyleBadgeEnabled.Render("● active")
	if a.Disable {
		statusBadge = StyleBadgeDisabled.Render("○ disabled")
	}
	modeBadge := ""
	if a.Mode != "" {
		modeBadge = "  " + StyleBadgeMode.Render(strings.ToLower(a.Mode))
	}
	builtinBadge := ""
	if a.Source == agent.SourceBuiltin {
		builtinBadge = "  " + StyleBadgeBuiltin.Render("[builtin]")
	}
	sb.WriteString(title + "  " + statusBadge + modeBadge + builtinBadge + "\n")
	sb.WriteString(sep + "\n\n")

	// Config fields table
	temp := "—"
	if a.Temperature != nil {
		temp = fmt.Sprintf("%.2f", *a.Temperature)
	}
	topP := "—"
	if a.TopP != nil {
		topP = fmt.Sprintf("%.2f", *a.TopP)
	}
	steps := "—"
	if a.Steps != nil {
		steps = fmt.Sprintf("%d", *a.Steps)
	}
	hidden := "no"
	if a.Hidden {
		hidden = "yes"
	}

	configRows := [][2]string{}
	if a.Model != "" {
		configRows = append(configRows, [2]string{"model", a.Model})
	}
	if a.Color != "" {
		configRows = append(configRows, [2]string{"color", a.Color})
	}
	configRows = append(configRows,
		[2]string{"temperature", temp},
		[2]string{"top p", topP},
		[2]string{"steps", steps},
		[2]string{"hidden", hidden},
	)
	sb.WriteString(d.renderTable(configRows))
	sb.WriteString("\n\n")

	// Permissions
	if len(a.Permission) > 0 {
		sb.WriteString(StyleSectionHeader.Render("permissions") + "\n")
		sb.WriteString(d.renderPermTable(a.Permission))
		sb.WriteString("\n\n")
	}

	// Description
	if a.Description != "" {
		sb.WriteString(StyleSectionHeader.Render("description") + "\n")
		sb.WriteString(sep + "\n")
		sb.WriteString(renderMarkdown(a.Description, d.viewport.Width) + "\n")
	}

	// Usage stats
	sb.WriteString(StyleSectionHeader.Render("usage stats") + "\n")
	if d.stats == nil {
		frame := statsSpinnerFrames[d.statsSpinFrame%len(statsSpinnerFrames)]
		sb.WriteString(StyleMuted.Render("  "+frame+" Loading stats...") + "\n\n")
	} else if s, ok := d.stats[strings.ToLower(a.ID)]; ok {
		sb.WriteString(d.renderStatsTable(s))
		sb.WriteString("\n\n")
	} else {
		sb.WriteString(StyleMuted.Render("  No usage data") + "\n\n")
	}

	// System prompt
	sb.WriteString(StyleSectionHeader.Render("system prompt") + "\n")
	sb.WriteString(sep + "\n")
	if a.Prompt != "" {
		sb.WriteString(renderMarkdown(a.Prompt, d.viewport.Width) + "\n")
	} else {
		sb.WriteString(StyleMuted.Render("  (no system prompt defined)") + "\n\n")
	}

	sb.WriteString(sep + "\n")
	toggleDesc := "disable"
	if a.Disable {
		toggleDesc = "enable"
	}
	keys := []struct{ key, desc string }{
		{"e", "edit"},
		{"d", toggleDesc},
		{"n", "new"},
	}
	var hints []string
	for _, k := range keys {
		hints = append(hints, StyleStatusKey.Render(k.key)+" "+StyleMuted.Render(k.desc))
	}
	sb.WriteString(strings.Join(hints, "  "))

	return sb.String()
}

func (d detailPane) Update(msg tea.Msg) (detailPane, tea.Cmd) {
	var cmd tea.Cmd
	if d.focused {
		if km, ok := msg.(tea.KeyMsg); ok {
			switch {
			case key.Matches(km, Keys.GoToBottom):
				d.viewport.GotoBottom()
				d.lastKey = ""
				return d, nil
			case key.Matches(km, Keys.GoToTop):
				if d.lastKey == "g" {
					d.viewport.GotoTop()
					d.lastKey = ""
					return d, nil
				}
				d.lastKey = "g"
				return d, nil
			default:
				d.lastKey = ""
			}
		}
		d.viewport, cmd = d.viewport.Update(msg)
	}
	return d, cmd
}

func (d detailPane) View(focused bool) string {
	borderStyle := StyleBorder
	if focused {
		borderStyle = StyleActiveBorder
	}
	return borderStyle.Width(d.width).Height(d.height - 2).Render(d.viewport.View())
}

// viewportKeyMap returns a viewport KeyMap that only uses ctrl+* and pgup/pgdn
// scroll keys, leaving single-letter keys free for app-level bindings.
func viewportKeyMap() viewport.KeyMap {
	km := viewport.DefaultKeyMap()
	km.PageDown = key.NewBinding(key.WithKeys("ctrl+f", "pgdown"), key.WithHelp("ctrl+f/pgdn", "page down"))
	km.PageUp = key.NewBinding(key.WithKeys("ctrl+b", "pgup"), key.WithHelp("ctrl+b/pgup", "page up"))
	km.HalfPageDown = key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "½ page down"))
	km.HalfPageUp = key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "½ page up"))
	// Keep arrow + j/k for up/down; clear h/l to avoid conflicting with pane focus keys
	km.Up = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up"))
	km.Down = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down"))
	km.Left = key.NewBinding(key.WithKeys())
	km.Right = key.NewBinding(key.WithKeys())
	return km
}

func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// renderContextBar renders an inline context-usage bar like "[████░░░░] 42%".
// barW is the number of bar characters (excluding brackets).
// bg, when non-nil, is applied as the background color to all text segments
// so the bar blends into a highlighted card background.
func renderContextBar(used, total int64, barW int, bg ...color.Color) string {
	pct := 0.0
	if total > 0 {
		pct = float64(used) / float64(total) * 100
		if pct > 100 {
			pct = 100
		}
	}
	filled := int(pct / 100 * float64(barW))
	if filled > barW {
		filled = barW
	}
	empty := barW - filled
	filledStyle := StyleBarFilled
	emptyStyle := StyleBarEmpty
	plainStyle := lipgloss.NewStyle()
	if len(bg) > 0 && bg[0] != nil {
		filledStyle = filledStyle.Background(bg[0])
		emptyStyle = emptyStyle.Background(bg[0])
		plainStyle = plainStyle.Background(bg[0])
	}
	bar := filledStyle.Render(strings.Repeat("█", filled)) +
		emptyStyle.Render(strings.Repeat("░", empty))
	return plainStyle.Render("[") + bar + plainStyle.Render("] "+fmt.Sprintf("%.1f%%", pct))
}

var (
	cachedRenderer      *glamour.TermRenderer
	cachedRendererWidth int
)

func getMarkdownRenderer(width int) *glamour.TermRenderer {
	if cachedRenderer != nil && cachedRendererWidth == width {
		return cachedRenderer
	}
	style := styles.DarkStyleConfig
	margin := uint(0)
	style.Document.Margin = &margin
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	cachedRenderer = r
	cachedRendererWidth = width
	return r
}

func renderMarkdown(content string, width int) string {
	if width <= 0 {
		width = 80
	}
	r := getMarkdownRenderer(width)
	if r == nil {
		return content
	}
	rendered, err := r.Render(content)
	if err != nil {
		return content
	}
	return rendered
}

func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	words := strings.Fields(s)
	var lines []string
	var current strings.Builder
	for _, w := range words {
		if current.Len()+len(w)+1 > width && current.Len() > 0 {
			lines = append(lines, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString(" ")
		}
		current.WriteString(w)
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-1]) + "…"
}
