package tui

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
	"github.com/stepanfeduniak/pixel-fleet/internal/blocker"
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
	"github.com/stepanfeduniak/pixel-fleet/internal/tmux"
)

// Mode represents the current TUI mode.
type Mode int

const (
	ModeGrid Mode = iota
	ModeNewSession
	ModeConfirmKill
	ModeHelp
	ModeScan
	ModeScanAdopt
	ModeArchive
	ModeBlockerPick
	ModeBlockerBreak
)

// New-session form field indices. Order on screen matches the constant
// values: agent first (so the rest of the form is context-aware — e.g.
// path may not be required for the chosen app), then name, machine,
// path. Tab cycles through them in this order.
const (
	fieldAgent = iota
	fieldName
	fieldMachine
	fieldPath
	fieldCount
)

// Model is the bubbletea model for the dashboard.
type Model struct {
	manager         *session.Manager
	tmuxSessionName string
	sessions        []session.Session
	selected        int
	width           int
	height          int
	mode            Mode
	err             error

	// killTarget pins the session name chosen for a kill at the moment the
	// user presses `x`. The background refresh tick reassigns and reorders
	// m.sessions while the confirm dialog is open (auto-restore can spawn a
	// new window between `x` and `y`), so resolving the target by the live
	// m.sessions[m.selected] index would kill whatever slid into that slot
	// instead of what the user selected. Pin by identity, not position.
	killTarget string

	// New session inputs: name, machine, path. focusedInput uses the
	// fieldXxx constants below so the order can be tweaked without
	// hunting magic numbers across the file.
	nameInput    textinput.Model
	pathInput    textinput.Model
	focusedInput int

	// Agent picker. selectedAgent indexes into newSessionApps — the
	// filtered list captured when the user pressed `n` (workflow agents)
	// or `N` (system / utility apps). Splitting the list keeps the
	// agent picker focused: most invocations are claude/codex/term, and
	// the rarer viewer apps live behind shift+n.
	selectedAgent  int
	newSessionApps []apps.App

	// Machine selection
	machines        []session.Machine
	selectedMachine int

	// Path suggestions
	pathSuggestions []string
	selectedSugg    int

	// Scan results
	discovered   []session.DiscoveredSession
	selectedDisc int
	scanning     bool
	adoptInput   textinput.Model

	// Refresh ticker
	refreshInterval time.Duration

	// Background discovery: every discoveryInterval the dashboard scans
	// known machines for cs sessions (orphans get surfaced in the header,
	// tracked-alive sessions whose local viewer is missing get auto-
	// restored). Set the interval to a non-positive value to disable.
	discoveryInterval time.Duration
	discoveryRunning  bool
	orphanedCount     int
	lastDiscovery     time.Time
	lastDiscoveryErr  error

	// previousSessionNames is the set of session names from the most
	// recent sessionsMsg. We diff each refresh to detect when the user
	// closes a window (present last tick, absent this tick) so we don't
	// auto-restore something they deliberately closed.
	previousSessionNames map[string]bool
	closedThisRun        map[string]bool

	// localWindowNames is the set of ALL session window names currently
	// in the local cs tmux (active + archived). Source of truth for
	// "does a local viewer already exist?" used by auto-restore to
	// avoid spawning duplicates. Refreshed every tick alongside the
	// filtered session list.
	localWindowNames map[string]bool

	// Archive view state. Archived sessions are hidden from the grid
	// (the local viewer + remote tmux keep running) and accessible via
	// the archive view (`A` from grid).
	archived         []session.Session
	selectedArchived int

	// Coding blocker. Deliberately NOT a Mode: a blocker leaves the
	// gallery on screen and navigable and only refuses the paths that
	// drop you inside a session, so it has to coexist with whatever mode
	// the user is in rather than replace the view. The two Mode values
	// above are just its dialogs (pick a duration, break early).
	//
	// blocker is a mirror of the on-disk state, reloaded at startup so a
	// crashed or ctrl+c'd dashboard comes back still blocked.
	blocker        blocker.State
	blockerPick    int             // index into blockerPresets; == len means "custom"
	blockerCustom  textinput.Model // custom duration entry
	blockerBreak   textinput.Model // typed confirmation to end early
	blockerNotice  time.Time       // when the user last bumped into the blocker
	blockerTicking bool            // a 1s countdown tick is in flight
	blockerDone    bool            // one-shot "blocker finished" flash
}

// restoreTask describes a tracked-alive remote session that needs a
// local viewer spawned on the next discovery cycle.
type restoreTask struct {
	Name        string
	Machine     string
	TmuxSession string
	WindowName  string // empty for the per-session model; set for legacy/adopted
}

// tickMsg triggers a refresh.
type tickMsg time.Time

// sessionsMsg carries refreshed session data.
type sessionsMsg []session.Session

// archivedMsg carries refreshed archived session list.
type archivedMsg []session.Session

// localWindowsMsg carries all cs tmux window names (active + archived).
type localWindowsMsg []string

// errMsg carries an error.
type errMsg error

// switchedMsg is sent after we've switched tmux windows.
type switchedMsg struct{}

// detachedMsg is sent after we've detached the tmux client.
type detachedMsg struct{}

// repoSearchMsg carries search results.
type repoSearchMsg []string

// promptSentMsg signals that the initial prompt was delivered to a session.
type promptSentMsg struct {
	sessionName string
	err         error
}

// scanMsg carries discovered sessions from a scan.
type scanMsg []session.DiscoveredSession

// discoveryTickMsg fires the next background orphan-scan.
type discoveryTickMsg time.Time

// discoveryResultMsg carries the orphan count and any tracked-alive
// sessions whose local viewer is missing from a background scan.
type discoveryResultMsg struct {
	orphanedCount int
	trackedAlive  []restoreTask
	err           error
	at            time.Time
}

// restoredMsg signals that auto-restore spawned N new viewer windows.
// We use it to trigger an immediate session refresh so the new windows
// show up in the dashboard without waiting for the next 2 s tick.
type restoredMsg int

