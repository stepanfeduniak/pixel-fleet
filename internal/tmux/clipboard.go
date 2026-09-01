package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ClipboardEnabled gates the clipboard and URL key bindings cs installs on the
// local tmux server. It is set once from config before any session is created
// and defaults to on, so the feature works without a config file.
var ClipboardEnabled = true

// Copying out of a cs session has to cross two boundaries that plain tmux
// does not handle on its own:
//
//  1. cs turns tmux mouse mode on, which means tmux — not the terminal
//     emulator — receives every mouse event. The terminal's own "drag to
//     select, Cmd-C to copy" never happens, so a selection lands in a tmux
//     paste buffer that the OS clipboard knows nothing about. The bindings
//     below pipe every copy through the OS clipboard tool instead.
//
//  2. A remote session is a nested tmux: the local viewer pane runs ssh,
//     which runs a second tmux on the remote host. Because the inner tmux
//     enables mouse reporting, the outer tmux forwards mouse events straight
//     through to it (#{mouse_any_flag} is 1), so the local bindings never
//     fire and the selection is made by the *remote* tmux — on a machine
//     with no access to this laptop's clipboard.
//
//     The remote side (see buildRemoteCommand) is configured to emit the
//     selection as an OSC 52 clipboard escape sequence. It travels back over
//     the ssh TTY into the local viewer pane, where `set-clipboard on` makes
//     the local tmux accept it into a buffer and fire the pane-set-clipboard
//     hook. That hook is what finally pushes the text to the OS clipboard.

// clipboardCopyCmd returns a shell command that reads stdin and puts it on the
// OS clipboard, or "" when no supported tool is present. On Linux the Wayland
// tool is preferred, then X11 ones, matching what a desktop session is most
// likely to have.
func clipboardCopyCmd() string {
	switch runtime.GOOS {
	case "darwin":
		return "pbcopy"
	case "linux":
		if os.Getenv("WAYLAND_DISPLAY") != "" && hasBin("wl-copy") {
			return "wl-copy"
		}
		if hasBin("xclip") {
			return "xclip -selection clipboard"
		}
		if hasBin("xsel") {
			return "xsel --clipboard --input"
		}
		if hasBin("wl-copy") {
			return "wl-copy"
		}
	}
	return ""
}

// clipboardPasteCmd is the inverse of clipboardCopyCmd: it writes the OS
// clipboard to stdout.
func clipboardPasteCmd() string {
	switch runtime.GOOS {
	case "darwin":
		return "pbpaste"
	case "linux":
		if os.Getenv("WAYLAND_DISPLAY") != "" && hasBin("wl-paste") {
			return "wl-paste --no-newline"
		}
		if hasBin("xclip") {
			return "xclip -selection clipboard -o"
		}
		if hasBin("xsel") {
			return "xsel --clipboard --output"
		}
		if hasBin("wl-paste") {
			return "wl-paste --no-newline"
		}
	}
	return ""
}

// openerBin returns the command that opens a URL in the user's browser.
func openerBin() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

func hasBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// shQuote single-quotes s for safe inclusion in a shell command.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// tmuxEscape escapes the one character tmux treats specially inside the
// command strings we hand to bind-key and set-hook: '#' introduces a format
// like #{pane_id}, and '##' is its literal form.
func tmuxEscape(s string) string {
	return strings.ReplaceAll(s, "#", "##")
}

// tmuxQuote wraps s in tmux double quotes for use inside a command string that
// tmux itself will parse (a display-menu entry, say). Within double quotes
// tmux processes backslash escapes and #{...} formats, so those three
// characters are the ones that need escaping — shell metacharacters do not,
// because tmux hands the result to the shell as one already-parsed argument.
func tmuxQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "#", "##")
	return `"` + r.Replace(s) + `"`
}

