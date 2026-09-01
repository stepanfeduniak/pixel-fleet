package tmux

import (
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

const DefaultSession = "cs"

// Window represents a tmux window.
type Window struct {
	Index  int
	Name   string
	Active bool
}

// SessionExists checks if a tmux session exists.
func SessionExists(session string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", session)
	return cmd.Run() == nil
}

// CreateSession creates a new tmux session (detached).
func CreateSession(session string) error {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", session)
	if err := cmd.Run(); err != nil {
		return err
	}
	return setSessionOptions(session)
}

// CreateSessionWithCommand creates a new tmux session with a named window running a command.
func CreateSessionWithCommand(session, windowName, command string) error {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", session, "-n", windowName, command)
	if err := cmd.Run(); err != nil {
		return err
	}
	return setSessionOptions(session)
}

// setSessionOptions configures tmux options for a cs-managed session.
// Uses -g for session options and -wg for window options so future windows
// inherit the values.
func setSessionOptions(session string) error {
	opts := [][]string{
		{"set-option", "-g", "history-limit", "50000"},
		{"set-option", "-g", "mouse", "on"},
		// remain-on-exit is a window option; use -wg to set the global default.
		{"set-option", "-wg", "remain-on-exit", "on"},
	}
	for _, args := range opts {
		cmd := exec.Command("tmux", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("tmux %s: %s (%w)", strings.Join(args, " "), out, err)
		}
	}
	// Copy/paste and URL bindings. Best-effort: a tmux that rejects one of
	// them should not stop the session from coming up, so the error is
	// logged rather than returned.
	if err := ConfigureClipboard(); err != nil {
		log.Printf("clipboard bindings: %v", err)
	}
	return nil
}

// NewWindow creates a new window in the session running the given command.
func NewWindow(session, name, command string) error {
	cmd := exec.Command("tmux", "new-window", "-t", session, "-n", name, command)
	return cmd.Run()
}

// KillWindow kills a window by name.
func KillWindow(session, name string) error {
	target := fmt.Sprintf("%s:%s", session, name)
	cmd := exec.Command("tmux", "kill-window", "-t", target)
	return cmd.Run()
}

// KillWindowByIndex kills a window by its numeric index. Required when two
// windows in a session share the same name — name-based targeting hits only
// one and is non-deterministic about which.
func KillWindowByIndex(session string, index int) error {
	target := fmt.Sprintf("%s:%d", session, index)
	cmd := exec.Command("tmux", "kill-window", "-t", target)
	return cmd.Run()
}

// KillSession kills the entire tmux session.
func KillSession(session string) error {
	cmd := exec.Command("tmux", "kill-session", "-t", session)
	return cmd.Run()
}

// ListWindows returns all windows in a session.
func ListWindows(session string) ([]Window, error) {
	cmd := exec.Command("tmux", "list-windows", "-t", session, "-F", "#{window_index}\t#{window_name}\t#{window_active}")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var windows []Window
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		idx, _ := strconv.Atoi(parts[0])
		windows = append(windows, Window{
			Index:  idx,
			Name:   parts[1],
			Active: parts[2] == "1",
		})
	}
	return windows, nil
}

// CapturePaneContent captures the visible content of a tmux pane.
func CapturePaneContent(session, windowName string, height int) (string, error) {
	target := fmt.Sprintf("%s:%s", session, windowName)
	args := []string{"capture-pane", "-t", target, "-p"}
	if height > 0 {
		// Capture last N lines
		args = append(args, "-S", fmt.Sprintf("-%d", height))
	}
	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SelectAndAttach selects a window and attaches to the session.
// Returns when the user detaches.
func SelectAndAttach(session, windowName string) error {
	if windowName != "" {
		target := fmt.Sprintf("%s:%s", session, windowName)
		selectCmd := exec.Command("tmux", "select-window", "-t", target)
		_ = selectCmd.Run()
	}

	cmd := exec.Command("tmux", "attach-session", "-t", session)
	cmd.Stdin = nil  // inherit from parent
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// IsPaneDead returns true if the first pane in the given window is dead.
func IsPaneDead(session, windowName string) bool {
	target := fmt.Sprintf("%s:%s", session, windowName)
	cmd := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_dead}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// RespawnPane kills and relaunches the first pane in a window with the given command.
func RespawnPane(session, windowName, command string) error {
	target := fmt.Sprintf("%s:%s", session, windowName)
	cmd := exec.Command("tmux", "respawn-pane", "-t", target, "-k", command)
	return cmd.Run()
}

// SelectWindow selects a specific window.
func SelectWindow(session, windowName string) error {
	target := fmt.Sprintf("%s:%s", session, windowName)
	cmd := exec.Command("tmux", "select-window", "-t", target)
	return cmd.Run()
}

// SendKeys sends keystrokes to a tmux pane.
func SendKeys(session, window, keys string) error {
	target := fmt.Sprintf("%s:%s", session, window)
	cmd := exec.Command("tmux", "send-keys", "-t", target, keys)
	return cmd.Run()
}

// SendKeysEnter sends keystrokes followed by Enter to a tmux pane.
func SendKeysEnter(session, window, keys string) error {
	target := fmt.Sprintf("%s:%s", session, window)
	cmd := exec.Command("tmux", "send-keys", "-t", target, keys, "Enter")
	return cmd.Run()
}

// LoadBuffer loads text into a tmux paste buffer. Suitable for multiline text.
func LoadBuffer(text string) error {
	cmd := exec.Command("tmux", "load-buffer", "-")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// PasteBuffer pastes the tmux buffer into a pane.
func PasteBuffer(session, window string) error {
	target := fmt.Sprintf("%s:%s", session, window)
	cmd := exec.Command("tmux", "paste-buffer", "-t", target)
	return cmd.Run()
}

// GetPaneSize returns the width and height of the current terminal.
func GetPaneSize(session, windowName string) (int, int, error) {
	target := fmt.Sprintf("%s:%s", session, windowName)
	cmd := exec.Command("tmux", "display-message", "-t", target, "-p", "#{pane_width}\t#{pane_height}")
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("unexpected output: %s", out)
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	return w, h, nil
}
