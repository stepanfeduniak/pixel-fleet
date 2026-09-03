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

	joined := strings.Join(sshControlOpts(), " ")
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

	if got := strings.Join(sshControlOpts(), " "); !strings.Contains(got, "ConnectTimeout=1") {
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

// The interactive attach is a separate path from sshControlOpts, and the
// two properties that matter on a weak link are easy to drop silently.
func TestInteractiveSSHOptsSurviveABadLink(t *testing.T) {
	// Without ConnectTimeout a reconnect against a still-down host waits out
	// the kernel's ~2-minute SYN timeout, freezing the pane long past the
	// reconnect loop's own retry delay.
	if !strings.Contains(sshOpts, "ConnectTimeout=") {
		t.Error("interactive sshOpts must bound the handshake")
	}
	if !strings.Contains(sshOpts, "ServerAliveInterval=") {
		t.Error("interactive sshOpts must keepalive, or a dead link reads as a hung session")
	}
	// A shared master would couple every session on a host to one TCP
	// connection: one blip would drop all of them instead of one.
	if strings.Contains(sshOpts, "ControlMaster") {
		t.Error("interactive sessions must not share a multiplexed master")
	}
}
