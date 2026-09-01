package session

import (
	"testing"

	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/builtin"
)

// The fixtures are the same live pane captures the status detector is pinned
// against (see detect_status_test.go), so identification is tested on real
// agent output rather than invented marker strings.
func TestDetectAgentFromPane(t *testing.T) {
	tests := []struct {
		name string
		pane string
		want string
	}{
		{"claude working", claudeWorkingPane, "claude"},
		{"claude waiting", claudeWaitingAfterQuestionPane, "claude"},
		{"claude idle", claudeIdleAfterStatementPane, "claude"},
		{"claude permission modal", claudePermissionPane, "claude"},

		{"codex idle", codexIdleAfterStatementPane, "codex"},
		{"codex waiting", codexWaitingAfterQuestionPane, "codex"},
		{"codex working", codexWorkingThinkingPane, "codex"},
		{"codex permission modal", codexPermissionRadioPane, "codex"},
		{"codex working esc-to-interrupt", codexWorkingEscToInterruptPane, "codex"},

		{"plain shell", terminalPane, apps.TerminalName},
		{"ssh error", sshErrorPane, apps.TerminalName},
		{"empty pane", "", apps.TerminalName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectAgent(tt.pane, "", ""); got != tt.want {
				t.Errorf("DetectAgent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// "esc to interrupt" is in both agents' footers. On its own it is evidence
// that *an* agent is running, not which one, and scoring it for either would
// make them indistinguishable mid-turn.
func TestSharedVocabularyDecidesNothing(t *testing.T) {
	const ambiguous = `
some output line
esc to interrupt
`
	if got := DetectAgent(ambiguous, "", ""); got != apps.TerminalName {
		t.Errorf("DetectAgent() = %q on a pane with only shared chrome, want %q", got, apps.TerminalName)
	}
	for _, a := range apps.All() {
		if score := a.MatchesPane(ambiguous); score != 0 {
			t.Errorf("%s scored %d on shared-vocabulary-only chrome, want 0", a.Name(), score)
		}
	}
}

func TestNoAppMatchesAPlainShell(t *testing.T) {
	for _, a := range apps.All() {
		if score := a.MatchesPane(terminalPane); score != 0 {
			t.Errorf("%s scored %d on a plain shell pane, want 0", a.Name(), score)
		}
	}
}

// The process name is the strongest signal and must beat the pane contents:
// a local session that has just launched claude may still be showing the
// shell's scrollback for a frame or two.
func TestProcessNameWinsOverPaneContent(t *testing.T) {
	if got := DetectAgent(terminalPane, "claude", ""); got != "claude" {
		t.Errorf("DetectAgent() = %q, want claude from the process name", got)
	}
	if got := DetectAgent(claudeIdleAfterStatementPane, "codex", ""); got != "codex" {
		t.Errorf("DetectAgent() = %q, want codex from the process name", got)
	}
}

// A single unreadable frame must not flip the badge — but a session sitting
// back at its shell prompt must not keep a stale one either.
func TestDetectAgentStickiness(t *testing.T) {
	const unreadable = `
        ┌──────────────┐
        │  redrawing   │
        └──────────────┘
`
	if got := DetectAgent(unreadable, "", "claude"); got != "claude" {
		t.Errorf("DetectAgent() = %q on an ambiguous frame, want the previous claude", got)
	}
	if got := DetectAgent(terminalPane, "", "claude"); got != apps.TerminalName {
		t.Errorf("DetectAgent() = %q back at a shell prompt, want %q", got, apps.TerminalName)
	}
	// An idle Claude pane ends with its own "❯" box, which must not be read
	// as a shell prompt.
	if got := DetectAgent(claudeIdleAfterStatementPane, "", "claude"); got != "claude" {
		t.Errorf("DetectAgent() = %q on an idle Claude pane, want claude", got)
	}
}

func TestAtShellPrompt(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want bool
	}{
		{"bash prompt", "user@host:~/dir$", true},
		{"zsh prompt", "user@host ~/dir %", true},
		{"root prompt", "root@host:/#", true},
		{"trailing blank lines are skipped", "user@host:~$\n\n   \n", true},
		{"claude input box", "─────\n❯ re-run the follower\n─────", false},
		{"mid-output", "Traceback (most recent call last):", false},
		{"empty", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := atShellPrompt(tt.in); got != tt.want {
				t.Errorf("atShellPrompt(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// A shell in the foreground is proof no agent is running, and must win over
// chrome the agent left on screen when it exited.
func TestShellProcessResetsDetection(t *testing.T) {
	for _, shell := range []string{"zsh", "-zsh", "bash", "-bash", "fish", "/bin/sh"} {
		if got := DetectAgent(claudeIdleAfterStatementPane, shell, "claude"); got != apps.TerminalName {
			t.Errorf("DetectAgent(process=%q) = %q, want %q", shell, got, apps.TerminalName)
		}
	}
}

// An agent started through a wrapper reports the wrapper's name, so a
// non-shell process must fall through to the chrome rather than being read as
// "no agent running".
func TestWrapperProcessFallsThroughToPaneContent(t *testing.T) {
	if got := DetectAgent(claudeIdleAfterStatementPane, "node", ""); got != "claude" {
		t.Errorf("DetectAgent(process=node) = %q, want claude from the pane", got)
	}
	// A pager opened over an agent: not a shell, no chrome — keep the answer.
	if got := DetectAgent("(END)", "less", "codex"); got != "codex" {
		t.Errorf("DetectAgent(process=less) = %q, want the previous codex", got)
	}
}

func TestIsShellProcess(t *testing.T) {
	for _, name := range []string{"zsh", "-zsh", "bash", "/usr/bin/fish", "sh"} {
		if !isShellProcess(name) {
			t.Errorf("isShellProcess(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "claude", "codex", "node", "ssh", "vim", "python3"} {
		if isShellProcess(name) {
			t.Errorf("isShellProcess(%q) = true, want false", name)
		}
	}
}
