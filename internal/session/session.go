package session

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stepanfeduniak/pixel-fleet/internal/apps"
)

// sshOpts are the SSH options used for all remote sessions.
const sshOpts = "-t -o ServerAliveInterval=15 -o ServerAliveCountMax=3"

// shellQuote single-quotes s for safe inclusion in a shell command.
// Embedded single quotes are escaped as '\''.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Status represents the state of a Claude Code session.
type Status int

const (
	StatusUnknown Status = iota
	StatusWorking
	StatusWaitingInput
	StatusIdle
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusWorking:
		return "working"
	case StatusWaitingInput:
		return "waiting for input"
	case StatusIdle:
		return "idle"
	case StatusError:
		return "error"
	default:
		return "unknown"
	}
}

func (s Status) Symbol() string {
	switch s {
	case StatusWorking:
		return "●"
	case StatusWaitingInput:
		return "◆"
	case StatusIdle:
		return "●"
	case StatusError:
		return "✗"
	default:
		return "○"
	}
}

// Session represents an agent session in a tmux window.
type Session struct {
	Name        string // user-given name (used as tmux window name)
	Machine     string // "home" or SSH host
	Path        string // repo path on the target machine
	Agent       string // "claude" (default) or "codex"; restored from store
	Archived    bool   // user has stashed this session out of the grid
	WindowIndex int    // tmux window index
	Status      Status
	LastOutput  string // last captured pane content
}

// BuildOpts collects the parameters for assembling a session-launch command.
//
// Agent selects which CLI to launch ("claude" or "codex"). When empty it
// defaults to "claude" for back-compat with callers that predate the
// agent-choice feature. The LocalClaudeBin / RemoteClaudeBin field names
// are historical — they hold whichever agent binary the manager picked
// (claude or codex). RemoteControl is ignored when Agent == "codex"
// because codex has no claude.ai/code bridge.
type BuildOpts struct {
	Machine         string // "home" or SSH host alias
	Path            string // working directory on the target machine
	Agent           string // "claude" (default) or "codex"
	LocalClaudeBin  string // agent binary path for "home"
	RemoteClaudeBin string // agent binary path on remote
	Setup           string // optional shell setup script (runs before agent)
	WindowName      string // local cs tmux window name (display)
	// RemoteSessionName is the unique tmux SESSION name on the remote
	// machine. Each cs session gets its own dedicated tmux session
	// containing exactly one window — there is no shared cs-remote.
	// This keeps each cs session fully isolated: no shared status bar,
	// no Ctrl-b N to switch between unrelated sessions, no other cs
	// sessions visible from inside this one.
	RemoteSessionName string
	RemoteControl     bool // launch claude with --remote-control (claude only; ignored for codex)
}

// BuildCommand is a thin wrapper preserving the legacy positional API for tests
// or callers that don't need the new persistence features. Prefer BuildShellCommand.
func BuildCommand(machine, path, claudeBin, remoteClaudeBin string) string {
	return BuildShellCommand(BuildOpts{
		Machine:         machine,
		Path:            path,
		LocalClaudeBin:  claudeBin,
		RemoteClaudeBin: remoteClaudeBin,
		WindowName:      "session",
		RemoteControl:   true,
	})
}

// BuildCommandWithSetup is the legacy positional API including a setup script.
// Prefer BuildShellCommand.
func BuildCommandWithSetup(machine, path, claudeBin, remoteClaudeBin, setup string) string {
	return BuildShellCommand(BuildOpts{
		Machine:         machine,
		Path:            path,
		LocalClaudeBin:  claudeBin,
		RemoteClaudeBin: remoteClaudeBin,
		Setup:           setup,
		WindowName:      "session",
		RemoteControl:   true,
	})
}

// BuildShellCommand returns the shell command string to be executed by the
// LOCAL tmux window so that a Claude Code session is launched.
//
// For machine == "home" the command runs Claude directly in the local window
// (the local cs tmux session already provides persistence).
//
// For remote machines the command SSHes to the remote, creates (or reattaches
// to) a dedicated per-session tmux session named RemoteSessionName, runs the
// user's setup + Claude inside its single window, and attaches the local
// viewer. Each cs session lives in its own remote tmux session — there is
// no shared cs-remote — so cs sessions on the same machine are fully
// isolated from each other. The Claude process survives SSH drops, laptop
// sleep, and viewer disconnects courtesy of tmux + systemd linger.
func BuildShellCommand(opts BuildOpts) string {
	if opts.Machine == "home" {
		return buildLocalCommand(opts)
	}
	return buildRemoteCommand(opts)
}

