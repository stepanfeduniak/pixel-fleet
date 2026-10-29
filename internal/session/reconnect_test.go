package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runReconnectLoop runs the real wrapWithReconnect program under sh with a
// fake ssh that fails `failures` times instantly and then exits 0. Stdin is
// closed, as it effectively is for a pane whose terminal was left in raw
// mode: anything that waits on input would stop the loop here.
func runReconnectLoop(t *testing.T, failures int) (calls int, output string) {
	t.Helper()
	reconnectBaseDelay, reconnectMaxDelay = 0, 0
	t.Cleanup(func() { reconnectBaseDelay, reconnectMaxDelay = 2, 30 })

	counter := filepath.Join(t.TempDir(), "calls")
	fakeSSH := fmt.Sprintf(`echo x >> %s; [ "$(wc -l < %s)" -gt %d ]`,
		shellQuote(counter), shellQuote(counter), failures)

	cmd := exec.Command("sh", "-c", wrapWithReconnect(fakeSSH))
	cmd.Stdin = nil
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = cmd.CombinedOutput(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("reconnect loop did not finish")
	}
	if err != nil {
		t.Fatalf("loop exited with %v\n%s", err, out)
	}
	data, _ := os.ReadFile(counter)
	return strings.Count(string(data), "x"), string(out)
}

// A long outage ends in a burst of instant failures (`Connection reset by
// peer` while the tailnet comes back). The loop must ride through it and
// reconnect on its own instead of stopping to wait for an Enter that a raw
// terminal never delivers.
func TestReconnectSurvivesFastFailuresWithoutInput(t *testing.T) {
	calls, out := runReconnectLoop(t, 8)
	if calls != 9 {
		t.Fatalf("ssh ran %d times, want 9 (8 failures then the reconnect)\n%s", calls, out)
	}
	if !strings.Contains(out, "Still retrying") {
		t.Errorf("expected the auth/host-key hint after repeated fast failures\n%s", out)
	}
	if !strings.Contains(out, "\033[?1000l") {
		t.Errorf("expected mouse reporting to be switched off after a drop\n%s", out)
	}
}

func TestReconnectBackoffDoublesAndCaps(t *testing.T) {
	prog := wrapWithReconnect("false")
	for _, want := range []string{"delay=2\n", `-lt 30 ]`, "delay=30\n", "-ge 60 ]"} {
		if !strings.Contains(prog, want) {
			t.Errorf("reconnect loop missing %q:\n%s", want, prog)
		}
	}
	if strings.Contains(prog, "read ") {
		t.Errorf("reconnect loop must never block on input:\n%s", prog)
	}
}

// A detach exits ssh cleanly while the session keeps running. The loop must
// reattach instead of closing the window, and only stop once the session
// itself is gone (ssh exits 0).
func TestReconnectReattachesAfterDetach(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "calls")
	fakeSSH := fmt.Sprintf(`(echo x >> %s; [ "$(wc -l < %s)" -gt 1 ] || exit %d)`,
		shellQuote(counter), shellQuote(counter), detachedExitCode)

	out, err := exec.Command("sh", "-c", wrapWithReconnect(fakeSSH)).CombinedOutput()
	if err != nil {
		t.Fatalf("loop exited with %v\n%s", err, out)
	}
	data, _ := os.ReadFile(counter)
	if calls := strings.Count(string(data), "x"); calls != 2 {
		t.Fatalf("ssh ran %d times, want 2 (detach then reattach)\n%s", calls, out)
	}
	if !strings.Contains(string(out), "still running. Reattaching") {
		t.Errorf("expected the reattach banner\n%s", out)
	}
}

// remoteAttach runs on the remote; a stub tmux stands in for the real one.
// After the client returns, the script must exit detachedExitCode while the
// session exists and 0 once it is gone.
func TestRemoteAttachReportsDetachVersusEnded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hasStatus int
		want      int
	}{
		{"detached", 0, detachedExitCode},
		{"ended", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			stub := fmt.Sprintf("#!/bin/sh\n[ \"$1\" = has-session ] && exit %d\nexit 0\n", tc.hasStatus)
			if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", "set -e\n"+remoteAttach("cs-x"))
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			err := cmd.Run()
			got := 0
			if ee, ok := err.(*exec.ExitError); ok {
				got = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("exit %d, want %d", got, tc.want)
			}
		})
	}
}