// killedMsg signals that a kill finished (the name is the session that was
// targeted). Kill runs on a goroutine — see runKill — so this is how the
// completed kill gets back onto the UI thread to trigger a refresh.
type killedMsg string

// FocusSession is set when the user wants to attach to a session.
var FocusSession string

// NewModel creates a new TUI model.
func NewModel(mgr *session.Manager, tmuxSessionName string, refreshInterval, discoveryInterval time.Duration) Model {
	ni := textinput.New()
	ni.Placeholder = "session name (e.g. training, frontend)"
	ni.CharLimit = 64
	ni.Width = 50

	pi := textinput.New()
	pi.Placeholder = "path (type to search repos...)"
	pi.CharLimit = 256
	pi.Width = 50

	ai := textinput.New()
	ai.Placeholder = "name for this session"
	ai.CharLimit = 64
	ai.Width = 50

	bc := textinput.New()
	bc.Placeholder = "e.g. 20m, 90m, 1h30m"
	bc.CharLimit = 12
	bc.Width = 20

	bb := textinput.New()
	// No placeholder: a dimmed "break" sitting in the field is hard to tell
	// from a typed one, and the prompt above it already says the word.
	bb.CharLimit = 16
	bb.Width = 20

	machines := session.ListMachines()

	// Reload any blocker left over from a previous dashboard process. This
	// is what makes ctrl+c useless as an escape: remain-on-exit respawns
	// the pane, and the deadline is still sitting on disk.
	blk := blocker.Load()

	return Model{
		manager:           mgr,
		tmuxSessionName:   tmuxSessionName,
		refreshInterval:   refreshInterval,
		discoveryInterval: discoveryInterval,
		nameInput:         ni,
		pathInput:         pi,
		adoptInput:        ai,
		machines:          machines,
		blockerCustom:     bc,
		blockerBreak:      bb,
		blocker:           blk,
		blockerTicking:    blk.Active(time.Now()),
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.refreshSessions,
		m.refreshArchived,
		m.refreshLocalWindows,
		m.tickCmd(),
	}
	if m.blocker.Active(time.Now()) {
		cmds = append(cmds, m.blockerTickCmd())
	}
	if m.discoveryInterval > 0 {
		// Kick off an immediate background scan so the orphan badge
		// appears as soon as the first scan completes, plus the recurring
		// ticker for subsequent scans.
		cmds = append(cmds, m.runDiscovery, m.discoveryTickCmd())
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.refreshSessions, m.refreshArchived, m.refreshLocalWindows, m.tickCmd())

	case switchedMsg:
		return m, nil

	case detachedMsg:
		return m, nil

	case sessionsMsg:
		// Diff the new session list against the previous tick's snapshot.
		// Any name that disappeared was closed (by `cs kill`, by the user
		// hitting `x`, or by a manual `tmux kill-window`). Mark such names
		// so auto-restore won't fight them this dashboard run.
		newNames := make(map[string]bool, len(msg))
		for _, s := range msg {
			newNames[s.Name] = true
		}
		if m.previousSessionNames != nil {
			if m.closedThisRun == nil {
				m.closedThisRun = make(map[string]bool)
			}
			for name := range m.previousSessionNames {
				if !newNames[name] {
					m.closedThisRun[name] = true
				}
			}
			// If a name reappears (user explicitly attaches it again) clear
			// the close flag so a future close-then-discover restores it.
			for name := range newNames {
				delete(m.closedThisRun, name)
			}
		}
		m.previousSessionNames = newNames

		m.sessions = msg
		if m.selected >= len(m.sessions) {
			m.selected = max(0, len(m.sessions)-1)
		}
		return m, nil

	case archivedMsg:
		m.archived = msg
		if m.selectedArchived >= len(m.archived) {
			m.selectedArchived = max(0, len(m.archived)-1)
		}
		return m, nil

	case localWindowsMsg:
		set := make(map[string]bool, len(msg))
		for _, n := range msg {
			set[n] = true
		}
		m.localWindowNames = set
		return m, nil

	case repoSearchMsg:
		m.pathSuggestions = msg
		m.selectedSugg = 0
		return m, nil

	case promptSentMsg:
		if msg.err != nil {
			log.Printf("Failed to send prompt to %s: %v", msg.sessionName, msg.err)
		} else {
			log.Printf("Prompt sent to session %s", msg.sessionName)
		}
		return m, nil

	case scanMsg:
		m.discovered = msg
		m.selectedDisc = 0
		m.scanning = false
		// Manual scan also refreshes the background orphan badge so the
		// header stays consistent with what the user just looked at. Note:
		// scanMsg comes from runScan -> FetchAll (everything tagged), so
		// counting !Tracked gives the orphan count, same as runDiscovery.
		count := 0
		for _, d := range msg {
			if !d.Tracked {
				count++
			}
		}
		m.orphanedCount = count
		m.lastDiscovery = time.Now()
		return m, nil

	case discoveryTickMsg:
		// Schedule the next tick first; only kick off another scan if the
		// previous one isn't still running (avoids piling up SSHs when a
		// machine is slow or unreachable).
		next := m.discoveryTickCmd()
		if m.discoveryRunning {
			return m, next
		}
		m.discoveryRunning = true
		return m, tea.Batch(m.runDiscovery, next)

	case discoveryResultMsg:
		m.discoveryRunning = false
		m.lastDiscovery = msg.at
		m.lastDiscoveryErr = msg.err
		if msg.err != nil {
			return m, nil
		}
		m.orphanedCount = msg.orphanedCount
		// Auto-restore: for each tracked-alive session whose local viewer
		// is missing AND the user didn't deliberately close it this run,
		// spawn a new viewer in the dashboard. The "viewer exists" check
		// uses localWindowNames (ALL cs tmux windows, including archived)
		// rather than previousSessionNames (which is the filtered grid
		// list and would falsely report archived viewers as missing).
		var spawn []restoreTask
		for _, t := range msg.trackedAlive {
			if m.localWindowNames[t.Name] {
				continue // viewer already exists locally (active or archived)
			}
			// Fallback for the very first tick before localWindowNames is
			// populated: defer to the filtered grid snapshot so we don't
			// spam restore commands.
			if m.localWindowNames == nil && m.previousSessionNames[t.Name] {
				continue
			}
			if m.closedThisRun[t.Name] {
				continue // user closed it this run; don't fight them
			}
			spawn = append(spawn, t)
		}
		if len(spawn) == 0 {
			return m, nil
		}
		return m, m.runAutoRestore(spawn)

	case restoredMsg:
		if int(msg) > 0 {
			// New windows just appeared; refresh immediately so the grid
			// shows them without waiting for the next 2 s tick.
			return m, m.refreshSessions
		}
		return m, nil

	case killedMsg:
		// The session is gone (or the kill failed and was logged); refresh
		// every view so it drops out of the grid/archive without waiting for
		// the next tick.
		return m, tea.Batch(m.refreshSessions, m.refreshArchived, m.refreshLocalWindows)

	case blockerTickMsg:
		return m.handleBlockerTick(time.Time(msg))

	case blockerBellMsg:
		return m, nil

	case errMsg:
		m.err = msg
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.mode == ModeNewSession || m.mode == ModeScanAdopt {
		return m.updateInputs(msg)
	}
	if m.mode == ModeBlockerPick || m.mode == ModeBlockerBreak {
		return m.updateBlockerInputs(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	log.Printf("Key pressed: %q (type=%d) mode=%d", msg.String(), msg.Type, m.mode)

	switch {
	case msg.String() == "ctrl+c":
		log.Printf("Ctrl+C pressed, quitting TUI")
		return m, tea.Quit
	}

	switch m.mode {
	case ModeGrid:
		return m.handleGridKey(msg)
	case ModeNewSession:
		return m.handleNewSessionKey(msg)
	case ModeConfirmKill:
		return m.handleConfirmKillKey(msg)
	case ModeHelp:
		return m.handleHelpKey(msg)
	case ModeScan:
		return m.handleScanKey(msg)
	case ModeScanAdopt:
		return m.handleScanAdoptKey(msg)
	case ModeArchive:
		return m.handleArchiveKey(msg)
	case ModeBlockerPick:
		return m.handleBlockerPickKey(msg)
	case ModeBlockerBreak:
		return m.handleBlockerBreakKey(msg)
	}

	return m, nil
}

func (m Model) handleGridKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cols, _ := gridLayout(len(m.sessions), m.width, m.height)

	// Any keypress dismisses the "blocker finished" flash.
	m.blockerDone = false

	switch {
	case msg.String() == "q":
		return m, func() tea.Msg {
			cmd := exec.Command("tmux", "detach-client")
			_ = cmd.Run()
			return detachedMsg{}
		}

	case msg.String() == "up" || msg.String() == "k":
		if m.selected >= cols {
			m.selected -= cols
		}

	case msg.String() == "down" || msg.String() == "j":
		if m.selected+cols < len(m.sessions) {
			m.selected += cols
		}

	case msg.String() == "left" || msg.String() == "h":
		if m.selected > 0 {
			m.selected--
		}

	case msg.String() == "right" || msg.String() == "l":
		if m.selected < len(m.sessions)-1 {
			m.selected++
		}

	case msg.String() == "enter":
		// The blocker's one job. Bail before building the select-window
		// command rather than discarding it afterwards, so there is no
		// path where a blocked focus still reaches tmux.
		if m.blocker.Active(time.Now()) {
			m.blockerNotice = time.Now()
			return m, nil
		}
		if len(m.sessions) > 0 && m.selected < len(m.sessions) {
			name := m.sessions[m.selected].Name
			sessionName := m.tmuxSessionName
			return m, func() tea.Msg {
				target := fmt.Sprintf("%s:%s", sessionName, name)
				cmd := exec.Command("tmux", "select-window", "-t", target)
				_ = cmd.Run()
				return switchedMsg{}
			}
		}

	case msg.String() == "n":
		agents, _ := apps.FilterByKind()
		return m.openNewSession(agents)

	case msg.String() == "N":
		// Shift+N: pick a system / viewer app (skills, app viewer).
		// Kept off the main `n` flow so the common path stays minimal.
		_, system := apps.FilterByKind()
		if len(system) == 0 {
			return m, nil
		}
		return m.openNewSession(system)

	case msg.String() == "x":
		if len(m.sessions) > 0 && m.selected < len(m.sessions) {
			// Capture the target by name now — m.selected is a positional
			// index into a list the refresh tick reorders out from under us.
			m.killTarget = m.sessions[m.selected].Name
			m.mode = ModeConfirmKill
		}

	case msg.String() == "s":
		m.mode = ModeScan
		m.scanning = true
		m.discovered = nil
		m.selectedDisc = 0
		return m, m.runScan

	case msg.String() == "r":
		return m, m.refreshSessions

	case msg.String() == "?":
		m.mode = ModeHelp

	case msg.String() == "a":
		// Archive currently selected session.
		if len(m.sessions) > 0 && m.selected < len(m.sessions) {
			name := m.sessions[m.selected].Name
			if err := m.manager.Archive(name); err != nil {
				m.err = err
				log.Printf("Archive %q failed: %v", name, err)
			} else {
				log.Printf("Archived %q", name)
			}
			return m, tea.Batch(m.refreshSessions, m.refreshArchived)
		}

	case msg.String() == "A":
		// Open archive view.
		m.mode = ModeArchive
		m.selectedArchived = 0
		return m, m.refreshArchived

	case msg.String() == "b" || msg.String() == "B":
		// Same key starts a blocker and breaks one — while blocked, the
		// only thing `b` could sensibly mean is "let me out".
		if m.blocker.Active(time.Now()) {
			m.mode = ModeBlockerBreak
			m.blockerBreak.Reset()
			m.blockerBreak.Focus()
			return m, textinput.Blink
		}
		m.mode = ModeBlockerPick
		m.blockerPick = 0
		m.blockerCustom.Reset()
		m.blockerCustom.Blur()
		return m, nil
	}

	return m, nil
}

// handleArchiveKey handles keys while the archive view is open.
func (m Model) handleArchiveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "A":
		m.mode = ModeGrid
		return m, nil

	case "up", "k":
		if m.selectedArchived > 0 {
			m.selectedArchived--
		}
		return m, nil

	case "down", "j":
		if m.selectedArchived < len(m.archived)-1 {
			m.selectedArchived++
		}
		return m, nil

	case "u", "enter":
		// Unarchive the highlighted entry.
		if len(m.archived) == 0 || m.selectedArchived >= len(m.archived) {
			return m, nil
		}
		name := m.archived[m.selectedArchived].Name
		if err := m.manager.Unarchive(name); err != nil {
			m.err = err
			log.Printf("Unarchive %q failed: %v", name, err)
			return m, nil
		}
		log.Printf("Unarchived %q", name)
		return m, tea.Batch(m.refreshSessions, m.refreshArchived)
	}
	return m, nil
}

