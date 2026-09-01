package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
)

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

	// Generic cell border (for sessions whose Agent doesn't match a
	// registered app — should be rare).
	cellBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderDim)

	cellBorderSelectedStyle = lipgloss.NewStyle().
				Border(lipgloss.DoubleBorder()).
				BorderForeground(accentColor).
				Bold(true)

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

// appCellBorder returns the rounded/double border style for a session's
// cell, given the app's colors and whether the cell is selected. Selected
// cells get a near-white tint and a DoubleBorder so the selection is
// unmistakable against the unselected accent.
func appCellBorder(c apps.Colors, selected bool) lipgloss.Style {
	if c == (apps.Colors{}) {
		// App didn't supply colors; use the framework defaults.
		if selected {
			return cellBorderSelectedStyle
		}
		return cellBorderStyle
	}
	if selected {
		return lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(c.BorderSelected).
			Bold(true)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c.Border)
}

// appTitleStyle returns the title text style for a session's name. Falls
// back to the white-bold default if the app didn't supply an accent color.
func appTitleStyle(c apps.Colors) lipgloss.Style {
	if c.Accent == "" {
		return cellTitleStyle
	}
	return cellTitleStyle.Foreground(c.Accent)
}

// appBadgeStyle returns the badge style (background + bold white text) for
// an app's label. Falls back to dim borderDim background if no colors.
func appBadgeStyle(c apps.Colors) lipgloss.Style {
	bg := c.Bg
	if bg == "" {
		bg = borderDim
	}
	return lipgloss.NewStyle().
		Foreground(whiteColor).
		Background(bg).
		Bold(true).
		Padding(0, 1)
}

// agentBadge renders the registered app's badge for a given agent name.
// Sessions with an empty Agent (legacy / pre-Agent-field records) are
// treated as the registry's default app — historically that's claude.
func agentBadge(agent string) string {
	app, _ := apps.Resolve(agent)
	if app == nil {
		return ""
	}
	return appBadgeStyle(app.Colors()).Render(app.Label())
}

// Coding blocker banner — dark text on the warning yellow, so it reads as
// a held state rather than an error, and can't be mistaken for chrome.
var (
	blockerBannerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#111827")).
				Background(warningColor).
				Bold(true).
				Padding(0, 1)

	blockerDoneStyle = lipgloss.NewStyle().
				Foreground(successColor).
				Bold(true).
				Padding(0, 1)
)
