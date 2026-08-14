package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type confirmModel struct {
	active  bool
	title   string
	message string
	onYes   func()
	width   int
	height  int
}

func newConfirm(title, message string, onYes func()) confirmModel {
	return confirmModel{
		active:  true,
		title:   title,
		message: message,
		onYes:   onYes,
	}
}

func (c *confirmModel) confirm() {
	if c.onYes != nil {
		c.onYes()
	}
	c.active = false
}

func (c *confirmModel) cancel() {
	c.active = false
}

func (c confirmModel) View(screenW, screenH int) string {
	if !c.active {
		return ""
	}

	boxW := 52
	if screenW*40/100 > boxW {
		boxW = screenW * 40 / 100
	}
	if boxW > 68 {
		boxW = 68
	}
	innerW := boxW - 6

	var rows []string

	rows = append(rows, StyleConfirmTitle.Render(c.title))
	rows = append(rows, "")
	rows = append(rows, StyleConfirmMessage.Width(innerW).Render(c.message))
	rows = append(rows, "")
	rows = append(rows, StyleConfirmSeparator.Render(strings.Repeat("─", innerW)))

	yesKey := StyleConfirmHint.Render("y")
	yesTxt := StyleMuted.Render(" confirm  ")
	noKey := StyleConfirmHint.Render("n")
	noTxt := StyleMuted.Render(" / ")
	escKey := StyleConfirmHint.Render("esc")
	escTxt := StyleMuted.Render(" cancel")
	footer := yesKey + yesTxt + noKey + noTxt + escKey + escTxt
	footerW := lipgloss.Width(footer)
	leftSpacer := (innerW - footerW) / 2
	if leftSpacer < 0 {
		leftSpacer = 0
	}
	rows = append(rows, strings.Repeat(" ", leftSpacer)+footer)

	inner := strings.Join(rows, "\n")
	box := StyleConfirmBox.Width(boxW).Render(inner)

	bw := lipgloss.Width(box)
	bh := lipgloss.Height(box)
	leftPad := (screenW - bw) / 2
	topPad := (screenH - bh) / 3
	if leftPad < 0 {
		leftPad = 0
	}
	if topPad < 0 {
		topPad = 0
	}

	boxLines := strings.Split(box, "\n")
	emptyLine := strings.Repeat(" ", screenW)

	var out []string
	for i := 0; i < topPad; i++ {
		out = append(out, emptyLine)
	}
	for _, line := range boxLines {
		out = append(out, strings.Repeat(" ", leftPad)+line)
	}
	for i := topPad + len(boxLines); i < screenH; i++ {
		out = append(out, emptyLine)
	}

	return strings.Join(out, "\n")
}
