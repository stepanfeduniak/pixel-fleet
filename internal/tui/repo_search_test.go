package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
)

// Path suggestions used to be searched once per keystroke, with every result
// applied on arrival. Against a machine that had dropped off the network each
// of those searches was an ssh that sat in TCP retry for over a minute, and
// they landed out of order. These tests pin the two halves of the fix: only
// the newest search runs, and only the newest result is taken.

func TestStaleRepoSearchResultIsDropped(t *testing.T) {
	m := Model{
		mode:            ModeNewSession,
		repoSearchSeq:   7,
		pathSuggestions: []string{"~/current"},
	}

	// A search from four keystrokes ago finally comes back.
	updated, _ := m.Update(repoSearchMsg{seq: 3, repos: []string{"~/stale"}})
	m = updated.(Model)

	if len(m.pathSuggestions) != 1 || m.pathSuggestions[0] != "~/current" {
		t.Errorf("stale result overwrote suggestions: got %v", m.pathSuggestions)
	}
}

func TestCurrentRepoSearchResultIsApplied(t *testing.T) {
	m := Model{
		mode:            ModeNewSession,
		repoSearchSeq:   7,
		pathSuggestions: []string{"~/old"},
		selectedSugg:    3,
	}

	updated, _ := m.Update(repoSearchMsg{seq: 7, repos: []string{"~/fresh", "~/fresher"}})
	m = updated.(Model)

	if len(m.pathSuggestions) != 2 || m.pathSuggestions[0] != "~/fresh" {
		t.Errorf("current result not applied: got %v", m.pathSuggestions)
	}
	if m.selectedSugg != 0 {
		t.Errorf("selection not reset onto the new list: got %d", m.selectedSugg)
	}
}

func TestSupersededDebounceDoesNotSearch(t *testing.T) {
	m := Model{mode: ModeNewSession, repoSearchSeq: 5}

	// The timer from an earlier keystroke in the burst fires.
	_, cmd := m.Update(repoSearchDebounceMsg(2))
	if cmd != nil {
		t.Error("a superseded debounce tick started a search")
	}
}

// Each keystroke must bump the sequence, so that in a burst of N keystrokes
// the first N-1 debounce timers find themselves superseded and only the last
// one searches.
func TestQueueRepoSearchBumpsSequence(t *testing.T) {
	// An offline machine, so the search the last timer starts short-circuits
	// instead of reaching for the nil manager.
	m := Model{
		mode:            ModeNewSession,
		machines:        []session.Machine{{Name: "follower-00001"}},
		selectedMachine: 0,
		machineHealth:   map[string]session.Health{"follower-00001": session.HealthOffline},
	}

	var cmd tea.Cmd
	for i := 1; i <= 4; i++ {
		m, cmd = m.queueRepoSearch()
		if m.repoSearchSeq != i {
			t.Fatalf("after %d keystrokes seq = %d, want %d", i, m.repoSearchSeq, i)
		}
	}
	if cmd == nil {
		t.Error("queueRepoSearch returned no timer command")
	}

	// Only the last keystroke's timer is live.
	if _, c := m.Update(repoSearchDebounceMsg(3)); c != nil {
		t.Error("keystroke 3's timer still searches after keystroke 4")
	}
	if _, c := m.Update(repoSearchDebounceMsg(4)); c == nil {
		t.Error("the final keystroke's timer did not search")
	}
}

// A machine the last scan could not reach must not be dialled again from the
// path input — that is the call that used to wedge for ~75s per keystroke.
func TestSearchSkipsUnreachableMachine(t *testing.T) {
	for _, h := range []session.Health{session.HealthOffline, session.HealthDenied} {
		m := Model{
			mode:            ModeNewSession,
			machines:        []session.Machine{{Name: "follower-00001"}},
			selectedMachine: 0,
			machineHealth:   map[string]session.Health{"follower-00001": h},
			// manager is deliberately nil: reaching it would mean the
			// short-circuit failed and an ssh was about to go out.
		}

		cmd := m.searchRepos(9)
		if cmd == nil {
			t.Fatalf("health %v: expected a command that resolves to an empty result", h)
		}
		msg, ok := cmd().(repoSearchMsg)
		if !ok {
			t.Fatalf("health %v: unexpected message type %T", h, cmd())
		}
		if msg.seq != 9 {
			t.Errorf("health %v: seq = %d, want 9", h, msg.seq)
		}
		if len(msg.repos) != 0 {
			t.Errorf("health %v: expected no suggestions, got %v", h, msg.repos)
		}
	}
}
