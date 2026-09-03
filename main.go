package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps/appviewer"
	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/builtin"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps/skillsviewer"
	"github.com/stepanfeduniak/pixel-fleet/internal/config"
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
	"github.com/stepanfeduniak/pixel-fleet/internal/tmux"
	"github.com/stepanfeduniak/pixel-fleet/internal/tui"
)

const dashboardWindow = "dashboard"

func initLogging() *os.File {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".config", "cs")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "cs.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil
	}
	log.SetOutput(f)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	return f
}

func main() {
	logFile := initLogging()
	if logFile != nil {
		defer logFile.Close()
	}
	log.Printf("cs started with args: %v", os.Args[1:])

	cfg := config.Load()
	// Must be set before any tmux session is created: it gates the copy and
	// URL key bindings installed on the local tmux server.
	tmux.ClipboardEnabled = cfg.IsClipboardEnabled()
	session.SetClipboardEnabled(cfg.IsClipboardEnabled())
	// Must be set before the first probe: every non-interactive SSH command
	// reads it.
	session.SetConnectTimeout(cfg.ConnectTimeout)
	mgr := session.NewManager(cfg)

	args := os.Args[1:]

	if len(args) == 0 {
		cmdDashboard(mgr, cfg)
		return
	}

	// System apps (skills viewer, app viewer) still get their own
	// subcommand — they are in-process TUIs cs launches, not something you
	// could type into a shell. Their positional args are all optional:
	// `cs skills` is enough.
	//
	// Agents deliberately do NOT get one. A session is a shell; you start
	// claude or codex in it yourself and cs recognises what is running.
	if name := apps.Normalize(args[0]); name != "" {
		app, _ := apps.Lookup(name)
		if app != nil && app.IsSystem() {
			n, machine, path := defaultsForViewer(args[1:], app.Name())
			cmdNewAndDashboard(mgr, cfg, n, machine, path, name)
			return
		}
		// An agent name where the session name goes. This used to be the
		// way in, so say what replaced it — otherwise the positional form
		// below would quietly create a session *called* "claude" on a
		// machine called "training".
		fmt.Fprintf(os.Stderr, "cs no longer takes an agent name — a session is a shell.\n\n")
		if len(args) >= 4 {
			fmt.Fprintf(os.Stderr, "  instead of:  cs %s %s %s %s\n", args[0], args[1], args[2], args[3])
			fmt.Fprintf(os.Stderr, "  run:         cs %s %s %s\n\n", args[1], args[2], args[3])
		} else {
			fmt.Fprintf(os.Stderr, "  instead of:  cs %s <name> <machine> <path>\n", args[0])
			fmt.Fprintf(os.Stderr, "  run:         cs <name> <machine> <path>\n\n")
		}
		fmt.Fprintf(os.Stderr, "Then type `%s` in the session — cs works out what is running.\n", name)
		os.Exit(1)
	}

	switch args[0] {
	case "ls", "list":
		cmdList(mgr)
	case "scan":
		cmdScan(mgr)
	case "doctor":
		cmdDoctor(args[1:])
	case "adopt":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: cs adopt <#> <name>")
			fmt.Fprintln(os.Stderr, "       Run 'cs ls' first to see session numbers.")
			os.Exit(1)
		}
		cmdAdopt(mgr, cfg, args[1], args[2])
	case "kill":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: cs kill <name>")
			os.Exit(1)
		}
		cmdKill(mgr, args[1])
	case "kill-all":
		cmdKillAll(mgr)
	case "urls":
		cmdURLs(args[1:])
	case "--open-url":
		// Re-entry point: a URL menu entry invoked us to open its choice.
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: cs --open-url <list-file> <n>")
			os.Exit(1)
		}
		n, err := strconv.Atoi(args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad index %q\n", args[2])
			os.Exit(1)
		}
		if err := tmux.OpenURLFromList(args[1], n); err != nil {
			log.Printf("open-url: %v", err)
			os.Exit(1)
		}
	case "help", "--help", "-h":
		cmdHelp()
	case "--dashboard-tui":
		cmdRunTUI(mgr, cfg)
	case "--skills-viewer":
		// Re-entry point: a session window invoked us to render the
		// skills viewer TUI. We never return — when the user quits the
		// TUI, the window closes.
		if err := skillsviewer.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "skills viewer: %v\n", err)
			os.Exit(1)
		}
	case "--app-viewer":
		if err := appviewer.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "app viewer: %v\n", err)
			os.Exit(1)
		}
	default:
		// `cs <name> <machine> <path>` — the one way to open a session.
		// It is a login shell; whatever you run in it is detected.
		if len(args) >= 3 {
			cmdNewAndDashboard(mgr, cfg, args[0], args[1], args[2], "")
			return
		}
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", args[0])
		fmt.Fprintf(os.Stderr, "To open a session: cs <name> <machine> <path>\n")
		fmt.Fprintf(os.Stderr, "Example:           cs training gpu-01 ~/ml-project\n\n")
		cmdHelp()
		os.Exit(1)
	}
}