// openNewSession enters ModeNewSession with the given app list as the
// available agents. Used by `n` (workflow agents) and `N` (system /
// viewer apps). Starts on the agent picker — picking the app first
// lets the rest of the form adapt (e.g. a viewer skips path entirely
// thanks to RequiresPath/requires_path).
func (m Model) openNewSession(available []apps.App) (Model, tea.Cmd) {
	m.mode = ModeNewSession
	m.nameInput.Reset()
	m.pathInput.Reset()
	m.pathSuggestions = nil
	m.selectedSugg = 0
	m.selectedMachine = 0
	m.selectedAgent = 0
	m.focusedInput = fieldAgent
	m.nameInput.Blur()
	m.pathInput.Blur()
	m.machines = session.ListMachines() // refresh
	m.newSessionApps = available
	return m, nil
}

// totalInputFields returns the number of input steps in the new session
// flow. Driven by the fieldXxx constants — adjust those, not this number.
func (m Model) totalInputFields() int {
	return fieldCount
}

// agentName returns the canonical agent string for the current selection.
// Indexes into m.newSessionApps — the filtered list captured at the
// moment the user opened the new-session form (workflow apps for `n`,
// system apps for `N`).
func (m Model) agentName() string {
	list := m.newSessionApps
	if len(list) == 0 {
		return ""
	}
	if m.selectedAgent < 0 || m.selectedAgent >= len(list) {
		return list[0].Name()
	}
	return list[m.selectedAgent].Name()
}

