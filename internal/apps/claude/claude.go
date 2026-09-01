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

func (app) Name() string             { return "claude" }
func (app) Aliases() []string        { return nil }
func (app) Label() string            { return "CLAUDE" }
func (app) DefaultLocalBin() string  { return "claude" }
func (app) DefaultRemoteBin() string { return "claude" }
func (app) NeedsBin() bool           { return true }
func (app) RequiresPath() bool       { return true }
func (app) IsSystem() bool           { return false }

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

// LaunchExec returns the bin path. cs no longer launches claude — a session
// is a shell and you start claude in it yourself — so this is only what the
// app viewer reports and what an out-of-tree caller building on the registry
// would use.
func (app) LaunchExec(ctx apps.LaunchCtx) string {
	if ctx.Bin == "" {
		return "claude"
	}
	return ctx.Bin
}

// claudeMarkers are the pieces of Claude Code's chrome that no other agent
// draws. Weights are derived from the live pane captures in
// internal/session/detect_status_test.go.
//
// Note what is absent: "esc to interrupt" appears in Codex's footer too, so
// it is evidence of *an* agent, not of Claude, and scoring it would make the
// two indistinguishable mid-turn.
var claudeMarkers = []apps.Marker{
	// The mode footer, present between turns and while generating.
	{Text: "auto mode on", Weight: 3},
	{Text: "? for shortcuts", Weight: 3},
	// The turn-completion line, e.g. "✻ Cogitated for 8s".
	{Text: "✻ ", Weight: 2},
	// The activity line's token counter, e.g. "(19s · ↓ 778 tokens)".
	{Text: " tokens)", Weight: 2},
	// Tool output gutter.
	{Text: "⎿", Weight: 2},
	// The permission modal, which carries no mode footer of its own.
	{Text: "Do you want to proceed?", Weight: 2},
	{Text: "Esc to cancel", Weight: 2},
	// Claude's conversation bullet — U+25CF, distinct from Codex's U+2022.
	{Text: "●", Weight: 1, LineStart: true},
}

// MatchesPane scores a pane against Claude Code's chrome.
func (app) MatchesPane(content string) int {
	return apps.Score(content, claudeMarkers)
}

// ProcessMatches returns true if the foreground command name looks like
// Claude. Discovery scans tmux's pane_current_command, which on most
// installs is just `claude` (or `node` if launched via a wrapper, which we
// don't try to detect — those sessions still land in the orphan list when
// the tmux session name starts with "cs-").
func (app) ProcessMatches(processName string) bool {
	return strings.Contains(strings.ToLower(processName), "claude")
}

// DoctorProbes contributes the standard install / version check. cs launches
// a shell and you start claude in it yourself, so all the doctor needs to
// confirm is that the binary is there.
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
	}
}
