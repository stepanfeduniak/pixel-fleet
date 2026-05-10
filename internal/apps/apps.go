// Package apps is the registry of launchable agent apps for cs.
//
// Each app (claude, codex, terminal, …) lives in its own subpackage and
// registers itself via init(). The registry decouples the rest of pixel-fleet
// from any one app: session.BuildShellCommand, the TUI agent picker, the
// doctor, and discovery all iterate apps.All() instead of switching on a
// hardcoded set.
//
// To add a new app:
//
//  1. Create internal/apps/<name>/<name>.go that returns a value implementing
//     App and calls apps.Register in its init().
//  2. Blank-import the package from internal/apps/builtin/builtin.go (or, for
//     out-of-tree apps, from your own main.go).
//
// External apps in another module follow the same pattern: implement App,
// call apps.Register in init(), then blank-import the package.
package apps

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// LaunchCtx is the resolved context for a session launch. Apps consume it to
// produce shell strings; pixel-fleet's session package builds it from
// CreateOptions, config, and the per-machine resolution.
type LaunchCtx struct {
	// Path is the working directory on the target machine. May contain ~ or
	// shell metacharacters; apps that splice it into shell strings should
	// quote with shellQuote.
	Path string
	// Setup is an optional shell snippet that runs before the agent launches.
	// Empty means "no setup".
	Setup string
	// Bin is the resolved binary path the agent should exec. Empty when the
	// app reports NeedsBin() == false (e.g. terminal).
	Bin string
	// Machine is "home" for local launches or an SSH host alias.
	Machine string
	// WindowName is the local cs tmux window name (the user-visible label).
	WindowName string
}

// Colors describes an app's accent palette. The TUI uses these to tint cell
// borders and badges. Apps that don't care can return the zero Colors{} and
// the TUI will fall back to a neutral default.
type Colors struct {
	Border         lipgloss.Color // unselected cell border
	BorderSelected lipgloss.Color // selected cell border (typically a near-white tint)
	Accent         lipgloss.Color // title-text + badge foreground
	Bg             lipgloss.Color // badge background
}

// Probe is a doctor check contributed by an app. The doctor concatenates
// every app's Shell snippets into a single SSH round-trip, parses the
// `KEY::value` lines, then asks each Evaluate to translate the value into a
// pass/warn/fail result.
//
// Key must be unique across all apps (e.g. "CLAUDE_BIN", "CLAUDE_VERSION",
// "CLAUDE_RC"). Shell must print exactly one line of the form `KEY::value`.
type Probe struct {
	Key      string
	Name     string
	Shell    string
	Evaluate func(value string) (status, detail string)
}

// App is the integration point for a launchable agent.
type App interface {
	// Name is the canonical lowercase identifier used in SessionRecord.Agent
	// and CLI subcommands. Examples: "claude", "codex", "terminal".
	Name() string
	// Aliases returns alternate names accepted on the CLI in addition to
	// Name(). Example: terminal aliases to "term", "shell", "bash".
	Aliases() []string
	// Label is the short uppercase tag the dashboard renders in cell badges.
	// Examples: "CLAUDE", "CODEX", "TERM".
	Label() string
	// Colors returns the app's accent palette. Return Colors{} for a neutral
	// look; the TUI handles the fallback.
	Colors() Colors

	// NeedsBin reports whether this app launches a binary that must exist on
	// PATH. When true, the remote launch script prepends ~/.local/bin and
	// sources nvm.sh, plus runs a not-found guard. When false (terminal),
	// these are skipped.
	NeedsBin() bool
	// DefaultLocalBin / DefaultRemoteBin are the default binary names when
	// pixel-fleet's config does not override them. Apps that don't need a
	// binary may return "".
	DefaultLocalBin() string
	DefaultRemoteBin() string

	// LaunchExec returns the shell command that the launch script execs to
	// start the agent. The launch wrapper handles cwd and setup; LaunchExec
	// just produces the final exec target.
	//
	// Examples:
	//   claude / codex: ctx.Bin                      (the resolved binary)
	//   terminal:        "${SHELL:-/bin/bash} -l"    (login shell)
	LaunchExec(ctx LaunchCtx) string

	// SupportsRemoteControl reports whether the cs RC bootstrap should run
	// for sessions of this app. Currently only Claude Code has the
	// `/remote-control` slash command, so only the claude app returns true.
	SupportsRemoteControl() bool

	// ProcessMatches reports whether the given process name (e.g. the
	// foreground tmux pane_current_command) suggests an instance of this
	// app. Used by discovery to identify orphans.
	ProcessMatches(processName string) bool

	// DoctorProbes returns shell-side checks the doctor should run on each
	// machine. Empty / nil is fine — apps that don't need bespoke probes
	// rely on the framework's default install + version checks (which it
	// auto-generates when NeedsBin() is true).
	DoctorProbes() []Probe
}