func (m Model) handleNewSessionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeGrid
		return m, nil

	case "tab":
		m.focusedInput = (m.focusedInput + 1) % m.totalInputFields()
		return m.focusNewInput()

	case "up":
		switch m.focusedInput {
		case fieldAgent:
			if m.selectedAgent > 0 {
				m.selectedAgent--
			}
			return m, nil
		case fieldMachine:
			if len(m.machines) > 0 && m.selectedMachine > 0 {
				m.selectedMachine--
			}
			return m, nil
		case fieldPath:
			if len(m.pathSuggestions) > 0 && m.selectedSugg > 0 {
				m.selectedSugg--
			}
			return m, nil
		}

	case "down":
		switch m.focusedInput {
		case fieldAgent:
			if m.selectedAgent < len(m.newSessionApps)-1 {
				m.selectedAgent++
			}
			return m, nil
		case fieldMachine:
			if len(m.machines) > 0 && m.selectedMachine < len(m.machines)-1 {
				m.selectedMachine++
			}
			return m, nil
		case fieldPath:
			if len(m.pathSuggestions) > 0 && m.selectedSugg < len(m.pathSuggestions)-1 {
				m.selectedSugg++
			}
			return m, nil
		}

	case "enter":
		// Path suggestions: accept the highlighted one.
		if m.focusedInput == fieldPath && len(m.pathSuggestions) > 0 && m.selectedSugg < len(m.pathSuggestions) {
			m.pathInput.SetValue(m.pathSuggestions[m.selectedSugg])
			m.pathSuggestions = nil
			return m, nil
		}

		// Try to create the session
		return m.tryCreateSession()
	}

	// Forward keystrokes to text input fields
	if m.focusedInput == fieldName {
		model, cmd := m.updateInputs(msg)
		m = model.(Model)
		return m, cmd
	}
	if m.focusedInput == fieldPath {
		prevPath := m.pathInput.Value()
		model, cmd := m.updateInputs(msg)
		m = model.(Model)
		if m.pathInput.Value() != prevPath {
			return m, tea.Batch(cmd, m.searchRepos)
		}
		return m, cmd
	}

	return m, nil
}

// focusNewInput blurs all inputs and focuses the one at m.focusedInput.
func (m Model) focusNewInput() (Model, tea.Cmd) {
	m.nameInput.Blur()
	m.pathInput.Blur()

	switch m.focusedInput {
	case fieldName:
		m.nameInput.Focus()
		return m, textinput.Blink
	case fieldPath:
		m.pathInput.Focus()
		return m, tea.Batch(textinput.Blink, m.searchRepos)
	}
	return m, nil
}

// tryCreateSession attempts to create a session from the form values.
func (m Model) tryCreateSession() (Model, tea.Cmd) {
	name := strings.TrimSpace(m.nameInput.Value())
	path := strings.TrimSpace(m.pathInput.Value())
	machine := ""
	if m.selectedMachine < len(m.machines) {
		machine = m.machines[m.selectedMachine].Name
	}

	agent := m.agentName()
	// Viewer apps don't need a working path. Regular agents can also
	// opt out via config (apps.<name>.requires_path: false), so use the
	// effective value rather than the app's own default.
	pathRequired := true
	if app, ok := apps.Lookup(agent); ok {
		pathRequired = m.manager.Config().RequiresPathFor(app.Name(), app.RequiresPath())
	}
	if name == "" || machine == "" || (pathRequired && path == "") {
		return m, nil
	}

	log.Printf("Creating session: name=%s machine=%s path=%s agent=%s", name, machine, path, agent)
	_, err := m.manager.CreateWithOptions(name, machine, path, session.CreateOptions{Agent: agent})
	if err != nil {
		m.err = err
		log.Printf("Create error: %v", err)
	}
	m.mode = ModeGrid
	return m, m.refreshSessions
}

