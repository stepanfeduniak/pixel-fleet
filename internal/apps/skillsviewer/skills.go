// Package skillsviewer registers the skills-viewer app and provides its
// TUI.
//
// The skills viewer is an in-process viewer (like the dashboard), not an
// external binary. The launch script invokes the cs binary with the
// `--skills-viewer` flag, which main.go routes back into Run.
package skillsviewer

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps/installables"
)

func init() {
	apps.Register(&app{})
}

type app struct{}

func (app) Name() string             { return "skills-viewer" }
func (app) Aliases() []string        { return []string{"skills", "skill"} }
func (app) Label() string            { return "SKILLS-VIEWER" }
func (app) DefaultLocalBin() string  { return "" }
func (app) DefaultRemoteBin() string { return "" }
// NeedsBin is false: the launch is the cs binary itself, which we resolve
// at launch time via os.Executable(). Setting this to false skips the
// remote PATH-prepend / not-found-guard machinery, which would be wrong
// for an in-process viewer.
func (app) NeedsBin() bool              { return false }
func (app) SupportsRemoteControl() bool { return false }
func (app) RequiresPath() bool          { return false }
func (app) IsSystem() bool              { return true }

// Skills viewer wears a warm amber so it stands out from the agents.
func (app) Colors() apps.Colors {
	return apps.Colors{
		Border:         lipgloss.Color("#F59E0B"), // amber-500
		BorderSelected: lipgloss.Color("#FEF3C7"), // amber-100
		Accent:         lipgloss.Color("#FCD34D"), // amber-300
		Bg:             lipgloss.Color("#78350F"), // amber-900
	}
}

// LaunchExec returns `<absolute-cs-path> --skills-viewer`. The framework's
// launch wrapper handles cd / setup; we just need to point at the cs
// binary that's currently running so the viewer renders inside the tmux
// window. Falls back to the bare `cs --skills-viewer` if Executable()
// fails (which happens on some exotic FS layouts) — PATH then resolves it.
func (app) LaunchExec(ctx apps.LaunchCtx) string {
	bin, err := os.Executable()
	if err != nil || bin == "" {
		return "cs --skills-viewer"
	}
	return fmt.Sprintf("%s --skills-viewer", bin)
}

// ProcessMatches deliberately returns false: the skills viewer is a
// pixel-fleet-internal viewer, not an external agent that discovery should
// orphan-detect by foreground-command name.
func (app) ProcessMatches(processName string) bool { return false }

// DoctorProbes returns a no-op check that confirms the user-level skills
// directory exists — useful so `cs doctor` flags a fresh machine that has
// no skills installed yet.
func (app) DoctorProbes() []apps.Probe {
	return []apps.Probe{
		{
			Key:   "SKILLS_DIR",
			Name:  "claude skills dir",
			Shell: `if [ -d "$HOME/.claude/skills" ]; then echo "SKILLS_DIR::$(find $HOME/.claude/skills -name SKILL.md -type f 2>/dev/null | wc -l)"; else echo "SKILLS_DIR::missing"; fi`,
			Evaluate: func(value string) (string, string) {
				value = strings.TrimSpace(value)
				if value == "" || value == "missing" {
					return "WARN", "~/.claude/skills not found (skills viewer will be empty)"
				}
				return "PASS", value + " skill(s) installed"
			},
		},
	}
}

// Run starts the skills viewer TUI. main.go calls this when invoked with
// `--skills-viewer`. Blocks until the user quits.
//
// The viewer renders through the shared installables.Run wrapper, which
// adds an `i` keybind that switches into a library-install picker (see
// Installables() for the entries). Selecting an installable spawns a
// new tmux window in the local cs session running Claude with an
// install prompt.
func Run() error {
	return installables.Run(&source{skills: Discover()}, Installables())
}

// source adapts the Skill list to viewerui.Source.
type source struct {
	skills []Skill
}

func (s *source) Title() string {
	return fmt.Sprintf("Skills Viewer   %d installed   [i] install library", len(s.skills))
}
func (s *source) Count() int { return len(s.skills) }

func (s *source) ListLine(i int) string {
	sk := s.skills[i]
	if sk.Group != "" && sk.Group != sk.Name {
		return fmt.Sprintf("%s · %s", sk.Group, sk.Name)
	}
	return sk.Name
}

func (s *source) Detail(i int) string {
	sk := s.skills[i]
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", sk.Name)
	if sk.Group != "" {
		fmt.Fprintf(&b, "Group:  %s\n", sk.Group)
	}
	fmt.Fprintf(&b, "Path:   %s\n", sk.Path)
	if sk.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", sk.Description)
	}
	fmt.Fprintf(&b, "\n──── SKILL.md ────\n\n%s", sk.Body)
	return b.String()
}
