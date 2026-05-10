package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors - bright, high-contrast for dark terminals
	accentColor  = lipgloss.Color("#A78BFA") // light purple
	dimColor     = lipgloss.Color("#9CA3AF") // lighter gray
	successColor = lipgloss.Color("#34D399") // bright green
	warningColor = lipgloss.Color("#FCD34D") // bright yellow
	errorColor   = lipgloss.Color("#F87171") // bright red
	workingColor = lipgloss.Color("#60A5FA") // bright blue
	whiteColor   = lipgloss.Color("#FFFFFF")
	borderDim    = lipgloss.Color("#4B5563") // medium gray for unselected borders

	// Header bar - no background, just bold white with underline
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(whiteColor).
			Padding(0, 1).
			Underline(true)

	// Footer bar - no background
	footerStyle = lipgloss.NewStyle().
			Foreground(dimColor).
			Padding(0, 1)

	footerKeyStyle = lipgloss.NewStyle().
			Foreground(whiteColor).
			Bold(true)

	// Codex-specific accent colors. Codex sessions get a distinct red
	// look so they're unmistakable in a sea of claude sessions.
	// SelectedColor is a near-white tint so the selection frame pops
	// hard against the unselected red border.
	codexBorderColor         = lipgloss.Color("#DC2626") // strong red, unselected
	codexBorderSelectedColor = lipgloss.Color("#FECACA") // red-200, near-white tint, selected frame
	codexAccentColor         = lipgloss.Color("#F87171") // bright red, title text/badge
	codexBgColor             = lipgloss.Color("#7F1D1D") // dark red, badge background

	// Claude-specific accent colors. Light sky-blue — pleasant and calm,
	// clearly distinct from codex's red and the working-status indicator's
	// medium blue (#60A5FA). SelectedColor goes near-white so the
	// selection frame is unmistakable against the unselected border.
	claudeBorderColor         = lipgloss.Color("#7DD3FC") // sky-300, unselected
	claudeBorderSelectedColor = lipgloss.Color("#F0F9FF") // sky-50, near-white, selected frame
	claudeAccentColor         = lipgloss.Color("#BAE6FD") // sky-200, title text/badge
	claudeBgColor             = lipgloss.Color("#0C4A6E") // sky-900, badge bg

	// Terminal (no-agent) accent colors. Green completes the stoplight:
	// claude blue / codex red / terminal green. Green-400 unselected with
	// a near-white green-100 selected frame to match the codex/claude
	// luminance pattern.
	terminalBorderColor         = lipgloss.Color("#4ADE80") // green-400, unselected
	terminalBorderSelectedColor = lipgloss.Color("#DCFCE7") // green-100, near-white frame
	terminalAccentColor         = lipgloss.Color("#86EFAC") // green-300, title/badge fg
	terminalBgColor             = lipgloss.Color("#14532D") // green-900, badge bg

	// Cell styles
	cellBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderDim)

	cellBorderSelectedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(accentColor).
				Bold(true)

	cellBorderCodexStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(codexBorderColor)

	// Selected codex cell: switch to DoubleBorder + near-white tint so
	// the frame is obviously distinct from the unselected red rounded
	// border. Bold prefix marker also helps.
	cellBorderCodexSelectedStyle = lipgloss.NewStyle().
					Border(lipgloss.DoubleBorder()).
					BorderForeground(codexBorderSelectedColor).
					Bold(true)

	codexBadgeStyle = lipgloss.NewStyle().
				Foreground(whiteColor).
				Background(codexBgColor).
				Bold(true).
				Padding(0, 1)

	cellBorderClaudeStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(claudeBorderColor)

	// Selected claude cell: same idea — DoubleBorder + near-white sky
	// tint. Stands out clearly against the soft sky-300 of unselected.
	cellBorderClaudeSelectedStyle = lipgloss.NewStyle().
					Border(lipgloss.DoubleBorder()).
					BorderForeground(claudeBorderSelectedColor).
					Bold(true)

	cellBorderTerminalStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(terminalBorderColor)

	cellBorderTerminalSelectedStyle = lipgloss.NewStyle().
					Border(lipgloss.DoubleBorder()).
					BorderForeground(terminalBorderSelectedColor).
					Bold(true)

	terminalBadgeStyle = lipgloss.NewStyle().
				Foreground(whiteColor).
				Background(terminalBgColor).
				Bold(true).
				Padding(0, 1)

	claudeBadgeStyle = lipgloss.NewStyle().
				Foreground(whiteColor).
				Background(claudeBgColor).
				Bold(true).
				Padding(0, 1)

	cellTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(whiteColor)

	cellContentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#D1D5DB")) // light gray, readable

	// Status badge styles
	statusWorkingStyle = lipgloss.NewStyle().
				Foreground(workingColor).
				Bold(true)

	statusWaitingStyle = lipgloss.NewStyle().
				Foreground(warningColor).
				Bold(true)

	statusIdleStyle = lipgloss.NewStyle().
			Foreground(successColor).
			Bold(true)

	statusErrorStyle = lipgloss.NewStyle().
				Foreground(errorColor).
				Bold(true)

	statusUnknownStyle = lipgloss.NewStyle().
				Foreground(dimColor)

	// Empty state
	emptyStyle = lipgloss.NewStyle().
			Foreground(dimColor).
			Italic(true).
			Align(lipgloss.Center)

	// New session prompt
	promptStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accentColor).
			Padding(1, 2)

	promptLabelStyle = lipgloss.NewStyle().
				Foreground(accentColor).
				Bold(true)
)

func statusStyle(s int) lipgloss.Style {
	switch s {
	case 1: // Working
		return statusWorkingStyle
	case 2: // WaitingInput
		return statusWaitingStyle
	case 3: // Idle
		return statusIdleStyle
	case 4: // Error
		return statusErrorStyle
	default:
		return statusUnknownStyle
	}
}
