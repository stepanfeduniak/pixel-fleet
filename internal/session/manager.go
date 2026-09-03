package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
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

	// Reachability from the most recent scan. Scans run on background
	// goroutines while the dashboard reads this from its event loop to
	// draw the machine picker, hence the lock.
	healthMu sync.RWMutex
	health   map[string]Health

	// Backs off machines that keep failing, so the recurring scan stops
	// paying a full timeout for hosts that are not there.
	throttle *ProbeThrottle
}

// NewManager creates a new session manager.
func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg, store: NewStore(), throttle: NewProbeThrottle()}
}

// recordHealth stores the reachability a scan just observed.
func (m *Manager) recordHealth(h map[string]Health) {
	m.healthMu.Lock()
	m.health = h
	m.healthMu.Unlock()
}

// MachineHealth returns the reachability seen by the most recent scan.
// Machines not probed yet are absent, which reads as HealthUnknown.
func (m *Manager) MachineHealth() map[string]Health {
	m.healthMu.RLock()
	defer m.healthMu.RUnlock()

	out := make(map[string]Health, len(m.health))
	for name, h := range m.health {
		out[name] = h
	}
	return out
}

// EnsureSession ensures the tmux session exists.
func (m *Manager) EnsureSession() error {
	if !tmux.SessionExists(m.cfg.SessionName) {
		return tmux.CreateSession(m.cfg.SessionName)
	}
	// The session predates this process — possibly an older cs — so reapply
	// the options and bindings it should have.
	if err := tmux.ApplySessionOptions(m.cfg.SessionName); err != nil {
		log.Printf("session options: %v", err)
	}
	return nil
}

// CreateOptions controls per-session overrides for Create.
type CreateOptions struct {
	// Agent names a system app to launch (skills viewer, app viewer).
	// Empty — every ordinary session — launches a login shell; what runs
	// inside it is detected, not declared. See session.DetectAgent.
	Agent string
}

// Create launches a new Claude Code session with a user-given name.
func (m *Manager) Create(name, machine, path string) (*Session, error) {
	return m.CreateWithOptions(name, machine, path, CreateOptions{})
}

