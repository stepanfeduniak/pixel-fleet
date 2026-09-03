package config

import (
	"testing"
	"time"
)

func TestScanTimeoutIsKeptAboveConnectTimeout(t *testing.T) {
	cases := []struct {
		name            string
		scan, connect   time.Duration
		wantScanAtLeast time.Duration
	}{
		{"user shortened scan below connect", 5 * time.Second, 12 * time.Second, 13 * time.Second},
		{"equal is still too tight", 12 * time.Second, 12 * time.Second, 13 * time.Second},
		{"a roomy pair is left alone", 40 * time.Second, 12 * time.Second, 40 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{ScanTimeout: tc.scan, ConnectTimeout: tc.connect}
			c.reconcileTimeouts()
			if c.ScanTimeout < tc.wantScanAtLeast {
				t.Errorf("ScanTimeout = %v, want >= %v", c.ScanTimeout, tc.wantScanAtLeast)
			}
			if c.ScanTimeout <= c.ConnectTimeout {
				t.Errorf("ScanTimeout %v must exceed ConnectTimeout %v", c.ScanTimeout, c.ConnectTimeout)
			}
		})
	}
}

func TestDefaultsLeaveRoomForAHandshake(t *testing.T) {
	c := DefaultConfig()
	if c.ScanTimeout <= c.ConnectTimeout {
		t.Fatalf("defaults are self-defeating: scan %v <= connect %v", c.ScanTimeout, c.ConnectTimeout)
	}
	// A cold handshake on weak wifi measured 3.2-4.5s; leave real headroom.
	if c.ConnectTimeout < 8*time.Second {
		t.Errorf("default ConnectTimeout %v is too tight for a lossy link", c.ConnectTimeout)
	}
}