// clickBinding builds the command sequence behind double- and triple-click:
// focus the pane, enter copy mode at the mouse position, select, copy, exit.
func clickBinding(selector, copyCmd string) string {
	return fmt.Sprintf("select-pane ; copy-mode -M ; send-keys -X %s ; send-keys -X copy-pipe-and-cancel %s",
		selector, tmuxQuote(copyCmd))
}

// ConfigureClipboard installs the copy and URL bindings on the local tmux
// server. Bindings and hooks are server-global (tmux has no per-session key
// tables), which is consistent with the other -g options cs already sets.
//
// It is idempotent and cheap, and is called on every cs entry point rather
// than only at session creation: the tmux server outlives cs, so an upgraded
// cs attaching to a session created by an older one must still get them.
//
// Every command is best-effort: an older tmux that rejects one option should
// not stop a session from launching, so failures are logged by the caller
// rather than propagated.
func ConfigureClipboard() error {
	if !ClipboardEnabled {
		return nil
	}
	copyCmd := clipboardCopyCmd()
	if copyCmd == "" {
		// No clipboard tool on this machine (a bare Linux box, say).
		// Leave tmux's defaults alone rather than install bindings that
		// would silently swallow selections.
		return nil
	}

	cmds := [][]string{
		// Accept OSC 52 from panes so a *remote* tmux's copy lands in a
		// local buffer, and advertise clipboard support for every TERM so
		// the sequence is actually emitted regardless of terminfo.
		{"set-option", "-s", "set-clipboard", "on"},
		{"set-option", "-sa", "terminal-features", ",*:clipboard"},
		// The remote half of the chain: buffer set by OSC 52 -> OS clipboard.
		{"set-hook", "-g", "pane-set-clipboard",
			fmt.Sprintf("run-shell %q", "tmux save-buffer - | "+copyCmd)},

		// Local panes: mouse drag, double-click and triple-click copy
		// straight to the OS clipboard.
		{"bind-key", "-T", "copy-mode", "MouseDragEnd1Pane", "send-keys", "-X", "copy-pipe-and-cancel", copyCmd},
		{"bind-key", "-T", "copy-mode-vi", "MouseDragEnd1Pane", "send-keys", "-X", "copy-pipe-and-cancel", copyCmd},
		// A ';' has to arrive as part of a single command-string argument.
		// Passed as its own argv element, tmux would read it as a separator
		// and run the rest immediately instead of binding it.
		{"bind-key", "-n", "DoubleClick1Pane", clickBinding("select-word", copyCmd)},
		{"bind-key", "-n", "TripleClick1Pane", clickBinding("select-line", copyCmd)},

		// Keyboard copy mode, for when the mouse is not an option.
		{"bind-key", "-T", "copy-mode", "Enter", "send-keys", "-X", "copy-pipe-and-cancel", copyCmd},
		{"bind-key", "-T", "copy-mode-vi", "Enter", "send-keys", "-X", "copy-pipe-and-cancel", copyCmd},
		{"bind-key", "-T", "copy-mode-vi", "y", "send-keys", "-X", "copy-pipe-and-cancel", copyCmd},
	}

	if pasteCmd := clipboardPasteCmd(); pasteCmd != "" {
		cmds = append(cmds, []string{"bind-key", "C-v", "run-shell",
			fmt.Sprintf("%s | tmux load-buffer - && tmux paste-buffer", pasteCmd)})
	}

	// URL bindings re-enter this binary: prefix+u opens a menu of the URLs on
	// screen, prefix+U copies the most recent one.
	if self, err := os.Executable(); err == nil {
		q := tmuxEscape(shQuote(self))
		cmds = append(cmds,
			[]string{"bind-key", "u", "run-shell", q + " urls '#{pane_id}'"},
			[]string{"bind-key", "U", "run-shell", q + " urls --copy '#{pane_id}'"},
		)
	}

	var firstErr error
	for _, args := range cmds {
		if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("tmux %s: %s (%w)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
		}
	}
	return firstErr
}
