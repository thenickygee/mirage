
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thenickygee/mirage/internal/session"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	lgv1 "github.com/charmbracelet/lipgloss"
)

// projectEntry groups sessions under a single working directory.
type projectEntry struct {
	directory    string
	sessions     []*session.Session
	lastUpdated  int64  // unix millis of most-recently-updated session
	activeAgent  string // agent name if any session in this dir is active, else ""
	activeSesCnt int    // number of active sessions in this directory
}

type dirsView struct {
	allProjects    []projectEntry    // full unfiltered list
	projects       []projectEntry    // filtered (or allProjects when no query)
	activeSessions map[string]string // sessionID -> agentName (from SSE)
	allSessions    []*session.Session
	searchInput    textinput.Model
	searching      bool
	cursor         int
	width          int
	height         int
	rowToCursor    []int // maps visible content row to cursor index (-1 for non-item rows)
}

func newDirsView(sessions []*session.Session) dirsView {
	v := dirsView{
		allSessions: sessions,
		searchInput: newDirsSearchInput(),
	}
	v.rebuild()
	return v
}

func newDirsSearchInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "type to filter directories..."
	ti.Prompt = "  › "
	ti.CharLimit = 100
	ti.TextStyle = lgv1.NewStyle().Background(lgv1.Color("#141414"))
	ti.PromptStyle = lgv1.NewStyle().Foreground(lgv1.Color("#3A5200")).Background(lgv1.Color("#141414"))
	ti.PlaceholderStyle = lgv1.NewStyle().Foreground(lgv1.Color("#555555")).Background(lgv1.Color("#141414"))
	ti.Cursor.Style = lgv1.NewStyle().Background(lgv1.Color("#141414"))
	return ti
}

func (v *dirsView) setSize(w, h int) {
	v.width = w
	v.height = h
}

// rebuild recomputes projectEntry list from allSessions + activeSessions.
func (v *dirsView) rebuild() {
	byDir := make(map[string]*projectEntry)
	for _, s := range v.allSessions {
		dir := s.Directory
		if dir == "" {
			dir = "."
		}
		e, ok := byDir[dir]
		if !ok {
			byDir[dir] = &projectEntry{directory: dir}
			e = byDir[dir]
		}
		e.sessions = append(e.sessions, s)
		if s.Time.Updated > e.lastUpdated {
			e.lastUpdated = s.Time.Updated
		}
	}

	// Mark active sessions
	for sessionID, agentName := range v.activeSessions {
		// Find which directory this session belongs to
		for _, s := range v.allSessions {
			if s.ID == sessionID {
				dir := s.Directory
				if dir == "" {
					dir = "."
				}
				if e, ok := byDir[dir]; ok {
					e.activeSesCnt++
					if e.activeAgent == "" {
						if agentName != "" {
							e.activeAgent = agentName
						} else {
							e.activeAgent = "running"
						}
					}
				}
				break
			}
		}
	}

	// Flatten to slice
	entries := make([]projectEntry, 0, len(byDir))
	for _, e := range byDir {
		entries = append(entries, *e)
	}

	// Sort all projects by lastUpdated descending
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].lastUpdated > entries[j].lastUpdated
	})

	v.allProjects = entries
	v.applyDirsFilter()
	if v.cursor >= len(v.projects) && len(v.projects) > 0 {
		v.cursor = len(v.projects) - 1
	}
}

func (v *dirsView) applyDirsFilter() {
	if v.searchInput.Value() == "" {
		v.projects = v.allProjects
		return
	}
	q := strings.ToLower(v.searchInput.Value())
	var out []projectEntry
	for _, e := range v.allProjects {
		if strings.Contains(strings.ToLower(e.directory), q) {
			out = append(out, e)
		}
	}
	v.projects = out
	v.cursor = 0
}

func (v dirsView) selected() *projectEntry {
	if len(v.projects) == 0 || v.cursor >= len(v.projects) {
		return nil
	}
	return &v.projects[v.cursor]
}

