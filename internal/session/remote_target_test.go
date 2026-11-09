package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Records from the shared cs-remote model become ordinary session+window
// records, so nothing downstream has to know that model existed.
func TestLegacyRecordsNormalizeToSessionAndWindow(t *testing.T) {
	for _, tc := range []struct {
		in         SessionRecord
		wantTarget string
	}{
		{SessionRecord{Name: "w", Machine: "gpu", RemoteWindow: "w-1a2b"}, "cs-remote:w-1a2b"},
		{SessionRecord{Name: "w", Machine: "gpu"}, "cs-remote:w"},
		{SessionRecord{Name: "w", Machine: "gpu", RemoteSession: "cs-w-1"}, "cs-w-1"},
		{SessionRecord{Name: "w", Machine: "home", RemoteSession: "cs-w-1"}, "cs-w-1"},
	} {
		rec := tc.in
		rec.normalize()
		if got := rec.RemoteTarget(); got != tc.wantTarget {
			t.Errorf("%+v -> %q, want %q", tc.in, got, tc.wantTarget)
		}
	}
}

func TestRecordMatchesItsTmuxTarget(t *testing.T) {
	d := DiscoveredSession{Machine: "gpu", TmuxSession: "work", WindowIndex: "2", WindowName: "claude"}
	for _, tc := range []struct {
		rec  SessionRecord
		want bool
	}{
		{SessionRecord{Machine: "gpu", RemoteSession: "work"}, true},                         // whole session
		{SessionRecord{Machine: "gpu", RemoteSession: "work", RemoteWindow: "2"}, true},      // by index
		{SessionRecord{Machine: "gpu", RemoteSession: "work", RemoteWindow: "claude"}, true}, // legacy by name
		{SessionRecord{Machine: "gpu", RemoteSession: "work", RemoteWindow: "3"}, false},
		{SessionRecord{Machine: "gpu", RemoteSession: "other"}, false},
		{SessionRecord{Machine: "cpu", RemoteSession: "work"}, false},
	} {
		if got := tc.rec.Matches(d); got != tc.want {
			t.Errorf("%+v matches=%v, want %v", tc.rec, got, tc.want)
		}
	}
}

// runBootstrap runs the generated remote bootstrap against a stub tmux that
// logs its subcommands. hasSession decides whether the session exists.
func runBootstrap(t *testing.T, hasSession bool) []string {
	t.Helper()
	cmd := BuildShellCommand(BuildOpts{
		Machine: "gpu-01", Path: "~/p", WindowName: "w",
		RemoteSessionName: "cs-w-1", Clipboard: true,
	})
	script := decodeBootstrap(t, cmd)

	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	hasStatus := "1"
	if hasSession {
		hasStatus = "0"
	}
	stub := "#!/bin/sh\necho \"$1\" >> " + shellQuote(log) + "\n" +
		"[ \"$1\" = has-session ] && exit " + hasStatus + "\n" +
		"[ \"$1\" = list-panes ] && echo 0\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// sleep is stubbed too, so the fresh-session pause does not slow tests.
	if err := os.WriteFile(filepath.Join(bin, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("bash", "-c", script)
	run.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	_ = run.Run() // exit code is the detach/ended signal, asserted elsewhere
	data, _ := os.ReadFile(log)
	return strings.Fields(string(data))
}

// Every reconnect reruns the bootstrap, so against a live session it must do
// nothing but check and attach — no relaunch, no reconfiguring.
func TestBootstrapOnLiveSessionOnlyAttaches(t *testing.T) {
	calls := strings.Join(runBootstrap(t, true), " ")
	for _, never := range []string{"new-session", "set-option", "bind-key", "kill-session"} {
		if strings.Contains(calls, never) {
			t.Errorf("reconnect to a live session ran tmux %s: %s", never, calls)
		}
	}
	if !strings.Contains(calls, "attach") {
		t.Errorf("did not attach: %s", calls)
	}
}

func TestBootstrapCreatesAndConfiguresAMissingSession(t *testing.T) {
	calls := strings.Join(runBootstrap(t, false), " ")
	for _, want := range []string{"new-session", "set-option", "set-window-option", "bind-key", "attach"} {
		if !strings.Contains(calls, want) {
			t.Errorf("first launch did not run tmux %s: %s", want, calls)
		}
	}
}
