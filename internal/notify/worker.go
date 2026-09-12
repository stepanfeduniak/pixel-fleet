package notify

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/stepanfeduniak/pixel-fleet/internal/blocker"
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
	"github.com/stepanfeduniak/pixel-fleet/internal/tmux"
)

func (s Store) Running() bool {
	f, err := s.lock("notify-worker.lock", true)
	if err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	f.Close()
	return false
}

// EnsureWorker starts one detached watcher for all current and future sessions.
func (s Store) EnsureWorker(tmuxSession string) error {
	if s.Running() {
		return nil
	}
	// On non-Mac hosts Slack must be configured before automatic delivery.
	if err := s.Ready(); err != nil {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "notify.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(exe, "--notify-worker", tmuxSession)
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	for i := 0; i < 20; i++ {
		if s.Running() {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("notification worker did not start; check %s/notify.log", s.Dir)
}

// Snapshot reads the visible pane without changing session metadata.
func Snapshot(tmuxSession string, rec session.SessionRecord) (string, session.Status) {
	screen, err := tmux.CapturePaneContent(tmuxSession, rec.Name, 0)
	if err != nil || tmux.IsPaneDead(tmuxSession, rec.Name) {
		return "", session.StatusUnknown
	}
	process, _ := tmux.PaneCurrentCommand(tmuxSession, rec.Name)
	return classifySnapshot(screen, process, rec.Machine != "home")
}

func classifySnapshot(screen, process string, remote bool) (string, session.Status) {
	if remote {
		// Remote viewers often run inside a local zsh reconnect wrapper. Its
		// process name says nothing about the agent; use the remote screen.
		process = ""
	}
	agent := session.DetectAgent(screen, process, "")
	return agent, session.DetectStatus(screen, agent)
}

func (s Store) Run(tmuxSession string) error {
	f, err := s.lock("notify-worker.lock", true)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	// Confirm readiness again after a restart instead of trusting stale panes.
	if err := s.Update(func(state map[string]Watch) error {
		for name, w := range state {
			w.Candidate, w.Pending, w.Since = "", "", time.Time{}
			state[name] = w
		}
		return nil
	}); err != nil {
		return err
	}
	for {
		records := session.NewStore().Load()
		if err := s.Update(func(state map[string]Watch) error {
			SyncSessions(state, records)
			return nil
		}); err != nil {
			return err
		}
		state, err := s.Read()
		if err != nil {
			return err
		}
		type observation struct {
			rec    session.SessionRecord
			found  bool
			agent  string
			status session.Status
		}
		observed := map[string]observation{}
		for name, w := range state {
			if !w.Armed {
				continue
			}
			rec, ok := session.NewStore().Lookup(name)
			o := observation{rec: rec, found: ok}
			if ok && rec.CreatedAt.Equal(w.CreatedAt) {
				o.agent, o.status = Snapshot(tmuxSession, rec)
			}
			observed[name] = o
		}
		err = s.Update(func(current map[string]Watch) error {
			now := time.Now()
			for name, o := range observed {
				w := current[name]
				// Don't apply an observation to a newly armed/replaced watch.
				if w != state[name] {
					continue
				}
				if !o.found || !o.rec.CreatedAt.Equal(w.CreatedAt) {
					delete(current, name)
					continue
				}
				w.Observe(o.agent, o.status, now)
				current[name] = w
			}
			Deliver(current, blocker.Load().Active(now), now, s.Send)
			return nil
		})
		if err != nil {
			log.Printf("notifications: %v", err)
		}
		time.Sleep(3 * time.Second)
	}
}

// SyncSessions enrolls new sessions automatically, preserves explicit muting,
// and forgets removed/replaced sessions so names cannot inherit old events.
func SyncSessions(state map[string]Watch, records []session.SessionRecord) {
	present := map[string]bool{}
	for _, rec := range records {
		present[rec.Name] = true
		w, ok := state[rec.Name]
		if !ok || !w.CreatedAt.Equal(rec.CreatedAt) {
			w = Watch{Name: rec.Name, Machine: rec.Machine, CreatedAt: rec.CreatedAt}
		}
		w.Armed = !w.Muted
		state[rec.Name] = w
	}
	for name := range state {
		if !present[name] {
			delete(state, name)
		}
	}
}
