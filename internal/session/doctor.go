package session

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
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
// same logical checks there.
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

func doctorLocal() DoctorReport {
	r := DoctorReport{Machine: "home"}
	r.Checks = append(r.Checks, checkLocalCmd("tmux installed", "tmux", "-V"))
	r.Checks = append(r.Checks, checkLocalCmd("claude installed", "claude", "--version"))
	r.Checks = append(r.Checks, checkLocalCmd("claude remote-control available", "claude", "remote-control", "--help"))
	// Linger isn't relevant locally — the laptop's tmux server isn't the persistence anchor.
	r.Checks = append(r.Checks, CheckResult{
		Name:   "linger",
		Status: "PASS",
		Detail: "n/a for home",
	})
	return r
}

func doctorRemote(machine string) DoctorReport {
	r := DoctorReport{Machine: machine}

	// Single SSH round-trip that prints structured output for every check.
	// We prepend ~/.local/bin to PATH the same way the session bootstrap
	// does, so doctor reports what a real cs session would actually use.
	probe := `
export PATH="$HOME/.local/bin:$PATH"
echo "TMUX::$(tmux -V 2>&1 || echo MISSING)"
echo "CLAUDE::$(claude --version 2>&1 || echo MISSING)"
echo "CLAUDEPATH::$(command -v claude 2>&1 || echo MISSING)"
if claude remote-control --help >/dev/null 2>&1; then
    echo "RC::PASS"
else
    echo "RC::FAIL"
fi
LINGER=$(loginctl show-user "$USER" 2>/dev/null | grep -i '^Linger=' | cut -d= -f2)
echo "LINGER::${LINGER:-unknown}"
TMUXSESSIONS=$(tmux list-sessions -F '#{session_name}' 2>/dev/null | tr '\n' ',' | sed 's/,$//')
echo "TMUXSESSIONS::${TMUXSESSIONS:-none}"
`
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

	r.Checks = append(r.Checks, statusFromVersion("tmux installed", values["TMUX"]))
	claudePath := values["CLAUDEPATH"]
	claudeDetail := values["CLAUDE"]
	if claudePath != "" && claudePath != "MISSING" {
		claudeDetail = claudeDetail + "  (" + claudePath + ")"
	}
	r.Checks = append(r.Checks, statusFromVersion("claude installed", claudeDetail))

	rcStatus := "FAIL"
	rcDetail := "claude remote-control --help failed"
	if values["RC"] == "PASS" {
		rcStatus = "PASS"
		rcDetail = "claude remote-control supported"
	}
	r.Checks = append(r.Checks, CheckResult{Name: "remote-control supported", Status: rcStatus, Detail: rcDetail})

	lingerStatus := "FAIL"
	lingerDetail := "loginctl Linger=" + values["LINGER"] + " (run: sudo loginctl enable-linger $USER)"
	if strings.EqualFold(values["LINGER"], "yes") {
		lingerStatus = "PASS"
		lingerDetail = "Linger=yes (sessions survive SSH drops)"
	}
	r.Checks = append(r.Checks, CheckResult{Name: "systemd linger", Status: lingerStatus, Detail: lingerDetail})

	sessions := values["TMUXSESSIONS"]
	if sessions == "" || sessions == "none" {
		r.Checks = append(r.Checks, CheckResult{Name: "remote tmux", Status: "PASS", Detail: "no sessions yet (will be created on first cs run)"})
	} else {
		r.Checks = append(r.Checks, CheckResult{Name: "remote tmux", Status: "PASS", Detail: "sessions: " + sessions})
	}

	return r
}

func checkLocalCmd(name string, args ...string) CheckResult {
	if len(args) == 0 {
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

func statusFromVersion(name, value string) CheckResult {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "MISSING") || strings.Contains(strings.ToLower(value), "command not found") {
		return CheckResult{Name: name, Status: "FAIL", Detail: "not installed"}
	}
	return CheckResult{Name: name, Status: "PASS", Detail: value}
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
