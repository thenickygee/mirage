package ui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/thenickygee/mirage/internal/session"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	lgv1 "github.com/charmbracelet/lipgloss"
)

type sessionView struct {
	allSessions []*session.Session // full unfiltered list
	sessions    []*session.Session // filtered (or allSessions when no query)
	searchInput textinput.Model
	searching   bool
	cursor      int
	flatOffset  int
	lastKey     string
	width       int
	height      int
	rowToCursor []int // maps visible content row to cursor index (-1 for non-item rows)
}

func newSessionSearchInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "type to filter sessions..."
	ti.Prompt = "  › "
	ti.CharLimit = 100
	ti.TextStyle = lgv1.NewStyle().Background(lgv1.Color("#141414"))
	ti.PromptStyle = lgv1.NewStyle().Foreground(lgv1.Color("#3A5200")).Background(lgv1.Color("#141414"))
	ti.PlaceholderStyle = lgv1.NewStyle().Foreground(lgv1.Color("#555555")).Background(lgv1.Color("#141414"))
	ti.Cursor.Style = lgv1.NewStyle().Background(lgv1.Color("#141414"))
	return ti
}

func newSessionView(sessions []*session.Session) sessionView {
	v := sessionView{
		allSessions: sessions,
		searchInput: newSessionSearchInput(),
	}
	v.applyFilter()
	return v
}

func (v *sessionView) setSessions(sessions []*session.Session) {
	v.allSessions = sessions
	v.applyFilter()
}

func (v *sessionView) applyFilter() {
	if v.searchInput.Value() == "" {
		v.sessions = v.allSessions
		return
	}
	q := strings.ToLower(v.searchInput.Value())
	var out []*session.Session
	for _, s := range v.allSessions {
		if strings.Contains(strings.ToLower(s.DisplayTitle()), q) ||
			strings.Contains(strings.ToLower(s.Directory), q) {
			out = append(out, s)
		}
	}
	v.sessions = out
	v.cursor = 0
}

func (v *sessionView) setSize(w, h int) {
	v.width = w
	v.height = h
}

func (v *sessionView) selected() *session.Session {
	if len(v.sessions) == 0 || v.cursor >= len(v.sessions) {
		return nil
	}
	return v.sessions[v.cursor]
}

func (v sessionView) Update(msg tea.Msg) (sessionView, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		n := len(v.sessions)
		if msg.Button == tea.MouseButtonWheelUp && v.cursor > 0 {
			v.cursor--
		} else if msg.Button == tea.MouseButtonWheelDown && v.cursor < n-1 {
			v.cursor++
		}
	case tea.KeyMsg:
		// Search input mode intercepts all keys
		if v.searching {
			switch msg.Type {
			case tea.KeyEscape:
				v.searching = false
				v.searchInput.SetValue("")
				v.searchInput.Blur()
				v.applyFilter()
			case tea.KeyEnter:
				v.searching = false
				v.searchInput.Blur()
			default:
				prev := v.searchInput.Value()
				var cmd tea.Cmd
				v.searchInput, cmd = v.searchInput.Update(msg)
				if v.searchInput.Value() != prev {
					v.applyFilter()
				}
				return v, cmd
			}
			return v, nil
		}
		// "/" opens search
		if msg.String() == "/" {
			v.searching = true
			v.searchInput.SetValue("")
			v.searchInput.Focus()
			v.applyFilter()
			v.lastKey = ""
			return v, nil
		}
		n := len(v.sessions)
		switch {
		case key.Matches(msg, Keys.Up):
			if v.cursor > 0 {
				v.cursor--
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.Down):
			if v.cursor < n-1 {
				v.cursor++
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.HalfPageDown):
			step := v.visibleRows() / 2
			if step < 1 {
				step = 1
			}
			v.cursor += step
			if v.cursor >= n {
				v.cursor = n - 1
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.HalfPageUp):
			step := v.visibleRows() / 2
			if step < 1 {
				step = 1
			}
			v.cursor -= step
			if v.cursor < 0 {
				v.cursor = 0
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.PageDown):
			v.cursor += v.visibleRows()
			if v.cursor >= n {
				v.cursor = n - 1
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.PageUp):
			v.cursor -= v.visibleRows()
			if v.cursor < 0 {
				v.cursor = 0
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.GoToBottom):
			if n > 0 {
				v.cursor = n - 1
			}
			v.lastKey = ""
		case key.Matches(msg, Keys.GoToTop):
			if v.lastKey == "g" {
				v.cursor = 0
				v.lastKey = ""
			} else {
				v.lastKey = "g"
			}
		case key.Matches(msg, Keys.OpenSession):
			// handled by parent app
			v.lastKey = ""
		default:
			v.lastKey = ""
		}
	}
	return v, nil
}

