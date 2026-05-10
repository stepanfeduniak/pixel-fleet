package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
)

// gridLayout calculates the number of columns and rows for the grid.
func gridLayout(count, termWidth, termHeight int) (cols, rows int) {
	if count == 0 {
		return 1, 1
	}
	if count == 1 {
		return 1, 1
	}
	if count == 2 {
		if termWidth > 120 {
			return 2, 1
		}
		return 1, 2
	}

	// For 3+, calculate optimal grid
	cols = int(math.Ceil(math.Sqrt(float64(count))))
	if cols < 2 {
		cols = 2
	}
	rows = int(math.Ceil(float64(count) / float64(cols)))
	return cols, rows
}

// renderGrid renders the session grid.
func renderGrid(sessions []session.Session, selected int, width, height int) string {
	if len(sessions) == 0 {
		return emptyStyle.Width(width).Height(height).Render(
			"\n\n  No active sessions.\n\n  Press [n] to create a new session.\n  Or run: cs <machine> <path>",
		)
	}

	cols, rows := gridLayout(len(sessions), width, height)

	// Calculate cell dimensions (accounting for borders: 2 chars each side)
	cellWidth := (width / cols) - 2
	if cellWidth < 20 {
		cellWidth = 20
	}
	// Reserve 2 lines for header, 1 for footer, borders take 2 per row
	availHeight := height - 3
	cellHeight := (availHeight / rows) - 2
	if cellHeight < 5 {
		cellHeight = 5
	}

	var rowStrings []string
	for row := 0; row < rows; row++ {
		var cellStrings []string
		for col := 0; col < cols; col++ {
			idx := row*cols + col
			if idx >= len(sessions) {
				// Empty cell
				cell := cellBorderStyle.
					Width(cellWidth).
					Height(cellHeight).
					Render("")
				cellStrings = append(cellStrings, cell)
				continue
			}

			s := sessions[idx]
			isSelected := idx == selected
			isCodex := s.Agent == "codex"
			isTerminal := s.Agent == "terminal"
			// Empty Agent (legacy / pre-Agent-field records) is treated as
			// claude — that's the historical default.
			isClaude := !isCodex && !isTerminal

			// Title bar: name + status + agent badge
			statusStr := statusStyle(int(s.Status)).Render(
				s.Status.Symbol() + " " + s.Status.String(),
			)
			name := s.Name
			if isSelected {
				name = "▸ " + name
			} else {
				name = "  " + name
			}
			titleStyle := cellTitleStyle
			switch {
			case isCodex:
				titleStyle = cellTitleStyle.Foreground(codexAccentColor)
			case isTerminal:
				titleStyle = cellTitleStyle.Foreground(terminalAccentColor)
			case isClaude:
				titleStyle = cellTitleStyle.Foreground(claudeAccentColor)
			}

			// Build the always-visible suffix: status + agent badge.
			suffix := "  " + statusStr
			switch {
			case isCodex:
				suffix += "  " + codexBadgeStyle.Render("CODEX")
			case isTerminal:
				suffix += "  " + terminalBadgeStyle.Render("TERM")
			case isClaude:
				suffix += "  " + claudeBadgeStyle.Render("CLAUDE")
			}

			// Build the variable prefix: name + @machine. When this combo
			// exceeds the cell width (minus the suffix), we truncate with
			// an ellipsis on unselected cells. Selected cells always show
			// the full title — the user is looking at that one specifically,
			// so a wrap there is acceptable.
			machineRaw := ""
			if s.Machine != "" {
				machineRaw = " @" + s.Machine
			}
			prefixVis := len(name) + len(machineRaw)
			suffixVis := len(stripAnsi(suffix))
			// cellWidth is the bordered cell's outer width; the content area
			// is cellWidth - 2 (one for each border edge). Round-trip with
			// the inter-segment "  " spacing.
			availForPrefix := cellWidth - 2 - suffixVis
			if availForPrefix < 4 {
				availForPrefix = 4
			}

			var styledPrefix string
			if !isSelected && prefixVis > availForPrefix {
				// Truncate the NAME, keep @machine whole. If the cell is so
				// narrow that there's no useful room for the name with the
				// machine still attached, drop the machine and let the name
				// have the whole prefix budget.
				spaceForName := availForPrefix - len(machineRaw)
				if spaceForName >= 6 {
					if spaceForName-1 < len(name) {
						name = name[:spaceForName-1] + "…"
					}
					styledPrefix = titleStyle.Render(name) + dimStyle().Render(machineRaw)
				} else {
					if availForPrefix-1 < len(name) {
						name = name[:availForPrefix-1] + "…"
					}
					styledPrefix = titleStyle.Render(name)
				}
			} else {
				styledPrefix = titleStyle.Render(name)
				if machineRaw != "" {
					styledPrefix += dimStyle().Render(machineRaw)
				}
			}
			title := styledPrefix + suffix

			// Content: captured pane output, trimmed to fit
			contentHeight := cellHeight - 2 // subtract title line and padding
			content := truncateContent(s.LastOutput, cellWidth-4, contentHeight)
			contentRendered := cellContentStyle.Render(content)

			cellContent := lipgloss.JoinVertical(lipgloss.Left, title, contentRendered)

			// Apply border style — each agent (or terminal) gets its own color.
			var borderStyle lipgloss.Style
			switch {
			case isCodex && isSelected:
				borderStyle = cellBorderCodexSelectedStyle
			case isCodex:
				borderStyle = cellBorderCodexStyle
			case isTerminal && isSelected:
				borderStyle = cellBorderTerminalSelectedStyle
			case isTerminal:
				borderStyle = cellBorderTerminalStyle
			case isClaude && isSelected:
				borderStyle = cellBorderClaudeSelectedStyle
			case isClaude:
				borderStyle = cellBorderClaudeStyle
			case isSelected:
				borderStyle = cellBorderSelectedStyle
			default:
				borderStyle = cellBorderStyle
			}

			cell := borderStyle.
				Width(cellWidth).
				Height(cellHeight).
				Render(cellContent)

			cellStrings = append(cellStrings, cell)
		}
		rowStrings = append(rowStrings, lipgloss.JoinHorizontal(lipgloss.Top, cellStrings...))
	}

	return lipgloss.JoinVertical(lipgloss.Left, rowStrings...)
}

// truncateContent trims captured pane output to fit within the cell.
func truncateContent(content string, width, height int) string {
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")

	// Take last N lines (most recent output)
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}

	// Truncate each line to width
	var result []string
	for _, line := range lines {
		// Strip ANSI escape codes for width calculation
		clean := stripAnsi(line)
		if len(clean) > width {
			line = clean[:width-1] + "…"
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// stripAnsi removes ANSI escape sequences from a string.
func stripAnsi(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}