// registry holds the registered apps. Built-ins register at init(); external
// apps register from their own init() after the user blank-imports them.
var (
	mu          sync.RWMutex
	registered  []App
	byName      = make(map[string]App)
	aliasOf     = make(map[string]string) // alias -> canonical name
	defaultName string                     // first registered app, the implicit default
)

// Register adds an app to the registry. Panics on duplicate Name() or alias
// collision — those are programming errors that should fail loudly at
// startup. Safe to call concurrently, but apps register at init() so in
// practice this runs single-threaded.
func Register(a App) {
	if a == nil {
		panic("apps.Register: nil app")
	}
	name := strings.ToLower(strings.TrimSpace(a.Name()))
	if name == "" {
		panic("apps.Register: app has empty Name()")
	}

	mu.Lock()
	defer mu.Unlock()

	if _, exists := byName[name]; exists {
		panic(fmt.Sprintf("apps.Register: duplicate app name %q", name))
	}
	if other, exists := aliasOf[name]; exists {
		panic(fmt.Sprintf("apps.Register: name %q collides with alias of %q", name, other))
	}

	for _, alias := range a.Aliases() {
		alias = strings.ToLower(strings.TrimSpace(alias))
		if alias == "" || alias == name {
			continue
		}
		if _, exists := byName[alias]; exists {
			panic(fmt.Sprintf("apps.Register: alias %q for %q collides with another app's name", alias, name))
		}
		if other, exists := aliasOf[alias]; exists {
			panic(fmt.Sprintf("apps.Register: alias %q for %q already registered to %q", alias, name, other))
		}
		aliasOf[alias] = name
	}

	byName[name] = a
	registered = append(registered, a)
	if defaultName == "" {
		defaultName = name
	}
}

// Lookup returns the app matching name (or one of its aliases), case- and
// whitespace-insensitive. Returns false if no app matches.
func Lookup(name string) (App, bool) {
	mu.RLock()
	defer mu.RUnlock()
	return lookupLocked(name)
}

func lookupLocked(name string) (App, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil, false
	}
	if a, ok := byName[key]; ok {
		return a, true
	}
	if canonical, ok := aliasOf[key]; ok {
		return byName[canonical], true
	}
	return nil, false
}

// Resolve is Lookup with a fallback to the default app. It returns the
// matched app and true on a hit, or the default app and false on a miss.
// Used by call sites that want a sensible default (e.g. legacy session
// records with an empty Agent field).
func Resolve(name string) (App, bool) {
	if a, ok := Lookup(name); ok {
		return a, true
	}
	mu.RLock()
	defer mu.RUnlock()
	if defaultName == "" {
		return nil, false
	}
	return byName[defaultName], false
}

// Normalize returns the canonical name for input, or "" if no app matches.
// Used for CLI subcommand dispatch and for normalizing the Agent field in
// CreateOptions before persistence.
func Normalize(input string) string {
	a, ok := Lookup(input)
	if !ok {
		return ""
	}
	return a.Name()
}

// All returns the registered apps in registration order. The first entry
// is the default app (the one returned by Resolve when nothing matches).
func All() []App {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]App, len(registered))
	copy(out, registered)
	return out
}

// Names returns the canonical names of every registered app, sorted
// lexicographically. Used for help text and CLI usage messages.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(byName))
	for name := range byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Default returns the default app — the first one registered. Returns nil
// only if nothing has registered yet, which would mean the binary was built
// without importing internal/apps/builtin.
func Default() App {
	mu.RLock()
	defer mu.RUnlock()
	if defaultName == "" {
		return nil
	}
	return byName[defaultName]
}

// AnyProcessMatches reports whether any registered app considers the given
// process name (e.g. tmux pane_current_command) to be an instance of itself.
// Used by discovery to filter the orphan list.
func AnyProcessMatches(processName string) bool {
	mu.RLock()
	defer mu.RUnlock()
	for _, a := range registered {
		if a.ProcessMatches(processName) {
			return true
		}
	}
	return false
}
