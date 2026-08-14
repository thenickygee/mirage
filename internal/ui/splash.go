package ui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	tea "github.com/charmbracelet/bubbletea"
)

type splashTickMsg struct{}
type splashPhase1Msg struct{} // text shown for 1s → switch to robot
type splashPhase2Msg struct{} // robot shown for 1s → flash + done
type splashDoneMsg struct{}
type splashFlashDoneMsg struct{}

func splashTick() tea.Cmd {
	return tea.Tick(220*time.Millisecond, func(t time.Time) tea.Msg {
		return splashTickMsg{}
	})
}

func splashPhase1() tea.Cmd {
	return tea.Tick(1000*time.Millisecond, func(t time.Time) tea.Msg {
		return splashPhase1Msg{}
	})
}

func splashPhase2() tea.Cmd {
	return tea.Tick(1000*time.Millisecond, func(t time.Time) tea.Msg {
		return splashPhase2Msg{}
	})
}

func splashFlashDone() tea.Cmd {
	return tea.Tick(1000*time.Millisecond, func(t time.Time) tea.Msg {
		return splashFlashDoneMsg{}
	})
}

type splashFlashTickMsg struct{}

func splashFlashTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return splashFlashTickMsg{}
	})
}

// ASCII robot face — inner head only
var eyeFrames = []string{
	// frame 0: idle
	`▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄
█ ┌───┐ ┌───┐ █
█ │ ◉ │ │ ◉ │ █
█ └───┘ └───┘ █
█  ▬▬▬▬▬▬▬▬▬  █
█  ▪ ▪ ▪ ▪ ▪  █
▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀`,

	// frame 1: eyes right
	`▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄
█ ┌───┐ ┌───┐ █
█ │  ◎│ │  ◎│ █
█ └───┘ └───┘ █
█  ▬▬▬▬▬▬▬▬▬  █
█  ▪ ▪ ▪ ▪ ▪  █
▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀`,

	// frame 2: eyes left
	`▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄
█ ┌───┐ ┌───┐ █
█ │◎  │ │◎  │ █
█ └───┘ └───┘ █
█  ▬▬▬▬▬▬▬▬▬  █
█  ▪ ▪ ▪ ▪ ▪  █
▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀`,

	// frame 3: blink
	`▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄
█ ┌───┐ ┌───┐ █
█ │ ═ │ │ ═ │ █
█ └───┘ └───┘ █
█  ▬▬▬▬▬▬▬▬▬  █
█  ▫ ▫ ▫ ▫ ▫  █
▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀`,

	// frame 4: talking
	`▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄
█ ┌───┐ ┌───┐ █
█ │ ◉ │ │ ◉ │ █
█ └───┘ └───┘ █
█  ▬▬▬▬▬▬▬▬▬  █
█  □ □ □ □ □  █
▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀`,

	// frame 5: wink
	`▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄
█ ┌───┐ ┌───┐ █
█ │ ═ │ │ ◉ │ █
█ └───┘ └───┘ █
█  ▬▬▬▬▬▬▬▬▬  █
█  ▪ ▪ ▪ ▪ ▪  █
▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀`,
}

// "MIRAGE VAULT" in ANSI Shadow style (█ block fill with ╗╝╚╔ connectors)
var splashTitleArt = []string{
	`███╗   ███╗██╗██████╗  █████╗  ██████╗ ███████╗    ██╗   ██╗ █████╗ ██╗   ██╗██╗  ████████╗`,
	`████╗ ████║██║██╔══██╗██╔══██╗██╔════╝ ██╔════╝    ██║   ██║██╔══██╗██║   ██║██║  ╚══██╔══╝`,
	`██╔████╔██║██║██████╔╝███████║██║  ███╗█████╗      ██║   ██║███████║██║   ██║██║     ██║   `,
	`██║╚██╔╝██║██║██╔══██╗██╔══██║██║   ██║██╔══╝      ╚██╗ ██╔╝██╔══██║██║   ██║██║     ██║   `,
	`██║ ╚═╝ ██║██║██║  ██║██║  ██║╚██████╔╝███████╗     ╚████╔╝ ██║  ██║╚██████╔╝███████╗██║   `,
	`╚═╝     ╚═╝╚═╝╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝ ╚══════╝      ╚═══╝  ╚═╝  ╚═╝ ╚═════╝ ╚══════╝╚═╝  `,
}

const robotFrameHeight = 7

func renderSplash(phase, frame, flashFrame int, flashing bool, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	var block string

	if phase == 0 {
		// Phase 0: title text centered
		var titleLines []string
		for i, l := range splashTitleArt {
			if i == len(splashTitleArt)-1 {
				titleLines = append(titleLines, splashTitleArtDimStyle.Render(l))
			} else {
				titleLines = append(titleLines, splashTitleArtBrightStyle.Render(l))
			}
		}
		block = strings.Join(titleLines, "\n")
	} else {
		// Phase 1: robot head centered
		eye := eyeFrames[frame%len(eyeFrames)]
		lines := strings.Split(strings.TrimLeft(eye, "\n"), "\n")
		for len(lines) < robotFrameHeight {
			lines = append(lines, "")
		}
		lines = lines[:robotFrameHeight]

		var eyeLines []string
		for _, l := range lines {
			eyeLines = append(eyeLines, splashEyeStyle.Render(l))
		}
		block = strings.Join(eyeLines, "\n")
	}

	blockH := lipgloss.Height(block)
	blockW := lipgloss.Width(block)

	topPad := (height - 1 - blockH) / 2
	if topPad < 0 {
		topPad = 0
	}
	leftPad := (width - blockW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	leftStr := strings.Repeat(" ", leftPad)

	result := make([]string, height)
	for i, l := range strings.Split(block, "\n") {
		row := topPad + i
		if row >= 0 && row < height-1 {
			result[row] = leftStr + l
		}
	}

	// Hotkey bar pinned to last row
	hintA := splashHintStyle.Render("  ")
	pressEnter := splashPressStyle.Render("enter")
	hintB := splashHintStyle.Render(" to skip  ")
	hotkeyBar := hintA + pressEnter + hintB
	hw := lipgloss.Width(hotkeyBar)
	hpad := (width - hw) / 2
	if hpad < 0 {
		hpad = 0
	}
	result[height-1] = strings.Repeat(" ", hpad) + hotkeyBar

	return strings.Join(result, "\n")
}