// resolveApp looks up the app for opts and falls back to the registry's
// default. Returns nil only if the registry is empty (which would mean
// internal/apps/builtin wasn't imported — a build configuration error).
func resolveApp(opts BuildOpts) apps.App {
	if a, ok := apps.Resolve(opts.Agent); ok {
		return a
	}
	return apps.Default()
}

// launchCtxFor builds the LaunchCtx an app sees, picking the right bin
// (local vs remote) for the target machine.
func launchCtxFor(opts BuildOpts) apps.LaunchCtx {
	bin := opts.RemoteClaudeBin
	if opts.Machine == "home" {
		bin = opts.LocalClaudeBin
	}
	return apps.LaunchCtx{
		Path:       opts.Path,
		Setup:      opts.Setup,
		Bin:        bin,
		Machine:    opts.Machine,
		WindowName: opts.WindowName,
	}
}

func buildLocalCommand(opts BuildOpts) string {
	app := resolveApp(opts)
	exec := app.LaunchExec(launchCtxFor(opts))
	// Apps that don't require a path (in-process viewers) skip the cd.
	if !app.RequiresPath() || opts.Path == "" {
		if opts.Setup == "" {
			return fmt.Sprintf("exec %s", exec)
		}
		return fmt.Sprintf("bash -c 'set -e; %s; exec %s'", opts.Setup, exec)
	}
	if opts.Setup == "" {
		return fmt.Sprintf("cd %s && exec %s", opts.Path, exec)
	}
	return fmt.Sprintf("bash -c 'set -e; %s; exec %s'", opts.Setup, exec)
}

