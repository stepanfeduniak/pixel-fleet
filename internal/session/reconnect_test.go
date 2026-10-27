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
