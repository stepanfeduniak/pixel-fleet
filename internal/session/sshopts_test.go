package session

import (
	"strings"
	"testing"
	"time"
)

// The socket path is a unix domain socket, capped at 104 bytes. Spelling the
// host out instead of hashing it silently blew that on long tailnet names
// and every connection failed with "ControlPath too long" — which looks
// exactly like a dead host from the dashboard's side.
func TestControlPathStaysUnderSocketLimit(t *testing.T) {
	if !strings.Contains(controlPath, "%C") {
		t.Fatalf("controlPath %q should hash the host with %%C, not spell it out", controlPath)
	}
	// %C expands to a 40-char SHA1 hex digest; assume a long home dir.
	expanded := "/Users/a-fairly-long-user-name" + strings.TrimPrefix(controlPath, "~")
	expanded = strings.Replace(expanded, "%C", strings.Repeat("a", 40), 1)
	if len(expanded) >= 104 {
		t.Errorf("expanded ControlPath is %d bytes, must stay under 104", len(expanded))
	}
}

func TestSSHControlOptsMultiplexesAndBoundsTheHandshake(t *testing.T) {
	SetConnectTimeout(9 * time.Second)
	t.Cleanup(func() { SetConnectTimeout(0) })

	joined := strings.Join(sshOptions(), " ")
	for _, want := range []string{
		"ConnectTimeout=9",
		"ControlMaster=auto",
		"ControlPersist=" + controlPersist,
		"BatchMode=yes",
		"ServerAliveInterval=",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
}

// ssh reads ConnectTimeout as whole seconds and treats 0 as "wait forever",
// which would restore the multi-minute SYN hang the option exists to avoid.
func TestSubSecondConnectTimeoutNeverRoundsToZero(t *testing.T) {
	SetConnectTimeout(200 * time.Millisecond)
	t.Cleanup(func() { SetConnectTimeout(0) })

	if got := strings.Join(sshOptions(), " "); !strings.Contains(got, "ConnectTimeout=1") {
		t.Errorf("200ms should clamp to 1s, got %s", got)
	}
}

func TestZeroConnectTimeoutFallsBackToDefault(t *testing.T) {
	SetConnectTimeout(0)
	if got := ConnectTimeout(); got != defaultConnectTimeout {
		t.Errorf("got %v, want the %v default", got, defaultConnectTimeout)
	}
}

func TestSSHArgsPutsMachineBeforeTheRemoteCommand(t *testing.T) {
	args := sshArgs("gpu-01", "tmux ls")
	if args[len(args)-2] != "gpu-01" || args[len(args)-1] != "tmux ls" {
		t.Errorf("machine/command misordered: %v", args[len(args)-3:])
	}
	for _, a := range args[:len(args)-2] {
		if a == "gpu-01" {
			t.Fatal("machine name leaked into the option list")
		}
	}
}

// Viewers ride the same master as everything else. When each pane had its
// own connection, a blip dropped all of them anyway and the reconnect cost
// one full handshake per pane, all at once.
func TestViewerSharesTheHostConnection(t *testing.T) {
	cmd := BuildReattachCommand("gpu-01", "cs-x", "")
	for _, want := range []string{
		"'ControlMaster=auto'",
		"'ControlPath=" + controlPath + "'",
		"'ConnectTimeout=",
		"'ServerAliveInterval=",
		"ssh -t ",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("viewer command missing %q:\n%s", want, cmd)
		}
	}
}
