package session

import (
	"testing"
	"time"
)

// A machine that has been down for an hour will still be down in another
// minute, and probing it costs a full ScanTimeout every cycle. These tests
// pin the backoff schedule and, more importantly, that one success wipes it.

func TestBackoffDoublesWithConsecutiveFailures(t *testing.T) {
	p := NewProbeThrottle()
	start := time.Now()

	// Nothing known yet: probe it.
	if skip, _ := p.skip("dead", start); skip {
		t.Fatal("an unseen machine was skipped")
	}

	want := []time.Duration{
		1 * time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		15 * time.Minute, // capped
		15 * time.Minute,
	}
	for i, d := range want {
		p.record("dead", HealthOffline, start)

		// Still inside the window.
		if skip, _ := p.skip("dead", start.Add(d-time.Second)); !skip {
			t.Errorf("failure %d: probed again after %v, expected a %v backoff", i+1, d-time.Second, d)
		}
		// Window elapsed.
		if skip, _ := p.skip("dead", start.Add(d)); skip {
			t.Errorf("failure %d: still backing off at %v, expected a retry", i+1, d)
		}
	}
}

func TestSuccessClearsBackoff(t *testing.T) {
	p := NewProbeThrottle()
	start := time.Now()

	for i := 0; i < 4; i++ {
		p.record("flaky", HealthOffline, start)
	}
	if skip, _ := p.skip("flaky", start.Add(time.Minute)); !skip {
		t.Fatal("precondition: expected the machine to be in backoff")
	}

	p.record("flaky", HealthOnline, start.Add(8*time.Minute))

	if skip, _ := p.skip("flaky", start.Add(8*time.Minute)); skip {
		t.Error("a machine that came back is still being skipped")
	}
}

// A skipped machine must keep the marker it last earned rather than reverting
// to "not probed" — the dashboard should say "unreachable", not go blank.
func TestSkippedMachineCarriesLastHealth(t *testing.T) {
	p := NewProbeThrottle()
	start := time.Now()

	p.record("denied", HealthDenied, start)

	skip, carried := p.skip("denied", start.Add(30*time.Second))
	if !skip {
		t.Fatal("expected the machine to be in backoff")
	}
	if carried != HealthDenied {
		t.Errorf("carried health = %v, want %v", carried, HealthDenied)
	}
}

// The manual fetch passes a nil throttle to force a real probe of everything.
func TestNilThrottleNeverSkips(t *testing.T) {
	var p *ProbeThrottle

	p.record("dead", HealthOffline, time.Now()) // must not panic
	if skip, _ := p.skip("dead", time.Now()); skip {
		t.Error("a nil throttle skipped a machine")
	}
}
