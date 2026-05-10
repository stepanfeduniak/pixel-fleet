package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
	"github.com/stepanfeduniak/pixel-fleet/internal/config"
	"github.com/stepanfeduniak/pixel-fleet/internal/tmux"
)

// remoteSessionName builds a unique tmux session name for a cs session on
// the remote machine. Each cs session gets its own dedicated tmux session
// (no shared cs-remote), so the name needs to be unique enough that two
// sessions with the same display name never collide on the same tmux
// session. The result looks like "cs-<sanitized-display>-<8hex>".
func remoteSessionName(displayName string) string {
	slug := sanitizeForTmux(displayName)
	if slug == "" {
		slug = "session"
	}
	if len(slug) > 24 {
		slug = slug[:24]
	}
	return "cs-" + slug + "-" + randomShortID()
}

func sanitizeForTmux(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.' || r == '/' || r == ':':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-_")
}

func randomShortID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Fall back to a timestamp-based ID; never reuse the same one twice in a session.
		return fmt.Sprintf("%x", time.Now().UnixNano()&0xFFFFFFFF)
	}
	return hex.EncodeToString(buf[:])
}

// Manager handles session lifecycle.
type Manager struct {
	cfg   *config.Config
	store *Store
}

// NewManager creates a new session manager.
func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg, store: NewStore()}
}

// EnsureSession ensures the tmux session exists.
func (m *Manager) EnsureSession() error {
	if !tmux.SessionExists(m.cfg.SessionName) {
		return tmux.CreateSession(m.cfg.SessionName)
	}
	return nil
}

// CreateOptions controls per-session overrides for Create.
type CreateOptions struct {
	// RemoteControl, if non-nil, overrides the config default for whether
	// Claude is launched with --remote-control. Ignored when Agent == "codex"
	// (codex has no claude.ai/code bridge).
	RemoteControl *bool
	// Agent selects which CLI to launch. "" or "claude" launches claude;
	// "codex" launches codex. Anything else falls through to claude with
	// a log warning.
	Agent string
}

// Create launches a new Claude Code session with a user-given name.
func (m *Manager) Create(name, machine, path string) (*Session, error) {
	return m.CreateWithOptions(name, machine, path, CreateOptions{})
}

// CreateWithOptions launches a new Claude Code session and accepts per-call
// overrides such as disabling remote-control for a single session.
func (m *Manager) CreateWithOptions(name, machine, path string, opts CreateOptions) (*Session, error) {
	freshSession := !tmux.SessionExists(m.cfg.SessionName)
	if err := m.EnsureSession(); err != nil {
		return nil, fmt.Errorf("failed to create tmux session: %w", err)
	}

	// Resolve path. Apps that don't require a path (skills viewer, app
	// viewer, anything the user has flipped via config) get an empty
	// resolvedPath when no path was provided — buildLocalCommand and
	// buildRemoteCommand both skip the `cd` in that case.
	app, _ := apps.Resolve(opts.Agent)
	requiresPath := true
	if app != nil {
		requiresPath = m.cfg.RequiresPathFor(app.Name(), app.RequiresPath())
	}
	resolvedPath := ""
	if path != "" || requiresPath {
		resolvedPath = m.resolvePath(machine, path)
	}
	log.Printf("Create session: name=%s machine=%s path=%s -> resolved=%s requiresPath=%v", name, machine, path, resolvedPath, requiresPath)

	// Check if session already exists
	sessions, err := m.List()
	if err == nil {
		for _, s := range sessions {
			if s.Name == name {
				return &s, fmt.Errorf("session %q already exists", name)
			}
		}
	}

	// (app already resolved above for the requires-path check.)
	agent := ""
	if app != nil {
		agent = app.Name()
	}
	localBin, remoteBin := m.binsForApp(app)

	// Build command
	rcEnabled := m.cfg.IsRemoteControlEnabled()
	if opts.RemoteControl != nil {
		rcEnabled = *opts.RemoteControl
	}
	if app == nil || !app.SupportsRemoteControl() {
		rcEnabled = false
	}
	// Each cs session gets its own dedicated remote tmux session.
	remoteSession := remoteSessionName(name)
	cmd := BuildShellCommand(BuildOpts{
		Machine:           machine,
		Path:              resolvedPath,
		Agent:             agent,
		LocalClaudeBin:    localBin,
		RemoteClaudeBin:   remoteBin,
		WindowName:        name,
		RemoteSessionName: remoteSession,
		RemoteControl:     rcEnabled,
	})
	log.Printf("Session command: %s", cmd)

	// Create tmux window
	if err := tmux.NewWindow(m.cfg.SessionName, name, cmd); err != nil {
		return nil, fmt.Errorf("failed to create window: %w", err)
	}

	// If we just created the session, kill the default empty bash window
	if freshSession {
		_ = tmux.KillWindow(m.cfg.SessionName, "bash")
	}

	// Persist metadata to disk
	_ = m.store.Save(SessionRecord{
		Name:          name,
		RemoteSession: remoteSession,
		Machine:       machine,
		Path:          resolvedPath,
		Agent:         agent,
		CreatedAt:     time.Now(),
	})

	return &Session{
		Name:    name,
		Machine: machine,
		Path:    resolvedPath,
		Status:  StatusUnknown,
	}, nil
}