// buildRemoteCommand assembles the SSH+remote-tmux launch command using the
// per-session model: each cs session lives in its own dedicated tmux session
// on the remote (no shared cs-remote, no neighboring windows). The remote
// script is base64-encoded so that arbitrarily quoted user setup scripts can
// be embedded without shell-quoting hazards.
func buildRemoteCommand(opts BuildOpts) string {
	remoteSession := opts.RemoteSessionName
	if remoteSession == "" {
		// Fallback for callers that didn't supply one. Real callers (the
		// manager) always set this to a unique cs-<sanitized>-<8hex> form.
		remoteSession = "cs-" + opts.WindowName
	}

	app := resolveApp(opts)
	ctx := launchCtxFor(opts)
	if ctx.Bin == "" && app.NeedsBin() {
		ctx.Bin = app.DefaultRemoteBin()
	}
	launchExec := app.LaunchExec(ctx)

	// The launch script runs INSIDE the remote tmux session's only window.
	// For apps that need a binary on PATH, we prepend ~/.local/bin (where
	// `claude install` lands) and source nvm.sh — non-interactive SSH
	// shells skip ~/.profile and ~/.bashrc, so without these the bootstrap
	// finds either a stale system-wide binary or none at all.
	//
	// Apps with NeedsBin() == false (the terminal app) skip both: terminal
	// execs a login shell which sources the user's startup files itself.
	pathPrepend := `export PATH="$HOME/.local/bin:$PATH"
[ -s "$HOME/.nvm/nvm.sh" ] && \. "$HOME/.nvm/nvm.sh" >/dev/null 2>&1 || true`
	// Pre-check: if the agent binary isn't on PATH, print a helpful error
	// and pause so the user actually sees it. Otherwise the launch fails so
	// fast that remain-on-exit doesn't latch and tmux just reports
	// "can't find session" with no useful detail.
	notFoundGuard := fmt.Sprintf(`if ! command -v %s >/dev/null 2>&1; then
  echo
  echo "[cs] %s not found in PATH on this machine."
  echo "PATH:"
  echo "$PATH" | tr ':' '\n' | sed 's/^/  /'
  echo
  echo "Hint: install or auth the agent (e.g. 'codex login'), then retry."
  echo "[cs] Press Enter to close..."
  IFS= read -r _ || true
  exit 1
fi`, launchExec, launchExec)
	var launchScript string
	switch {
	case !app.NeedsBin():
		// No binary to verify — just cd (or run setup) and exec. Apps
		// that don't require a path (viewers) skip the cd entirely.
		switch {
		case !app.RequiresPath() || opts.Path == "":
			if opts.Setup == "" {
				launchScript = fmt.Sprintf("exec %s\n", launchExec)
			} else {
				launchScript = fmt.Sprintf("set -e\n%s\nexec %s\n",
					opts.Setup, launchExec)
			}
		case opts.Setup == "":
			launchScript = fmt.Sprintf("cd %s\nexec %s\n",
				shellQuote(opts.Path), launchExec)
		default:
			launchScript = fmt.Sprintf("set -e\n%s\nexec %s\n",
				opts.Setup, launchExec)
		}
	case opts.Setup == "":
		launchScript = fmt.Sprintf("set -e\n%s\ncd %s\n%s\nexec %s\n",
			pathPrepend, shellQuote(opts.Path), notFoundGuard, launchExec)
	default:
		launchScript = fmt.Sprintf("set -e\n%s\n%s\n%s\nexec %s\n",
			pathPrepend, opts.Setup, notFoundGuard, launchExec)
	}
	launchB64 := base64.StdEncoding.EncodeToString([]byte(launchScript))

	// After creating the new tmux session, fire a detached background poller
	// (still on the remote machine) that watches the session's pane for the
	// Claude prompt and sends `/remote-control` to it. setsid + nohup
	// detaches it from the SSH session so it survives the cs CLI exiting.
	// Self-terminates after ~60 attempts. Only injected for apps that
	// declare SupportsRemoteControl.
	rcEnableSnippet := ""
	if opts.RemoteControl && app.SupportsRemoteControl() {
		rcEnableSnippet = `
setsid nohup bash -c '
    SESSION="$1"
    for i in $(seq 1 60); do
        sleep 0.5
        content=$(tmux capture-pane -t "$SESSION" -p 2>/dev/null || true)
        case "$content" in
            *"Remote Control"*|*"/remote-control is active"*)
                exit 0
                ;;
            *"❯"*)
                tmux send-keys -t "$SESSION" "/remote-control" Enter 2>/dev/null
                exit 0
                ;;
        esac
    done
' _ "$SESSION" </dev/null >/dev/null 2>&1 &
disown 2>/dev/null || true
`
	}

	// The bootstrap script runs in the remote user's shell. It ensures the
	// per-session tmux exists (creating it on first call, reusing it on
	// reattach), then attaches the SSH client to its single pane.
	//
	// Reattach is detected by `tmux has-session`. If the existing session's
	// pane is dead (claude exited but remain-on-exit kept the corpse around),
	// we kill the session and recreate it so the user can re-launch.
	bootstrap := fmt.Sprintf(`set -e
SESSION=%s
LAUNCH_FILE=$(mktemp -t cs-launch-XXXXXX.sh)
trap 'rm -f "$LAUNCH_FILE"' EXIT
echo %s | base64 -d > "$LAUNCH_FILE"
chmod +x "$LAUNCH_FILE"
SESSION_FRESH=0
if tmux has-session -t "$SESSION" 2>/dev/null; then
    DEAD=$(tmux list-panes -t "$SESSION" -F '#{pane_dead}' 2>/dev/null | head -1)
    if [ "$DEAD" = "1" ]; then
        tmux kill-session -t "$SESSION" 2>/dev/null || true
    fi
fi
if ! tmux has-session -t "$SESSION" 2>/dev/null; then
    tmux new-session -d -s "$SESSION" "bash $LAUNCH_FILE"
    SESSION_FRESH=1
fi
tmux set-option -t "$SESSION" history-limit 50000 >/dev/null 2>&1 || true
tmux set-option -t "$SESSION" mouse on >/dev/null 2>&1 || true
tmux set-option -t "$SESSION" status off >/dev/null 2>&1 || true
tmux set-window-option -t "$SESSION" remain-on-exit on >/dev/null 2>&1 || true
if [ "$SESSION_FRESH" = "1" ]; then
    sleep 0.5%s
fi
exec tmux attach -t "$SESSION"
`, shellQuote(remoteSession), launchB64, rcEnableSnippet)

	bootstrapB64 := base64.StdEncoding.EncodeToString([]byte(bootstrap))

	// Outer SSH command. The remote shell decodes the bootstrap and passes
	// it to `bash -c` as an argument (not via stdin). This matters because
	// the bootstrap ends with `exec tmux attach`, and tmux attach requires
	// the process stdin to be a TTY. If we piped the script into `bash`,
	// the bash process would inherit the pipe as stdin, so the exec'd
	// tmux would also get the pipe — and fail with "not a terminal".
	// Using bash -c "$(echo ... | base64 -d)" lets bash keep the SSH TTY
	// as its stdin.
	inner := fmt.Sprintf(`bash -c "$(echo %s | base64 -d)"`, bootstrapB64)
	sshCmd := fmt.Sprintf("ssh %s %s %s",
		sshOpts, opts.Machine, shellQuote(inner))
	return wrapWithReconnect(sshCmd)
}

