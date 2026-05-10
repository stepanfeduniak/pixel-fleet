package session

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Machine represents an available machine to connect to.
type Machine struct {
	Name     string // SSH host alias or "home"
	HostName string // actual hostname/IP (empty for "home")
	User     string
}

// ListMachines returns all available machines (home + SSH config hosts).
func ListMachines() []Machine {
	machines := []Machine{
		{Name: "home", HostName: "localhost", User: os.Getenv("USER")},
	}

	sshHosts := parseSSHConfig()
	machines = append(machines, sshHosts...)

	return machines
}

// parseSSHConfig reads ~/.ssh/config and extracts Host entries.
func parseSSHConfig() []Machine {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	f, err := os.Open(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var machines []Machine
	var current *Machine

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])

		switch key {
		case "host":
			// Skip wildcard entries
			if strings.Contains(value, "*") || strings.Contains(value, "?") {
				current = nil
				continue
			}
			m := Machine{Name: value}
			machines = append(machines, m)
			current = &machines[len(machines)-1]
		case "hostname":
			if current != nil {
				current.HostName = value
			}
		case "user":
			if current != nil {
				current.User = value
			}
		}
	}

	return machines
}
