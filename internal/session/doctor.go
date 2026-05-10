package session

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
)

// CheckResult represents the outcome of a single doctor check.
type CheckResult struct {
	Name    string
	Status  string // "PASS", "WARN", "FAIL"
	Detail  string
}

// DoctorReport summarizes the doctor checks for a single machine.
type DoctorReport struct {
	Machine string
	Checks  []CheckResult
}

// Ok returns true if every check is PASS.
func (r DoctorReport) Ok() bool {
	for _, c := range r.Checks {
		if c.Status != "PASS" {
			return false
		}
	}
	return true
}

// Doctor runs preflight checks against a machine. For "home" it inspects the
// local environment; for any other name it SSHes to the remote and runs the
// same logical checks there. Per-app checks are contributed by the apps
// registry — adding a new app automatically extends the doctor.
func Doctor(machine string) DoctorReport {
	if machine == "home" {
		return doctorLocal()
	}
	return doctorRemote(machine)
}

// DoctorAll runs Doctor against every known machine.
func DoctorAll() []DoctorReport {
	machines := ListMachines()
	reports := make([]DoctorReport, 0, len(machines))
	for _, m := range machines {
		reports = append(reports, Doctor(m.Name))
	}
	return reports
}

// frameworkProbes are the always-run probes that aren't tied to any one app.
// Each one prints a single `KEY::value` line on stdout. Doctor parses them
// alongside whatever apps contribute.
func frameworkProbes() []apps.Probe {
	return []apps.Probe{
		{
			Key:   "TMUX",
			Name:  "tmux installed",
			Shell: `echo "TMUX::$(tmux -V 2>&1 || echo MISSING)"`,
			Evaluate: func(value string) (string, string) {
				value = strings.TrimSpace(value)
				if value == "" || strings.Contains(value, "MISSING") {
					return "FAIL", "not installed"
				}
				return "PASS", value
			},
		},
		{
			Key:   "LINGER",
			Name:  "systemd linger",
			Shell: `LINGER=$(loginctl show-user "$USER" 2>/dev/null | grep -i '^Linger=' | cut -d= -f2); echo "LINGER::${LINGER:-unknown}"`,
			Evaluate: func(value string) (string, string) {
				if strings.EqualFold(strings.TrimSpace(value), "yes") {
					return "PASS", "Linger=yes (sessions survive SSH drops)"
				}
				return "FAIL", "loginctl Linger=" + value + " (run: sudo loginctl enable-linger $USER)"
			},
		},
		{
			Key:   "TMUXSESSIONS",
			Name:  "remote tmux",
			Shell: `TMUXSESSIONS=$(tmux list-sessions -F '#{session_name}' 2>/dev/null | tr '\n' ',' | sed 's/,$//'); echo "TMUXSESSIONS::${TMUXSESSIONS:-none}"`,
			Evaluate: func(value string) (string, string) {
				value = strings.TrimSpace(value)
				if value == "" || value == "none" {
					return "PASS", "no sessions yet (will be created on first cs run)"
				}
				return "PASS", "sessions: " + value
			},
		},
	}
}

// collectProbes returns the framework probes followed by every app's
// DoctorProbes. The order is the order the doctor reports their results.
func collectProbes() []apps.Probe {
	probes := frameworkProbes()
	for _, app := range apps.All() {
		probes = append(probes, app.DoctorProbes()...)
	}
	return probes
}

// localLingerProbe is a stand-in for systemd-linger on the user's laptop.
// Linger is meaningless locally (the laptop's tmux is a viewer, not the
// persistence anchor), so we just report "n/a" without running anything.
func doctorLocal() DoctorReport {
	r := DoctorReport{Machine: "home"}
	r.Checks = append(r.Checks, checkLocalCmd("tmux installed", "tmux", "-V"))
	for _, app := range apps.All() {
		if !app.NeedsBin() {
			continue
		}
		r.Checks = append(r.Checks, checkLocalCmd(app.Name()+" installed", app.DefaultLocalBin(), "--version"))
	}
	r.Checks = append(r.Checks, CheckResult{
		Name:   "linger",
		Status: "PASS",
		Detail: "n/a for home",
	})
	return r
}

func doctorRemote(machine string) DoctorReport {
	r := DoctorReport{Machine: machine}
	probes := collectProbes()

	// Single SSH round-trip: prepend ~/.local/bin (matches the session
	// bootstrap) then run every probe's Shell snippet, in order. Each
	// snippet emits one or more KEY::value lines.
	var sb strings.Builder
	sb.WriteString(`export PATH="$HOME/.local/bin:$PATH"` + "\n")
	for _, p := range probes {
		sb.WriteString(p.Shell)
		sb.WriteString("\n")
	}
	probe := sb.String()

	cmd := exec.Command("ssh", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", machine, probe)
	out, err := withTimeout(cmd, 10*time.Second)
	if err != nil {
		r.Checks = append(r.Checks, CheckResult{
			Name:   "ssh reachable",
			Status: "FAIL",
			Detail: err.Error(),
		})
		return r
	}
	r.Checks = append(r.Checks, CheckResult{
		Name:   "ssh reachable",
		Status: "PASS",
		Detail: machine,
	})

	values := parseDoctorOutput(out)
	for _, p := range probes {
		status, detail := p.Evaluate(values[p.Key])
		r.Checks = append(r.Checks, CheckResult{Name: p.Name, Status: status, Detail: detail})
	}

	return r
}

func checkLocalCmd(name string, args ...string) CheckResult {
	if len(args) == 0 || args[0] == "" {
		return CheckResult{Name: name, Status: "FAIL", Detail: "no command"}
	}
	cmd := exec.Command(args[0], args[1:]...)
	out, err := withTimeout(cmd, 5*time.Second)
	if err != nil {
		return CheckResult{Name: name, Status: "FAIL", Detail: err.Error()}
	}
	first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if first == "" {
		first = "ok"
	}
	return CheckResult{Name: name, Status: "PASS", Detail: first}
}

func parseDoctorOutput(out []byte) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, "::")
		if idx < 0 {
			continue
		}
		key := line[:idx]
		val := line[idx+2:]
		result[key] = val
	}
	return result
}

func withTimeout(cmd *exec.Cmd, timeout time.Duration) ([]byte, error) {
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
		return out, err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("timed out after %s", timeout)
	}
}
