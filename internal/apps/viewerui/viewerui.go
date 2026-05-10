// Package viewerui provides a small shared two-pane layout for in-process
// "viewer" apps (skills viewer, app viewer, …) — left-side list, right-side
// detail, TAB to switch focus.
//
// Apps that want this layout implement Source: a pointer to a slice of
// items and a function that renders the right-pane detail for a given
// index. The Run helper runs the bubbletea program with the shared
// keybindings and styles. Apps with bespoke needs can copy this code or
// embed Model directly.
package viewerui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Source is what a viewer app provides to render content in the two panes.
type Source interface {
	// Title is the dashboard-bar title (e.g. "Skills (129)" or "Apps (3)").
	Title() string
	// Count returns the number of list items.
	Count() int
	// ListLine returns the short label for item i shown in the left pane.
	ListLine(i int) string
	// Detail returns the multi-line text shown in the right pane for item i.
	Detail(i int) string
}

type focus int

const (
	focusList focus = iota
	focusDetail
)

// Model is a bubbletea model for the two-pane viewer.
type Model struct {
	src          Source
	selected     int
	focus        focus
	width        int
	height       int
	detailScroll int
	// listScroll keeps the highlighted row visible when the list grows
	// taller than the pane.
	listScroll int
}

// NewModel returns a viewer for the given source.
func NewModel(src Source) Model {
	return Model{src: src}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			if m.focus == focusList {
				m.focus = focusDetail
			} else {
				m.focus = focusList
			}
		case "shift+tab":
			if m.focus == focusList {
				m.focus = focusDetail
			} else {
				m.focus = focusList
			}
		case "up", "k":
			switch m.focus {
			case focusList:
				if m.selected > 0 {
					m.selected--
					m.detailScroll = 0
				}
			case focusDetail:
				if m.detailScroll > 0 {
					m.detailScroll--
				}
			}
		case "down", "j":
			switch m.focus {
			case focusList:
				if m.selected < m.src.Count()-1 {
					m.selected++
					m.detailScroll = 0
				}
			case focusDetail:
				m.detailScroll++ // bound-checked at render time
			}
		case "pgup":
			if m.focus == focusDetail {
				step := pageStep(m.height)
				m.detailScroll -= step
				if m.detailScroll < 0 {
					m.detailScroll = 0
				}
			}
		case "pgdown":
			if m.focus == focusDetail {
				m.detailScroll += pageStep(m.height)
			}
		case "home", "g":
			m.detailScroll = 0
		}
	}
	return m, nil
}

func pageStep(termHeight int) int {
	step := termHeight - 6
	if step < 1 {
		step = 1
	}
	return step
}

var (
	dimColor       = lipgloss.Color("#9CA3AF")
	whiteColor     = lipgloss.Color("#FFFFFF")
	accentColor    = lipgloss.Color("#A78BFA") // light purple — focused panel
	borderDimColor = lipgloss.Color("#4B5563")

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(whiteColor).
			Padding(0, 1).
			Underline(true)

	footerStyle = lipgloss.NewStyle().
			Foreground(dimColor).
			Padding(0, 1)
)

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	header := headerStyle.Width(m.width).Render(" pixel-fleet   " + m.src.Title())

	footer := footerStyle.Width(m.width).Render(
		" [tab] switch panel  [↑↓/jk] navigate  [pgup/pgdn] scroll detail  [q] quit",
	)

	bodyHeight := m.height - 2 // header + footer
	if bodyHeight < 4 {
		bodyHeight = 4
	}

	listWidth := m.width / 3
	if listWidth < 22 {
		listWidth = 22
	}
	if listWidth > 50 {
		listWidth = 50
	}
	detailWidth := m.width - listWidth
	if detailWidth < 20 {
		detailWidth = 20
	}

	listPanel := m.renderList(listWidth, bodyHeight)
	detailPanel := m.renderDetail(detailWidth, bodyHeight)

	body := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, detailPanel)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) renderList(width, height int) string {
	innerW := width - 2
	innerH := height - 2
	if innerH < 1 {
		innerH = 1
	}

	// Keep the selected row visible by scrolling the list.
	scroll := m.listScroll
	if m.selected < scroll {
		scroll = m.selected
	}
	if m.selected >= scroll+innerH {
		scroll = m.selected - innerH + 1
	}

	count := m.src.Count()
	var lines []string
	for i := scroll; i < scroll+innerH && i < count; i++ {
		raw := m.src.ListLine(i)
		raw = truncateLine(raw, innerW-2)
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(dimColor)
		if i == m.selected {
			prefix = "▸ "
			if m.focus == focusList {
				style = lipgloss.NewStyle().Foreground(whiteColor).Bold(true)
			} else {
				style = lipgloss.NewStyle().Foreground(whiteColor)
			}
		}
		lines = append(lines, style.Render(prefix+raw))
	}
	for len(lines) < innerH {
		lines = append(lines, "")
	}

	return panelStyle(m.focus == focusList).
		Width(width).
		Height(height).
		Render(strings.Join(lines, "\n"))
}

func (m Model) renderDetail(width, height int) string {
	innerW := width - 2
	innerH := height - 2
	if innerH < 1 {
		innerH = 1
	}
	if innerW < 1 {
		innerW = 1
	}

	var content string
	if m.src.Count() == 0 {
		content = "Nothing to show."
	} else if m.selected < m.src.Count() {
		content = m.src.Detail(m.selected)
	}

	// Wrap each line to the inner width, preserving paragraph breaks. We
	// don't wrap by word — long lines are truncated with an ellipsis.
	rawLines := strings.Split(content, "\n")
	wrapped := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		wrapped = append(wrapped, truncateLine(line, innerW))
	}

	if m.detailScroll > len(wrapped)-1 {
		m.detailScroll = max0(len(wrapped) - 1)
	}
	if m.detailScroll > 0 {
		wrapped = wrapped[m.detailScroll:]
	}
	if len(wrapped) > innerH {
		wrapped = wrapped[:innerH]
	}
	for len(wrapped) < innerH {
		wrapped = append(wrapped, "")
	}

	return panelStyle(m.focus == focusDetail).
		Width(width).
		Height(height).
		Render(strings.Join(wrapped, "\n"))
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func panelStyle(focused bool) lipgloss.Style {
	color := borderDimColor
	if focused {
		color = accentColor
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(0, 1)
}

func truncateLine(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if len([]rune(s)) <= width {
		return s
	}
	r := []rune(s)
	if width <= 1 {
		return string(r[:width])
	}
	return string(r[:width-1]) + "…"
}

// Run starts the viewer. Blocks until the user quits.
func Run(src Source) error {
	p := tea.NewProgram(NewModel(src), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
