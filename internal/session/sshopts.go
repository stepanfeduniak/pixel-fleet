package session

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// Every ssh cs runs — viewer panes, discovery probes, doctor, repo listing,
// remote kills — goes through sshOptions. There is one option set and one
// connection per host.
//
// Viewers used to open their own connection each, on the theory that a blip
// would then drop one pane instead of all of them. In practice every viewer
// to a host shares the same network path, so a blip dropped them all anyway
// — and then ten panes ran ten full handshakes at once over a ~210ms link.
// Sharing a master costs nothing in isolation and turns that into one
// handshake: the first command to a host opens the master, everything else
// (including every reconnect) rides it.
const (
	// defaultConnectTimeout bounds the TCP connect plus SSH handshake. A
	// cold handshake over a weak link measures 3-4.5s, so anything near 5s
	// classifies live hosts as offline.
	defaultConnectTimeout = 12 * time.Second

	// Keepalives turn a silently dead link into an error. They run on the
	// master, so they decide when every pane on the host notices a drop:
	// 5s x 4 gives up after ~20s while tolerating jitter spikes.
	keepaliveInterval = 5
	keepaliveCountMax = 4

	// controlPersist keeps the master open this long after its last user.
	// Viewers keep it busy while they are attached; between them, 3m spans
	// several discovery cycles so the handshake is paid once.
	controlPersist = "180s"

	// controlPath is where the master sockets live. %C hashes (local host,
	// remote host, port, user): a unix socket path is capped at 104 bytes
	// on macOS and long tailnet names spelled out overflow it.
	controlPath = "~/.ssh/cs-%C"
)

// connectTimeout is set once at startup from config. Atomic because the
// dashboard scans machines from parallel goroutines.
var connectTimeout atomic.Int64

// SetConnectTimeout overrides the default connect timeout. A value <= 0
// restores the default.
func SetConnectTimeout(d time.Duration) {
	if d <= 0 {
		d = defaultConnectTimeout
	}
	connectTimeout.Store(int64(d))
}

// ConnectTimeout reports the timeout currently in effect.
func ConnectTimeout() time.Duration {
	if v := connectTimeout.Load(); v > 0 {
		return time.Duration(v)
	}
	return defaultConnectTimeout
}

// sshOptions returns the options for every ssh cs runs.
//
// BatchMode applies to viewers too: discovery already needs non-interactive
// auth to see a host at all, and a viewer that stops to prompt inside a
// pane just looks hung.
func sshOptions() []string {
	// ssh takes whole seconds and treats 0 as "no timeout", which would
	// bring back the kernel's ~2-minute SYN hang.
	secs := int(ConnectTimeout().Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return []string{
		"-o", fmt.Sprintf("ConnectTimeout=%d", secs),
		"-o", fmt.Sprintf("ServerAliveInterval=%d", keepaliveInterval),
		"-o", fmt.Sprintf("ServerAliveCountMax=%d", keepaliveCountMax),
		"-o", "BatchMode=yes",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + controlPath,
		"-o", "ControlPersist=" + controlPersist,
	}
}

// sshArgs builds an argv for exec: options, the machine, the remote command.
func sshArgs(machine string, rest ...string) []string {
	args := sshOptions()
	args = append(args, machine)
	return append(args, rest...)
}

// sshShellCommand builds the same invocation as a shell command line for a
// viewer pane, with a TTY for the remote tmux client.
func sshShellCommand(machine, remoteCmd string) string {
	parts := []string{"ssh", "-t"}
	for _, a := range sshArgs(machine, remoteCmd) {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}
