package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stepanfeduniak/pixel-fleet/internal/blocker"
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
)

func pressKey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeString(m Model, s string) Model {
	for _, r := range s {
		updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	return m
}

func blockedModel(d time.Duration) Model {
	return Model{
		mode:     ModeGrid,
		sessions: []session.Session{sess("alpha"), sess("beta")},
		selected: 0,
		blocker: blocker.State{
			Until:     time.Now().Add(d),
			StartedAt: time.Now(),
			Duration:  d,
		},
	}
}

// The core guarantee: while blocked, pressing enter on a grid cell must not
// produce a command. The focus path builds an exec.Command("tmux",
// "select-window", ...) inside the returned tea.Cmd, so a non-nil command
// here is a session the user got into.
func TestBlockedGridEnterEmitsNoCommand(t *testing.T) {
	m := blockedModel(20 * time.Minute)

	updated, cmd := m.handleKey(pressKey("enter"))
	m = updated.(Model)

	if cmd != nil {
		t.Fatal("blocked enter returned a command; the focus path is still reachable")
	}
	if m.mode != ModeGrid {
		t.Errorf("mode = %d, want ModeGrid", m.mode)
	}
	if m.blockerNotice.IsZero() {
		t.Error("expected the banner notice to be bumped so the user gets feedback")
	}
}

// The same door, from the scan view. A tracked discovered session also
// switches windows, so it needs the same gate.
func TestBlockedScanEnterEmitsNoCommand(t *testing.T) {
	m := blockedModel(20 * time.Minute)
	m.mode = ModeScan
	m.discovered = []session.DiscoveredSession{
		{Machine: "gpu-01", Tracked: true, DisplayName: "alpha"},
	}
	m.selectedDisc = 0

	updated, cmd := m.handleKey(pressKey("enter"))
	m = updated.(Model)

	if cmd != nil {
		t.Fatal("blocked scan-view enter returned a command; that door is still open")
	}
	if m.mode != ModeScan {
		t.Errorf("mode = %d, want ModeScan", m.mode)
	}
}

// Not blocked: enter must still work. A gate that's always shut is just a
// broken dashboard.
func TestUnblockedGridEnterStillFocuses(t *testing.T) {
	m := Model{
		mode:     ModeGrid,
		sessions: []session.Session{sess("alpha")},
		selected: 0,
	}

	_, cmd := m.handleKey(pressKey("enter"))
	if cmd == nil {
		t.Fatal("unblocked enter returned no command; focus is broken")
	}
}

// Everything except going into a session stays live during a blocker: the
// user asked to keep the gallery, and navigating it is not the habit being
// broken.
func TestBlockedGridStillNavigates(t *testing.T) {
	m := blockedModel(20 * time.Minute)
	m.sessions = []session.Session{sess("a"), sess("b"), sess("c")}
	m.selected = 0

	updated, _ := m.handleKey(pressKey("l"))
	m = updated.(Model)

	if m.selected != 1 {
		t.Errorf("selected = %d, want 1 — the grid should still be navigable", m.selected)
	}
	if m.mode != ModeGrid {
		t.Errorf("mode = %d, want ModeGrid", m.mode)
	}
}

func TestBlockerPickStartsBlocker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := Model{mode: ModeGrid, blockerCustom: textinput.New(), blockerBreak: textinput.New()}

	updated, _ := m.handleKey(pressKey("b"))
	m = updated.(Model)
	if m.mode != ModeBlockerPick {
		t.Fatalf("mode = %d, want ModeBlockerPick", m.mode)
	}

	// Second row: 25 min.
	updated, _ = m.handleKey(pressKey("down"))
	m = updated.(Model)
	updated, _ = m.handleKey(pressKey("enter"))
	m = updated.(Model)

	if m.mode != ModeGrid {
		t.Errorf("mode = %d, want ModeGrid after starting", m.mode)
	}
	if !m.blocker.Active(time.Now()) {
		t.Fatal("no blocker active after picking a duration")
	}
	if m.blocker.Duration != 25*time.Minute {
		t.Errorf("Duration = %v, want 25m", m.blocker.Duration)
	}
	if !m.blockerTicking {
		t.Error("countdown tick was not started")
	}
	// It must have reached disk, or it won't survive a restart.
	if !blocker.Load().Active(time.Now()) {
		t.Error("blocker was not persisted")
	}
}

func TestBlockerCustomDuration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := Model{mode: ModeBlockerPick, blockerCustom: textinput.New(), blockerBreak: textinput.New()}
	m.blockerCustom.Focus()
	m.blockerPick = len(blockerPresets) // the custom row

	m = typeString(m, "40")
	updated, _ := m.handleKey(pressKey("enter"))
	m = updated.(Model)

	if m.blocker.Duration != 40*time.Minute {
		t.Errorf("Duration = %v, want 40m from bare-number input", m.blocker.Duration)
	}
}