// CreateWithOptions launches a new Claude Code session and accepts per-call
// overrides, namely the system app to launch for a viewer window.
func (m *Manager) CreateWithOptions(name, machine, path string, opts CreateOptions) (*Session, error) {
	freshSession := !tmux.SessionExists(m.cfg.SessionName)
	if err := m.EnsureSession(); err != nil {
		return nil, fmt.Errorf("failed to create tmux session: %w", err)
	}

	// Resolve path. Apps that don't require a path (skills viewer, app
	// viewer, anything the user has flipped via config) get an empty
	// resolvedPath when no path was provided — buildLocalCommand and
	// buildRemoteCommand both skip the `cd` in that case.
	//
	// ForSession, not Resolve: an empty Agent is an ordinary session, which
	// is a shell. Resolve would hand back the registry's first-registered
	// app — claude — and this value is what the launch command is built
	// from, so the session would exec an agent instead of a login shell.
	app := apps.ForSession(opts.Agent)
	requiresPath := true
	if app != nil {
		requiresPath = m.cfg.RequiresPathFor(app.Name(), app.RequiresPath())
	}
	resolvedPath := ""
	if path != "" || requiresPath {
		resolvedPath = m.resolvePath(machine, path)
	}
	log.Printf("Create session: name=%s machine=%s path=%s -> resolved=%s requiresPath=%v", name, machine, path, resolvedPath, requiresPath)

	// Check if session already exists. Use AllWindowNames (raw tmux state,
	// includes archived) rather than List() — an archived session whose
	// local viewer is still running (e.g. rebuilt by auto-restore) would
	// be invisible to List() but tmux still has the window, and creating
	// a second window with the same name produces an unreachable duplicate.
	existing, err := m.AllWindowNames()
	if err == nil {
		for _, n := range existing {
			if n == name {
				return nil, fmt.Errorf("session %q already exists (may be archived)", name)
			}
		}
	}

	// (app already resolved above for the requires-path check.)
	agent := ""
	if app != nil {
		agent = app.Name()
	}
	localBin, remoteBin := m.binsForApp(app)

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
		Clipboard:         m.cfg.IsClipboardEnabled(),
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
//
// Local windows are enumerated from raw tmux.ListWindows (not List, which
// dedupes) and killed by index, so duplicate-named windows — created by
// historical archived-collision bugs or external tmux activity — all get
// removed in a single Kill call instead of leaving a "ghost" behind.
func (m *Manager) Kill(name string) error {
	type target struct {
		windowName   string
		record       SessionRecord
		hasRecord    bool
		localIndices []int
	}

	windows, _ := tmux.ListWindows(m.cfg.SessionName)
	records := m.store.Load()

	// Prefer an exact name match. Substring matching is a convenience for
	// `cs kill <partial>` on the CLI, but it's a footgun: `kill app` would
	// also take down `app-viewer`/`apps`. So only fall back to substring
	// matching when nothing matches the name exactly. The dashboard always
	// passes the exact (pinned) session name, so it never hits the fallback.
	exact := false
	for _, w := range windows {
		if w.Name == name {
			exact = true
			break
		}
	}
	if !exact {
		for _, rec := range records {
			if rec.Name == name {
				exact = true
				break
			}
		}
	}
	matches := func(n string) bool {
		if exact {
			return n == name
		}
		return strings.Contains(n, name)
	}

	seen := make(map[string]*target)

	// Enumerate every tmux window (no dedupe) so duplicate names produce
	// one entry per index.
	for _, w := range windows {
		if w.Name == "dashboard" || w.Name == "bash" {
			continue
		}
		if !matches(w.Name) {
			continue
		}
		t, ok := seen[w.Name]
		if !ok {
			t = &target{windowName: w.Name}
			if rec, ok := m.store.Lookup(w.Name); ok {
				t.record = rec
				t.hasRecord = true
			}
			seen[w.Name] = t
		}
		t.localIndices = append(t.localIndices, w.Index)
	}

	for _, rec := range records {
		if !matches(rec.Name) {
			continue
		}
		if t, ok := seen[rec.Name]; ok {
			t.record = rec
			t.hasRecord = true
		} else {
			seen[rec.Name] = &target{windowName: rec.Name, record: rec, hasRecord: true}
		}
	}

	if len(seen) == 0 {
		return fmt.Errorf("session %q not found", name)
	}

	for _, t := range seen {
		// Kill the remote tmux first (once per name, even if multiple
		// duplicate local windows exist).
		if t.hasRecord && t.record.Machine != "" && t.record.Machine != "home" {
			if err := m.killRemoteForRecord(t.record); err != nil {
				log.Printf("warning: remote kill failed for %s on %s: %v", t.windowName, t.record.Machine, err)
			}
		}
		// Kill every local window with this name. Targeting by index
		// because tmux name-targeting hits only one of N duplicates.
		for _, idx := range t.localIndices {
			if err := tmux.KillWindowByIndex(m.cfg.SessionName, idx); err != nil {
				log.Printf("warning: local window kill failed for %s (index %d): %v", t.windowName, idx, err)
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
//
// Options come from sshControlOpts so this agrees with the discovery and
// doctor probes: ConnectTimeout bounds the handshake so an unreachable host
// fails in seconds instead of hanging on the kernel's ~2-minute SYN timeout
// (without it, killing a session whose host is down blocked the caller —
// and, in the TUI, the whole event loop — for minutes), BatchMode keeps a
// missing key from prompting forever, and the shared master means this
// usually reuses a connection the last scan already opened.
func (m *Manager) runRemoteTmux(machine, remoteCmd string) error {
	cmd := exec.Command("ssh", sshArgs(machine, remoteCmd)...)
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

// CaptureAll captures pane content for all sessions, works out what is
// running in each, and updates their status.
//
// The agent is detected here rather than chosen at creation: every session is
// a shell, so what it *is* can only be known by looking at it. Detection has
// to happen before DetectStatus, which picks its rules from the answer.
func (m *Manager) CaptureAll(sessions []Session, height int) []Session {
	for i := range sessions {
		output, err := m.Capture(sessions[i].Name, height)
		if err != nil {
			sessions[i].Status = StatusError
			sessions[i].LastOutput = fmt.Sprintf("capture error: %v", err)
			continue
		}
		sessions[i].LastOutput = output

		// The pane's own process is only visible for local sessions; a
		// remote one is running ssh, and is identified from the rendered
		// screen instead.
		processName := ""
		if sessions[i].Machine == "home" {
			processName, _ = tmux.PaneCurrentCommand(m.cfg.SessionName, sessions[i].Name)
		}
		// A second capture, without scrollback: `output` above deliberately
		// reaches back through history for the cell preview, but detection
		// must see only what is on screen now or an exited agent's chrome
		// keeps it alive forever.
		screen, err := m.Capture(sessions[i].Name, 0)
		if err != nil {
			screen = output
		}
		detected := DetectAgent(screen, processName, sessions[i].Agent)
		if detected != sessions[i].Agent {
			sessions[i].Agent = detected
			m.persistAgent(sessions[i].Name, detected)
		}

		sessions[i].Status = DetectStatus(output, sessions[i].Agent)
	}
	return sessions
}

// persistAgent records a newly detected agent so the badge survives a
// dashboard restart. Called only when the value actually changes — the
// refresh loop runs every couple of seconds and the store is a whole-file
// rewrite.
func (m *Manager) persistAgent(name, agent string) {
	rec, ok := m.store.Lookup(name)
	if !ok || rec.Agent == agent {
		return
	}
	rec.Agent = agent
	if err := m.store.Save(rec); err != nil {
		log.Printf("persist detected agent for %s: %v", name, err)
	}
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
//
// Every machine is probed, backoff or not: a user who asks for a fetch is
// asking to stop trusting the cache. Background callers want
// FetchAllThrottled instead.
func (m *Manager) FetchAll() ([]DiscoveredSession, error) {
	return m.fetchAll(nil)
}

// FetchAllThrottled is FetchAll for the recurring background scan: machines
// that keep failing are skipped for a while rather than costing a full
// ScanTimeout every cycle.
func (m *Manager) FetchAllThrottled() ([]DiscoveredSession, error) {
	return m.fetchAll(m.throttle)
}

func (m *Manager) fetchAll(throttle *ProbeThrottle) ([]DiscoveredSession, error) {
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

	all, health := ScanAll(machines, m.cfg.ScanTimeout, throttle)
	m.recordHealth(health)

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

	all, health := ScanAll(machines, m.cfg.ScanTimeout, nil)
	m.recordHealth(health)

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
	base := m.cfg.RemoteBaseFor(machine)
	if base == "~" {
		return "~/" + path
	}
	return base + "/" + path
}
