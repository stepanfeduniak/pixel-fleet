package tui

import (
	"strings"
	"testing"

	"github.com/stepanfeduniak/pixel-fleet/internal/session"
)

// renderGrid is handed the exact number of lines it may occupy. Returning
// more makes the whole view taller than the terminal, which scrolls the
// alternate screen and leaves a torn copy of the previous frame on screen —
// the footer appears twice.
func TestRenderGridRespectsItsHeightBudget(t *testing.T) {
	// A realistic pane capture: long lines, box-drawing characters and
	// multi-byte glyphs, which is what an agent session preview looks like.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		switch i % 4 {
		case 0:
			b.WriteString(strings.Repeat("─", 180) + "\n")
		case 1:
			b.WriteString("⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt · ← 2 agents\n")
		case 2:
			b.WriteString(strings.Repeat("a very long ascii line of output ", 12) + "\n")
		default:
			b.WriteString("│ ❯ some prompt text │\n")
		}
	}
	output := b.String()

	sessions := []session.Session{
		{Name: "clean the loop", Machine: "68-209-72-130", LastOutput: output},
		{Name: "d", Machine: "home", LastOutput: output},
	}

	// The geometry the bug was reported at: a 204x59 pane, so View() hands
	// renderGrid height-3.
	for _, tc := range []struct{ w, h int }{
		{204, 56}, {204, 30}, {120, 20}, {80, 15}, {204, 10},
	} {
		got := lines(renderGrid(sessions, 0, tc.w, tc.h))
		if got > tc.h {
			t.Errorf("renderGrid(w=%d, h=%d) returned %d lines, budget is %d", tc.w, tc.h, got, tc.h)
		}
	}
}

func TestRenderGridRespectsBudgetForManySessions(t *testing.T) {
	var sessions []session.Session
	for i := 0; i < 9; i++ {
		sessions = append(sessions, session.Session{
			Name:       "s",
			LastOutput: strings.Repeat("output line\n", 60),
		})
	}
	for _, tc := range []struct{ w, h int }{
		{204, 56}, {204, 24}, {120, 18},
	} {
		got := lines(renderGrid(sessions, 0, tc.w, tc.h))
		if got > tc.h {
			t.Errorf("renderGrid(%d sessions, w=%d, h=%d) returned %d lines, budget is %d",
				len(sessions), tc.w, tc.h, got, tc.h)
		}
	}
}

func lines(s string) int { return strings.Count(s, "\n") + 1 }