// cmdURLs backs the prefix+u / prefix+U tmux bindings: it finds the URLs on a
// pane and either offers them in a menu or copies the newest to the clipboard.
// With no pane argument it uses the caller's own pane, so `cs urls` works when
// typed straight into a session.
func cmdURLs(args []string) {
	copyOnly := false
	pane := ""
	for _, a := range args {
		if a == "--copy" {
			copyOnly = true
			continue
		}
		pane = a
	}
	if pane == "" {
		pane = os.Getenv("TMUX_PANE")
	}
	if pane == "" {
		fmt.Fprintln(os.Stderr, "cs urls: not inside tmux (no pane to read)")
		os.Exit(1)
	}

	var err error
	if copyOnly {
		err = tmux.CopyNewestURL(pane)
	} else {
		err = tmux.ShowURLMenu(pane)
	}
	if err != nil {
		log.Printf("urls: %v", err)
		fmt.Fprintf(os.Stderr, "cs urls: %v\n", err)
		os.Exit(1)
	}
}

// defaultsForViewer fills in sensible defaults for viewer apps (skills,
// app viewer, …) that don't require a working path. The user can still
// override any positional arg explicitly:
//
//	cs skills                            -> name="skills", machine="home", path=""
//	cs skills foo                        -> name="foo",    machine="home", path=""
//	cs skills foo gpu-01                 -> name="foo",    machine="gpu-01", path=""
//	cs skills foo gpu-01 ~/x             -> name="foo",    machine="gpu-01", path="~/x"
//
// The default name is the app's canonical name (e.g. "skills"), so a
// second `cs skills` invocation returns to the same window instead of
// creating a duplicate. Args that look like flags (start with `-`) are
// ignored to keep `cs skills --help` from being interpreted as
// `cs skills <name=--help>`.
func defaultsForViewer(extra []string, appName string) (name, machine, path string) {
	name, machine, path = appName, "home", ""
	pos := positional(extra)
	if len(pos) >= 1 {
		name = pos[0]
	}
	if len(pos) >= 2 {
		machine = pos[1]
	}
	if len(pos) >= 3 {
		path = pos[2]
	}
	return name, machine, path
}

func positional(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func cmdDashboard(mgr *session.Manager, cfg *config.Config) {
	ensureDashboard(mgr, cfg)
	attach(cfg.SessionName)
}

// cmdNewAndDashboard opens a session and drops the user into the dashboard.
// agent is empty for an ordinary session — a shell — and names a system app
// only for the `cs skills` / `cs apps` windows.
func cmdNewAndDashboard(mgr *session.Manager, cfg *config.Config, name, machine, path, agent string) {
	ensureDashboard(mgr, cfg)

	s, err := mgr.CreateWithOptions(name, machine, path, session.CreateOptions{Agent: agent})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	} else {
		fmt.Printf("Created session: %s (machine=%s, path=%s)\n", s.Name, machine, s.Path)
	}

	_ = tmux.SelectWindow(cfg.SessionName, dashboardWindow)
	attach(cfg.SessionName)
}

