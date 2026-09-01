package session

import (
	"errors"
	"fmt"
	"testing"
)

// The strings are real ssh failures taken from a scan across a tailnet where
// most hosts were down — the distinction the markers draw (machine absent vs.
// machine present but refusing us) is only as good as this classification.
func TestClassifyProbe(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Health
	}{
		{"success", nil, HealthOnline},
		{
			"connect timed out",
			probeErr("ssh: connect to host follower-00001.taild4a0b0.ts.net port 22: Operation timed out"),
			HealthOffline,
		},
		{
			"probe killed by the scan timeout",
			errors.New("probe failed: signal: killed ()"),
			HealthOffline,
		},
		{
			"no such host",
			probeErr("ssh: Could not resolve hostname h100.taild4a0b0.ts.net: nodename nor servname provided, or not known"),
			HealthOffline,
		},
		{
			"connection refused",
			probeErr("ssh: connect to host zedmini0 port 22: Connection refused"),
			HealthOffline,
		},
		{
			"publickey rejected",
			probeErr("inloop@zedmini0.taild4a0b0.ts.net: Permission denied (publickey,password)."),
			HealthDenied,
		},
		{
			"too many auth failures",
			probeErr("Received disconnect from 10.0.0.4 port 22:2: Too many authentication failures"),
			HealthDenied,
		},
		{
			"host key changed",
			probeErr("Host key verification failed."),
			HealthDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyProbe(tt.err); got != tt.want {
				t.Errorf("classifyProbe(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// probeErr wraps output the way ScanMachine does, since classifyProbe reads
// the ssh output back out of the error text.
func probeErr(output string) error {
	return fmt.Errorf("probe failed: %w (%s)", errors.New("exit status 255"), output)
}