// mostRecentSession returns the most recently updated session in this project.
func (e *projectEntry) mostRecentSession() *session.Session {
	var best *session.Session
	for _, s := range e.sessions {
		if best == nil || s.Time.Updated > best.Time.Updated {
			best = s
		}
	}
	return best
}

func (v dirsView) Update(msg tea.Msg) (dirsView, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		n := len(v.projects)
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
				v.applyDirsFilter()
			case tea.KeyEnter:
				v.searching = false
				v.searchInput.Blur()
			default:
				prev := v.searchInput.Value()
				var cmd tea.Cmd
				v.searchInput, cmd = v.searchInput.Update(msg)
				if v.searchInput.Value() != prev {
					v.applyDirsFilter()
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
			v.applyDirsFilter()
			return v, nil
		}
		n := len(v.projects)
		switch {
		case key.Matches(msg, Keys.Up):
			if v.cursor > 0 {
				v.cursor--
			}
		case key.Matches(msg, Keys.Down):
			if v.cursor < n-1 {
				v.cursor++
			}
		case key.Matches(msg, Keys.GoToBottom):
			if n > 0 {
				v.cursor = n - 1
			}
		case key.Matches(msg, Keys.GoToTop):
			v.cursor = 0
		case key.Matches(msg, Keys.HalfPageDown):
			step := v.visibleRows() / 2
			if step < 1 {
				step = 1
			}
			v.cursor += step
			if v.cursor >= n {
				v.cursor = n - 1
			}
		case key.Matches(msg, Keys.HalfPageUp):
			step := v.visibleRows() / 2
			if step < 1 {
				step = 1
			}
			v.cursor -= step
			if v.cursor < 0 {
				v.cursor = 0
			}
		case key.Matches(msg, Keys.OpenSession):
			if sel := v.selected(); sel != nil {
				if s := sel.mostRecentSession(); s != nil {
					return v, OpenSessionCmd(s)
				}
			}
		}
	}
	return v, nil
}

func (v dirsView) visibleRows() int {
	r := v.height - 9
	if r < 1 {
		r = 1
	}
	return r
}