// List returns active (non-archived) sessions for the grid.
func (m *Manager) List() ([]Session, error) {
	return m.listFiltered(false)
}

// ListArchived returns only archived sessions for the archive view.
func (m *Manager) ListArchived() ([]Session, error) {
	return m.listFiltered(true)
}

// listFiltered enumerates tmux windows and returns the subset matching
// archived==wantArchived. Sessions whose store record is missing are
// treated as not-archived.
func (m *Manager) listFiltered(wantArchived bool) ([]Session, error) {
	if !tmux.SessionExists(m.cfg.SessionName) {
		return nil, nil
	}

	windows, err := tmux.ListWindows(m.cfg.SessionName)
	if err != nil {
		return nil, err
	}

	var sessions []Session
	// Dedupe by name. tmux allows duplicate window names (and a buggy
	// auto-restore prior to the IsArchived guard could create them) — we
	// don't want the grid or archive view to render the same session
	// twice. Keep the first occurrence; remaining dups stay in tmux but
	// are invisible. They get cleaned up naturally on `cs kill` or when
	// the user closes the duplicate window manually.
	seen := make(map[string]bool)
	for _, w := range windows {
		// Skip the dashboard window
		if w.Name == "dashboard" || w.Name == "bash" {
			continue
		}
		if seen[w.Name] {
			continue
		}
		seen[w.Name] = true
		s := Session{
			Name:        w.Name,
			WindowIndex: w.Index,
		}
		archived := false
		if rec, ok := m.store.Lookup(w.Name); ok {
			s.Machine = rec.Machine
			s.Path = rec.Path
			s.Agent = rec.Agent
			archived = rec.Archived
			s.Archived = archived
		}
		if archived != wantArchived {
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// Archive marks a session as archived in the store. The local viewer
// window and remote tmux session keep running — only the dashboard's
// view of the session changes.
func (m *Manager) Archive(name string) error {
	return m.setArchived(name, true)
}

// Unarchive clears the archived flag.
func (m *Manager) Unarchive(name string) error {
	return m.setArchived(name, false)
}

// AllWindowNames returns the deduped names of every session window in the
// local cs tmux session, regardless of archived state. The dashboard uses
// this as the source of truth for "is there already a local viewer for X"
// when deciding whether to auto-restore a tracked-alive remote session —
// List() can't be used for that check because it filters archived
// sessions out and would falsely report "no viewer" for a hidden one.
func (m *Manager) AllWindowNames() ([]string, error) {
	if !tmux.SessionExists(m.cfg.SessionName) {
		return nil, nil
	}
	windows, err := tmux.ListWindows(m.cfg.SessionName)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var names []string
	for _, w := range windows {
		if w.Name == "dashboard" || w.Name == "bash" {
			continue
		}
		if seen[w.Name] {
			continue
		}
		seen[w.Name] = true
		names = append(names, w.Name)
	}
	return names, nil
}

func (m *Manager) setArchived(name string, archived bool) error {
	rec, ok := m.store.Lookup(name)
	if !ok {
		return fmt.Errorf("session %q not found in store", name)
	}
	if rec.Archived == archived {
		return nil
	}
	rec.Archived = archived
	return m.store.Save(rec)
}

// Kill kills a session by name (supports partial match).
// For remote sessions this also kills the corresponding window inside the
// remote cs-remote tmux session so the Claude process actually stops.
// The lookup walks both the live local windows AND the persistent store so
// sessions whose local viewer has been closed can still be killed.
func (m *Manager) Kill(name string) error {
	type target struct {
		windowName string
		record     SessionRecord
		hasRecord  bool
		hasLocal   bool
	}

	// Collect candidates from local windows and store records.
	seen := make(map[string]*target)
	local, _ := m.List()
	for _, s := range local {
		if s.Name == name || strings.Contains(s.Name, name) {
			t := &target{windowName: s.Name, hasLocal: true}
			if rec, ok := m.store.Lookup(s.Name); ok {
				t.record = rec
				t.hasRecord = true
			}
			seen[s.Name] = t
		}
	}
	for _, rec := range m.store.Load() {
		if rec.Name == name || strings.Contains(rec.Name, name) {
			if t, ok := seen[rec.Name]; ok {
				t.record = rec
				t.hasRecord = true
			} else {
				seen[rec.Name] = &target{windowName: rec.Name, record: rec, hasRecord: true}
			}
		}
	}

	if len(seen) == 0 {
		return fmt.Errorf("session %q not found", name)
	}

	for _, t := range seen {
		// Kill the remote tmux first.
		if t.hasRecord && t.record.Machine != "" && t.record.Machine != "home" {
			if err := m.killRemoteForRecord(t.record); err != nil {
				log.Printf("warning: remote kill failed for %s on %s: %v", t.windowName, t.record.Machine, err)
			}
		}
		// Kill the local viewer if present.
		if t.hasLocal {
			if err := tmux.KillWindow(m.cfg.SessionName, t.windowName); err != nil {
				log.Printf("warning: local window kill failed for %s: %v", t.windowName, err)
			}
		}
		// Clean up git worktree if this session used one.
		if t.hasRecord && t.record.SourcePath != "" {
			m.cleanupWorktree(t.record.SourcePath, t.record.Path)
		}
		_ = m.store.Remove(t.windowName)
	}
	return nil
}

// killRemoteForRecord routes to the right tmux kill command based on which
// fields the SessionRecord has populated:
//
//   - RemoteSession set (current model): tmux kill-session -t <RemoteSession>
//   - RemoteWindow set (legacy unique-id model): tmux kill-window -t cs-remote:<RemoteWindow>
//   - Neither set (oldest legacy model): tmux kill-window -t cs-remote:<Name>
func (m *Manager) killRemoteForRecord(rec SessionRecord) error {
	if rec.RemoteSession != "" {
		return m.runRemoteTmux(rec.Machine, fmt.Sprintf("tmux kill-session -t %s", shellQuote(rec.RemoteSession)))
	}
	legacySession := "cs-remote"
	target := fmt.Sprintf("%s:%s", legacySession, rec.RemoteWindow)
	if rec.RemoteWindow == "" {
		target = fmt.Sprintf("%s:%s", legacySession, rec.Name)
	}
	return m.runRemoteTmux(rec.Machine, fmt.Sprintf("tmux kill-window -t %s", shellQuote(target)))
}

// runRemoteTmux executes a tmux command on the given remote machine via SSH.
func (m *Manager) runRemoteTmux(machine, remoteCmd string) error {
	cmd := exec.Command("ssh",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "BatchMode=yes",
		machine, remoteCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh %s: %s (%w)", remoteCmd, strings.TrimSpace(string(out)), err)
	}
	return nil
}


// cleanupWorktree removes a git worktree and its directory.
func (m *Manager) cleanupWorktree(sourceRepo, worktreePath string) {
	cmd := exec.Command("git", "-C", sourceRepo, "worktree", "remove", "--force", worktreePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("git worktree remove failed: %v\n%s", err, out)
		// Fall back to manual cleanup
		_ = os.RemoveAll(worktreePath)
		pruneCmd := exec.Command("git", "-C", sourceRepo, "worktree", "prune")
		_ = pruneCmd.Run()
	}
	log.Printf("Cleaned up worktree: %s", worktreePath)
}

// KillAll kills all sessions.
func (m *Manager) KillAll() error {
	_ = m.store.Clear()
	if tmux.SessionExists(m.cfg.SessionName) {
		return tmux.KillSession(m.cfg.SessionName)
	}
	return nil
}

// Capture captures the pane content for a session.
func (m *Manager) Capture(name string, height int) (string, error) {
	return tmux.CapturePaneContent(m.cfg.SessionName, name, height)
}

// CaptureAll captures pane content for all sessions and updates their status.
func (m *Manager) CaptureAll(sessions []Session, height int) []Session {
	for i := range sessions {
		output, err := m.Capture(sessions[i].Name, height)
		if err != nil {
			sessions[i].Status = StatusError
			sessions[i].LastOutput = fmt.Sprintf("capture error: %v", err)
			continue
		}
		sessions[i].LastOutput = output
		sessions[i].Status = DetectStatus(output)
	}
	return sessions
}

// LocalRepoPaths returns the configured local search paths for repo discovery.
func (m *Manager) LocalRepoPaths() []string {
	paths := []string{m.cfg.LocalBase}
	paths = append(paths, m.cfg.LocalExtraPaths...)
	return paths
}

// Config returns the manager's config.
func (m *Manager) Config() *config.Config {
	return m.cfg
}

// FetchAll discovers ALL cs-managed sessions across all known machines.
// Unlike Scan (which only returns orphans), FetchAll returns everything
// and marks each session as tracked or orphaned relative to the local store.
func (m *Manager) FetchAll() ([]DiscoveredSession, error) {
	machines := ListMachines()

	// Build lookup of locally tracked sessions for tagging.
	type pair struct{ machine, ident string }
	tracked := make(map[pair]string) // maps to display name
	for _, rec := range m.store.Load() {
		if rec.Machine == "" {
			continue
		}
		if rec.RemoteSession != "" {
			tracked[pair{rec.Machine, rec.RemoteSession}] = rec.Name
		}
	}

	all := ScanAll(machines, m.cfg.ScanTimeout)

	var results []DiscoveredSession
	for _, d := range all {
		// Skip local cs dashboard windows
		if d.Machine == "home" && d.TmuxSession == m.cfg.SessionName {
			continue
		}
		// Tag tracked sessions with their display name. Three cases:
		//   1. Per-session model: tmux session is "cs-<slug>-<id>" — match
		//      against records by RemoteSession.
		//   2. Legacy shared cs-remote: match against records by RemoteWindow.
		//   3. Arbitrary tmux session adopted by the user (e.g. someone ran
		//      `tmux new -s claude` manually, then adopted it): same WindowName
		//      match as legacy. Without this clause, adopted non-cs sessions
		//      keep showing up as "orphaned" forever.
		isPerSession := strings.HasPrefix(d.TmuxSession, "cs-") && d.TmuxSession != "cs-remote"
		if isPerSession {
			if name, ok := tracked[pair{d.Machine, d.TmuxSession}]; ok {
				d.Tracked = true
				d.DisplayName = name
				d.Reattachable = true
			}
		} else {
			if name, ok := tracked[pair{d.Machine, d.WindowName}]; ok {
				d.Tracked = true
				d.DisplayName = name
				d.Reattachable = true
			}
		}
		results = append(results, d)
	}

	return results, nil
}

// Scan discovers orphaned Claude sessions across all known machines.
func (m *Manager) Scan() ([]DiscoveredSession, error) {
	machines := ListMachines()

	// Build sets of (machine, identifier) pairs that cs is already tracking,
	// so we can filter them out of the orphan list. We use the persistent
	// store as the source of truth — the local viewer may or may not exist,
	// but if the store has the record, the session is ours.
	//
	// Two key spaces, one per supported model:
	//   knownSessions: per-session model — (machine, RemoteSession)
	//   knownWindows : legacy cs-remote model — (machine, RemoteWindow|Name)
	type pair struct{ machine, ident string }
	knownSessions := make(map[pair]bool)
	knownWindows := make(map[pair]bool)
	for _, rec := range m.store.Load() {
		if rec.Machine == "" {
			continue
		}
		if rec.RemoteSession != "" {
			knownSessions[pair{rec.Machine, rec.RemoteSession}] = true
			continue
		}
		w := rec.RemoteWindow
		if w == "" {
			w = rec.Name
		}
		knownWindows[pair{rec.Machine, w}] = true
	}

	all := ScanAll(machines, m.cfg.ScanTimeout)

	var orphaned []DiscoveredSession
	for _, d := range all {
		if d.Machine == "home" && d.TmuxSession == m.cfg.SessionName {
			continue // local cs windows
		}
		// Three cases (mirrors FetchAll's tagging logic):
		//   1. Per-session "cs-<slug>-<id>": match by RemoteSession.
		//   2. Legacy "cs-remote": match by RemoteWindow.
		//   3. Arbitrary adopted tmux session (e.g. user's manual `tmux new
		//      -s foo`): also match by RemoteWindow, since Adopt stores
		//      such records with rec.RemoteWindow set.
		isPerSession := strings.HasPrefix(d.TmuxSession, "cs-") && d.TmuxSession != "cs-remote"
		if isPerSession {
			if knownSessions[pair{d.Machine, d.TmuxSession}] {
				continue
			}
		} else {
			if knownWindows[pair{d.Machine, d.WindowName}] {
				continue
			}
		}
		orphaned = append(orphaned, d)
	}

	return orphaned, nil
}

// Adopt creates a local tmux window that reattaches to a discovered remote session.
func (m *Manager) Adopt(d DiscoveredSession, name string) (*Session, error) {
	if err := m.EnsureSession(); err != nil {
		return nil, fmt.Errorf("failed to ensure tmux session: %w", err)
	}

	// For per-session adoptions (cs-<slug>-<id>) attach to the whole tmux
	// session — there's only one window. For legacy cs-remote adoptions,
	// keep the explicit window target so we land on the right pane.
	var cmd string
	rec := SessionRecord{
		Name:      name,
		Machine:   d.Machine,
		Path:      d.Path,
		CreatedAt: time.Now(),
	}
	if strings.HasPrefix(d.TmuxSession, "cs-") && d.TmuxSession != "cs-remote" {
		cmd = BuildReattachCommand(d.Machine, d.TmuxSession, "")
		rec.RemoteSession = d.TmuxSession
	} else {
		cmd = BuildReattachCommand(d.Machine, d.TmuxSession, d.WindowName)
		rec.RemoteWindow = d.WindowName
	}
	log.Printf("Adopt session: name=%s cmd=%s", name, cmd)

	if err := tmux.NewWindow(m.cfg.SessionName, name, cmd); err != nil {
		return nil, fmt.Errorf("failed to create window: %w", err)
	}

	_ = m.store.Save(rec)

	return &Session{
		Name:    name,
		Machine: d.Machine,
		Path:    d.Path,
		Status:  StatusUnknown,
	}, nil
}

// binsForApp returns the (local, remote) binary paths to launch for the
// given app. Apps that don't need a binary (terminal) return empty strings;
// session.go uses the app's LaunchExec to produce the right exec target.
//
// Resolution order: the config's per-app override (BinFor), then the app's
// own DefaultLocalBin / DefaultRemoteBin. Unknown apps return empty pairs.
func (m *Manager) binsForApp(app apps.App) (string, string) {
	if app == nil || !app.NeedsBin() {
		return "", ""
	}
	local := m.cfg.BinFor(app.Name(), false)
	if local == "" {
		local = app.DefaultLocalBin()
	}
	remote := m.cfg.BinFor(app.Name(), true)
	if remote == "" {
		remote = app.DefaultRemoteBin()
	}
	return local, remote
}

func (m *Manager) resolvePath(machine, path string) string {
	if machine == "home" {
		// For local: expand ~ and resolve relative paths
		expanded := ExpandHome(path)
		if strings.HasPrefix(expanded, "/") {
			return expanded
		}
		// Relative path - resolve against local base
		return ExpandHome(m.cfg.LocalBase) + "/" + path
	}

	// Remote: keep ~ as-is (shell will expand on remote)
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~") {
		return path
	}
	base := m.cfg.RemoteBase
	if base == "~" {
		return "~/" + path
	}
	return base + "/" + path
}
