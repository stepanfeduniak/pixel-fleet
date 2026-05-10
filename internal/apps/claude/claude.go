// Package claude registers the Claude Code app with the cs apps registry.
//
// Importing this package for side effects is enough — no exported symbols
// are intended for direct use. Pixel-fleet's internal/apps/builtin handles
// the import.
package claude

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
)

func init() {
	apps.Register(&app{})
}

type app struct{}

func (app) Name() string         { return "claude" }
func (app) Aliases() []string    { return nil }
func (app) Label() string        { return "CLAUDE" }
func (app) DefaultLocalBin() string  { return "claude" }
func (app) DefaultRemoteBin() string { return "claude" }
func (app) NeedsBin() bool       { return true }
func (app) SupportsRemoteControl() bool { return true }
func (app) RequiresPath() bool   { return true }
func (app) IsSystem() bool       { return false }

// Colors for Claude — light sky-blue. Pleasant, calm, and clearly distinct
// from codex's red and the working-status indicator's medium blue (#60A5FA).
// SelectedColor is sky-50 so the selection frame is unmistakable against
// the unselected sky-300 border.
func (app) Colors() apps.Colors {
	return apps.Colors{
		Border:         lipgloss.Color("#7DD3FC"), // sky-300
		BorderSelected: lipgloss.Color("#F0F9FF"), // sky-50
		Accent:         lipgloss.Color("#BAE6FD"), // sky-200
		Bg:             lipgloss.Color("#0C4A6E"), // sky-900
	}
}

// LaunchExec returns the bin path. Remote-control bridging is enabled after
// the session is up by sending the `/remote-control` slash command via
// tmux send-keys (see the RC enable snippet in session.buildRemoteCommand).
//
// We deliberately do NOT use `claude remote-control` as the launch command:
// all three of its spawn modes (same-dir, worktree, session) turn the
// launching terminal into a server-status landing page that the user
// cannot type into.
func (app) LaunchExec(ctx apps.LaunchCtx) string {
	if ctx.Bin == "" {
		return "claude"
	}
	return ctx.Bin
}

// ProcessMatches returns true if the foreground command name looks like
// Claude. Discovery scans tmux's pane_current_command, which on most
// installs is just `claude` (or `node` if launched via a wrapper, which we
// don't try to detect — those sessions still land in the orphan list when
// the tmux session name starts with "cs-").
func (app) ProcessMatches(processName string) bool {
	return strings.Contains(strings.ToLower(processName), "claude")
}

// DoctorProbes contributes the standard install / version checks plus the
// Claude-specific `claude remote-control --help` probe (which verifies the
// installed Claude is new enough to support the RC bridge).
func (app) DoctorProbes() []apps.Probe {
	return []apps.Probe{
		{
			Key:   "CLAUDE_BIN",
			Name:  "claude installed",
			Shell: `echo "CLAUDE_BIN::$(command -v claude 2>/dev/null || echo MISSING)|$(claude --version 2>&1 | head -n1 || true)"`,
			Evaluate: func(value string) (string, string) {
				path, version, _ := strings.Cut(value, "|")
				if path == "" || path == "MISSING" {
					return "FAIL", "not installed"
				}
				detail := strings.TrimSpace(version)
				if detail == "" {
					detail = path
				} else {
					detail = detail + "  (" + path + ")"
				}
				return "PASS", detail
			},
		},
		{
			Key:   "CLAUDE_RC",
			Name:  "remote-control supported",
			Shell: `if claude remote-control --help >/dev/null 2>&1; then echo "CLAUDE_RC::yes"; else echo "CLAUDE_RC::no"; fi`,
			Evaluate: func(value string) (string, string) {
				if strings.EqualFold(value, "yes") {
					return "PASS", "claude remote-control supported"
				}
				return "FAIL", "claude remote-control --help failed"
			},
		},
	}
}
