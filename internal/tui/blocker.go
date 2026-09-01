package tui

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stepanfeduniak/pixel-fleet/internal/blocker"
)

// blockerBreakPhrase is what you have to type out to end a blocker early.
// The friction is the feature: enough to defeat a reflex, not enough to
// trap you when something actually needs you.
const blockerBreakPhrase = "break"

// blockerNoticeTTL is how long "you're blocked" stays on the banner after
// the user tries a door that's shut.
const blockerNoticeTTL = 3 * time.Second

// blockerPresets are the offered durations. The custom entry lives at
// index len(blockerPresets), so the picker has len+1 rows.
var blockerPresets = []time.Duration{
	15 * time.Minute,
	25 * time.Minute,
	45 * time.Minute,
	60 * time.Minute,
	90 * time.Minute,
}

// blockerTickMsg drives the countdown. It runs at 1 s only while a blocker
// is active — the dashboard's own refresh tick is 2 s by default, which
// would make a seconds counter visibly stutter.
type blockerTickMsg time.Time

// blockerBellMsg reports that the end-of-blocker bell was written.
type blockerBellMsg struct{}

func (m Model) blockerTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return blockerTickMsg(t)
	})
}

// blockerBellCmd rings the terminal bell. It writes straight to /dev/tty
// rather than stdout, which bubbletea owns while in the alt screen. BEL
// moves no cursor, so it can't disturb the rendered frame.
func blockerBellCmd() tea.Cmd {
	return func() tea.Msg {
		if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
			_, _ = f.WriteString("\a")
			_ = f.Close()
		}
		return blockerBellMsg{}
	}
}

// handleBlockerTick advances the countdown and retires the blocker when
// its deadline passes.
func (m Model) handleBlockerTick(now time.Time) (tea.Model, tea.Cmd) {
	if m.blocker.Active(now) {
		return m, m.blockerTickCmd()
	}

	m.blockerTicking = false
	if m.blocker.Until.IsZero() {
		// Already cleared — an early break beat this tick to it. Say
		// nothing: the user knows, and "finished" would be a lie.
		return m, nil
	}

	log.Printf("Blocker finished after %s", m.blocker.Duration)
	if err := blocker.Clear(); err != nil {
		log.Printf("Failed to clear blocker state: %v", err)
	}
	m.blocker = blocker.State{}
	m.blockerDone = true
	return m, blockerBellCmd()
}

// startBlocker begins a blocker and returns to the grid.
func (m Model) startBlocker(d time.Duration, now time.Time) (Model, tea.Cmd) {
	st, err := blocker.Start(d, now)
	if err != nil {
		// The deadline is still good in memory, so hold the blocker for
		// this process rather than dropping it. Only the survive-a-restart
		// guarantee is lost, and the user is told.
		log.Printf("Blocker state not persisted (%v); holding in memory only", err)
		m.err = fmt.Errorf("blocker not saved to disk: %w", err)
	}
	m.blocker = st
	m.blockerDone = false
	m.blockerCustom.Blur()
	m.mode = ModeGrid
	log.Printf("Blocker started for %s (until %s)", d, st.Until.Format(time.RFC3339))

	if m.blockerTicking {
		return m, nil
	}
	m.blockerTicking = true
	return m, m.blockerTickCmd()
}

// pickedDuration resolves the highlighted row to a duration. The bool is
// false when the custom field holds something unparseable, which keeps
// enter inert rather than starting a garbage blocker.
func (m Model) pickedDuration() (time.Duration, bool) {
	if m.blockerPick < len(blockerPresets) {
		return blockerPresets[m.blockerPick], true
	}
	return blocker.ParseDuration(m.blockerCustom.Value())
}

func (m Model) handleBlockerPickKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	customIdx := len(blockerPresets)
	onCustom := m.blockerPick == customIdx
	key := msg.String()

	// j/k navigate the list, but must fall through to the text input once
	// the custom row has focus. The rule is about focus, not about which
	// letters happen to be safe to swallow.
	up := key == "up" || (!onCustom && key == "k")
	down := key == "down" || (!onCustom && key == "j")

	switch {
	case key == "esc":
		m.blockerCustom.Blur()
		m.mode = ModeGrid
		return m, nil

	case up:
		if m.blockerPick > 0 {
			m.blockerPick--
		}
		return m.focusBlockerPick()

	case down:
		if m.blockerPick < customIdx {
			m.blockerPick++
		}
		return m.focusBlockerPick()

	case key == "tab":
		m.blockerPick = (m.blockerPick + 1) % (customIdx + 1)
		return m.focusBlockerPick()

	case key == "enter":
		d, ok := m.pickedDuration()
		if !ok {
			return m, nil
		}
		return m.startBlocker(d, time.Now())
	}

	if onCustom {
		var cmd tea.Cmd
		m.blockerCustom, cmd = m.blockerCustom.Update(msg)
		return m, cmd
	}
	return m, nil
}