func ensureDashboard(mgr *session.Manager, cfg *config.Config) {
	if !tmux.SessionExists(cfg.SessionName) {
		csPath, _ := os.Executable()
		dashCmd := fmt.Sprintf("%s --dashboard-tui", csPath)
		log.Printf("Creating tmux session with dashboard: %s", dashCmd)
		if err := tmux.CreateSessionWithCommand(cfg.SessionName, dashboardWindow, dashCmd); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create tmux session: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Reapply on an existing session: the tmux server can be older than
		// this binary, so its options and bindings may predate them.
		if err := tmux.ApplySessionOptions(cfg.SessionName); err != nil {
			log.Printf("session options: %v", err)
		}
		windows, _ := tmux.ListWindows(cfg.SessionName)
		hasDashboard := false
		for _, w := range windows {
			if w.Name == dashboardWindow {
				hasDashboard = true
				break
			}
		}
		if !hasDashboard {
			csPath, _ := os.Executable()
			dashCmd := fmt.Sprintf("%s --dashboard-tui", csPath)
			_ = tmux.NewWindow(cfg.SessionName, dashboardWindow, dashCmd)
		} else if tmux.IsPaneDead(cfg.SessionName, dashboardWindow) {
			csPath, _ := os.Executable()
			dashCmd := fmt.Sprintf("%s --dashboard-tui", csPath)
			_ = tmux.RespawnPane(cfg.SessionName, dashboardWindow, dashCmd)
		}
	}

	setupKeybindings(cfg.SessionName)
}

func setupKeybindings(sessionName string) {
	target := sessionName + ":" + dashboardWindow

	// Ctrl+q: switch back to dashboard window
	cmd := exec.Command("tmux", "bind-key", "-T", "root", "C-q",
		"select-window", "-t", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("Failed to bind Ctrl+q: %v (%s)", err, string(out))
	} else {
		log.Printf("Bound Ctrl+q -> select-window -t %s", target)
	}

	// F1 as fallback
	cmd2 := exec.Command("tmux", "bind-key", "-T", "root", "F1",
		"select-window", "-t", target)
	if out, err := cmd2.CombinedOutput(); err != nil {
		log.Printf("Failed to bind F1: %v (%s)", err, string(out))
	} else {
		log.Printf("Bound F1 -> select-window -t %s", target)
	}

	// prefix + q
	cmd3 := exec.Command("tmux", "bind-key", "q",
		"select-window", "-t", target)
	if out, err := cmd3.CombinedOutput(); err != nil {
		log.Printf("Failed to bind prefix+q: %v (%s)", err, string(out))
	} else {
		log.Printf("Bound prefix+q -> select-window -t %s", target)
	}
}

func attach(sessionName string) {
	if os.Getenv("TMUX") != "" {
		_ = tmux.SelectWindow(sessionName, dashboardWindow)
		return
	}

	cmd := exec.Command("tmux", "attach-session", "-t", sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to attach: %v\n", err)
		os.Exit(1)
	}
}

