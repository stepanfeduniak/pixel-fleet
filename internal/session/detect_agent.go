package session

import (
	"strings"

	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
)

// DetectAgent works out what is running in a session's pane.
//
// cs no longer asks which agent to launch: every session is a login shell,
// and whatever the user starts in it — claude, codex, nothing — is recognised
// from what it draws. The result feeds the dashboard's badge and border colour
// and picks the status detector in DetectStatus.
//
// Signals, strongest first:
//
//  1. processName, tmux's pane_current_command for the session's window. Only
//     meaningful for sessions on "home": a remote session's local pane is
//     running ssh, which says nothing about the far side. Callers pass "" for
//     those.
//  2. screen, scored against each app's chrome. This is what carries remote
//     sessions, since the local pane holds the rendered remote screen.
//
// screen must be the *visible* pane, not a capture including scrollback.
// Detection answers "what is running now", and an agent's chrome stays in the
// scrollback long after it exits — a session would never stop calling itself
// claude.
//
// previous is the last detected name, used to keep the answer steady: a single
// ambiguous frame, such as a mid-redraw or a pager opened over the agent, must
// not flip the badge.
func DetectAgent(screen, processName, previous string) string {
	if app := apps.DetectFromProcess(processName); app != nil {
		return app.Name()
	}
	// A shell in the foreground is proof that no agent is: this is the clean
	// reset for local sessions, and it beats the pane contents because an
	// agent that has just exited usually leaves its last screen behind.
	//
	// Only an actual shell counts. Any other command falls through to the
	// content, because an agent started through a wrapper shows up under the
	// wrapper's name (claude via node, say) and its chrome is the better
	// evidence.
	if isShellProcess(processName) {
		return apps.TerminalName
	}
	if app := apps.DetectFromPane(screen); app != nil {
		return app.Name()
	}
	if previous != "" && previous != apps.TerminalName && !atShellPrompt(screen) {
		return previous
	}
	return apps.TerminalName
}

// shellNames are the interactive shells a cs session can be sitting in. A
// login shell shows up with a leading "-" (e.g. "-zsh"), and tmux reports the
// command without its path.
var shellNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true,
	"dash": true, "ksh": true, "csh": true, "tcsh": true,
}

func isShellProcess(processName string) bool {
	name := strings.TrimSpace(processName)
	name = strings.TrimPrefix(name, "-")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return shellNames[name]
}

// atShellPrompt reports whether the pane's last non-empty line looks like a
// shell waiting for a command. It is the reset for remote sessions, which have
// no process signal of their own — without it one would wear a stale badge
// until the agent's last screen scrolled away.
//
// Deliberately narrow: only the conventional shell terminators count. "❯" is
// excluded because Claude Code's input box uses it, and treating it as a shell
// prompt would reset the badge on every idle Claude session.
func atShellPrompt(screen string) bool {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimRight(lines[i], " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		return strings.HasSuffix(line, "$") ||
			strings.HasSuffix(line, "%") ||
			strings.HasSuffix(line, "#")
	}
	return false
}
