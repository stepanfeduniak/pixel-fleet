// Package notify implements automatic session transition notifications.
package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Watch struct {
	Name            string
	Machine         string
	CreatedAt       time.Time // identifies the session even if its name is reused
	Armed           bool      // enabled; retained for compatibility with saved state
	Muted           bool      // explicit per-session opt-out
	ArmedAt         time.Time
	SeenWorking     bool
	Candidate       string
	Since           time.Time
	Pending         string
	Issue           string
	LastError       string
	RetryAt         time.Time
	LastObserved    string
	LastObservedAt  time.Time
	LastSubmittedAt time.Time // OS/API accepted submission, not proof of visible display
}

type Store struct{ Dir string }

func DefaultStore() Store {
	home, _ := os.UserHomeDir()
	return Store{filepath.Join(home, ".config", "cs")}
}

func (s Store) lock(name string, nonblock bool) (*os.File, error) {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	flags := syscall.LOCK_EX
	if nonblock {
		flags |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), flags); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Update serializes CLI, dashboard and worker changes across processes.
func (s Store) Update(fn func(map[string]Watch) error) error {
	f, err := s.lock("notifications.lock", false)
	if err != nil {
		return err
	}
	defer f.Close()
	path := filepath.Join(s.Dir, "notifications.json")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	state := map[string]Watch{}
	original := append([]byte(nil), data...)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &state); err != nil {
			return err
		}
		if state == nil {
			state = map[string]Watch{}
		}
	}
	if err := fn(state); err != nil {
		return err
	}
	data, err = json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if bytes.Equal(original, data) {
		return nil
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (s Store) Read() (map[string]Watch, error) {
	var state map[string]Watch
	err := s.Update(func(m map[string]Watch) error { state = m; return nil })
	return state, err
}