func (v sessionView) visibleRows() int {
	// border(2) + title(1) + sep(1) + padding(2) = 6
	r := v.height - 6
	if r < 1 {
		r = 1
	}
	return r
}

// OpenSessionCmd suspends the TUI and runs opencode for the given session.
func OpenSessionCmd(s *session.Session) tea.Cmd {
	dir := s.Directory
	if dir == "" {
		dir = "."
	}
	c := exec.Command("opencode", "--mdns", "--session", s.ID)
	c.Dir = filepath.Clean(dir)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return sessionOpenDoneMsg{err: err}
	})
}

type sessionOpenDoneMsg struct{ err error }

// newSessionServeMsg is returned when a background `opencode serve --mdns`
// process is healthy and ready to be attached to.
type newSessionServeMsg struct {
	url string
	dir string
	err error
}

// NewSessionInDirCmd starts a headless `opencode serve --mdns` in the given
// directory and waits for it to become reachable. The server process keeps
// running after the user detaches, so it stays visible in the instances overview.
func NewSessionInDirCmd(dir string) tea.Cmd {
	if dir == "" {
		dir = "."
	}
	return func() tea.Msg {
		cleanDir := filepath.Clean(dir)
		c := exec.Command("opencode", "serve", "--mdns")
		c.Dir = cleanDir
		c.Stdin = nil
		c.Stderr = nil

		// Capture stdout to read the server URL.
		stdout, err := c.StdoutPipe()
		if err != nil {
			return newSessionServeMsg{err: fmt.Errorf("creating stdout pipe: %w", err)}
		}

		if err := c.Start(); err != nil {
			return newSessionServeMsg{err: fmt.Errorf("starting opencode serve: %w", err)}
		}

		// Read whatever the process writes to stdout (expecting the URL).
		// Give it up to 10 seconds to start.
		urlCh := make(chan string, 1)
		go func() {
			buf := make([]byte, 4096)
			n, _ := stdout.Read(buf)
			if n > 0 {
				urlCh <- strings.TrimSpace(string(buf[:n]))
			}
			close(urlCh)
		}()

		select {
		case raw := <-urlCh:
			// Release the child so it isn't reaped when mirage exits.
			_ = c.Process.Release()
			// The output may contain the URL or other text; find an http URL.
			url := extractURL(raw)
			if url != "" {
				return newSessionServeMsg{url: url, dir: cleanDir}
			}
			// Couldn't parse URL, but process started; fall through to mDNS discovery.
			return newSessionServeMsg{dir: cleanDir}
		case <-time.After(10 * time.Second):
			_ = c.Process.Release()
			return newSessionServeMsg{err: fmt.Errorf("timed out waiting for opencode serve to start in %s", dir)}
		}
	}
}

// extractURL finds the first http/https URL in a string.
// Replaces 0.0.0.0 with 127.0.0.1 since the former isn't connectable.
func extractURL(s string) string {
	for _, word := range strings.Fields(s) {
		if strings.HasPrefix(word, "http://") || strings.HasPrefix(word, "https://") {
			u := strings.TrimRight(word, ".,;")
			u = strings.Replace(u, "://0.0.0.0", "://127.0.0.1", 1)
			return u
		}
	}
	return ""
}

