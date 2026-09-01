package apps_test

import (
	"testing"

	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/builtin"
)

// An empty agent name means "an ordinary session", which is a login shell.
//
// Resolve falls back to the first-registered app instead, which is claude.
// That fallback is what ForSession exists to avoid: the resolved app is what
// the launch command is built from, so getting it wrong does not just
// mislabel a cell — it execs an agent where the user asked for a shell.
func TestForSessionFallsBackToTerminal(t *testing.T) {
	for _, name := range []string{"", "  ", "something-unregistered"} {
		if got := apps.ForSession(name).Name(); got != apps.TerminalName {
			t.Errorf("ForSession(%q) = %q, want %q", name, got, apps.TerminalName)
		}
	}

	// Known names still resolve to themselves.
	for _, name := range []string{"claude", "codex", "terminal", "skills-viewer"} {
		if got := apps.ForSession(name).Name(); got != name {
			t.Errorf("ForSession(%q) = %q, want %q", name, got, name)
		}
	}
}

// Pins the trap itself, so that if the registration order ever changes this
// test explains why ForSession exists rather than silently becoming moot.
func TestResolveEmptyIsNotTerminal(t *testing.T) {
	got, matched := apps.Resolve("")
	if matched {
		t.Fatal(`Resolve("") reported a match`)
	}
	if got.Name() == apps.TerminalName {
		t.Skip("registration order changed; Resolve's fallback is now terminal")
	}
	t.Logf(`Resolve("") falls back to %q — this is why ForSession exists`, got.Name())
}
