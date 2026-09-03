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

// Health is what the last probe of a machine found. It drives the markers
// in the dashboard's machine picker, and lets callers skip work that would
// only ever time out against a machine that is not answering.
type Health int

const (
	HealthUnknown Health = iota // not probed yet this run
	HealthOnline                // the probe came back
	HealthOffline               // unreachable: DNS, TCP, or the probe timed out
	HealthDenied                // the machine is there, but SSH would not let us in
)

const (
	// probeBackoffBase is the wait after a machine's first failed probe, and
	// doubles with each consecutive failure up to probeBackoffMax.
	probeBackoffBase = time.Minute
	probeBackoffMax  = 15 * time.Minute

	// probeMaxFailures clamps the doubling so the shift can't overflow.
	probeMaxFailures = 10
)

// ProbeThrottle backs off machines that keep failing to answer.
//
// Without it every scan pays a full ScanTimeout for each machine that is not
// there — a laptop that has been off for a week is probed just as eagerly as
// one that answered a second ago, and with a handful of stale ~/.ssh/config
// entries that is most of the scan. A host that has been down for an hour
// will still be down in another minute, so each consecutive failure pushes
// the next attempt further out. One success clears the penalty immediately.
type ProbeThrottle struct {
	mu    sync.Mutex
	state map[string]*probeState
}

type probeState struct {
	failures int
	nextTry  time.Time
	health   Health // last observed, carried forward while we skip
}

// NewProbeThrottle returns a throttle with no history: every machine is
// probed on the first scan.
func NewProbeThrottle() *ProbeThrottle {
	return &ProbeThrottle{state: make(map[string]*probeState)}
}

// skip reports whether a machine is still inside its backoff window, along
// with the Health to carry forward for it. A nil throttle never skips, which
// is what the manual "fetch all" wants.
func (p *ProbeThrottle) skip(name string, now time.Time) (bool, Health) {
	if p == nil {
		return false, HealthUnknown
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	st, ok := p.state[name]
	if !ok || !now.Before(st.nextTry) {
		return false, HealthUnknown
	}
	return true, st.health
}

// record folds a probe result in: success clears the backoff, failure
// lengthens it.
func (p *ProbeThrottle) record(name string, h Health, now time.Time) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	st, ok := p.state[name]
	if !ok {
		st = &probeState{}
		p.state[name] = st
	}
	st.health = h

	if h == HealthOnline {
		st.failures = 0
		st.nextTry = time.Time{}
		return
	}

	st.failures++
	if st.failures > probeMaxFailures {
		st.failures = probeMaxFailures
	}
	delay := probeBackoffBase << (st.failures - 1)
	if delay > probeBackoffMax {
		delay = probeBackoffMax
	}
	st.nextTry = now.Add(delay)
}

// pardonSweep undoes the failure this scan recorded against each named
// machine, and lets them be probed again on the next cycle.
//
// It exists because ProbeThrottle cannot, on its own, tell "this host is
// down" from "my wifi just dropped a packet". Both look like a timeout. On
// a weak link the second is common, and the consequence was bad: one bad
// sweep marked every machine offline, and a few of them in a row escalated
// the backoff to its 15 minute cap. The wifi would come back and the
// dashboard would stay empty for a quarter of an hour, with every machine
// in it up the whole time.
//
// The health each machine last showed is left alone — they really did fail
// to answer, and the dashboard should say so — only the penalty is dropped.
func (p *ProbeThrottle) pardonSweep(names []string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, name := range names {
		st, ok := p.state[name]
		if !ok {
			continue
		}
		if st.failures > 0 {
			st.failures--
		}
		st.nextTry = time.Time{}
	}
}

// classifyProbe turns a probe failure into a Health. The distinction worth
// drawing is "the machine is not there" against "the machine is there and
// refused us" — the first is a stale ~/.ssh/config entry, the second is a
// key to fix, and they want different things from the user.
//
// ScanMachine folds the ssh output into its error, so matching on the error
// text is enough and ScanMachine keeps its signature.
func classifyProbe(err error) Health {
	if err == nil {
		return HealthOnline
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "Permission denied"),
		strings.Contains(text, "Too many authentication failures"),
		strings.Contains(text, "Host key verification failed"):
		return HealthDenied
	}
	return HealthOffline
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
		cmd = exec.CommandContext(ctx, "ssh", sshArgs(machine.Name, script)...)
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

// ScanAll probes all machines in parallel for Claude sessions. Alongside
// the sessions it returns each machine's Health, so a scan that mostly
// fails still tells the dashboard something worth showing.
//
// Machines still inside their backoff window are skipped and their previous
// Health carried forward. Pass a nil throttle to probe everything — that is
// what a user-initiated fetch does, since asking for a scan is a good reason
// to stop trusting the cache.
func ScanAll(machines []Machine, timeout time.Duration, throttle *ProbeThrottle) ([]DiscoveredSession, map[string]Health) {
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results []DiscoveredSession
		health  = make(map[string]Health, len(machines))
	)

	// Remote machines probed this sweep, and how many of them came back
	// HealthOffline, so a link failure can be told from host failures.
	var probedRemote []string
	offlineRemote := 0

	now := time.Now()
	for _, m := range machines {
		// Sequential, before any goroutine starts, so no lock is needed.
		if skip, carried := throttle.skip(m.Name, now); skip {
			health[m.Name] = carried
			continue
		}
		if m.Name != "home" {
			probedRemote = append(probedRemote, m.Name)
		}

		wg.Add(1)
		go func(machine Machine) {
			defer wg.Done()
			found, err := ScanMachine(machine, timeout)
			h := classifyProbe(err)
			throttle.record(machine.Name, h, time.Now())

			mu.Lock()
			health[machine.Name] = h
			if h == HealthOffline && machine.Name != "home" {
				offlineRemote++
			}
			if err == nil {
				results = append(results, found...)
			}
			mu.Unlock()

			if err != nil {
				log.Printf("Scan %s: %v", machine.Name, err)
			}
		}(m)
	}

	wg.Wait()

	// Every remote machine timing out at once is far more likely to be this
	// laptop's link than every host in the fleet dying in the same second,
	// so don't let it escalate the per-host backoff. Two is the smallest
	// number that makes the inference worth anything: with one machine
	// probed, "it failed" and "everything failed" are the same event.
	//
	// HealthDenied deliberately does not count — a machine that refused our
	// key answered us, which is a fact about that host and not about the
	// network, and it should still back off.
	if len(probedRemote) >= 2 && offlineRemote == len(probedRemote) {
		log.Printf("Scan: all %d remote machines unreachable, treating as a local network fault", offlineRemote)
		throttle.pardonSweep(probedRemote)
	}

	return results, health
}