func (v *dirsView) View() string {
	innerW := v.width - 4
	innerH := v.height - 4

	// column widths: directory | sessions(10) | last active(14) | agent
	sessColW := 10
	lastColW := 14
	agentColW := 18
	dirColW := innerW - sessColW - lastColW - agentColW - 6 // gaps
	if dirColW < 10 {
		dirColW = 10
	}

	sep := StyleDim.Render("  ")

	colHeader := StyleTableHeader.Render(padRight("DIRECTORY", dirColW)) +
		sep +
		StyleTableHeader.Render(padRight("SESSIONS", sessColW)) +
		sep +
		StyleTableHeader.Render(padRight("LAST ACTIVE", lastColW)) +
		sep +
		StyleTableHeader.Render(padRight("AGENT", agentColW))

	countStr := fmt.Sprintf("%d", len(v.projects))
	if v.searchInput.Value() != "" {
		countStr = fmt.Sprintf("%d / %d", len(v.projects), len(v.allProjects))
	}
	paneTitle := StylePaneTitleActive.Render("◈ DIRS")
	gap := innerW - lipgloss.Width("◈ DIRS") - len(countStr)
	if gap < 1 {
		gap = 1
	}
	titleLine := paneTitle + strings.Repeat(" ", gap) + StyleMuted.Render(countStr)

	// Split projects into active and inactive
	var activeProjs, inactiveProjs []int
	for i, e := range v.projects {
		if e.activeSesCnt > 0 {
			activeProjs = append(activeProjs, i)
		} else {
			inactiveProjs = append(inactiveProjs, i)
		}
	}

	// Build flat display list: header rows (projIdx=-1) + project rows
	type listItem struct {
		header  string
		projIdx int
	}
	var items []listItem

	if len(activeProjs) > 0 {
		items = append(items, listItem{
			header:  fmt.Sprintf("● ACTIVE  (%d)", len(activeProjs)),
			projIdx: -1,
		})
		for _, idx := range activeProjs {
			items = append(items, listItem{projIdx: idx})
		}
	}

	inactiveLabel := fmt.Sprintf("ALL PROJECTS  (%d)", len(inactiveProjs))
	items = append(items, listItem{header: inactiveLabel, projIdx: -1})
	for _, idx := range inactiveProjs {
		items = append(items, listItem{projIdx: idx})
	}

	// Find flat index of current cursor; fall back to 0 if not found (e.g. empty list)
	cursorFlat := 0
	for fi, item := range items {
		if item.projIdx == v.cursor {
			cursorFlat = fi
			break
		}
	}
	visibleRows := innerH - 5 // title + sep + searchbar + colheader + dottedSep
	if visibleRows < 1 {
		visibleRows = 1
	}

	flatOffset := 0
	if cursorFlat < flatOffset {
		flatOffset = cursorFlat
	}
	if cursorFlat >= flatOffset+visibleRows {
		flatOffset = cursorFlat - visibleRows + 1
	}
	if flatOffset < 0 {
		flatOffset = 0
	}

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

	end := flatOffset + visibleRows
	if end > len(items) {
		end = len(items)
	}

	for fi := flatOffset; fi < end; fi++ {
		item := items[fi]

		if item.header != "" {
			var hdr string
			if strings.HasPrefix(item.header, "●") {
				hdr = StyleDirsActiveHeader.Width(innerW).Render(item.header)
			} else {
				hdr = StyleSessionDateGroup.Width(innerW).Render(item.header)
			}
			rows = append(rows, hdr)
			continue
		}

		e := v.projects[item.projIdx]
		selected := item.projIdx == v.cursor

		dirText := shortenDirPath(e.directory)
		sessText := fmt.Sprintf("%d", len(e.sessions))
		lastText := ""
		if e.lastUpdated > 0 {
			t := time.UnixMilli(e.lastUpdated)
			lastText = relativeTime(t)
		}
		agentText := e.activeAgent

		if selected {
			row := StyleSessionItemSelected.Width(innerW).Render(
				padRight(truncate(dirText, dirColW), dirColW) + "  " +
					padRight(sessText, sessColW) + "  " +
					padRight(lastText, lastColW) + "  " +
					padRight(truncate(agentText, agentColW), agentColW),
			)
			rows = append(rows, row)
		} else {
			isActive := e.activeSesCnt > 0
			var dirPart string
			if isActive {
				dirPart = StyleDirsActiveDir.Render(padRight(truncate(dirText, dirColW), dirColW))
			} else {
				dirPart = StyleSessionItem.Render(padRight(truncate(dirText, dirColW), dirColW))
			}
			sessPart := StyleMuted.Render(padRight(sessText, sessColW))
			lastPart := StyleDim.Render(padRight(lastText, lastColW))
			agentPart := StyleMuted.Render(padRight(truncate(agentText, agentColW), agentColW))
			rows = append(rows, dirPart+sep+sessPart+sep+lastPart+sep+agentPart)
		}
	}

	content := strings.Join(rows, "\n")
	// Populate rowToCursor for mouse click support.
	v.rowToCursor = make([]int, len(rows))
	for i := range v.rowToCursor {
		v.rowToCursor[i] = -1
	}
	rowIdx := 5 // title, sep, searchbar, colHeader, dottedSep
	for fi := flatOffset; fi < end; fi++ {
		if rowIdx >= len(v.rowToCursor) {
			break
		}
		item := items[fi]
		if item.header != "" {
			v.rowToCursor[rowIdx] = -1
		} else {
			v.rowToCursor[rowIdx] = item.projIdx
		}
		rowIdx++
	}
	return StyleActiveBorder.Width(v.width).Height(v.height - 2).Render(content)
}

// shortenDirPath abbreviates the home directory as ~ and shows the last 2 path components.
func shortenDirPath(p string) string {
	if p == "" || p == "." {
		return p
	}
	clean := filepath.Clean(p)
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(clean, home) {
		clean = "~" + clean[len(home):]
	}
	parts := strings.Split(clean, string(filepath.Separator))
	if len(parts) <= 3 {
		return clean
	}
	return "~/" + strings.Join(parts[len(parts)-2:], "/")
}
