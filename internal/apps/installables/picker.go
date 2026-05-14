package installables

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps/viewerui"
	"github.com/stepanfeduniak/pixel-fleet/internal/config"
	"github.com/stepanfeduniak/pixel-fleet/internal/tmux"
)

// Run starts a bubbletea program that shows the given source in its
// normal browse mode, and switches into an install picker over `items`
// when the user presses `i`. Mirrors viewerui.Run's signature so
// existing viewers can swap their viewerui.Run(src) call for
// installables.Run(src, items) with no other changes.
//
// If items is empty the picker still binds `i` but reports "Nothing
// installable yet" — useful for stubbing during development.
func Run(src viewerui.Source, items []Installable) error {
	p := tea.NewProgram(newRootModel(src, items), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// viewMode is which sub-UI the wrapper is rendering: the host viewer's
// normal list, or the install-libraries picker.
type viewMode int

const (
	modeBrowse viewMode = iota
	modeInstall
)

// rootModel wraps two viewerui.Models — one over the host source, one
// over the installables source — and routes the user between them with
// `i` (browse → install) and `esc` (install → browse). Enter in install
// mode spawns a new tmux window in the local cs session running Claude
// with the installable's prompt, then switches tmux focus to it.
type rootModel struct {
	browse       viewerui.Model
	install      viewerui.Model
	installables []Installable
	mode         viewMode
	cfg          *config.Config

	// flash is a banner shown after a spawn so the user has feedback
	// without quitting the viewer. Persists until the user does
	// something next (esc, enter, navigation) — Bubble Tea only
	// re-renders on messages, so a wall-clock timer would need a Tick
	// to fire the redraw and isn't worth the complexity here.
	flash   string
	flashOK bool
}

func newRootModel(hostSrc viewerui.Source, items []Installable) rootModel {
	return rootModel{
		browse:       viewerui.NewModel(hostSrc),
		install:      viewerui.NewModel(&installSource{items: items}),
		installables: items,
		cfg:          config.Load(),
	}
}

func (m rootModel) Init() tea.Cmd { return m.browse.Init() }

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Window-size events go to both sub-models so the inactive one is
	// already correctly laid out when the user switches to it.
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		b, _ := m.browse.Update(msg)
		m.browse = b.(viewerui.Model)
		i, _ := m.install.Update(msg)
		m.install = i.(viewerui.Model)
		return m, nil
	}

	if k, ok := msg.(tea.KeyMsg); ok {
		switch m.mode {
		case modeBrowse:
			if k.String() == "i" {
				if len(m.installables) == 0 {
					m.flash = "Nothing installable yet."
					m.flashOK = false
					return m, nil
				}
				m.flash = ""
				m.mode = modeInstall
				return m, nil
			}
		case modeInstall:
			switch k.String() {
			case "esc":
				m.flash = ""
				m.mode = modeBrowse
				return m, nil
			case "enter":
				idx := m.install.Selected()
				if idx < 0 || idx >= len(m.installables) {
					return m, nil
				}
				inst := m.installables[idx]
				if err := m.spawnInstall(inst); err != nil {
					m.flash = "Install failed: " + err.Error()
					m.flashOK = false
					return m, nil
				}
				m.flash = fmt.Sprintf("Spawned %q — tmux switched to that window. Ctrl+q returns here.", inst.WindowName)
				m.flashOK = true
				return m, nil
			}
		}
	}

	// Default: forward to whichever sub-model owns the current mode.
	switch m.mode {
	case modeInstall:
		i, cmd := m.install.Update(msg)
		m.install = i.(viewerui.Model)
		return m, cmd
	default:
		b, cmd := m.browse.Update(msg)
		m.browse = b.(viewerui.Model)
		return m, cmd
	}
}

// spawnInstall creates a new tmux window in the local cs session running
// `claude '<inst.Prompt>'`. The prompt becomes Claude's first user
// message; Claude then walks the user through whatever shell or slash
// commands the install needs. After the window is up we switch tmux
// focus to it so the user lands in the install conversation.
func (m rootModel) spawnInstall(inst Installable) error {
	sessionName := m.cfg.SessionName
	if sessionName == "" {
		sessionName = "cs"
	}
	if !tmux.SessionExists(sessionName) {
		return fmt.Errorf("cs tmux session %q is not running", sessionName)
	}
	bin := m.cfg.ClaudeBin
	if bin == "" {
		bin = "claude"
	}
	// Build: cd ~ && exec claude '<prompt>'.
	// Single-quote the prompt so claude sees it as one positional arg.
	cmd := fmt.Sprintf("cd ~ && exec %s %s", bin, shellSingleQuote(inst.Prompt))
	if err := tmux.NewWindow(sessionName, inst.WindowName, cmd); err != nil {
		return err
	}
	// Best-effort focus switch. If it fails the install is still
	// running; the user can navigate manually.
	_ = tmux.SelectWindow(sessionName, inst.WindowName)
	return nil
}