func (m Model) handleConfirmKillKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		var cmd tea.Cmd
		if m.killTarget != "" {
			// Kill the name pinned when `x` was pressed, NOT the live
			// m.sessions[m.selected] — a background refresh may have
			// reordered the grid while this dialog was open. Kill off the
			// event loop: it does a remote tmux kill over SSH, which can take
			// a few seconds against an unreachable host. Run inline and the
			// whole dashboard freezes until it returns.
			cmd = m.runKill(m.killTarget)
		}
		m.killTarget = ""
		m.mode = ModeGrid
		return m, cmd

	case "n", "N", "esc":
		m.killTarget = ""
		m.mode = ModeGrid
	}

	return m, nil
}

func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.mode = ModeGrid
	return m, nil
}

func (m Model) handleScanKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeGrid
		return m, nil

	case "up", "k":
		if m.selectedDisc > 0 {
			m.selectedDisc--
		}

	case "down", "j":
		if m.selectedDisc < len(m.discovered)-1 {
			m.selectedDisc++
		}

	case "enter":
		if len(m.discovered) > 0 && m.selectedDisc < len(m.discovered) {
			d := m.discovered[m.selectedDisc]
			if d.Tracked {
				// Already tracked — switch to that session's local window.
				// The scan view is the other door into a session, so the
				// blocker has to hold it shut too.
				if m.blocker.Active(time.Now()) {
					m.blockerNotice = time.Now()
					return m, nil
				}
				if d.DisplayName != "" {
					sessionName := m.tmuxSessionName
					name := d.DisplayName
					return m, func() tea.Msg {
						target := fmt.Sprintf("%s:%s", sessionName, name)
						cmd := exec.Command("tmux", "select-window", "-t", target)
						_ = cmd.Run()
						return switchedMsg{}
					}
				}
				return m, nil
			}
			m.mode = ModeScanAdopt
			m.adoptInput.Reset()
			m.adoptInput.Focus()
			return m, textinput.Blink
		}
	}

	return m, nil
}

func (m Model) handleScanAdoptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = ModeScan
		return m, nil

	case "enter":
		name := strings.TrimSpace(m.adoptInput.Value())
		if name != "" && m.selectedDisc < len(m.discovered) {
			d := m.discovered[m.selectedDisc]
			log.Printf("Adopting session: name=%s machine=%s path=%s", name, d.Machine, d.Path)
			_, err := m.manager.Adopt(d, name)
			if err != nil {
				m.err = err
				log.Printf("Adopt error: %v", err)
			}
			m.mode = ModeGrid
			return m, m.refreshSessions
		}
	}

	// Forward keystrokes to the adopt input
	var cmd tea.Cmd
	m.adoptInput, cmd = m.adoptInput.Update(msg)
	return m, cmd
}

func (m Model) updateInputs(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.focusedInput {
	case fieldName:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case fieldPath:
		m.pathInput, cmd = m.pathInput.Update(msg)
	}
	return m, cmd
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	switch m.mode {
	case ModeGrid:
		return m.viewGrid()
	case ModeNewSession:
		return m.viewNewSession()
	case ModeConfirmKill:
		return m.viewConfirmKill()
	case ModeHelp:
		return m.viewHelp()
	case ModeScan, ModeScanAdopt:
		return m.viewScan()
	case ModeArchive:
		return m.viewArchive()
	case ModeBlockerPick:
		return m.viewBlockerPick()
	case ModeBlockerBreak:
		return m.viewBlockerBreak()
	}
	return ""
}

