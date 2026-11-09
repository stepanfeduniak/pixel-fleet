package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionRecord is the persisted metadata for a session.
//
// A remote session is identified by one tmux target: RemoteSession, plus
// RemoteWindow when the record covers a single window rather than the whole
// session. Sessions cs launches own a dedicated tmux session and leave
// RemoteWindow empty; adopted ones name the window (by index, which survives
// tmux's automatic renaming). Records from the old shared "cs-remote" model
// are rewritten into this form on load — see normalize.
type SessionRecord struct {
	Name          string    `json:"name"`
	RemoteSession string    `json:"remote_session,omitempty"`
	RemoteWindow  string    `json:"remote_window,omitempty"`
	Machine       string    `json:"machine"`
	Path          string    `json:"path"`
	SourcePath    string    `json:"source_path,omitempty"`
	Agent         string    `json:"agent,omitempty"` // "claude" (default if empty) or "codex"
	// Archived hides the session from the active grid without killing
	// anything: the local viewer window and the remote tmux session both
	// keep running. The user can unarchive from the archive view.
	Archived  bool      `json:"archived,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store handles JSON persistence of session metadata.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore creates a store at ~/.config/cs/sessions.json.
func NewStore() *Store {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "cs")
	_ = os.MkdirAll(dir, 0755)
	return &Store{path: filepath.Join(dir, "sessions.json")}
}

func (s *Store) load() []SessionRecord {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var records []SessionRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil
	}
	for i := range records {
		records[i].normalize()
	}
	return records
}

// legacySharedSession is the single tmux session every remote cs session
// used to live in, one window each.
const legacySharedSession = "cs-remote"

// normalize rewrites a record from the shared cs-remote model, which stored
// only a window (or nothing, meaning the window was named after the
// session), into the session+window form everything else uses.
func (r *SessionRecord) normalize() {
	if r.Machine == "" || r.Machine == "home" || r.RemoteSession != "" {
		return
	}
	r.RemoteSession = legacySharedSession
	if r.RemoteWindow == "" {
		r.RemoteWindow = r.Name
	}
}

// RemoteTarget is the tmux target for the record's remote session.
func (r SessionRecord) RemoteTarget() string {
	return tmuxTarget(r.RemoteSession, r.RemoteWindow)
}

// Matches reports whether a discovered tmux window belongs to this record.
func (r SessionRecord) Matches(d DiscoveredSession) bool {
	if r.Machine != d.Machine || r.RemoteSession != d.TmuxSession {
		return false
	}
	return r.RemoteWindow == "" || r.RemoteWindow == d.WindowIndex || r.RemoteWindow == d.WindowName
}

func (s *Store) save(records []SessionRecord) error {
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Save upserts a session record by name.
func (s *Store) Save(rec SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records := s.load()
	found := false
	for i, r := range records {
		if r.Name == rec.Name {
			records[i] = rec
			found = true
			break
		}
	}
	if !found {
		records = append(records, rec)
	}
	return s.save(records)
}

// Remove deletes a session record by name.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records := s.load()
	filtered := records[:0]
	for _, r := range records {
		if r.Name != name {
			filtered = append(filtered, r)
		}
	}
	return s.save(filtered)
}

// Clear removes all session records.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(nil)
}

// Load returns all session records.
func (s *Store) Load() []SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Lookup finds a session record by name.
func (s *Store) Lookup(name string) (SessionRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, r := range s.load() {
		if r.Name == name {
			return r, true
		}
	}
	return SessionRecord{}, false
}
