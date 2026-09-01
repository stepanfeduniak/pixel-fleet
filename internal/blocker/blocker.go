// Package blocker implements the coding blocker: a self-imposed, timed
// lockout that keeps you out of your sessions without stopping them.
//
// The state is a single absolute expiry timestamp persisted to
// ~/.config/cs/blocker.json. Storing the deadline rather than a countdown
// is what makes a blocker survive things that would otherwise defeat it —
// the dashboard TUI crashing, a ctrl+c, or a fresh `cs` invocation all
// reload the same deadline and pick the lockout back up.
//
// This is a commitment device, not a security boundary. Anyone willing to
// delete the state file is out, and that is fine: the job is to break the
// reflex of dropping into a session every thirty seconds, not to imprison
// a determined user.
package blocker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaxDuration caps how long a single blocker can run. A typo in the custom
// field ("500" meaning 500 minutes, or a stray zero) shouldn't be able to
// wall you off for a week.
const MaxDuration = 24 * time.Hour

// State is the on-disk blocker record.
//
// Until is the authority — Duration and StartedAt exist so the log and the
// UI can say something more useful than a bare deadline.
type State struct {
	Until     time.Time     `json:"until"`
	StartedAt time.Time     `json:"started_at"`
	Duration  time.Duration `json:"duration"`
}

// Path returns the location of the blocker state file. It sits alongside
// cs.log in the config dir the rest of cs already uses.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "cs", "blocker.json"), nil
}

// Active reports whether a blocker is running at the given instant.
func (s State) Active(now time.Time) bool {
	return !s.Until.IsZero() && now.Before(s.Until)
}

// Remaining returns the time left on the blocker, or 0 if none is running.
func (s State) Remaining(now time.Time) time.Duration {
	if !s.Active(now) {
		return 0
	}
	return s.Until.Sub(now)
}

// Load reads the persisted blocker state.
//
// Every failure mode — no file, unreadable file, malformed JSON — reads as
// "not blocked" rather than an error. A corrupt state file must never be
// able to brick the dashboard, and the safe direction to fail is open.
func Load() State {
	path, err := Path()
	if err != nil {
		return State{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}
	}
	return s
}

// Save writes the blocker state, creating the config dir if needed.
//
// The write goes to a temp file and is renamed into place, so an
// interrupted write leaves the previous state intact instead of a
// half-written file that Load would discard (silently unblocking you).
func Save(s State) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Clear removes the blocker state. Absent state is not an error.
func Clear() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Start begins a blocker of duration d and persists it. The returned State
// is usable even when the write failed, so a read-only config dir degrades
// to an in-memory blocker for the current process rather than no blocker.
func Start(d time.Duration, now time.Time) (State, error) {
	s := State{
		Until:     now.Add(d),
		StartedAt: now,
		Duration:  d,
	}
	return s, Save(s)
}

// ParseDuration reads a user-typed blocker duration.
//
// A bare number means minutes ("45" -> 45m), which is what anyone typing
// into a "how long?" box means. Anything else goes to time.ParseDuration,
// so "90m", "1h30m" and "2h" all work. Zero, negative, and anything past
// MaxDuration are rejected.
func ParseDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var d time.Duration
	if n, err := strconv.Atoi(s); err == nil {
		d = time.Duration(n) * time.Minute
	} else {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return 0, false
		}
		d = parsed
	}
	if d <= 0 || d > MaxDuration {
		return 0, false
	}
	return d, true
}

// FormatRemaining renders a countdown: mm:ss under an hour, h:mm:ss over.
//
// Seconds are rounded up so the final second reads "0:01" and not "0:00" —
// a counter that sits on zero while still blocking looks broken.
func FormatRemaining(d time.Duration) string {
	if d <= 0 {
		return "0:00"
	}
	total := int((d + time.Second - 1) / time.Second)
	h := total / 3600
	m := (total % 3600) / 60
	sec := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

// FormatDuration renders a chosen duration for a menu ("45 min", "1 h 30 min").
func FormatDuration(d time.Duration) string {
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%d h %d min", h, m)
	case h > 0:
		return fmt.Sprintf("%d h", h)
	default:
		return fmt.Sprintf("%d min", m)
	}
}
