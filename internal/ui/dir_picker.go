
package ui

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	lgv1 "github.com/charmbracelet/lipgloss"
)

type dirPickerSelectMsg struct{ dir string }

type dirPickerModel struct {
	active    bool
	dirs      []string
	cursor    int
	input     textinput.Model
	inputMode bool
	lastKey   string
	width     int
	height    int
}

func newDirPicker(projects []projectEntry) dirPickerModel {
	ti := textinput.New()
	ti.Placeholder = "filter recent directories..."
	ti.Prompt = "  › "
	ti.CharLimit = 256
	ti.TextStyle = lgv1.NewStyle().Background(lgv1.Color("#141414"))
	ti.PromptStyle = lgv1.NewStyle().Foreground(lgv1.Color("#3A5200")).Background(lgv1.Color("#141414"))
	ti.PlaceholderStyle = lgv1.NewStyle().Foreground(lgv1.Color("#555555")).Background(lgv1.Color("#141414"))
	ti.Cursor.Style = lgv1.NewStyle().Background(lgv1.Color("#141414"))

	seen := make(map[string]bool)
	dirs := make([]string, 0, len(projects))
	for _, p := range projects {
		if !seen[p.directory] {
			seen[p.directory] = true
			dirs = append(dirs, p.directory)
		}
	}

	ti.Focus()

	return dirPickerModel{
		active:    true,
		dirs:      dirs,
		input:     ti,
		inputMode: true,
	}
}

// expandTilde replaces a leading ~/ with the user's home directory.
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return home + path[1:]
		}
	}
	return path
}

// filteredDirs returns dirs filtered by the current input value.
func (m dirPickerModel) filteredDirs() []string {
	q := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if q == "" {
		return m.dirs
	}
	var out []string
	for _, d := range m.dirs {
		if strings.Contains(strings.ToLower(d), q) {
			out = append(out, d)
		}
	}
	return out
}

func (m dirPickerModel) Update(msg tea.Msg) (dirPickerModel, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.inputMode {
		switch km.String() {
		case "esc":
			// If there's text, clear it first; otherwise close the picker
			if m.input.Value() != "" {
				m.input.SetValue("")
				m.cursor = 0
				return m, nil
			}
			m.active = false
			m.input.Blur()
			return m, nil
		case "enter":
			filtered := m.filteredDirs()
			if len(filtered) > 0 {
				// Select highlighted item from filtered list
				idx := m.cursor
				if idx >= len(filtered) {
					idx = len(filtered) - 1
				}
				dir := filtered[idx]
				m.active = false
				m.input.Blur()
				return m, func() tea.Msg { return dirPickerSelectMsg{dir: dir} }
			}
			// No match — treat input as custom path
			v := strings.TrimSpace(m.input.Value())
			if v != "" {
				m.active = false
				m.input.Blur()
				dir := expandTilde(v)
				return m, func() tea.Msg { return dirPickerSelectMsg{dir: dir} }
			}
			return m, nil
		case "up", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "ctrl+n":
			filtered := m.filteredDirs()
			if m.cursor < len(filtered)-1 {
				m.cursor++
			}
			return m, nil
		default:
			prevVal := m.input.Value()
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			// Reset cursor when filter changes
			if m.input.Value() != prevVal {
				m.cursor = 0
			}
			return m, cmd
		}
	}

	k := km.String()
	defer func() { m.lastKey = k }()

	switch k {
	case "esc":
		m.active = false
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "j", "down":
		if m.cursor < len(m.dirs)-1 {
			m.cursor++
		}
	case "G":
		if len(m.dirs) > 0 {
			m.cursor = len(m.dirs) - 1
		}
	case "g":
		if m.lastKey == "g" {
			m.cursor = 0
			m.lastKey = ""
		}
		return m, nil
	case "/", "i":
		m.inputMode = true
		m.input.Focus()
		return m, m.input.Cursor.BlinkCmd()
	case "enter":
		if len(m.dirs) > 0 {
			dir := m.dirs[m.cursor]
			m.active = false
			return m, func() tea.Msg { return dirPickerSelectMsg{dir: dir} }
		}
		// No dirs available — enter custom input mode
		m.inputMode = true
		m.input.Focus()
		return m, m.input.Cursor.BlinkCmd()
	}

	return m, nil
}

func (m dirPickerModel) View() string {
	if !m.active {
		return ""
	}

	modalW := 60
	if modalW > m.width-4 {
		modalW = m.width - 4
	}
	innerW := modalW - 4

	bg := lipgloss.NewStyle().Background(colorDeep).Width(modalW).Padding(0, 2)

	// Title
	title := StyleLeaderSectionHeader.Render("NEW SESSION")
	hint := StyleLeaderDismiss.Render("esc · close")
	gap := modalW - lipgloss.Width(title) - lipgloss.Width(hint) - 4
	if gap < 1 {
		gap = 1
	}
	titleLine := bg.Render(title + lipgloss.NewStyle().Background(colorDeep).Render(strings.Repeat(" ", gap)) + hint)

	sep := bg.Render(lipgloss.NewStyle().Foreground(colorMid).Background(colorDeep).Render(
		strings.Repeat("─", innerW),
	))

	// Directory list
	maxVisible := 12
	if maxVisible > m.height-10 {
		maxVisible = m.height - 10
		if maxVisible < 3 {
			maxVisible = 3
		}
	}

	offset := 0
	if m.cursor >= offset+maxVisible {
		offset = m.cursor - maxVisible + 1
	}
	if m.cursor < offset {
		offset = m.cursor
	}

	var rows []string
	rows = append(rows, titleLine)
	rows = append(rows, sep)

	sectionLabel := StyleLeaderSectionHeader.Render("RECENT DIRECTORIES")
	rows = append(rows, bg.Render(sectionLabel))

	filtered := m.filteredDirs()
	if len(m.dirs) == 0 {
		rows = append(rows, bg.Render(lipgloss.NewStyle().Background(colorDeep).Foreground(colorStone).Render("  no recent directories — type a path below")))
	} else if len(filtered) == 0 {
		rows = append(rows, bg.Render(lipgloss.NewStyle().Background(colorDeep).Foreground(colorStone).Render("  no matches")))
	} else {
		end := offset + maxVisible
		if end > len(filtered) {
			end = len(filtered)
		}
		for i := offset; i < end; i++ {
			dirText := shortenDirPath(filtered[i])
			if i == m.cursor {
				row := StyleSessionItemSelected.Width(innerW).Render(dirText)
				rows = append(rows, bg.Render(row))
			} else {
				row := lipgloss.NewStyle().Background(colorDeep).Foreground(colorStone).Width(innerW).Render(dirText)
				rows = append(rows, bg.Render(row))
			}
		}
	}

	rows = append(rows, sep)

	// Search input
	searchLabel := StyleLeaderSectionHeader.Render("SEARCH / CUSTOM PATH")
	rows = append(rows, bg.Render(searchLabel))

	m.input.Width = innerW - lipgloss.Width(m.input.Prompt) - 1
	inputLine := lipgloss.NewStyle().Background(colorDeep).Width(innerW).Render(m.input.View())
	rows = append(rows, bg.Render(inputLine))

	// Bottom padding
	rows = append(rows, bg.Render(""))

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