func cmdRunTUI(mgr *session.Manager, cfg *config.Config) {
	// Reapply the session options and clipboard bindings here too, not just
	// on the `cs` entry point that creates the session.
	//
	// The dashboard is the process that actually runs for days and gets
	// restarted in place (tmux respawn-pane) after an upgrade, while the
	// tmux server outlives all of it. Without this, anything that clears a
	// binding or the pane-set-clipboard hook — a config reload, an option
	// reset, a server that predates the feature — stays broken until the
	// user happens to run bare `cs` or create a session, and restarting the
	// dashboard, the obvious thing to try, fixes nothing.
	if err := tmux.ApplySessionOptions(cfg.SessionName); err != nil {
		log.Printf("session options: %v", err)
	}

	if err := tui.Run(mgr, cfg.SessionName, cfg.RefreshInterval, cfg.DiscoveryInterval); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

func cmdList(mgr *session.Manager) {
	fmt.Println("Fetching sessions from all machines...")
	discovered, err := mgr.FetchAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(discovered) == 0 {
		fmt.Println("No active sessions found on any machine.")
		return
	}

	fmt.Printf("\n%-4s %-15s %-20s %-30s %s\n", "#", "Machine", "Name", "Path", "Status")
	fmt.Println(strings.Repeat("-", 95))
	for i, d := range discovered {
		name := d.DisplayName
		if name == "" {
			name = d.WindowName
		}
		path := d.Path
		if len(path) > 30 {
			path = "..." + path[len(path)-27:]
		}
		status := "orphaned"
		if d.Tracked {
			status = "tracked"
		}
		fmt.Printf("%-4d %-15s %-20s %-30s %s\n", i+1, d.Machine, name, path, status)
	}
	fmt.Printf("\n%d session(s) across all machines.\n", len(discovered))

	// Count orphaned
	orphaned := 0
	for _, d := range discovered {
		if !d.Tracked {
			orphaned++
		}
	}
	if orphaned > 0 {
		fmt.Printf("%d orphaned — use 'cs adopt <#>' to reclaim, or 'cs scan' in the dashboard.\n", orphaned)
	}
}

func cmdKill(mgr *session.Manager, name string) {
	if err := mgr.Kill(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Killed session: %s\n", name)
}

func cmdScan(mgr *session.Manager) {
	fmt.Println("Scanning machines for orphaned Claude sessions...")
	discovered, err := mgr.Scan()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(discovered) == 0 {
		fmt.Println("No orphaned sessions found.")
		return
	}

	fmt.Printf("\n%-15s %-20s %-12s %s\n", "Machine", "Path", "tmux", "Command")
	fmt.Println(strings.Repeat("-", 70))
	for _, d := range discovered {
		path := d.Path
		if len(path) > 20 {
			path = "..." + path[len(path)-17:]
		}
		fmt.Printf("%-15s %-20s %-12s %s\n", d.Machine, path, d.TmuxSession+":"+d.WindowIndex, d.Command)
	}
	fmt.Printf("\n%d session(s) found. Use the dashboard (press 's') to adopt them.\n", len(discovered))
}

func cmdAdopt(mgr *session.Manager, cfg *config.Config, indexStr, name string) {
	idx, err := strconv.Atoi(indexStr)
	if err != nil || idx < 1 {
		fmt.Fprintf(os.Stderr, "Invalid session number: %s\n", indexStr)
		os.Exit(1)
	}

	fmt.Println("Fetching sessions from all machines...")
	discovered, err := mgr.FetchAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if idx > len(discovered) {
		fmt.Fprintf(os.Stderr, "Session #%d not found (only %d sessions discovered).\n", idx, len(discovered))
		os.Exit(1)
	}

	d := discovered[idx-1]
	if d.Tracked {
		fmt.Fprintf(os.Stderr, "Session #%d (%s on %s) is already tracked as %q.\n", idx, d.TmuxSession, d.Machine, d.DisplayName)
		os.Exit(1)
	}

	ensureDashboard(mgr, cfg)
	s, err := mgr.Adopt(d, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error adopting: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Adopted session: %s (machine=%s, path=%s)\n", s.Name, s.Machine, s.Path)

	_ = tmux.SelectWindow(cfg.SessionName, dashboardWindow)
	attach(cfg.SessionName)
}

func cmdKillAll(mgr *session.Manager) {
	if err := mgr.KillAll(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("All sessions killed.")
}

func cmdDoctor(args []string) {
	var reports []session.DoctorReport
	if len(args) == 0 {
		reports = session.DoctorAll()
	} else {
		for _, m := range args {
			reports = append(reports, session.Doctor(m))
		}
	}

	allOk := true
	for _, r := range reports {
		fmt.Printf("\n== %s ==\n", r.Machine)
		for _, c := range r.Checks {
			marker := "✓"
			if c.Status != "PASS" {
				marker = "✗"
				allOk = false
			}
			fmt.Printf("  %s %-30s %s\n", marker, c.Name, c.Detail)
		}
	}
	fmt.Println()
	if !allOk {
		fmt.Println("Some checks failed. Common fixes:")
		fmt.Println("  • linger:  ssh <machine> 'sudo loginctl enable-linger $USER'")
		fmt.Println("  • claude:  install or upgrade claude-code on the machine")
		fmt.Println("  • tmux:    sudo apt install tmux")
		os.Exit(1)
	}
}

func cmdHelp() {
	// System apps still get their own subcommand and are listed from the
	// registry, so a newly registered one shows up here automatically.
	// Agents do not: you start those in the session yourself.
	var appLines strings.Builder
	for _, a := range apps.All() {
		if !a.IsSystem() {
			continue
		}
		appLines.WriteString(fmt.Sprintf("  cs %-40sOpen %s\n", a.Name(), a.Label()))
	}
	fmt.Printf(`pixel-fleet (cs) - Multi-machine agent session manager

Usage:
  cs                                         Open the dashboard
  cs <name> <machine> <path>                 Open a session (a shell) and go to it
%s  cs ls                                      List all sessions across all machines
  cs adopt <#> <name>                        Adopt orphaned session by number from cs ls
  cs scan                                    Scan machines for orphaned sessions
  cs doctor [machine...]                     Preflight checks (linger, tmux, agents)
  cs kill <name>                             Kill a session by name
  cs kill-all                                Kill all sessions
  cs urls [--copy]                           List URLs on this pane (menu, or copy newest)
  cs help                                    Show this help`, appLines.String())
	fmt.Print(`

Sessions:
  A session is a login shell on the target machine. Start whatever you want
  in it — claude, codex, a build, nothing — and the dashboard works out what
  is running and badges it accordingly. There is no agent to pick up front.

Persistence:
  Remote sessions run inside a persistent tmux session on the target machine,
  so they survive SSH drops, laptop sleep, and viewer disconnects.

  Prerequisite: each remote needs systemd user-linger enabled once:
  ssh <machine> 'sudo loginctl enable-linger $USER'.
  Run 'cs doctor' to verify all known machines.

Examples:
  cs training gpu-01 ~/ml-project           Open a session on gpu-01 in ~/ml-project
  cs frontend home ~/webapp                 Open one locally in ~/webapp
  cs doctor                                 Preflight on all known machines

Machines:
  "home"       Local machine
  Any host     From ~/.ssh/config (e.g. gpu-01, h100)

Dashboard keys:
  arrow/hjkl  Navigate       enter  Focus into session
  n           New session    x      Kill session
  s           Scan machines  r      Refresh
  b           Coding blocker q      Detach
  ?           Help

Coding blocker:
  Press b in the dashboard and pick a duration. For that long the
  gallery stays on screen but you cannot go into a session. Sessions
  keep running the whole time — the blocker only stops you watching.
  It survives a restart. Press b again and type 'break' to end early.

Copy and links (inside any session, local or remote):
  drag        Select with the mouse - lands on the system clipboard
  dbl-click   Copy a word          triple-click  Copy a line
  prefix u    Menu of the URLs on screen; press its number to open it
  prefix U    Copy the newest URL on screen
  prefix C-v  Paste the system clipboard into the pane
  prefix [    Scrollback / copy mode; Enter copies the selection

  Remote sessions copy through an OSC 52 bridge back to this machine.
  Turn the whole feature off with 'clipboard: false' in config.yaml.

Return to dashboard:
  F1          Always works
  ctrl+q      Works if flow control disabled
  ctrl+b q    tmux prefix + q

Config: ~/.config/cs/config.yaml
Logs:   ~/.config/cs/cs.log
`)
}
