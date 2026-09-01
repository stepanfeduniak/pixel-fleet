// Package terminal registers the plain-shell "no agent" app with the cs
// apps registry. Useful when the user just wants a persistent shell on a
// machine — the cs persistence (tmux + linger) still applies.
package terminal

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
)

func init() {
	apps.Register(&app{})
}

type app struct{}

func (app) Name() string      { return "terminal" }
func (app) Aliases() []string { return []string{"term", "shell", "bash"} }
func (app) Label() string     { return "TERM" }

// Terminal has no binary of its own — it execs the user's $SHELL. NeedsBin
// is false so the remote launch skips the not-found guard and the
// PATH/nvm prepend.
func (app) DefaultLocalBin() string  { return "" }
func (app) DefaultRemoteBin() string { return "" }
func (app) NeedsBin() bool           { return false }
func (app) RequiresPath() bool       { return true }
func (app) IsSystem() bool           { return false }

// Green completes the stoplight: claude blue, codex red, terminal green.
func (app) Colors() apps.Colors {
	return apps.Colors{
		Border:         lipgloss.Color("#4ADE80"), // green-400
		BorderSelected: lipgloss.Color("#DCFCE7"), // green-100
		Accent:         lipgloss.Color("#86EFAC"), // green-300
		Bg:             lipgloss.Color("#14532D"), // green-900
	}
}

// LaunchExec returns a login-shell exec target. We use $SHELL with a
// /bin/bash fallback so the user gets their normal interactive environment
// (nvm, aliases, prompt, etc.). The launching shell will exec into this,
// so when the user exits the shell, the tmux window closes naturally.
func (app) LaunchExec(ctx apps.LaunchCtx) string {
	return `${SHELL:-/bin/bash} -l`
}

// MatchesPane returns 0. Terminal is the fallback, not something to detect:
// a session is a shell until one of the agents is recognised in it, and a
// shell has no chrome of its own to match on.
func (app) MatchesPane(content string) int { return 0 }

// ProcessMatches deliberately returns false. A bare login shell is too
// generic to mark as an "orphaned terminal session" by command name alone
// — the cs-managed tmux session naming convention (cs-<slug>-<id>) is what
// catches orphaned terminals in discovery.
func (app) ProcessMatches(processName string) bool {
	return false
}

// DoctorProbes returns nothing. The framework's default tmux check covers
// terminal — there's no separate binary to verify.
func (app) DoctorProbes() []apps.Probe {
	return nil
}
