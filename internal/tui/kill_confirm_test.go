package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
)

func sess(name string) session.Session { return session.Session{Name: name} }

// TestConfirmKillPinsTargetAcrossReorder reproduces the bug where killing the
// app-viewer killed a working session instead. The kill target used to be
// resolved live as m.sessions[m.selected] at confirm time, but the background
// refresh tick reassigns and reorders m.sessions while the confirm dialog is
// open (auto-restore can spawn a new window between `x` and `y`). When the
// grid reorders, the positional index slides onto a different session.
//
// The fix pins the chosen name when `x` is pressed. This test asserts the pin
// survives a reordering sessionsMsg — and that resolving via the live index
// would now point at the wrong session, which is exactly the old behavior.
func TestConfirmKillPinsTargetAcrossReorder(t *testing.T) {
	m := Model{
		mode:     ModeGrid,
		sessions: []session.Session{sess("alpha"), sess("beta"), sess("app-viewer")},
		selected: 2, // user has app-viewer highlighted
	}

	// Press `x`: enter confirm mode and pin the target by name.
	xKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}
	updated, _ := m.handleKey(xKey)
	m = updated.(Model)

	if m.mode != ModeConfirmKill {
		t.Fatalf("expected ModeConfirmKill after x, got mode %d", m.mode)
	}
	if m.killTarget != "app-viewer" {
		t.Fatalf("expected killTarget pinned to %q, got %q", "app-viewer", m.killTarget)
	}

	// A background tick fires while the dialog is open: auto-restore spawned
	// "orchestrator" at a lower window index, so the grid reorders. m.selected
	// stays 2, but slot 2 is now a working session ("beta").
	reordered := sessionsMsg{sess("orchestrator"), sess("alpha"), sess("beta"), sess("app-viewer")}
	updated, _ = m.Update(reordered)
	m = updated.(Model)

	// Guard: the live positional index now points at the WRONG session.
	if got := m.sessions[m.selected].Name; got != "beta" {
		t.Fatalf("precondition: expected slot %d to now hold %q, got %q", m.selected, "beta", got)
	}

	// The pin must still target what the user actually selected.
	if m.killTarget != "app-viewer" {
		t.Errorf("killTarget drifted after reorder: got %q, want %q", m.killTarget, "app-viewer")
	}
}

// TestConfirmKillCancelClearsTarget ensures cancelling the dialog drops the pin
// so a later stray render/confirm can't act on a stale target.
func TestConfirmKillCancelClearsTarget(t *testing.T) {
	m := Model{
		mode:       ModeConfirmKill,
		killTarget: "app-viewer",
		sessions:   []session.Session{sess("app-viewer")},
		selected:   0,
	}
	nKey := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}
	updated, _ := m.handleKey(nKey)
	m = updated.(Model)

	if m.mode != ModeGrid {
		t.Fatalf("expected ModeGrid after cancel, got mode %d", m.mode)
	}
	if m.killTarget != "" {
		t.Errorf("expected killTarget cleared on cancel, got %q", m.killTarget)
	}
}