// shellSingleQuote wraps s in single quotes for safe inclusion in a
// shell command. Embedded single quotes become '\''. Mirrors the
// shellQuote helper in internal/session/session.go but kept local so
// installables doesn't pull the session package transitively.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func (m rootModel) View() string {
	var body string
	switch m.mode {
	case modeInstall:
		body = m.install.View()
	default:
		body = m.browse.View()
	}
	// Always override viewerui's footer with a mode-aware help line so
	// `i` is discoverable in browse mode and `enter`/`esc` are obvious in
	// install mode. The flash banner, when set, takes the same slot and
	// wins until the next user action.
	body = m.renderFooterOver(body)
	if m.flash != "" {
		body = m.renderFlashOver(body)
	}
	return body
}

// renderFooterOver replaces the last rendered line (viewerui's default
// help footer) with a mode-aware help line that advertises the install
// keybinds — so the user can discover `i` without having to know it.
func (m rootModel) renderFooterOver(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return body
	}
	var help string
	switch m.mode {
	case modeInstall:
		help = " [↑↓/jk] navigate  [tab] switch panel  [enter] install  [esc] back  [q] quit"
	default:
		help = " [↑↓/jk] navigate  [tab] switch panel  [i] install library  [q] quit"
	}
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#9CA3AF")).
		Padding(0, 1)
	width := len([]rune(stripANSI(lines[len(lines)-1])))
	if width <= 0 {
		width = 80
	}
	lines[len(lines)-1] = style.Width(width).Render(help)
	return strings.Join(lines, "\n")
}

// flashStyle picks a green or red background depending on success.
func flashStyle(ok bool) lipgloss.Style {
	bg := lipgloss.Color("#065F46") // emerald-800
	if !ok {
		bg = lipgloss.Color("#7F1D1D") // red-900
	}
	return lipgloss.NewStyle().
		Background(bg).
		Foreground(lipgloss.Color("#F9FAFB")).
		Padding(0, 1).
		Bold(true)
}

// renderFlashOver overlays the flash banner on the body's footer line
// (the bottom row of the bubbletea-rendered output). We replace the
// footer with the banner so the help line is hidden while the flash
// is visible; the flash persists until the user takes an action that
// clears it.
func (m rootModel) renderFlashOver(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return body
	}
	style := flashStyle(m.flashOK)
	width := len([]rune(stripANSI(lines[len(lines)-1])))
	if width <= 0 {
		width = 80
	}
	banner := style.Width(width).Render(" " + m.flash)
	lines[len(lines)-1] = banner
	return strings.Join(lines, "\n")
}

// stripANSI removes ANSI escape sequences from s so we can measure a
// styled line's visible width. lipgloss doesn't expose Width() for a
// rendered string, so this is the poor-man's strip — enough for our
// "match the footer width" use case.
func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == 0x1b {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// installSource adapts the Installable list to viewerui.Source.
type installSource struct {
	items []Installable
}

func (s *installSource) Title() string {
	return fmt.Sprintf("Install   %d available   press [enter] to install, [esc] to go back", len(s.items))
}
func (s *installSource) Count() int { return len(s.items) }

func (s *installSource) ListLine(i int) string {
	it := s.items[i]
	return fmt.Sprintf("%-12s  %s", it.Name, it.Tagline)
}

func (s *installSource) Detail(i int) string {
	it := s.items[i]
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", it.Name)
	if it.Tagline != "" {
		fmt.Fprintf(&b, "%s\n", it.Tagline)
	}
	fmt.Fprintf(&b, "\n%s\n", it.Body)
	fmt.Fprintf(&b, "\n──── Spawns ────\n")
	fmt.Fprintf(&b, "Window: %s\n", it.WindowName)
	fmt.Fprintf(&b, "Prompt sent to claude:\n\n%s\n", it.Prompt)
	return b.String()
}