// BuildReattachCommand builds a command to reattach to an existing tmux session.
// `tmuxSession` is the remote tmux session name; `window` may be empty for the
// new per-session model. If a window is provided we attach to that window
// (used by Adopt for orphaned cs-remote:WINDOW legacy entries).
func BuildReattachCommand(machine, tmuxSession, window string) string {
	target := tmuxSession
	if window != "" {
		target = tmuxSession + ":" + window
	}
	if machine == "home" {
		return fmt.Sprintf("tmux attach -t %s", target)
	}
	sshCmd := fmt.Sprintf("ssh %s %s 'tmux attach -t %s'", sshOpts, machine, target)
	return wrapWithReconnect(sshCmd)
}

// wrapWithReconnect wraps an ssh-based command in a small shell loop that
// reconnects on disconnect. The remote per-session tmux persists across SSH
// drops (linger keeps the user scope alive), so a reconnect is just another
// `tmux attach` to the same session — zero state loss.
//
// Loop semantics:
//   - ssh exits 0 (clean detach via Ctrl-b d): break, window closes normally.
//   - ssh exits non-zero (network drop, broken pipe, signal): print a banner
//     and reconnect after a short delay.
//   - 5 consecutive fast failures (each <3s) likely indicate auth/host-key
//     or remote-config errors that won't fix themselves; pause for Enter
//     before retrying so the loop doesn't spin.
//
// The returned snippet is a single shell program suitable for tmux
// new-window's command argument (tmux passes it to /bin/sh -c verbatim).
func wrapWithReconnect(sshCmd string) string {
	return fmt.Sprintf(`attempts=0
while :; do
    start=$(date +%%s)
    %s
    ec=$?
    if [ "$ec" = "0" ]; then
        break
    fi
    duration=$(( $(date +%%s) - start ))
    attempts=$(( attempts + 1 ))
    printf '\n\033[33m[cs] viewer disconnected (ssh exit %%d after %%ds). Reconnecting (attempt %%d)...\033[0m\n' "$ec" "$duration" "$attempts"
    if [ "$duration" -lt 3 ] && [ "$attempts" -ge 5 ]; then
        printf '\033[31m[cs] 5 fast failures — likely an auth/config error. Press Enter to retry, Ctrl-C to close.\033[0m\n'
        IFS= read -r _ || break
        attempts=0
    fi
    sleep 2
done
`, sshCmd)
}

// ExpandHome expands ~ to the user's home directory (for local paths).
func ExpandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	return path
}

// DetectStatus analyzes a captured pane to determine the session's status.
//
// Detection is per-agent: Claude Code, Codex, and a plain shell ("terminal")
// each have their own TUI vocabulary, and the previous detector was Claude-only
// — Codex sessions ended up mis-classified, and Claude itself had "esc to
// interrupt" tagged as a waiting-for-input signal when it's actually shown
// while Claude is actively generating. The patterns below are grounded in real
// pane snapshots; the test file lists the fixtures they were derived from.
//
// `agent` matches SessionRecord.Agent ("claude", "codex", "terminal", or "").
// Empty falls back to Claude for legacy records.
func DetectStatus(output, agent string) Status {
	if output == "" {
		return StatusUnknown
	}

	// Last 15 non-empty lines (the bottom of the pane carries the verdict).
	allLines := strings.Split(output, "\n")
	var lines []string
	for i := len(allLines) - 1; i >= 0 && len(lines) < 15; i-- {
		trimmed := strings.TrimSpace(allLines[i])
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	joined := strings.Join(lines, "\n")

	// SSH-level errors look the same regardless of agent — handle once up top.
	if hasSSHError(lines) {
		return StatusError
	}

	switch agent {
	case "codex":
		return detectCodex(joined, lines)
	case "terminal":
		// Plain shell sessions: there is no LLM concept of "working" or
		// "waiting for input" to detect. The previous detector accidentally
		// matched on log/output keywords and produced misleading icons.
		return StatusIdle
	default:
		// "" (legacy, pre-multi-agent) and "claude" both use Claude detection.
		return detectClaude(joined, lines)
	}
}

func hasSSHError(lines []string) bool {
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "connection refused") ||
			strings.Contains(lower, "connection closed") ||
			strings.Contains(lower, "connection timed out") ||
			strings.Contains(lower, "no route to host") ||
			strings.Contains(lower, "host key verification failed") {
			return true
		}
	}
	return false
}

