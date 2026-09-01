package session

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	// The app registry is populated by init(); without it resolveApp has
	// nothing to return and buildRemoteCommand cannot run.
	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/builtin"
)

// decodeBootstrap pulls the base64 bootstrap script back out of the ssh
// command line so the generated remote shell can be asserted on directly.
func decodeBootstrap(t *testing.T, cmd string) string {
	t.Helper()
	// The outer command is: ssh ... 'bash -c "$(echo <b64> | base64 -d)"'
	re := regexp.MustCompile(`echo ([A-Za-z0-9+/=]{16,}) \| base64 -d`)
	m := re.FindAllStringSubmatch(cmd, -1)
	if len(m) == 0 {
		t.Fatalf("no base64 payload in command: %s", cmd)
	}
	// The last match is the outer bootstrap; the first is the launch script.
	raw, err := base64.StdEncoding.DecodeString(m[len(m)-1][1])
	if err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	return string(raw)
}

func TestRemoteBootstrapInstallsClipboardBridge(t *testing.T) {
	cmd := BuildShellCommand(BuildOpts{
		Machine:           "gpu-01",
		Path:              "~/proj",
		LocalClaudeBin:    "claude",
		RemoteClaudeBin:   "claude",
		WindowName:        "work",
		RemoteSessionName: "cs-work-deadbeef",
		Clipboard:         true,
	})
	got := decodeBootstrap(t, cmd)

	// set-clipboard is what makes the remote tmux emit OSC 52 at all; without
	// it a selection never leaves the remote host.
	for _, want := range []string{
		"set-option -s set-clipboard on",
		"terminal-features ',*:clipboard'",
		"bind-key -T copy-mode    MouseDragEnd1Pane send-keys -X copy-pipe-and-cancel",
		// One quoted string, not shell `\;` — see clipboardBridgeSnippet.
		"bind-key -n DoubleClick1Pane 'select-pane ; copy-mode -M ;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("remote bootstrap missing %q\n--- got ---\n%s", want, got)
		}
	}

	// Every bridge command must be non-fatal: the bootstrap runs under
	// `set -e`, so an older remote tmux rejecting one would abort the launch.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "tmux set-option -s") || strings.HasPrefix(line, "tmux bind-key") {
			if !strings.HasSuffix(line, "|| true") {
				t.Errorf("bridge line is fatal under set -e: %q", line)
			}
			// A bare `;` would end the tmux command line, leaving the rest
			// of a multi-command binding to run immediately instead.
			if strings.Contains(line, `\;`) {
				t.Errorf("bridge line uses shell-escaped ';', which tmux reads as a separator: %q", line)
			}
		}
	}
}

func TestRemoteBootstrapOmitsBridgeWhenDisabled(t *testing.T) {
	cmd := BuildShellCommand(BuildOpts{
		Machine:           "gpu-01",
		Path:              "~/proj",
		RemoteClaudeBin:   "claude",
		WindowName:        "work",
		RemoteSessionName: "cs-work-deadbeef",
		Clipboard:         false,
	})
	if got := decodeBootstrap(t, cmd); strings.Contains(got, "set-clipboard") {
		t.Errorf("clipboard bridge present despite Clipboard=false:\n%s", got)
	}
}

func TestReattachCommandInstallsBridge(t *testing.T) {
	SetClipboardEnabled(true)
	t.Cleanup(func() { SetClipboardEnabled(true) })

	cmd := BuildReattachCommand("gpu-01", "cs-adopted-1234", "")
	got := decodeBootstrap(t, cmd)

	if !strings.Contains(got, "set-option -s set-clipboard on") {
		t.Errorf("reattach script has no clipboard bridge:\n%s", got)
	}
	// Attaching must still be the last thing it does, and must be exec'd so
	// tmux inherits the ssh TTY as stdin.
	if !strings.Contains(got, "exec tmux attach -t cs-adopted-1234") {
		t.Errorf("reattach script does not attach:\n%s", got)
	}
}

func TestReattachHomeIsUnchanged(t *testing.T) {
	// The local tmux server is configured in-process; a home reattach must
	// stay the plain one-liner.
	if got, want := BuildReattachCommand("home", "cs", "work"), "tmux attach -t cs:work"; got != want {
		t.Errorf("BuildReattachCommand(home) = %q, want %q", got, want)
	}
}

func TestReattachOmitsBridgeWhenDisabled(t *testing.T) {
	SetClipboardEnabled(false)
	t.Cleanup(func() { SetClipboardEnabled(true) })

	if got := decodeBootstrap(t, BuildReattachCommand("gpu-01", "cs-x", "")); strings.Contains(got, "set-clipboard") {
		t.Errorf("bridge present despite clipboard: false:\n%s", got)
	}
}

func TestProbeScriptEnforcesBridgeOnRemotesOnly(t *testing.T) {
	SetClipboardEnabled(true)
	t.Cleanup(func() { SetClipboardEnabled(true) })

	remote := probeScriptFor(Machine{Name: "gpu-01"})
	if !strings.Contains(remote, "set-option -s set-clipboard on") {
		t.Errorf("remote probe does not reapply the bridge:\n%s", remote)
	}

	// Installing the remote bindings locally would replace the pbcopy pipe
	// with an OSC 52 emission the terminal discards — local copy would
	// silently stop working.
	if home := probeScriptFor(Machine{Name: "home"}); strings.Contains(home, "set-clipboard") {
		t.Errorf("home probe must not be given the remote bridge:\n%s", home)
	}

	SetClipboardEnabled(false)
	if off := probeScriptFor(Machine{Name: "gpu-01"}); strings.Contains(off, "set-clipboard") {
		t.Errorf("bridge present in probe despite clipboard: false:\n%s", off)
	}
}

// The scan output is parsed line by line, so nothing the bridge prepends may
// reach stdout or the discovery results would be corrupted.
func TestProbeBridgeLinesAreSilent(t *testing.T) {
	for _, line := range strings.Split(clipboardBridgeSnippet, "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(line, ">/dev/null 2>&1") {
			t.Errorf("bridge line can write to the scan's stdout: %q", line)
		}
	}

	// And the probe itself must survive being prepended to.
	SetClipboardEnabled(true)
	t.Cleanup(func() { SetClipboardEnabled(true) })
	if got := probeScriptFor(Machine{Name: "gpu-01"}); !strings.HasSuffix(got, probeScript) {
		t.Errorf("probe script was altered, not prefixed:\n%s", got)
	}
}

// terminal-features is set with `-a`, which appends. The scan reapplies the
// bridge to every machine on an interval, so an unconditional append adds
// another copy of the entry every cycle and the option grows for as long as
// the remote tmux server lives — a real host was found holding 150 copies,
// climbing by one a minute. The line must read the current value first.
func TestTerminalFeaturesAppendIsGuarded(t *testing.T) {
	var line string
	for _, l := range strings.Split(clipboardBridgeSnippet, "\n") {
		if strings.Contains(l, "terminal-features") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("no terminal-features line in the bridge snippet")
	}
	if !strings.Contains(line, "show-options") {
		t.Errorf("terminal-features is appended unconditionally, so every scan adds a duplicate:\n  %s", line)
	}
	if !strings.Contains(line, "-sa terminal-features") {
		t.Errorf("expected the guarded line to still perform the append:\n  %s", line)
	}
}