func (m Model) viewGrid() string {
	now := time.Now()
	blocked := m.blocker.Active(now)

	sessionCount := fmt.Sprintf("%d sessions", len(m.sessions))
	header := fmt.Sprintf(" pixel-fleet   %s", sessionCount)
	if m.orphanedCount > 0 {
		header += fmt.Sprintf("   %d orphaned remote (press s)", m.orphanedCount)
	}
	if len(m.archived) > 0 {
		header += fmt.Sprintf("   %d archived (press A)", len(m.archived))
	}
	title := headerStyle.Width(m.width).Render(header)

	// The banner stacks above the grid rather than replacing it: during a
	// blocker the gallery stays on screen and navigable, only the way in
	// is shut. It costs one line, so the grid gets one line shorter.
	banner := ""
	chrome := 3 // title + footer + the grid's own trailing line
	switch {
	case blocked:
		banner = m.viewBlockerBanner(now)
		chrome++
	case m.blockerDone:
		banner = blockerDoneStyle.Width(m.width).Render(
			" ✓  blocker finished — the fleet is yours again")
		chrome++
	}

	gridHeight := m.height - chrome
	grid := renderGrid(m.sessions, m.selected, m.width, gridHeight)

	focusHint := "focus"
	blockHint := footerKeyStyle.Render("[b]") + " block"
	if blocked {
		focusHint = dimStyle().Render("blocked")
		blockHint = footerKeyStyle.Render("[b]") + " break"
	}
	footer := footerStyle.Width(m.width).Render(
		fmt.Sprintf(" %s new  %s system  %s %s  %s kill  %s archive  %s archive view  %s fetch all  %s refresh  %s  %s detach",
			footerKeyStyle.Render("[n]"),
			footerKeyStyle.Render("[N]"),
			footerKeyStyle.Render("[enter]"),
			focusHint,
			footerKeyStyle.Render("[x]"),
			footerKeyStyle.Render("[a]"),
			footerKeyStyle.Render("[A]"),
			footerKeyStyle.Render("[s]"),
			footerKeyStyle.Render("[r]"),
			blockHint,
			footerKeyStyle.Render("[q]"),
		),
	)

	errLine := ""
	if m.err != nil {
		errLine = lipgloss.NewStyle().Foreground(errorColor).Render(
			fmt.Sprintf(" Error: %v", m.err),
		)
		m.err = nil
	}

	parts := []string{title}
	if banner != "" {
		parts = append(parts, banner)
	}
	parts = append(parts, grid, footer)
	if errLine != "" {
		parts = append(parts, errLine)
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m Model) viewNewSession() string {
	title := headerStyle.Width(m.width).Render(" pixel-fleet   New Session")

	// Resolve which app is currently picked so we can adapt later
	// fields: a viewer (or any app with requires_path: false in config)
	// gets its path field annotated as optional rather than required.
	var pickedApp apps.App
	if m.selectedAgent < len(m.newSessionApps) {
		pickedApp = m.newSessionApps[m.selectedAgent]
	}
	pathRequired := true
	if pickedApp != nil {
		pathRequired = m.manager.Config().RequiresPathFor(pickedApp.Name(), pickedApp.RequiresPath())
	}

	label := func(text string, focused bool) string {
		if focused {
			return lipgloss.NewStyle().Foreground(whiteColor).Bold(true).Underline(true).Render(text)
		}
		return promptLabelStyle.Render(text)
	}

	// Build form sections in order: agent first, then name, machine, path.
	var formParts []string

	// Agent picker — driven by the apps registry. Adding an app to
	// internal/apps/builtin extends the picker without touching this code.
	agentLines := make([]string, 0, len(m.newSessionApps))
	for i, a := range m.newSessionApps {
		prefix := "  "
		style := dimStyle()
		if i == m.selectedAgent {
			prefix = "▸ "
			if m.focusedInput == fieldAgent {
				style = lipgloss.NewStyle().Foreground(whiteColor).Bold(true)
			} else {
				style = lipgloss.NewStyle().Foreground(whiteColor)
			}
		}
		agentLines = append(agentLines, style.Render(prefix+a.Name()))
	}
	formParts = append(formParts,
		label("Agent:    [↑↓ to select]", m.focusedInput == fieldAgent),
		strings.Join(agentLines, "\n"),
		"",
	)

	// Name
	formParts = append(formParts,
		label("Name:", m.focusedInput == fieldName),
		m.nameInput.View(),
		"",
	)

	// Machine selector
	machineLines := make([]string, 0, len(m.machines))
	for i, mach := range m.machines {
		prefix := "  "
		style := dimStyle()
		if i == m.selectedMachine {
			prefix = "▸ "
			if m.focusedInput == fieldMachine {
				style = lipgloss.NewStyle().Foreground(whiteColor).Bold(true)
			} else {
				style = lipgloss.NewStyle().Foreground(whiteColor)
			}
		}
		machLine := mach.Name
		if mach.HostName != "" && mach.HostName != "localhost" {
			machLine += "  " + dimStyle().Render(mach.HostName)
		}
		machineLines = append(machineLines, style.Render(prefix+machLine))
	}
	formParts = append(formParts,
		label("Machine:  [↑↓ to select]", m.focusedInput == fieldMachine),
		strings.Join(machineLines, "\n"),
		"",
	)

	// Path — annotated when the picked agent doesn't require one.
	pathLabel := "Path:"
	if !pathRequired {
		pathLabel = "Path:     (optional for this app)"
	}
	suggList := ""
	if m.focusedInput == fieldPath && len(m.pathSuggestions) > 0 {
		var lines []string
		maxShow := 8
		if len(m.pathSuggestions) < maxShow {
			maxShow = len(m.pathSuggestions)
		}
		for i := 0; i < maxShow; i++ {
			prefix := "  "
			if i == m.selectedSugg {
				prefix = "▸ "
			}
			lines = append(lines, prefix+m.pathSuggestions[i])
		}
		if len(m.pathSuggestions) > maxShow {
			lines = append(lines, fmt.Sprintf("  ... %d more", len(m.pathSuggestions)-maxShow))
		}
		suggList = "\n" + dimStyle().Render(strings.Join(lines, "\n"))
	}
	formParts = append(formParts,
		label(pathLabel, m.focusedInput == fieldPath),
		m.pathInput.View()+suggList,
		"",
	)

	formParts = append(formParts, dimStyle().Render("[enter] create/select  [tab] next  [↑↓] navigate  [esc] cancel"))

	form := promptStyle.Render(lipgloss.JoinVertical(lipgloss.Left, formParts...))
	centered := lipgloss.Place(m.width, m.height-2, lipgloss.Center, lipgloss.Center, form)

	footer := footerStyle.Width(m.width).Render(
		fmt.Sprintf(" %s create  %s next field  %s cancel",
			footerKeyStyle.Render("[enter]"),
			footerKeyStyle.Render("[tab]"),
			footerKeyStyle.Render("[esc]"),
		),
	)

	return lipgloss.JoinVertical(lipgloss.Left, title, centered, footer)
}

func (m Model) viewConfirmKill() string {
	if m.killTarget == "" {
		m.mode = ModeGrid
		return m.viewGrid()
	}

	name := m.killTarget
	title := headerStyle.Width(m.width).Render(" pixel-fleet   Confirm Kill")

	msg := promptStyle.Render(
		fmt.Sprintf("Kill session %s?\n\n[y] yes  [n] no", lipgloss.NewStyle().Bold(true).Render(name)),
	)

	centered := lipgloss.Place(m.width, m.height-2, lipgloss.Center, lipgloss.Center, msg)

	return lipgloss.JoinVertical(lipgloss.Left, title, centered)
}

func (m Model) viewHelp() string {
	title := headerStyle.Width(m.width).Render(" pixel-fleet   Help")

	help := promptStyle.Width(60).Render(lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Render("Keyboard Shortcuts"),
		"",
		"  ↑↓←→ / hjkl    Navigate grid",
		"  enter           Focus into selected session",
		"  n               New session (claude/codex/term)",
		"  N (shift+n)     New system app (skills/app viewer)",
		"  x               Kill selected session",
		"  a               Archive selected (hide; nothing stops)",
		"  A               Open archive view",
		"  b               Start a coding blocker (or break one)",
		"  s               Scan for orphaned sessions",
		"  r               Refresh sessions",
		"  q               Detach (everything keeps running)",
		"  ?               Toggle this help",
		"",
		lipgloss.NewStyle().Bold(true).Render("From a focused session:"),
		"",
		"  F1              Return to dashboard",
		"  ctrl+b q        Return to dashboard",
		"",
		lipgloss.NewStyle().Bold(true).Render("CLI Usage"),
		"",
		"  cs                                Open dashboard",
		"  cs claude <name> <machine> <path> New Claude session",
		"  cs codex  <name> <machine> <path> New Codex session",
		"  cs term   <name> <machine> <path> New plain terminal session",
		"  cs ls                             List sessions",
		"  cs scan                           Find orphaned sessions",
		"  cs kill <name>                    Kill a session",
		"  cs kill-all                       Kill all sessions",
		"",
		lipgloss.NewStyle().Bold(true).Render("Coding blocker"),
		"",
		"  Press b, pick a duration. The gallery stays; going",
		"  into a session does not. Sessions keep running the",
		"  whole time — the blocker only stops you watching.",
		"  It survives a restart. Press b again and type",
		"  'break' to end it early.",
		"",
		dimStyle().Render("Press any key to close"),
	))

	centered := lipgloss.Place(m.width, m.height-2, lipgloss.Center, lipgloss.Center, help)

	return lipgloss.JoinVertical(lipgloss.Left, title, centered)
}

