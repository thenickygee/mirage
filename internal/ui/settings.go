package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type settingsModel struct {
	active  bool
	cursor  int
	current string // currently applied palette name
	width   int
	height  int
	onApply func(AccentPalette)
}

func newSettings(currentName string, onApply func(AccentPalette)) settingsModel {
	cursor := 0
	for i, p := range AccentPalettes {
		if p.Name == currentName {
			cursor = i
			break
		}
	}
	return settingsModel{
		active:  true,
		cursor:  cursor,
		current: currentName,
		onApply: onApply,
	}
}

func (s *settingsModel) moveUp() {
	if s.cursor > 0 {
		s.cursor--
	}
}

func (s *settingsModel) moveDown() {
	if s.cursor < len(AccentPalettes)-1 {
		s.cursor++
	}
}

func (s *settingsModel) selectCurrent() {
	p := AccentPalettes[s.cursor]
	s.current = p.Name
	if s.onApply != nil {
		s.onApply(p)
	}
}

func (s *settingsModel) dismiss() {
	s.active = false
}

func (s settingsModel) View(screenW int) string {
	if !s.active {
		return ""
	}

	boxW := 44
	if screenW*35/100 > boxW {
		boxW = screenW * 35 / 100
	}
	if boxW > 56 {
		boxW = 56
	}
	innerW := boxW - 6

	var rows []string

	rows = append(rows, StyleSettingsTitle.Render("ACCENT COLOR"))
	rows = append(rows, "")

	for i, p := range AccentPalettes {
		swatch := lipgloss.NewStyle().Foreground(lipgloss.Color(p.Bright)).Background(colorDeep).Render("██")
		var check string
		if p.Name == s.current {
			if i == s.cursor {
				check = StyleSettingsCheckSelected.Render("✓ ")
			} else {
				check = StyleSettingsCheck.Render("✓ ")
			}
		} else {
			check = lipgloss.NewStyle().Background(colorDeep).Render("  ")
		}

		var label string
		if i == s.cursor {
			selectedSwatch := lipgloss.NewStyle().Foreground(lipgloss.Color(p.Bright)).Background(colorShadow).Render("██")
			label = StyleSettingsSelected.Width(innerW - 6).Render(" " + selectedSwatch + " " + p.Label)
			if p.Name == s.current {
				check = StyleSettingsCheckSelected.Render("✓ ")
			} else {
				check = lipgloss.NewStyle().Background(colorShadow).Render("  ")
			}
			rows = append(rows, lipgloss.NewStyle().Background(colorShadow).Render(check)+label)
		} else {
			label = StyleSettingsItem.Width(innerW - 6).Render(" " + swatch + " " + p.Label)
			rows = append(rows, lipgloss.NewStyle().Background(colorDeep).Render(check)+label)
		}
	}

	rows = append(rows, "")
	sep := lipgloss.NewStyle().Foreground(colorMid).Background(colorDeep).Render(strings.Repeat("─", innerW))
	rows = append(rows, sep)

	enterKey := StyleConfirmHint.Render("enter")
	enterTxt := StyleMuted.Render(" select  ")
	escKey := StyleConfirmHint.Render("esc")
	escTxt := StyleMuted.Render(" close")
	footer := enterKey + enterTxt + escKey + escTxt
	footerW := lipgloss.Width(footer)
	leftSpacer := (innerW - footerW) / 2
	if leftSpacer < 0 {
		leftSpacer = 0
	}
	rows = append(rows, strings.Repeat(" ", leftSpacer)+footer)

	inner := strings.Join(rows, "\n")
	return StyleSettingsBox.Width(boxW).Render(inner)
}
