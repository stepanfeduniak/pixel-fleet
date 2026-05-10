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
	mgr := session.NewManager(cfg)

	args := os.Args[1:]

	// --no-rc may appear anywhere on the command line; pull it off.
	noRC := false
	filtered := args[:0]
	for _, a := range args {
		switch a {
		case "--no-rc", "--no-remote-control":
			noRC = true
		default:
			filtered = append(filtered, a)
		}
	}
	args = filtered

	if len(args) == 0 {
		cmdDashboard(mgr, cfg)
		return
	}

	// First check if args[0] is a registered app's name or alias. Any app
	// registered via apps.Register automatically gets its own subcommand.
	//
	// Session apps (claude, codex, terminal): require <name> <machine> <path>.
	// Viewer apps (skills, app-viewer): all positional args optional —
	//   `cs skills` is enough; the launch happens on home with no path.
	if agent := apps.Normalize(args[0]); agent != "" {
		app, _ := apps.Lookup(agent)
		// requires_path can be overridden in config (apps.<name>.requires_path:
		// false), so even a regular agent can opt out of needing a path.
		if app != nil && !cfg.RequiresPathFor(app.Name(), app.RequiresPath()) {
			name, machine, path := defaultsForViewer(args[1:], app.Name())
			cmdNewAndDashboard(mgr, cfg, name, machine, path, noRC, agent)
			return
		}
		if len(args) < 4 {
			fmt.Fprintf(os.Stderr, "Usage: cs %s <name> <machine> <path>\n", args[0])
			fmt.Fprintf(os.Stderr, "Example: cs %s training gpu-01 ~/ml-project\n", args[0])
			os.Exit(1)
		}
		cmdNewAndDashboard(mgr, cfg, args[1], args[2], args[3], noRC, agent)
		return
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
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", args[0])
		cmdHelp()
		os.Exit(1)
	}
}

// defaultsForViewer fills in sensible defaults for viewer apps (skills,
// app viewer, …) that don't require a working path. The user can still
// override any positional arg explicitly:
//
//   cs skills                            -> name="skills", machine="home", path=""
//   cs skills foo                        -> name="foo",    machine="home", path=""
//   cs skills foo gpu-01                 -> name="foo",    machine="gpu-01", path=""
//   cs skills foo gpu-01 ~/x             -> name="foo",    machine="gpu-01", path="~/x"
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

func cmdNewAndDashboard(mgr *session.Manager, cfg *config.Config, name, machine, path string, noRC bool, agent string) {
	ensureDashboard(mgr, cfg)

	opts := session.CreateOptions{Agent: agent}
	if noRC {
		f := false
		opts.RemoteControl = &f
	}
	s, err := mgr.CreateWithOptions(name, machine, path, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	} else {
		shownAgent := agent
		if shownAgent == "" {
			shownAgent = "claude"
		}
		fmt.Printf("Created session: %s (agent=%s, machine=%s, path=%s)\n", s.Name, shownAgent, machine, s.Path)
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
	// Build a per-app usage section dynamically from the registry. New apps
	// added to internal/apps/builtin (or imported from out-of-tree modules)
	// show up in help automatically.
	var appLines strings.Builder
	for _, a := range apps.All() {
		flag := "         " // 9 spaces, matches " [--no-rc]" width
		if a.SupportsRemoteControl() {
			flag = " [--no-rc]"
		}
		// System apps don't need a path — show short-form usage so users
		// don't get tripped up by "<machine> <path>" they don't need.
		usage := "<name> <machine> <path>"
		if a.IsSystem() {
			usage = "                       " // 23 spaces — keeps trailing column aligned
		}
		appLines.WriteString(fmt.Sprintf("  cs %-13s %s%s  Open %s\n",
			a.Name(), usage, flag, a.Label()))
	}
	fmt.Printf(`pixel-fleet (cs) - Multi-machine agent session manager

Usage:
  cs                                         Open the dashboard
%s  cs ls                                      List all sessions across all machines
  cs adopt <#> <name>                        Adopt orphaned session by number from cs ls
  cs scan                                    Scan machines for orphaned sessions
  cs doctor [machine...]                     Preflight checks (linger, tmux, claude, RC)
  cs kill <name>                             Kill a session by name
  cs kill-all                                Kill all sessions
  cs help                                    Show this help`, appLines.String())
	fmt.Print(`

Persistence and remote control:
  Remote sessions run inside a persistent tmux session (cs-remote) on the
  target machine, so they survive SSH drops, laptop sleep, and viewer
  disconnects. Claude sessions also launch with --remote-control by default,
  exposing them on claude.ai/code and the Claude mobile/desktop apps.
  Use --no-rc on a claude/codex command to opt out for a single session.

  Prerequisite for persistence: each remote needs systemd user-linger enabled
  once: ssh <machine> 'sudo loginctl enable-linger $USER'.
  Run 'cs doctor' to verify all known machines.

Examples:
  cs claude training gpu-01 ~/ml-project    Launch Claude on gpu-01 in ~/ml-project
  cs claude frontend home ~/webapp          Launch Claude locally in ~/webapp
  cs claude lab a100 ~/proj --no-rc         Launch without --remote-control
  cs codex review gpu-01 ~/ml-project       Launch Codex on gpu-01
  cs term shell gpu-01 ~/ml-project         Plain login shell — no agent
  cs doctor                                 Preflight on all known machines

Machines:
  "home"       Local machine
  Any host     From ~/.ssh/config (e.g. gpu-01, h100)

Dashboard keys:
  arrow/hjkl  Navigate       enter  Focus into session
  n           New session    x      Kill session
  s           Scan machines  r      Refresh
  q           Detach         ?      Help

Return to dashboard:
  F1          Always works
  ctrl+q      Works if flow control disabled
  ctrl+b q    tmux prefix + q

Config: ~/.config/cs/config.yaml
Logs:   ~/.config/cs/cs.log
`)
}
