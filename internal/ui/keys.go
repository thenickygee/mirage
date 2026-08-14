package ui

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	Up             key.Binding
	Down           key.Binding
	HalfPageDown   key.Binding
	HalfPageUp     key.Binding
	PageDown       key.Binding
	PageUp         key.Binding
	GoToBottom     key.Binding
	GoToTop        key.Binding
	Enter          key.Binding
	Esc            key.Binding
	New            key.Binding
	Edit           key.Binding
	Toggle         key.Binding
	Prompt         key.Binding
	Save           key.Binding
	Tab            key.Binding
	ShiftTab       key.Binding
	Quit           key.Binding
	NextTab        key.Binding
	PrevTab        key.Binding
	FocusPaneRight key.Binding
	FocusPaneLeft  key.Binding
	Leader         key.Binding
	Find           key.Binding
	CtrlP          key.Binding
	CtrlN          key.Binding
	OpenSession    key.Binding
	InsertMode     key.Binding
}

var Keys = KeyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	HalfPageDown: key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "half page down"),
	),
	HalfPageUp: key.NewBinding(
		key.WithKeys("ctrl+u"),
		key.WithHelp("ctrl+u", "half page up"),
	),
	PageDown: key.NewBinding(
		key.WithKeys("ctrl+f", "pgdown"),
		key.WithHelp("ctrl+f/pgdn", "page down"),
	),
	PageUp: key.NewBinding(
		key.WithKeys("ctrl+b", "pgup"),
		key.WithHelp("ctrl+b/pgup", "page up"),
	),
	GoToBottom: key.NewBinding(
		key.WithKeys("G"),
		key.WithHelp("G", "go to bottom"),
	),
	GoToTop: key.NewBinding(
		key.WithKeys("g"),
		key.WithHelp("gg", "go to top"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	),
	Esc: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	New: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new"),
	),
	Edit: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "edit"),
	),
	Toggle: key.NewBinding(
		key.WithKeys("D"),
		key.WithHelp("D", "toggle disable"),
	),
	Prompt: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "view prompt"),
	),
	Save: key.NewBinding(
		key.WithKeys("ctrl+s"),
		key.WithHelp("ctrl+s", "save"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next field"),
	),
	ShiftTab: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "prev field"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	NextTab: key.NewBinding(
		key.WithKeys("right"),
		key.WithHelp("→", "next tab"),
	),
	PrevTab: key.NewBinding(
		key.WithKeys("left"),
		key.WithHelp("←", "prev tab"),
	),
	FocusPaneRight: key.NewBinding(
		key.WithKeys("l", "enter"),
		key.WithHelp("l/enter", "focus right pane"),
	),
	FocusPaneLeft: key.NewBinding(
		key.WithKeys("h", "esc"),
		key.WithHelp("h/esc", "focus left pane"),
	),
	Leader: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "leader menu"),
	),
	Find: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "find"),
	),
	CtrlP: key.NewBinding(
		key.WithKeys("ctrl+p"),
		key.WithHelp("ctrl+p", "up"),
	),
	CtrlN: key.NewBinding(
		key.WithKeys("ctrl+n"),
		key.WithHelp("ctrl+n", "down"),
	),
	OpenSession: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "open in opencode"),
	),
	InsertMode: key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "chat"),
	),
}