// A junk custom duration must leave enter inert rather than start a
// nonsense blocker or crash out of the dialog.
func TestBlockerCustomRejectsGarbage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := Model{mode: ModeBlockerPick, blockerCustom: textinput.New(), blockerBreak: textinput.New()}
	m.blockerCustom.Focus()
	m.blockerPick = len(blockerPresets)

	m = typeString(m, "soon")
	updated, _ := m.handleKey(pressKey("enter"))
	m = updated.(Model)

	if m.blocker.Active(time.Now()) {
		t.Error("garbage duration started a blocker")
	}
	if m.mode != ModeBlockerPick {
		t.Errorf("mode = %d, want to stay in ModeBlockerPick", m.mode)
	}
}

func TestBreakRequiresThePhrase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := blockedModel(20 * time.Minute)
	m.blockerCustom = textinput.New()
	m.blockerBreak = textinput.New()

	// `b` while blocked opens the break dialog rather than starting another.
	updated, _ := m.handleKey(pressKey("b"))
	m = updated.(Model)
	if m.mode != ModeBlockerBreak {
		t.Fatalf("mode = %d, want ModeBlockerBreak", m.mode)
	}

	// Wrong word: field clears, blocker holds.
	m = typeString(m, "yes")
	updated, _ = m.handleKey(pressKey("enter"))
	m = updated.(Model)
	if !m.blocker.Active(time.Now()) {
		t.Fatal("wrong phrase ended the blocker")
	}
	if m.blockerBreak.Value() != "" {
		t.Errorf("field = %q, want cleared after a wrong phrase", m.blockerBreak.Value())
	}

	// esc leaves the blocker standing.
	updated, _ = m.handleKey(pressKey("esc"))
	m = updated.(Model)
	if !m.blocker.Active(time.Now()) {
		t.Fatal("esc ended the blocker")
	}

	// The real phrase gets you out.
	updated, _ = m.handleKey(pressKey("b"))
	m = updated.(Model)
	m = typeString(m, blockerBreakPhrase)
	updated, _ = m.handleKey(pressKey("enter"))
	m = updated.(Model)

	if m.blocker.Active(time.Now()) {
		t.Error("correct phrase did not end the blocker")
	}
	if m.mode != ModeGrid {
		t.Errorf("mode = %d, want ModeGrid", m.mode)
	}
	if blocker.Load().Active(time.Now()) {
		t.Error("break did not clear the persisted state")
	}
}

// After a break, the in-flight countdown tick must not announce that the
// blocker "finished" — it was ended, and the user knows.
func TestTickAfterBreakDoesNotFlashFinished(t *testing.T) {
	m := Model{mode: ModeGrid, blockerTicking: true} // blocker already zeroed by the break

	updated, cmd := m.handleBlockerTick(time.Now())
	m = updated.(Model)

	if m.blockerDone {
		t.Error("stale tick flashed 'finished' after an early break")
	}
	if m.blockerTicking {
		t.Error("tick loop should have stopped")
	}
	if cmd != nil {
		t.Error("no further tick should be scheduled")
	}
}

func TestBlockerExpires(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	d := 5 * time.Minute
	start := time.Now()
	st, err := blocker.Start(d, start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	m := Model{mode: ModeGrid, blocker: st, blockerTicking: true}

	// A tick before the deadline keeps the loop going.
	updated, cmd := m.handleBlockerTick(start.Add(time.Minute))
	m = updated.(Model)
	if cmd == nil {
		t.Error("countdown stopped ticking before the deadline")
	}
	if !m.blockerTicking {
		t.Error("blockerTicking cleared while still blocked")
	}

	// A tick past it retires the blocker and clears disk state.
	updated, _ = m.handleBlockerTick(start.Add(d + time.Second))
	m = updated.(Model)

	if m.blocker.Active(time.Now()) {
		t.Error("blocker still active past its deadline")
	}
	if !m.blockerDone {
		t.Error("expected the 'finished' flash")
	}
	if m.blockerTicking {
		t.Error("tick loop should have stopped")
	}
	if blocker.Load().Active(time.Now()) {
		t.Error("expired blocker was left on disk")
	}

	// Any keypress dismisses the flash.
	updated, _ = m.handleKey(pressKey("r"))
	if updated.(Model).blockerDone {
		t.Error("flash survived a keypress")
	}
}

// A blocker written by a previous dashboard process must be picked back up
// at startup — that's what makes ctrl+c useless as an escape hatch.
func TestBlockerSurvivesModelRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, err := blocker.Start(30*time.Minute, time.Now()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	m := NewModel(nil, "cs", 2*time.Second, 60*time.Second)
	if !m.blocker.Active(time.Now()) {
		t.Fatal("a fresh model did not reload the blocker from disk")
	}
	if !m.blockerTicking {
		t.Error("reloaded blocker did not arm the countdown")
	}

	// And the reloaded blocker actually blocks.
	m.sessions = []session.Session{sess("alpha")}
	_, cmd := m.handleKey(pressKey("enter"))
	if cmd != nil {
		t.Error("reloaded blocker did not gate enter")
	}
}
