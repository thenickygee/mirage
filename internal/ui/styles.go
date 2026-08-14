package ui

import lipgloss "charm.land/lipgloss/v2"

// AccentPalette defines four shades of an accent color.
type AccentPalette struct {
	Name   string
	Label  string
	Dim    string
	Mid    string
	Base   string
	Bright string
}

var AccentPalettes = []AccentPalette{
	{Name: "lime", Label: "Lime", Dim: "#1A2A00", Mid: "#3A5200", Base: "#7AB800", Bright: "#AADD00"},
	{Name: "blue", Label: "Blue", Dim: "#0A1A2E", Mid: "#1A3A6E", Base: "#3B82F6", Bright: "#60A5FA"},
	{Name: "purple", Label: "Purple", Dim: "#1A0A2E", Mid: "#3A1A6E", Base: "#8B5CF6", Bright: "#A78BFA"},
	{Name: "orange", Label: "Orange", Dim: "#2A1A00", Mid: "#5A3A00", Base: "#E08A00", Bright: "#FFAA33"},
	{Name: "rose", Label: "Rose", Dim: "#2A0A14", Mid: "#6E1A3A", Base: "#F43F5E", Bright: "#FB7185"},
	{Name: "teal", Label: "Teal", Dim: "#0A2A2A", Mid: "#1A5252", Base: "#14B8A6", Bright: "#2DD4BF"},
	{Name: "amber", Label: "Amber", Dim: "#2A1E00", Mid: "#5A4000", Base: "#F59E0B", Bright: "#FBBF24"},
	{Name: "cyan", Label: "Cyan", Dim: "#0A1E2A", Mid: "#0E4D6E", Base: "#06B6D4", Bright: "#22D3EE"},
}

func PaletteByName(name string) AccentPalette {
	for _, p := range AccentPalettes {
		if p.Name == name {
			return p
		}
	}
	return AccentPalettes[0]
}