// detectClaude classifies a Claude Code pane.
//
// Grounded in live captures of v2.1.x:
//   - Working: footer shows "esc to interrupt"; activity line of the form
//     "✢ Pouncing… (Xs · ↓ Nk tokens)" (verb + "…" + tokens); or a braille
//     spinner frame from the agent's own animation.
//   - Permission gate: the explicit approval modal ("Do you want to proceed?"
//     / "Esc to cancel") → Waiting.
//   - Otherwise the pane is between turns. The input prompt ("❯") is always
//     visible whenever Claude isn't generating, so its presence cannot tell
//     Idle from Waiting on its own. We walk the conversation bottom-up
//     (skipping chrome — input box, separators, footer hints, completion
//     summaries like "✻ Sautéed for 6m 11s") and check whether the assistant's
//     last few sentences end with a question. If yes → Waiting (there's a real
//     QnA the user needs to answer). If no → Idle (Claude finished and is just
//     sitting there).
func detectClaude(joined string, lines []string) Status {
	if strings.Contains(joined, "Do you want to proceed?") ||
		strings.Contains(joined, "Do you want to continue?") ||
		strings.Contains(joined, "Esc to cancel") {
		return StatusWaitingInput
	}

	if strings.Contains(joined, "esc to interrupt") ||
		containsAny(joined, brailleSpinners) ||
		hasClaudeActivityLine(lines) {
		return StatusWorking
	}

	return idleOrWaitingByQuestion(lines, isClaudeChrome)
}

// detectCodex classifies an OpenAI Codex CLI pane.
//
// Grounded in live captures of the gpt-5.5-era TUI:
//   - Working: present-tense progress verb with an ellipsis ("Thinking…",
//     "Working…", "Generating…", "Reasoning…"), or an explicit "esc to
//     interrupt" hint in the Codex chrome (Codex shows
//     "• Working (Xs • esc to interrupt)" mid-turn).
//   - Permission gate: tool approval prompts like "Allow?", "Approve?",
//     "Apply this patch?" → Waiting.
//   - Otherwise between turns. Codex's "› " input prompt is always visible
//     when idle, so it can't distinguish Idle from Waiting. Walk the
//     conversation bottom-up (skipping the "› " input line, the
//     "  gpt-X.X default fast · <cwd>" model footer, the "─ Worked for X ─"
//     turn separator, and any decorative separators) and check whether the
//     assistant's last few lines end with a question. Yes → Waiting; no →
//     Idle.
//
// Conservative on Working: a single ellipsis in tool output (e.g. "connecting…")
// shouldn't flip the status, so we require a known verb prefix.
func detectCodex(joined string, lines []string) Status {
	if strings.Contains(joined, "Apply this patch?") ||
		strings.Contains(joined, "Approve?") ||
		strings.Contains(joined, "Allow?") {
		return StatusWaitingInput
	}

	if strings.Contains(joined, "esc to interrupt") ||
		containsAny(joined, codexWorkingVerbs) {
		return StatusWorking
	}

	return idleOrWaitingByQuestion(lines, isCodexChrome)
}

// idleOrWaitingByQuestion decides between Idle and WaitingInput for a between-
// turns pane. It walks bottom-up skipping chrome, and returns WaitingInput as
// soon as any of the last `lastConversationLookback` real conversation lines
// ends with question-like punctuation. Otherwise it returns Idle.
//
// We look at several lines, not just the bottom one, because the assistant
// often appends a short trailing remark or shows a numbered options list AFTER
// the question — e.g. "Do you want me to restart now ... ?" followed by
// "✻ Brewed for ..." and a feedback survey. The actual question may not be on
// the very bottom non-chrome line.
func idleOrWaitingByQuestion(lines []string, isChrome func(string) bool) Status {
	seen := 0
	for _, line := range lines {
		if isChrome(line) {
			continue
		}
		seen++
		if endsLikeQuestion(line) {
			return StatusWaitingInput
		}
		if seen >= lastConversationLookback {
			break
		}
	}
	return StatusIdle
}

