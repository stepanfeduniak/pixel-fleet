package session

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Tuning for every non-interactive SSH cs makes (probes, doctor, repo
// listing, remote tmux control). Interactive attach is deliberately not
// covered — see SSHControlOpts.
const (
	// defaultConnectTimeout bounds the TCP connect plus SSH handshake.
	//
	// This used to be 5s, which is fine on a LAN and actively harmful on a
	// weak link: a cold handshake over 2.4GHz wifi at ~300ms RTT measures
	// 3.2-4.5s, so a host that was answering perfectly well lost the race
	// with its own timeout, got classified HealthOffline, and was then
	// pushed into a minutes-long backoff by ProbeThrottle. The dashboard
	// went empty while every machine in it was up.
	defaultConnectTimeout = 12 * time.Second

	// Keepalives are what turn a silently dead link into an error. Without
	// them a connection that stops passing traffic mid-command hangs until
	// the context deadline; 5s x 3 gives up after ~15s.
	keepaliveInterval = 5
	keepaliveCountMax = 3

	// controlPersist keeps the shared master connection open this long
	// after the last command through it. The dashboard re-probes every
	// DiscoveryInterval (60s default), so 3m spans several cycles and the
	// handshake cost is paid once rather than per scan.
	controlPersist = "180s"

	// controlPath is where the multiplexing sockets live.
	//
	// %C is a hash of (local host, remote host, port, user), which matters
	// for more than tidiness: the socket path is a unix domain socket and
	// so is capped at 104 bytes on macOS. Spelling the host out instead
	// silently exceeds that on long tailnet names and every connection
	// fails with "ControlPath too long".
	controlPath = "~/.ssh/cs-%C"
)

// SSHControlOpts returns the options for a short, non-interactive SSH
// command.
//
// The important one is connection multiplexing. cs fires a lot of small
// commands at the same handful of hosts — a discovery probe per machine per
// cycle, plus repo listings and tmux control — and each one used to pay a
// full TCP + SSH handshake. On a bad link that handshake dominates
// completely: measured against a live host at ~300ms RTT, cold connections
// took 3.2-4.5s where ones reusing a master took 0.45-1.5s. ControlMaster
// makes the first command pay the handshake and every later one ride the
// open connection.
//
// This is not used for interactive attach. Those are long-lived and already
// self-heal via wrapWithReconnect; putting them on a shared master would
// couple every session on a host to one TCP connection, so a single blip
// would drop all of them at once instead of one.
// connectTimeout is the live value, set once at startup from config so
// every SSH call site agrees without threading a second duration through
// signatures that already carry an overall timeout. Atomic because the
// dashboard scans machines in parallel goroutines.
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

// sshControlOpts returns the options for a short, non-interactive SSH
// command.
//
// The important one is connection multiplexing. cs fires a lot of small
// commands at the same handful of hosts — a discovery probe per machine per
// cycle, plus repo listings and tmux control — and each one used to pay a
// full TCP + SSH handshake. On a bad link that handshake dominates
// completely: measured against a live host at ~300ms RTT, cold connections
// took 3.2-4.5s where ones reusing a master took 0.45-1.5s. ControlMaster
// makes the first command pay the handshake and every later one ride the
// open connection.
//
// This is not used for interactive attach. Those are long-lived and already
// self-heal via wrapWithReconnect; putting them on a shared master would
// couple every session on a host to one TCP connection, so a single blip
// would drop all of them at once instead of one.
func sshControlOpts() []string {
	// ssh takes ConnectTimeout in whole seconds and treats 0 as "no
	// timeout", which would reintroduce the multi-minute SYN hang that
	// ConnectTimeout exists to prevent.
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

// sshArgs builds a full ssh argv: control options, then the machine, then
// the remote command.
func sshArgs(machine string, rest ...string) []string {
	args := sshControlOpts()
	args = append(args, machine)
	return append(args, rest...)
}