var (
	// Neutral monochrome — value only, no hue
	colorDeep   = lipgloss.Color("#141414")
	colorShadow = lipgloss.Color("#1E1E1E")
	colorDark   = lipgloss.Color("#2A2A2A")
	colorMid    = lipgloss.Color("#3D3D3D")
	colorSlate  = lipgloss.Color("#555555")
	colorStone  = lipgloss.Color("#777777")
	colorAsh    = lipgloss.Color("#9A9A9A")
	colorSilver = lipgloss.Color("#BBBBBB")
	colorWhite  = lipgloss.Color("#F0F0F0")

	// Accent colors — set by ApplyAccent, default to lime
	colorAmberDim    = lipgloss.Color("#1A2A00")
	colorAmberMid    = lipgloss.Color("#3A5200")
	colorAmber       = lipgloss.Color("#7AB800")
	colorAmberBright = lipgloss.Color("#AADD00")

	colorErrText = lipgloss.Lighten(lipgloss.Color("#CC6655"), 0.05)
	colorOkText  = lipgloss.Lighten(lipgloss.Color("#88AA88"), 0.05)

	// Borders — plain single line, dim
	StyleBorder = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorMid)

	StyleActiveBorder = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(colorAmberBright)

	// Header
	StyleAppHeader = lipgloss.NewStyle().
			Background(colorDark).
			Foreground(colorAmberBright).
			Bold(true).
			Padding(0, 2)

	StyleAppHeaderAccent = lipgloss.NewStyle().
				Background(colorShadow).
				Foreground(colorStone).
				Padding(0, 2)

	// Titles
	StylePaneTitle = lipgloss.NewStyle().
			Foreground(colorStone)

	StylePaneTitleActive = lipgloss.NewStyle().
				Foreground(colorSilver).
				Bold(true)

	// List items
	StyleListItem = lipgloss.NewStyle().
			Foreground(colorAsh).
			Bold(true).
			Padding(1, 1)

	StyleListItemSelected = lipgloss.NewStyle().
				Background(colorDark).
				Foreground(colorAmberBright).
				Bold(true).
				Padding(1, 1)

	StyleListItemDisabled = lipgloss.NewStyle().
				Foreground(colorMid).
				Bold(true).
				Padding(1, 1)

	// Detail
	StyleDetailTitle = lipgloss.NewStyle().
				Foreground(colorAmberBright).
				Bold(true).
				UnderlineStyle(lipgloss.UnderlineDotted).
				UnderlineColor(colorAmberMid)

	StyleSectionHeader = lipgloss.NewStyle().
				Foreground(colorStone)

	StyleLabel = lipgloss.NewStyle().
			Foreground(colorSilver)

	StyleValue = lipgloss.NewStyle().
			Foreground(colorAsh)

	StyleMuted = lipgloss.NewStyle().
			Foreground(colorStone)

	StyleDim = lipgloss.NewStyle().
			Foreground(colorMid)

	// Badges — minimal, no bg fill on most
	StyleBadgeEnabled = lipgloss.NewStyle().
				Foreground(colorOkText)

	StyleBadgeDisabled = lipgloss.NewStyle().
				Foreground(colorErrText)

	StyleBadgeMode = lipgloss.NewStyle().
			Foreground(colorStone)

	StyleBadgeModel = lipgloss.NewStyle().
			Foreground(colorMid)

	// Status bar
	StyleStatusBar = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorMid).
			Foreground(colorMid)

	StyleStatusKey = lipgloss.NewStyle().
			Foreground(colorAmber).
			Bold(true)

	StyleStatusDesc = lipgloss.NewStyle().
			Foreground(colorMid)

	StyleStatusMsg = lipgloss.NewStyle().
			Foreground(colorOkText)

	StyleStatusErr = lipgloss.NewStyle().
			Foreground(colorErrText)

	// Form
	StyleFormTitle = lipgloss.NewStyle().
			Foreground(colorSilver).
			Bold(true)

	StyleActiveInput = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(colorAmberMid).
				Padding(0, 1)

	StyleInactiveInput = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(colorMid).
				Padding(0, 1)

	StyleSeparator = lipgloss.NewStyle().
			Foreground(colorDark)

	// Table styles
	StyleTableBorder = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(colorMid)

	StyleTableHeader = lipgloss.NewStyle().
				Foreground(colorSlate).
				Bold(true)

	StyleTableHeaderBorder = lipgloss.NewStyle().
				Foreground(colorMid)

	StyleTableCell = lipgloss.NewStyle().
			Foreground(colorAsh)

	StyleTableCellMuted = lipgloss.NewStyle().
				Foreground(colorSlate)

	StyleTableCellDim = lipgloss.NewStyle().
				Foreground(colorDark)

	// Prompt view
	StylePromptHeader = lipgloss.NewStyle().
				Background(colorDark).
				Foreground(colorSilver).
				Padding(0, 2)

	// Tabs
	StyleTabBar = lipgloss.NewStyle()

	StyleTabActive = lipgloss.NewStyle().
			Foreground(colorAmberBright).
			Bold(true)

	StyleTabInactive = lipgloss.NewStyle().
				Foreground(colorMid)

	StyleTabDot = lipgloss.NewStyle().
			Foreground(colorAmber)

	StyleTabActiveHotkey = lipgloss.NewStyle().
				Foreground(colorAmber)

	StyleTabInactiveHotkey = lipgloss.NewStyle().
				Foreground(colorStone)

	// Diff indicators
	StyleDiff = lipgloss.NewStyle().
			Foreground(colorOkText)

	StyleDiffDel = lipgloss.NewStyle().
			Foreground(colorErrText)

	// Skill / command badges
	StyleBadgeSkill = lipgloss.NewStyle().
			Foreground(colorStone)

	StyleBadgeSession = lipgloss.NewStyle().
				Foreground(colorAmber)

	StyleBadgeCommand = lipgloss.NewStyle().
				Foreground(colorSlate)

	StyleBadgeSubtask = lipgloss.NewStyle().
				Foreground(colorMid)

	StyleBadgeTool = lipgloss.NewStyle().
			Foreground(colorAsh)

	// Leader panel (bottom strip)
	StyleLeaderPanelBg = lipgloss.NewStyle().
				Background(colorDeep).
				Foreground(colorMid)

	StyleLeaderPanelSep = lipgloss.NewStyle().
				Foreground(colorMid).
				Background(colorDeep)

	StyleLeaderTitle = lipgloss.NewStyle().
				Foreground(colorAmber).
				Bold(true).
				Background(colorDeep).
				Padding(0, 1)

	StyleLeaderKey = lipgloss.NewStyle().
			Foreground(colorAmberBright).
			Bold(true).
			Background(colorDeep)

	StyleLeaderGroup = lipgloss.NewStyle().
				Foreground(colorAsh).
				Background(colorDeep)

	StyleLeaderLabel = lipgloss.NewStyle().
				Foreground(colorStone).
				Background(colorDeep)

	StyleLeaderDismiss = lipgloss.NewStyle().
				Foreground(colorSlate).
				Background(colorDeep).
				Padding(0, 1)

	StyleLeaderDimSep = lipgloss.NewStyle().
				Foreground(colorDark).
				Background(colorDeep)

	StyleLeaderSectionHeader = lipgloss.NewStyle().
					Foreground(colorAmberMid).
					Background(colorDeep).
					Bold(true)

	// Finder overlay
	StyleFinderBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAmberMid).
			Background(colorDeep).
			Padding(1, 2)

	StyleFinderSeparator = lipgloss.NewStyle().
				Foreground(colorMid).
				Background(colorDeep)

	StyleFinderSelected = lipgloss.NewStyle().
				Background(colorShadow).
				Foreground(colorWhite)

	StyleFinderSelectedLabel = lipgloss.NewStyle().
					Foreground(colorAmberBright).
					Bold(true).
					Background(colorShadow)

	StyleFinderSelectedDesc = lipgloss.NewStyle().
				Foreground(colorStone).
				Background(colorShadow)

	StyleFinderItem = lipgloss.NewStyle().
			Foreground(colorStone).
			Background(colorDeep)

	StyleFinderItemLabel = lipgloss.NewStyle().
				Foreground(colorAsh).
				Background(colorDeep)

	StyleFinderItemDesc = lipgloss.NewStyle().
				Foreground(colorSlate).
				Background(colorDeep)

	StyleFinderCount = lipgloss.NewStyle().
				Foreground(colorSlate).
				Background(colorDeep)

	StyleFinderHint = lipgloss.NewStyle().
			Foreground(colorStone).
			Background(colorDeep)

	// Finder kind badges — unselected (colorDeep bg)
	StyleBadgeFinderAgent = lipgloss.NewStyle().
				Foreground(colorOkText).
				Background(colorDeep)

	StyleBadgeFinderSkill = lipgloss.NewStyle().
				Foreground(colorAmber).
				Background(colorDeep)

	StyleBadgeFinderCommand = lipgloss.NewStyle().
				Foreground(colorSlate).
				Background(colorDeep)

	StyleBadgeFinderTool = lipgloss.NewStyle().
				Foreground(colorAsh).
				Background(colorDeep)

	// Finder kind badges — selected (colorShadow bg)
	StyleBadgeFinderAgentSel = lipgloss.NewStyle().
					Foreground(colorOkText).
					Background(colorShadow)

	StyleBadgeFinderSkillSel = lipgloss.NewStyle().
					Foreground(colorAmber).
					Background(colorShadow)

	StyleBadgeFinderCommandSel = lipgloss.NewStyle().
					Foreground(colorSlate).
					Background(colorShadow)

	StyleBadgeFinderToolSel = lipgloss.NewStyle().
				Foreground(colorAsh).
				Background(colorShadow)

	// Builtin agent badge
	StyleBadgeBuiltin = lipgloss.NewStyle().
				Foreground(colorSlate)

	// Bar graph
	StyleBarFilled = lipgloss.NewStyle().Foreground(colorAmber)
	StyleBarEmpty  = lipgloss.NewStyle().Foreground(colorDark)

	// Session list rows — compact, no vertical padding
	StyleSessionItem = lipgloss.NewStyle().
				Foreground(colorAsh).
				Bold(true).
				Padding(0, 1)

	StyleSessionItemSelected = lipgloss.NewStyle().
					Background(colorDark).
					Foreground(colorAmberBright).
					Bold(true).
					Padding(0, 1)

	StyleSessionDateGroup = lipgloss.NewStyle().
				Foreground(colorAmberMid).
				Bold(true).
				Padding(0, 1)

	StyleDirsActiveHeader = lipgloss.NewStyle().
				Foreground(colorAmber).
				Bold(true)

	StyleDirsActiveDir = lipgloss.NewStyle().
				Foreground(colorAmberBright)

	// Overview — connected servers section
	StyleOverviewSectionConnected = lipgloss.NewStyle().
					Foreground(colorOkText).
					Bold(true)

	// Overview tab
	StyleOverviewSectionAll = lipgloss.NewStyle().
				Foreground(colorSlate).
				Bold(true)

	StyleOverviewSpinner = lipgloss.NewStyle().
				Foreground(colorAmberBright)

	StyleOverviewAgentActive = lipgloss.NewStyle().
					Foreground(colorAmberBright).
					Bold(true)

	StyleOverviewAgentIdle = lipgloss.NewStyle().
				Foreground(colorAsh)

	StyleOverviewSelectedRow = lipgloss.NewStyle().
					Background(colorDark)

	// Selected-row variants — carry colorDark background so inline renders
	// match the highlight strip and never show a mismatched bg color.
	StyleOverviewSelectedMuted = lipgloss.NewStyle().
					Foreground(colorStone).
					Background(colorDark)

	StyleOverviewSelectedSpinner = lipgloss.NewStyle().
					Foreground(colorAmberBright).
					Background(colorDark)

	StyleOverviewSelectedAgentActive = lipgloss.NewStyle().
						Foreground(colorAmberBright).
						Background(colorDark).
						Bold(true)

	StyleOverviewSelectedAgentIdle = lipgloss.NewStyle().
					Foreground(colorAsh).
					Background(colorDark)

	StyleOverviewSelectedYellowDot = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#FFAA00")).
					Background(colorDark)

	StyleOverviewDir = lipgloss.NewStyle().
				Foreground(colorSilver)

	// Confirm dialog overlay
	StyleConfirmBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAmberMid).
			Background(colorDeep).
			Padding(1, 2)

	StyleConfirmTitle = lipgloss.NewStyle().
				Foreground(colorAmberBright).
				Bold(true).
				Background(colorDeep)

	StyleConfirmMessage = lipgloss.NewStyle().
				Foreground(colorAsh).
				Background(colorDeep)

	StyleConfirmSeparator = lipgloss.NewStyle().
				Foreground(colorMid).
				Background(colorDeep)

	StyleConfirmHint = lipgloss.NewStyle().
				Foreground(colorAmber).
				Background(colorDeep)

	// Settings dialog
	StyleSettingsBox = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorAmberMid).
				Background(colorDeep).
				Padding(1, 2)

	StyleSettingsTitle = lipgloss.NewStyle().
				Foreground(colorAmberBright).
				Bold(true).
				Background(colorDeep)

	StyleSettingsItem = lipgloss.NewStyle().
				Foreground(colorAsh).
				Background(colorDeep)

	StyleSettingsSelected = lipgloss.NewStyle().
				Foreground(colorAmberBright).
				Bold(true).
				Background(colorShadow)

	StyleSettingsCheck = lipgloss.NewStyle().
				Foreground(colorAmber).
				Background(colorDeep)

	StyleSettingsCheckSelected = lipgloss.NewStyle().
					Foreground(colorAmberBright).
					Background(colorShadow)

	splashEyeStyle = lipgloss.NewStyle().
			Foreground(colorAmberBright).
			Bold(true)

	splashHintStyle = lipgloss.NewStyle().
			Foreground(colorMid)

	splashPressStyle = lipgloss.NewStyle().
				Background(colorAmberDim).
				Foreground(colorAmberBright).
				Padding(0, 1)

	splashTitleArtDimStyle = lipgloss.NewStyle().
				Foreground(colorAmberMid)

	splashTitleArtBrightStyle = lipgloss.NewStyle().
					Foreground(colorAmberBright).
					Bold(true)
)