// focusBlockerPick focuses the custom-duration input only while its row is
// highlighted, so the caret appears exactly where typing would land.
func (m Model) focusBlockerPick() (Model, tea.Cmd) {
	if m.blockerPick == len(blockerPresets) {
		m.blockerCustom.Focus()
		return m, textinput.Blink
	}
	m.blockerCustom.Blur()
	return m, nil
}

func (m Model) handleBlockerBreakKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.blockerBreak.Blur()
		m.mode = ModeGrid
		return m, nil

	case "enter":
		if !strings.EqualFold(strings.TrimSpace(m.blockerBreak.Value()), blockerBreakPhrase) {
			// Wrong word: clear the field and stay put. No error text —
			// the prompt already says what to type.
			m.blockerBreak.Reset()
			return m, nil
		}
		// Log the break with what was left on the clock. Breaking should
		// leave a trace you can look back on.
		left := m.blocker.Remaining(time.Now())
		log.Printf("Blocker broken early with %s left of %s", blocker.FormatRemaining(left), m.blocker.Duration)
		if err := blocker.Clear(); err != nil {
			log.Printf("Failed to clear blocker state: %v", err)
		}
		m.blocker = blocker.State{}
		m.blockerTicking = false
		m.blockerDone = false
		m.blockerBreak.Blur()
		m.mode = ModeGrid
		return m, nil
	}

	var cmd tea.Cmd
	m.blockerBreak, cmd = m.blockerBreak.Update(msg)
	return m, cmd
}

// updateBlockerInputs forwards non-key messages (cursor blink) to whichever
// blocker input is on screen.
func (m Model) updateBlockerInputs(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.mode {
	case ModeBlockerPick:
		m.blockerCustom, cmd = m.blockerCustom.Update(msg)
	case ModeBlockerBreak:
		m.blockerBreak, cmd = m.blockerBreak.Update(msg)
	}
	return m, cmd
}

// viewBlockerBanner renders the one-line countdown above the grid.
//
// Countdown only, by design. A "2 sessions waiting for input" badge here
// would hand back the exact itch the blocker exists to stop.
func (m Model) viewBlockerBanner(now time.Time) string {
	left := blocker.FormatRemaining(m.blocker.Remaining(now))
	text := fmt.Sprintf(" ⏸  BLOCKED — %s left    sessions keep running; you stay out    [b] break", left)
	if now.Sub(m.blockerNotice) < blockerNoticeTTL {
		text = fmt.Sprintf(" ⏸  BLOCKED — %s left    not while the blocker is up.    [b] break", left)
	}
	return blockerBannerStyle.Width(m.width).Render(text)
}

func (m Model) viewBlockerPick() string {
	title := headerStyle.Width(m.width).Render(" pixel-fleet   Coding Blocker")

	var lines []string
	lines = append(lines,
		lipgloss.NewStyle().Bold(true).Render("Block yourself out of the fleet for how long?"),
		"",
	)

	rowStyle := func(selected bool) lipgloss.Style {
		if selected {
			return lipgloss.NewStyle().Foreground(whiteColor).Bold(true)
		}
		return dimStyle()
	}

	for i, d := range blockerPresets {
		prefix := "  "
		if i == m.blockerPick {
			prefix = "▸ "
		}
		lines = append(lines, rowStyle(i == m.blockerPick).Render(prefix+blocker.FormatDuration(d)))
	}

	customIdx := len(blockerPresets)
	prefix := "  "
	if m.blockerPick == customIdx {
		prefix = "▸ "
	}
	lines = append(lines,
		rowStyle(m.blockerPick == customIdx).Render(prefix+"custom")+"  "+m.blockerCustom.View(),
		"",
		dimStyle().Render("The gallery stays. Sessions keep running."),
		dimStyle().Render("You just can't go into them."),
		"",
		dimStyle().Render("[enter] start  [↑↓] pick  [esc] cancel"),
	)

	form := promptStyle.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	centered := lipgloss.Place(m.width, m.height-2, lipgloss.Center, lipgloss.Center, form)

	footer := footerStyle.Width(m.width).Render(
		fmt.Sprintf(" %s start  %s pick  %s cancel",
			footerKeyStyle.Render("[enter]"),
			footerKeyStyle.Render("[↑↓]"),
			footerKeyStyle.Render("[esc]"),
		),
	)

	return lipgloss.JoinVertical(lipgloss.Left, title, centered, footer)
}

func (m Model) viewBlockerBreak() string {
	title := headerStyle.Width(m.width).Render(" pixel-fleet   Break Blocker")

	left := blocker.FormatRemaining(m.blocker.Remaining(time.Now()))
	body := promptStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%s still on the clock.", left)),
		"",
		dimStyle().Render("Type "+lipgloss.NewStyle().Foreground(warningColor).Bold(true).Render(blockerBreakPhrase)+" to end it early."),
		"",
		m.blockerBreak.View(),
		"",
		dimStyle().Render("[enter] confirm  [esc] stay blocked"),
	))

	centered := lipgloss.Place(m.width, m.height-2, lipgloss.Center, lipgloss.Center, body)

	return lipgloss.JoinVertical(lipgloss.Left, title, centered)
}
