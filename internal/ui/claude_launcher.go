package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// claudeLauncherSubmitMsg is emitted when the user submits the New Claude
// Session form. Dir may be empty (use the source's configured working dir).
type claudeLauncherSubmitMsg struct {
	prompt string
	dir    string
}

type claudeLauncherModel struct {
	active bool
	prompt textarea.Model
	dir    textarea.Model
	focus  int // 0 = prompt, 1 = dir
	err    string
	width  int
	height int
}

func newClaudeLauncher() claudeLauncherModel {
	prompt := textarea.New()
	prompt.CharLimit = 0
	prompt.SetWidth(56)
	prompt.SetHeight(6)
	prompt.ShowLineNumbers = false
	prompt.Placeholder = "initial message for the claude session..."

	dir := textarea.New()
	dir.CharLimit = 0
	dir.SetWidth(56)
	dir.SetHeight(1)
	dir.ShowLineNumbers = false
	dir.Placeholder = "working directory (blank = configured default)"

	m := claudeLauncherModel{
		active: true,
		prompt: prompt,
		dir:    dir,
		focus:  0,
	}
	m.prompt.Focus()
	return m
}

func (m claudeLauncherModel) Update(msg tea.Msg) (claudeLauncherModel, tea.Cmd) {
	if !m.active {
		return m, nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		var c1, c2 tea.Cmd
		m.prompt, c1 = m.prompt.Update(msg)
		m.dir, c2 = m.dir.Update(msg)
		return m, tea.Batch(c1, c2)
	}

	switch km.String() {
	case "esc":
		m.active = false
		return m, nil
	case "ctrl+s":
		p := strings.TrimSpace(m.prompt.Value())
		if p == "" {
			m.err = "prompt is required"
			return m, nil
		}
		m.active = false
		return m, func() tea.Msg {
			return claudeLauncherSubmitMsg{prompt: p, dir: strings.TrimSpace(m.dir.Value())}
		}
	case "tab":
		m.focus = 1
		m.prompt.Blur()
		m.dir.Focus()
		return m, nil
	case "shift+tab":
		m.focus = 0
		m.dir.Blur()
		m.prompt.Focus()
		return m, nil
	}

	var cmd tea.Cmd
	switch m.focus {
	case 0:
		m.prompt, cmd = m.prompt.Update(msg)
	case 1:
		m.dir, cmd = m.dir.Update(msg)
	}
	return m, cmd
}

func (m claudeLauncherModel) View() string {
	if !m.active {
		return ""
	}

	modalW := 64
	if modalW > m.width-4 {
		modalW = m.width - 4
	}
	innerW := modalW - 4

	bg := lipgloss.NewStyle().Background(colorDeep).Width(modalW).Padding(0, 2)

	title := StyleLeaderSectionHeader.Render("NEW CLAUDE SESSION")
	hint := StyleLeaderDismiss.Render("ctrl+s · launch   esc · close")
	gap := modalW - lipgloss.Width(title) - lipgloss.Width(hint) - 4
	if gap < 1 {
		gap = 1
	}
	titleLine := bg.Render(title + lipgloss.NewStyle().Background(colorDeep).Render(strings.Repeat(" ", gap)) + hint)

	sep := bg.Render(lipgloss.NewStyle().Foreground(colorMid).Background(colorDeep).Render(
		strings.Repeat("─", innerW),
	))

	renderField := func(label string, active bool, ta textarea.Model) string {
		lbl := StyleLeaderSectionHeader.Render(label)
		var box string
		if active {
			box = StyleActiveInput.Render(ta.View())
		} else {
			box = StyleInactiveInput.Render(ta.View())
		}
		return bg.Render(lbl) + "\n" + bg.Render(lipgloss.NewStyle().Background(colorDeep).Width(innerW).Render(box))
	}

	var rows []string
	rows = append(rows, titleLine)
	rows = append(rows, sep)

	if m.err != "" {
		rows = append(rows, bg.Render(StyleBadgeDisabled.Render(" ✗ "+m.err+" ")))
		rows = append(rows, bg.Render(""))
	}

	rows = append(rows, renderField("PROMPT", m.focus == 0, m.prompt))
	rows = append(rows, bg.Render(""))
	rows = append(rows, renderField("WORKING DIRECTORY", m.focus == 1, m.dir))
	rows = append(rows, bg.Render(""))

	helpKeys := []struct{ k, d string }{
		{"tab", "next"},
		{"shift+tab", "prev"},
		{"ctrl+s", "launch"},
		{"esc", "close"},
	}
	var helpParts []string
	for _, h := range helpKeys {
		helpParts = append(helpParts, StyleStatusKey.Render(h.k)+" "+StyleMuted.Render(h.d))
	}
	rows = append(rows, bg.Render(strings.Join(helpParts, "  ")))
	rows = append(rows, bg.Render(""))

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