func (v *sessionView) View() string {
	innerW := v.width - 4
	innerH := v.height - 4

	// ── flat item list (date headers + session rows) ──────────────────────────
	type listItem struct {
		header string
		sesIdx int
	}

	now := time.Now()
	today := now.Truncate(24 * time.Hour)
	yesterday := today.Add(-24 * time.Hour)
	weekAgo := today.Add(-7 * 24 * time.Hour)

	dateGroup := func(t time.Time) string {
		d := t.Truncate(24 * time.Hour)
		switch {
		case !d.Before(today):
			return "TODAY"
		case !d.Before(yesterday):
			return "YESTERDAY"
		case !d.Before(weekAgo):
			return "THIS WEEK"
		default:
			return t.Format("January 2006")
		}
	}

	var items []listItem
	lastGroup := ""
	for i, s := range v.sessions {
		if s.Depth == 0 {
			g := dateGroup(s.UpdatedAt())
			if g != lastGroup {
				items = append(items, listItem{header: g, sesIdx: -1})
				lastGroup = g
			}
		}
		items = append(items, listItem{sesIdx: i})
	}

	// Find flat index of cursor
	cursorFlat := 0
	for fi, item := range items {
		if item.sesIdx == v.cursor {
			cursorFlat = fi
			break
		}
	}

	visibleRows := innerH - 3 // title + sep + search bar + col header + dotted sep = 5 fixed rows; subtract 3 here (2 more below)
	if visibleRows < 1 {
		visibleRows = 1
	}

	flatOffset := v.flatOffset
	if cursorFlat < flatOffset {
		flatOffset = cursorFlat
	}
	if cursorFlat >= flatOffset+visibleRows {
		flatOffset = cursorFlat - visibleRows + 1
	}
	if flatOffset < 0 {
		flatOffset = 0
	}

	// ── column widths ─────────────────────────────────────────────────────────
	// title | directory | date  (ratio 40/45/15)
	titleColW := innerW * 40 / 100
	dirColW := innerW * 45 / 100
	dateColW := innerW - titleColW - dirColW - 4 // 4 for column gaps
	if dateColW < 10 {
		dateColW = 10
	}

	// ── header row ────────────────────────────────────────────────────────────
	colSep := StyleDim.Render("  ")
	colHeader := StyleTableHeader.Render(padRight("TITLE", titleColW)) +
		colSep +
		StyleTableHeader.Render(padRight("DIRECTORY", dirColW)) +
		colSep +
		StyleTableHeader.Render(padRight("UPDATED", dateColW))

	countStr := fmt.Sprintf("%d", len(v.sessions))
	if v.searchInput.Value() != "" {
		countStr = fmt.Sprintf("%d / %d", len(v.sessions), len(v.allSessions))
	}
	paneTitle := StylePaneTitleActive.Render("◈ SESSIONS")
	gap := innerW - len("◈ SESSIONS") - len(countStr)
	if gap < 1 {
		gap = 1
	}
	titleLine := paneTitle + strings.Repeat(" ", gap) + StyleMuted.Render(countStr)

	var rows []string
	rows = append(rows, titleLine)
	rows = append(rows, StyleSeparator.Render(strings.Repeat("─", innerW)))

	// ── search bar ────────────────────────────────────────────────────────────
	var searchBar string
	if v.searching {
		searchBar = v.searchInput.View()
	} else if v.searchInput.Value() != "" {
		searchBar = StyleMuted.Render("  › ") + StyleAppHeaderAccent.Render(v.searchInput.Value()) + "  " + StyleDim.Render("[esc to clear]")
	} else {
		searchBar = StyleDim.Render("  › press / to search")
	}
	rows = append(rows, searchBar)

	rows = append(rows, colHeader)
	rows = append(rows, StyleSeparator.Render(strings.Repeat("╌", innerW)))

	end := flatOffset + visibleRows - 3 // -3 for col header + dotted sep + extra search row offset
	if end > len(items) {
		end = len(items)
	}

	for fi := flatOffset; fi < end; fi++ {
		item := items[fi]
		if item.header != "" {
			rows = append(rows, StyleSessionDateGroup.Width(innerW).Render(item.header))
			continue
		}
		s := v.sessions[item.sesIdx]
		selected := item.sesIdx == v.cursor

		indent := strings.Repeat("  ", s.Depth)

		titleText := indent + s.DisplayTitle()
		dirText := shortenPath(s.Directory)
		dateText := ""
		if !s.UpdatedAt().IsZero() {
			dateText = relativeTime(s.UpdatedAt())
		}

		if selected {
			row := StyleSessionItemSelected.Width(innerW).Render(
				padRight(truncate(titleText, titleColW), titleColW) + "  " +
					padRight(truncate(dirText, dirColW), dirColW) + "  " +
					padRight(dateText, dateColW),
			)
			rows = append(rows, row)
		} else {
			titlePart := StyleSessionItem.Render(padRight(truncate(titleText, titleColW), titleColW))
			dirPart := StyleMuted.Render(padRight(truncate(dirText, dirColW), dirColW))
			datePart := StyleDim.Render(padRight(dateText, dateColW))
			rows = append(rows, titlePart+colSep+dirPart+colSep+datePart)
		}
	}

	content := strings.Join(rows, "\n")
	// Populate rowToCursor: maps each inner content row to a session cursor index.
	v.rowToCursor = make([]int, len(rows))
	for i := range v.rowToCursor {
		v.rowToCursor[i] = -1
	}
	// rows[0]=title, [1]=sep, [2]=search, [3]=colHeader, [4]=dottedSep, [5+]=items
	rowIdx := 5
	for fi := flatOffset; fi < end; fi++ {
		item := items[fi]
		if rowIdx >= len(v.rowToCursor) {
			break
		}
		if item.header != "" {
			v.rowToCursor[rowIdx] = -1
		} else {
			v.rowToCursor[rowIdx] = item.sesIdx
		}
		rowIdx++
	}
	return StyleActiveBorder.Width(v.width).Height(v.height - 2).Render(content)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func shortenPath(p string) string {
	// Simple: take last 2 path components
	parts := strings.Split(filepath.Clean(p), string(filepath.Separator))
	if len(parts) <= 2 {
		return p
	}
	return "~/" + strings.Join(parts[len(parts)-2:], "/")
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}