func (m Model) viewArchive() string {
	header := fmt.Sprintf(" pixel-fleet   Archive   %d session(s)", len(m.archived))
	title := headerStyle.Width(m.width).Render(header)

	var body string
	if len(m.archived) == 0 {
		body = emptyStyle.Width(m.width).Height(m.height - 3).Render(
			"\n\n  Nothing archived.\n\n  Press [esc] to go back.\n  In the grid, press [a] to archive a session.",
		)
	} else {
		var lines []string
		for i, s := range m.archived {
			prefix := "  "
			if i == m.selectedArchived {
				prefix = "▸ "
			}
			agentTag := agentBadge(s.Agent)
			machine := s.Machine
			if machine == "" {
				machine = "?"
			}
			path := s.Path
			if len(path) > 60 {
				path = "..." + path[len(path)-57:]
			}
			line := fmt.Sprintf("%s%-22s  %s  %-12s  %s",
				prefix, s.Name, agentTag, machine, path)
			if i == m.selectedArchived {
				line = lipgloss.NewStyle().Foreground(whiteColor).Bold(true).Render(line)
			} else {
				line = lipgloss.NewStyle().Foreground(dimColor).Render(line)
			}
			lines = append(lines, line)
		}
		body = strings.Join(lines, "\n")
		body = lipgloss.NewStyle().Padding(2, 4).Render(body)
	}

	footer := footerStyle.Width(m.width).Render(
		fmt.Sprintf(" %s unarchive  %s navigate  %s back",
			footerKeyStyle.Render("[u/enter]"),
			footerKeyStyle.Render("[↑↓/jk]"),
			footerKeyStyle.Render("[esc]"),
		),
	)

	gap := lipgloss.NewStyle().Height(m.height - lipgloss.Height(title) - lipgloss.Height(body) - lipgloss.Height(footer)).Render("")
	return lipgloss.JoinVertical(lipgloss.Left, title, body, gap, footer)
}

func (m Model) viewScan() string {
	title := headerStyle.Width(m.width).Render(" pixel-fleet   All Sessions")

	if m.scanning {
		body := promptStyle.Render("Fetching sessions from all machines...")
		centered := lipgloss.Place(m.width, m.height-2, lipgloss.Center, lipgloss.Center, body)
		return lipgloss.JoinVertical(lipgloss.Left, title, centered)
	}

	if len(m.discovered) == 0 {
		body := promptStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
			"No active sessions found on any machine.",
			"",
			dimStyle().Render("[esc] back"),
		))
		centered := lipgloss.Place(m.width, m.height-2, lipgloss.Center, lipgloss.Center, body)
		return lipgloss.JoinVertical(lipgloss.Left, title, centered)
	}

	// Count tracked vs orphaned
	tracked, orphaned := 0, 0
	for _, d := range m.discovered {
		if d.Tracked {
			tracked++
		} else {
			orphaned++
		}
	}

	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("%d session(s) across all machines  (%d tracked, %d orphaned)", len(m.discovered), tracked, orphaned),
	))
	lines = append(lines, "")

	maxShow := 15
	if len(m.discovered) < maxShow {
		maxShow = len(m.discovered)
	}
	for i := 0; i < maxShow; i++ {
		d := m.discovered[i]
		prefix := "  "
		style := dimStyle()
		if i == m.selectedDisc {
			prefix = "▸ "
			style = lipgloss.NewStyle().Foreground(whiteColor)
		}

		name := d.DisplayName
		if name == "" {
			name = d.WindowName
		}
		status := "orphaned"
		if d.Tracked {
			status = "tracked"
		}

		path := d.Path
		if len(path) > 25 {
			path = "..." + path[len(path)-22:]
		}

		label := fmt.Sprintf("%-12s %-16s %-25s  %s", d.Machine, name, path, status)
		lines = append(lines, style.Render(prefix+label))
	}
	if len(m.discovered) > maxShow {
		lines = append(lines, dimStyle().Render(fmt.Sprintf("  ... %d more", len(m.discovered)-maxShow)))
	}

	if m.mode == ModeScanAdopt {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Bold(true).Render("Name for adopted session:"))
		lines = append(lines, m.adoptInput.View())
	}

	lines = append(lines, "")
	if orphaned > 0 {
		lines = append(lines, dimStyle().Render("[enter] adopt orphaned  [↑↓] navigate  [esc] back"))
	} else {
		lines = append(lines, dimStyle().Render("[↑↓] navigate  [esc] back"))
	}

	body := promptStyle.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
	centered := lipgloss.Place(m.width, m.height-2, lipgloss.Center, lipgloss.Center, body)

	return lipgloss.JoinVertical(lipgloss.Left, title, centered)
}