// lastConversationLookback is how many non-chrome lines back we scan when
// deciding Idle vs WaitingInput. Five comfortably covers a question followed
// by a few trailing details, lists, or summaries.
const lastConversationLookback = 5

// endsLikeQuestion reports whether a line ends with question-like punctuation.
// Accepts a trailing closing bracket or quote after the "?" (so lines like
// "...intact?)" or `Is it ok?"` still count) but does not search anywhere
// other than the line's tail — a "?" buried in the middle of a sentence
// followed by more text is NOT a pending question.
func endsLikeQuestion(line string) bool {
	s := strings.TrimRight(strings.TrimSpace(line), " \t")
	return strings.HasSuffix(s, "?") ||
		strings.HasSuffix(s, "?)") ||
		strings.HasSuffix(s, "?]") ||
		strings.HasSuffix(s, "?\"") ||
		strings.HasSuffix(s, "?'") ||
		strings.HasSuffix(s, "?»") ||
		strings.HasSuffix(s, "?”")
}

// isClaudeChrome reports whether a line is part of Claude Code's TUI chrome
// (input box, separators, footer hints, completion summaries) rather than
// conversation content. False positives here can only undercount conversation
// lines, which biases toward Idle — that's the safer direction for ambiguous
// chrome.
func isClaudeChrome(line string) bool {
	if isSeparatorLine(line) {
		return true
	}
	// Input box: "❯", "❯ user-typed-draft", etc.
	if line == "❯" || strings.HasPrefix(line, "❯ ") || strings.HasPrefix(line, "❯\t") {
		return true
	}
	// Footer hints. "⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt"
	// and variants.
	if strings.HasPrefix(line, "⏵⏵ ") {
		return true
	}
	if strings.Contains(line, "? for shortcuts") ||
		strings.Contains(line, "← for agents") ||
		strings.Contains(line, "shift+tab to cycle") {
		return true
	}
	// Post-completion status summaries: "✻ Sautéed for 6m 11s",
	// "✻ Brewed for 33s", "✻ Cogitated for 22m 3s". A "✻ Verb for Xs" line
	// is structurally chrome, not assistant content.
	if strings.HasPrefix(line, "✻ ") && strings.Contains(line, " for ") {
		return true
	}
	return false
}

// isCodexChrome reports whether a line is part of Codex CLI chrome.
func isCodexChrome(line string) bool {
	if isSeparatorLine(line) {
		return true
	}
	if line == "›" || strings.HasPrefix(line, "› ") || strings.HasPrefix(line, "›\t") {
		return true
	}
	// "  gpt-5.5 default fast · ~/path" model+cwd footer (sometimes indented).
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "gpt-") {
		return true
	}
	// "─ Worked for 3m 15s ──────────" turn-completion separator.
	if strings.Contains(line, "Worked for ") && strings.ContainsRune(line, '─') {
		return true
	}
	return false
}

// isSeparatorLine reports whether a line consists entirely of horizontal
// separator characters and whitespace. Used to skip decorative rules between
// chrome blocks.
func isSeparatorLine(line string) bool {
	if line == "" {
		return true
	}
	for _, r := range line {
		switch r {
		case '─', '━', '═', '-', ' ', '\t':
			continue
		default:
			return false
		}
	}
	return true
}

// brailleSpinners are the Unicode Braille characters used by Claude Code's spinner.
var brailleSpinners = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// codexWorkingVerbs are the progress verbs the Codex CLI shows mid-turn.
// Each ends in "…" so the match doesn't fire on completed-step lines like
// "• Implemented locally." that have no ellipsis.
var codexWorkingVerbs = []string{
	"Thinking…", "Working…", "Generating…", "Reasoning…",
	"Drafting…", "Reviewing…", "Editing…", "Planning…",
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// hasClaudeActivityLine detects Claude Code's mid-generation status line:
//
//	"· Pouncing… (3m 29s · ↓ 6.3k tokens · thought for 8s)"
//	"✢ Pouncing… (3m 41s · ↓ 6.4k tokens)"
//	"* Drizzling… (19s · ↓ 778 tokens)"
//
// The "…" + tokens combo only appears while the model is actively streaming;
// post-completion summaries ("✻ Sautéed for 6m 11s") have neither, so they
// don't false-positive.
func hasClaudeActivityLine(lines []string) bool {
	for _, line := range lines {
		if strings.Contains(line, "…") &&
			(strings.Contains(line, "· ↓") || strings.Contains(line, "tokens")) {
			return true
		}
	}
	return false
}
