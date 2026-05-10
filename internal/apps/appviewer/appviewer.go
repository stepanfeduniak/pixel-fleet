// Package appviewer registers an in-process TUI that lists every app in
// the registry and shows its metadata. Useful while debugging, while
// writing a new app, or just to discover what's installed.
//
// Like the skills viewer, the app viewer is in-process: the launch script
// invokes the cs binary with `--app-viewer`, which main.go routes back
// into Run.
package appviewer

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps/viewerui"
)

func init() {
	apps.Register(&app{})
}

type app struct{}

func (app) Name() string                { return "apps" }
func (app) Aliases() []string           { return []string{"app", "appviewer"} }
func (app) Label() string               { return "APPS" }
func (app) DefaultLocalBin() string     { return "" }
func (app) DefaultRemoteBin() string    { return "" }
func (app) NeedsBin() bool              { return false }
func (app) SupportsRemoteControl() bool { return false }
func (app) RequiresPath() bool          { return false }
func (app) IsSystem() bool              { return true }

// Indigo — distinct from skills' amber and the agent palette.
func (a *app) Colors() apps.Colors {
	return apps.Colors{
		Border:         lipgloss.Color("#818CF8"), // indigo-400
		BorderSelected: lipgloss.Color("#E0E7FF"), // indigo-100
		Accent:         lipgloss.Color("#A5B4FC"), // indigo-300
		Bg:             lipgloss.Color("#312E81"), // indigo-900
	}
}

func (a *app) LaunchExec(ctx apps.LaunchCtx) string {
	bin, err := os.Executable()
	if err != nil || bin == "" {
		return "cs --app-viewer"
	}
	return fmt.Sprintf("%s --app-viewer", bin)
}

func (a *app) ProcessMatches(processName string) bool { return false }
func (a *app) DoctorProbes() []apps.Probe              { return nil }

// Run starts the app viewer TUI. Blocks until the user quits.
func Run() error {
	return viewerui.Run(&source{apps: apps.All()})
}

type source struct {
	apps []apps.App
}

func (s *source) Title() string {
	return fmt.Sprintf("App Viewer   %d registered", len(s.apps))
}
func (s *source) Count() int { return len(s.apps) }

func (s *source) ListLine(i int) string {
	a := s.apps[i]
	return fmt.Sprintf("%-10s  %s", a.Name(), a.Label())
}

func (s *source) Detail(i int) string {
	a := s.apps[i]
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", a.Name())
	fmt.Fprintf(&b, "Label:                  %s\n", a.Label())
	if aliases := a.Aliases(); len(aliases) > 0 {
		fmt.Fprintf(&b, "Aliases:                %s\n", strings.Join(aliases, ", "))
	}
	fmt.Fprintf(&b, "Needs binary:           %v\n", a.NeedsBin())
	if a.NeedsBin() {
		fmt.Fprintf(&b, "Default local bin:      %s\n", emptyDash(a.DefaultLocalBin()))
		fmt.Fprintf(&b, "Default remote bin:     %s\n", emptyDash(a.DefaultRemoteBin()))
	}
	fmt.Fprintf(&b, "Supports remote-control: %v\n", a.SupportsRemoteControl())
	fmt.Fprintf(&b, "Requires path:          %v\n", a.RequiresPath())

	probes := a.DoctorProbes()
	fmt.Fprintf(&b, "Doctor probes:          %d\n", len(probes))
	for _, p := range probes {
		fmt.Fprintf(&b, "  · %-22s %s\n", p.Key, p.Name)
	}

	colors := a.Colors()
	if (colors != apps.Colors{}) {
		fmt.Fprintf(&b, "\nColors:\n")
		fmt.Fprintf(&b, "  Border:           %s\n", colors.Border)
		fmt.Fprintf(&b, "  Border selected:  %s\n", colors.BorderSelected)
		fmt.Fprintf(&b, "  Accent:           %s\n", colors.Accent)
		fmt.Fprintf(&b, "  Background:       %s\n", colors.Bg)
	}

	// LaunchExec for an example launch. We don't have a real ctx here, so
	// pass an empty one to show the bare exec form.
	example := a.LaunchExec(apps.LaunchCtx{})
	fmt.Fprintf(&b, "\nExample launch exec:\n  %s\n", example)
	return b.String()
}

func emptyDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