// ApplyAccent updates all accent-dependent color vars and styles.
func ApplyAccent(p AccentPalette) {
	colorAmberDim = lipgloss.Color(p.Dim)
	colorAmberMid = lipgloss.Color(p.Mid)
	colorAmber = lipgloss.Color(p.Base)
	colorAmberBright = lipgloss.Color(p.Bright)

	// Re-derive all styles that use accent colors
	StyleActiveBorder = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colorAmberBright)

	StyleAppHeader = lipgloss.NewStyle().
		Background(colorDark).
		Foreground(colorAmberBright).
		Bold(true).
		Padding(0, 2)

	StyleListItemSelected = lipgloss.NewStyle().
		Background(colorDark).
		Foreground(colorAmberBright).
		Bold(true).
		Padding(1, 1)

	StyleDetailTitle = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Bold(true).
		UnderlineStyle(lipgloss.UnderlineDotted).
		UnderlineColor(colorAmberMid)

	StyleStatusKey = lipgloss.NewStyle().
		Foreground(colorAmber).
		Bold(true)

	StyleActiveInput = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(colorAmberMid).
		Padding(0, 1)

	StyleTabActive = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Bold(true)

	StyleTabDot = lipgloss.NewStyle().
		Foreground(colorAmber)

	StyleTabActiveHotkey = lipgloss.NewStyle().
		Foreground(colorAmber)

	StyleBadgeSession = lipgloss.NewStyle().
		Foreground(colorAmber)

	StyleLeaderTitle = lipgloss.NewStyle().
		Foreground(colorAmber).
		Bold(true).
		Background(colorDeep).
		Padding(0, 1)

	StyleLeaderKey = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Bold(true).
		Background(colorDeep)

	StyleLeaderSectionHeader = lipgloss.NewStyle().
		Foreground(colorAmberMid).
		Background(colorDeep).
		Bold(true)

	StyleFinderBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAmberMid).
		Background(colorDeep).
		Padding(1, 2)

	StyleFinderSelectedLabel = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Bold(true).
		Background(colorShadow)

	StyleBadgeFinderSkill = lipgloss.NewStyle().
		Foreground(colorAmber).
		Background(colorDeep)

	StyleBadgeFinderSkillSel = lipgloss.NewStyle().
		Foreground(colorAmber).
		Background(colorShadow)

	StyleBarFilled = lipgloss.NewStyle().Foreground(colorAmber)

	StyleSessionItemSelected = lipgloss.NewStyle().
		Background(colorDark).
		Foreground(colorAmberBright).
		Bold(true).
		Padding(0, 1)

	StyleSessionDateGroup = lipgloss.NewStyle().
		Foreground(colorAmberMid).
		Bold(true).
		Padding(0, 1)

	StyleDirsActiveHeader = lipgloss.NewStyle().
		Foreground(colorAmber).
		Bold(true)

	StyleDirsActiveDir = lipgloss.NewStyle().
		Foreground(colorAmberBright)

	StyleOverviewSpinner = lipgloss.NewStyle().
		Foreground(colorAmberBright)

	StyleOverviewAgentActive = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Bold(true)

	StyleOverviewSelectedSpinner = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Background(colorDark)

	StyleOverviewSelectedAgentActive = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Background(colorDark).
		Bold(true)

	StyleOverviewSelectedAgentIdle = lipgloss.NewStyle().
		Foreground(colorAsh).
		Background(colorDark)

	StyleOverviewDir = lipgloss.NewStyle().
		Foreground(colorSilver)

	StyleConfirmBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAmberMid).
		Background(colorDeep).
		Padding(1, 2)

	StyleConfirmHint = lipgloss.NewStyle().
		Foreground(colorAmber).
		Background(colorDeep)

	StyleSettingsBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAmberMid).
		Background(colorDeep).
		Padding(1, 2)

	StyleSettingsTitle = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Bold(true).
		Background(colorDeep)

	StyleSettingsSelected = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Bold(true).
		Background(colorShadow)

	StyleSettingsCheck = lipgloss.NewStyle().
		Foreground(colorAmber).
		Background(colorDeep)

	StyleSettingsCheckSelected = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Background(colorShadow)

	splashEyeStyle = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Bold(true)

	splashHintStyle = lipgloss.NewStyle().
		Foreground(colorMid)

	splashPressStyle = lipgloss.NewStyle().
		Background(colorAmberDim).
		Foreground(colorAmberBright).
		Padding(0, 1)

	splashTitleArtDimStyle = lipgloss.NewStyle().
		Foreground(colorAmberMid)

	splashTitleArtBrightStyle = lipgloss.NewStyle().
		Foreground(colorAmberBright).
		Bold(true)
}
