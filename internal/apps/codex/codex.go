// Package codex registers the Codex CLI app with the cs apps registry.
package codex

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
)

func init() {
	apps.Register(&app{})
}

type app struct{}

func (app) Name() string             { return "codex" }
func (app) Aliases() []string        { return nil }
func (app) Label() string            { return "CODEX" }
func (app) DefaultLocalBin() string  { return "codex" }
func (app) DefaultRemoteBin() string { return "codex" }
func (app) NeedsBin() bool           { return true }
func (app) RequiresPath() bool       { return true }
func (app) IsSystem() bool           { return false }

// Codex sessions wear strong red so they're unmistakable in a sea of claude
// blue. SelectedColor is red-200, a near-white tint that pops hard against
// the unselected red border.
func (app) Colors() apps.Colors {
	return apps.Colors{
		Border:         lipgloss.Color("#DC2626"), // red-600
		BorderSelected: lipgloss.Color("#FECACA"), // red-200
		Accent:         lipgloss.Color("#F87171"), // red-400
		Bg:             lipgloss.Color("#7F1D1D"), // red-900
	}
}

func (app) LaunchExec(ctx apps.LaunchCtx) string {
	if ctx.Bin == "" {
		return "codex"
	}
	return ctx.Bin
}

// codexMarkers are the pieces of the Codex TUI that Claude never draws.
// Weights are derived from the live pane captures in
// internal/session/detect_status_test.go.
var codexMarkers = []apps.Marker{
	// Turn separator, e.g. "─ Worked for 3m 15s ─────".
	{Text: "─ Worked for ", Weight: 3},
	// Streaming status line, e.g. "• Working (4m 03s • esc to interrupt)".
	{Text: "• Working (", Weight: 3},
	// The radio-button approval modal's footer.
	{Text: "Press enter to confirm", Weight: 3},
	// Model + cwd footer, e.g. "gpt-5.5 default fast · ~/repo".
	{Text: "default fast ·", Weight: 3},
	{Text: "gpt-", Weight: 2},
	// Codex's input prompt — U+203A, distinct from Claude's "❯".
	{Text: "› ", Weight: 2, LineStart: true},
	// Tool-call line and its output gutter.
	{Text: "• Ran ", Weight: 2, LineStart: true},
	{Text: "└ ", Weight: 1, LineStart: true},
	{Text: "Thinking…", Weight: 1},
}

// MatchesPane scores a pane against the Codex TUI.
func (app) MatchesPane(content string) int {
	return apps.Score(content, codexMarkers)
}

func (app) ProcessMatches(processName string) bool {
	return strings.Contains(strings.ToLower(processName), "codex")
}

// DoctorProbes contributes a single install-or-missing check. Codex's
// version output is unstable across releases, so we just report the path
// and the first line of `--version` together (best-effort).
func (app) DoctorProbes() []apps.Probe {
	return []apps.Probe{
		{
			Key:   "CODEX_BIN",
			Name:  "codex installed",
			Shell: `echo "CODEX_BIN::$(command -v codex 2>/dev/null || echo MISSING)|$(codex --version 2>&1 | head -n1 || true)"`,
			Evaluate: func(value string) (string, string) {
				path, version, _ := strings.Cut(value, "|")
				if path == "" || path == "MISSING" {
					return "WARN", "not installed (codex sessions on this machine will fail)"
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