func (m Model) runScan() tea.Msg {
	discovered, err := m.manager.FetchAll()
	if err != nil {
		log.Printf("FetchAll error: %v", err)
		return scanMsg(nil)
	}
	return scanMsg(discovered)
}

func (m Model) refreshSessions() tea.Msg {
	sessions, err := m.manager.List()
	if err != nil {
		return errMsg(err)
	}

	cellHeight := 15
	if m.height > 0 {
		_, rows := gridLayout(len(sessions), m.width, m.height)
		cellHeight = (m.height - 3) / max(rows, 1)
	}

	sessions = m.manager.CaptureAll(sessions, cellHeight)
	return sessionsMsg(sessions)
}

// refreshArchived fetches the current archive list. Cheap (just reads the
// JSON store + tmux window list); we don't capture pane content for
// archived sessions since they aren't shown as live cells.
func (m Model) refreshArchived() tea.Msg {
	archived, err := m.manager.ListArchived()
	if err != nil {
		return errMsg(err)
	}
	return archivedMsg(archived)
}

// refreshLocalWindows fetches the names of every cs tmux window
// (active + archived). Drives the duplicate-prevention check in
// auto-restore.
func (m Model) refreshLocalWindows() tea.Msg {
	names, err := m.manager.AllWindowNames()
	if err != nil {
		return errMsg(err)
	}
	return localWindowsMsg(names)
}

func (m Model) searchRepos() tea.Msg {
	query := strings.TrimSpace(m.pathInput.Value())
	machine := ""
	if m.selectedMachine < len(m.machines) {
		machine = m.machines[m.selectedMachine].Name
	}

	if machine == "" || machine == "home" {
		repos := session.FindRepos(m.manager.LocalRepoPaths(), query)
		return repoSearchMsg(repos)
	}

	// Remote search
	remotePath := m.manager.Config().RemoteBaseFor(machine)
	repos := session.FindRemoteRepos(machine, remotePath, query)
	return repoSearchMsg(repos)
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(m.refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// discoveryTickCmd schedules the next background orphan-scan tick.
// Returns nil when auto-discovery is disabled (interval <= 0).
func (m Model) discoveryTickCmd() tea.Cmd {
	if m.discoveryInterval <= 0 {
		return nil
	}
	return tea.Tick(m.discoveryInterval, func(t time.Time) tea.Msg {
		return discoveryTickMsg(t)
	})
}

// runDiscovery scans known machines for cs sessions across all machines.
// Returns:
//   - count of true orphans (alive remote, no store record) for the badge
//   - list of tracked-alive sessions that need a local viewer spawned
//
// Runs on a goroutine via tea.Cmd. SSH timeouts are bounded by
// cfg.ScanTimeout (parallel across machines).
func (m Model) runDiscovery() tea.Msg {
	all, err := m.manager.FetchAll()
	if err != nil {
		log.Printf("background discovery: %v", err)
		return discoveryResultMsg{err: err, at: time.Now()}
	}
	result := discoveryResultMsg{at: time.Now()}
	for _, d := range all {
		if !d.Tracked {
			result.orphanedCount++
			continue
		}
		// Tracked-alive: build a restore task. Per-session model attaches
		// to the whole tmux session; legacy/adopted attaches to a window.
		// Archived sessions are included — auto-restore should rebuild a
		// missing local viewer regardless of archive state. The duplicate
		// prevention happens in the Update handler via localWindowNames
		// (which sees the actual tmux state, not the filtered grid).
		t := restoreTask{
			Name:        d.DisplayName,
			Machine:     d.Machine,
			TmuxSession: d.TmuxSession,
		}
		isPerSession := strings.HasPrefix(d.TmuxSession, "cs-") && d.TmuxSession != "cs-remote"
		if !isPerSession {
			t.WindowName = d.WindowName
		}
		result.trackedAlive = append(result.trackedAlive, t)
	}
	return result
}

// runAutoRestore spawns local viewer windows for the given restore tasks.
// Each spawn calls tmux new-window with a reattach command (which is
// already wrapped in the auto-reconnect loop by BuildReattachCommand).
// Failures are logged but don't abort the batch.
func (m Model) runAutoRestore(tasks []restoreTask) tea.Cmd {
	tmuxSession := m.tmuxSessionName
	return func() tea.Msg {
		spawned := 0
		for _, t := range tasks {
			cmd := session.BuildReattachCommand(t.Machine, t.TmuxSession, t.WindowName)
			if err := tmux.NewWindow(tmuxSession, t.Name, cmd); err != nil {
				log.Printf("auto-restore %s: %v", t.Name, err)
				continue
			}
			log.Printf("auto-restored %s -> %s:%s", t.Name, t.Machine, t.TmuxSession)
			spawned++
		}
		return restoredMsg(spawned)
	}
}

// runKill removes a session on a goroutine via tea.Cmd. Kill performs a
// remote tmux kill over SSH (bounded by ConnectTimeout in runRemoteTmux);
// running it inline in Update would block the entire event loop until the
// SSH returns — for an unreachable host that meant a multi-second-to-minutes
// frozen dashboard. The error is logged inside Kill's callees; we only need
// the completion signal to trigger a refresh.
func (m Model) runKill(name string) tea.Cmd {
	mgr := m.manager
	return func() tea.Msg {
		if err := mgr.Kill(name); err != nil {
			log.Printf("kill %s: %v", name, err)
		}
		return killedMsg(name)
	}
}

func dimStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(dimColor)
}

// Run starts the TUI. It blocks until ctrl+c.
func Run(mgr *session.Manager, tmuxSessionName string, refreshInterval, discoveryInterval time.Duration) error {
	log.Printf("Starting TUI (session=%s, refresh=%s, discovery=%s)", tmuxSessionName, refreshInterval, discoveryInterval)
	m := NewModel(mgr, tmuxSessionName, refreshInterval, discoveryInterval)
	p := tea.NewProgram(m, tea.WithAltScreen())

	_, err := p.Run()
	if err != nil {
		log.Printf("TUI exited with error: %v", err)
	} else {
		log.Printf("TUI exited cleanly")
	}
	return err
}
