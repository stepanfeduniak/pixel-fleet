package session

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
)

// DiscoveredSession represents a Claude session found on a remote or local machine.
type DiscoveredSession struct {
	Machine      string
	TmuxSession  string
	WindowIndex  string
	WindowName   string
	Path         string
	Command      string
	Reattachable bool
	Tracked      bool   // true if this session is in the local store
	DisplayName  string // local display name if tracked, otherwise empty
}

// Label returns a human-readable label for display.
func (d DiscoveredSession) Label() string {
	if d.Path != "" {
		return fmt.Sprintf("%s:%s (%s)", d.Machine, d.Path, d.TmuxSession)
	}
	return fmt.Sprintf("%s:%s/%s", d.Machine, d.TmuxSession, d.WindowName)
}

// probeCommand is the single-roundtrip SSH command to discover tmux sessions running claude.
const probeScript = `tmux list-sessions -F '#{session_name}' 2>/dev/null; for s in $(tmux list-sessions -F '#{session_name}' 2>/dev/null); do tmux list-windows -t "$s" -F "SESSION:$s	#{window_index}	#{window_name}	#{pane_current_path}	#{pane_current_command}" 2>/dev/null; done`

// probeScriptFor returns the script to run on a machine's shell.
//
// For remote machines the clipboard bridge is prepended, which is how cs
// enforces it on tmux sessions it did not launch itself: the scan already
// reaches every known machine on an interval, so the settings are reapplied
// there for free — no extra SSH round trip — and sessions started by an older
// cs, or adopted from outside it, end up configured too. The bridge commands
// are all silenced and non-fatal, so they cannot disturb the scan output that
// parseScanOutput reads.
//
// "home" is deliberately excluded. The local tmux copies through pbcopy
// directly (see internal/tmux.ConfigureClipboard); installing the remote
// bindings there would replace that with an OSC 52 emission the terminal
// discards, silently breaking local copy.
func probeScriptFor(machine Machine) string {
	if machine.Name == "home" || !ClipboardEnabled() {
		return probeScript
	}
	return clipboardBridgeSnippet + "\n" + probeScript
}

// ScanMachine probes a single machine for Claude sessions.
func ScanMachine(machine Machine, timeout time.Duration) ([]DiscoveredSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	script := probeScriptFor(machine)

	var cmd *exec.Cmd
	if machine.Name == "home" {
		cmd = exec.CommandContext(ctx, "bash", "-c", script)
	} else {
		cmd = exec.CommandContext(ctx, "ssh", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", machine.Name, script)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("probe failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	return parseScanOutput(machine.Name, string(out)), nil
}

func parseScanOutput(machineName, output string) []DiscoveredSession {
	var results []DiscoveredSession

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "SESSION:") {
			continue
		}

		// Format: SESSION:<session>\t<window_index>\t<window_name>\t<pane_current_path>\t<pane_current_command>
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 5 {
			continue
		}

		sessionName := strings.TrimPrefix(parts[0], "SESSION:")
		command := parts[4]

		// Include windows running an agent any registered app recognizes,
		// OR any window in a cs-managed tmux session (legacy shared
		// "cs-remote", or per-session "cs-<slug>-<id>"). The cs-managed
		// check is needed because the foreground command might briefly be
		// bash or a subprocess of the agent even though the session is
		// one of ours.
		isCsManaged := sessionName == "cs-remote" || strings.HasPrefix(sessionName, "cs-")
		if !apps.AnyProcessMatches(command) && !isCsManaged {
			continue
		}

		results = append(results, DiscoveredSession{
			Machine:      machineName,
			TmuxSession:  sessionName,
			WindowIndex:  parts[1],
			WindowName:   parts[2],
			Path:         parts[3],
			Command:      parts[4],
			Reattachable: true,
		})
	}

	return results
}

// ScanAll probes all machines in parallel for Claude sessions.
func ScanAll(machines []Machine, timeout time.Duration) []DiscoveredSession {
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results []DiscoveredSession
	)

	for _, m := range machines {
		wg.Add(1)
		go func(machine Machine) {
			defer wg.Done()
			found, err := ScanMachine(machine, timeout)
			if err != nil {
				log.Printf("Scan %s: %v", machine.Name, err)
				return
			}
			if len(found) > 0 {
				mu.Lock()
				results = append(results, found...)
				mu.Unlock()
			}
		}(m)
	}

	wg.Wait()
	return results
}
