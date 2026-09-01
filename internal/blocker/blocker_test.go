package blocker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isolate points the blocker state file at a temp dir. Path() resolves via
// os.UserHomeDir, which reads $HOME on unix, so overriding it keeps tests
// off the real ~/.config/cs/blocker.json.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".config", "cs", "blocker.json")
}

func TestActiveAndRemaining(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		state     State
		want      bool
		remaining time.Duration
	}{
		{"zero value is not blocked", State{}, false, 0},
		{"future deadline blocks", State{Until: now.Add(10 * time.Minute)}, true, 10 * time.Minute},
		{"past deadline does not block", State{Until: now.Add(-time.Second)}, false, 0},
		// The deadline instant itself is already expired: Active uses
		// now.Before(Until), so a blocker never outlives its own timestamp.
		{"exact deadline does not block", State{Until: now}, false, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.Active(now); got != tc.want {
				t.Errorf("Active() = %v, want %v", got, tc.want)
			}
			if got := tc.state.Remaining(now); got != tc.remaining {
				t.Errorf("Remaining() = %v, want %v", got, tc.remaining)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	isolate(t)

	now := time.Now().Truncate(time.Second)
	want, err := Start(45*time.Minute, now)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := Load()
	if !got.Until.Equal(want.Until) {
		t.Errorf("Until = %v, want %v", got.Until, want.Until)
	}
	if got.Duration != 45*time.Minute {
		t.Errorf("Duration = %v, want 45m", got.Duration)
	}
	if !got.Active(now.Add(time.Minute)) {
		t.Error("reloaded blocker should still be active one minute in")
	}
	if got.Active(now.Add(time.Hour)) {
		t.Error("reloaded blocker should have expired after an hour")
	}
}

// A blocker must survive the dashboard TUI dying — that's the whole point
// of persisting a deadline instead of counting down in memory. Loading
// fresh state from disk is exactly what a restarted TUI does.
func TestBlockerSurvivesReload(t *testing.T) {
	isolate(t)

	now := time.Now()
	if _, err := Start(30*time.Minute, now); err != nil {
		t.Fatalf("Start: %v", err)
	}

	reloaded := Load()
	if !reloaded.Active(now) {
		t.Fatal("blocker did not survive a reload")
	}

	if err := Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if Load().Active(now) {
		t.Error("blocker still active after Clear")
	}
}

// Every failure to read state must read as "not blocked". Failing closed on
// a corrupt file would lock the user out with no way to see or clear it.
func TestLoadFailsOpen(t *testing.T) {
	path := isolate(t)

	if Load().Active(time.Now()) {
		t.Error("missing state file should not block")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if Load().Active(time.Now()) {
		t.Error("corrupt state file should not block")
	}
}

func TestClearIsIdempotent(t *testing.T) {
	isolate(t)
	if err := Clear(); err != nil {
		t.Errorf("Clear on absent state = %v, want nil", err)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"45", 45 * time.Minute, true}, // bare number means minutes
		{"  30  ", 30 * time.Minute, true},
		{"90m", 90 * time.Minute, true},
		{"1h30m", 90 * time.Minute, true},
		{"2h", 2 * time.Hour, true},
		{"24h", 24 * time.Hour, true},
		{"", 0, false},
		{"0", 0, false},
		{"-5", 0, false},
		{"-5m", 0, false},
		{"soon", 0, false},
		{"25h", 0, false},  // past MaxDuration
		{"5000", 0, false}, // 5000 minutes, past MaxDuration
	}

	for _, tc := range tests {
		got, ok := ParseDuration(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseDuration(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFormatRemaining(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "0:00"},
		{-time.Second, "0:00"},
		{500 * time.Millisecond, "0:01"}, // rounds up: never show 0:00 while blocking
		{time.Second, "0:01"},
		{90 * time.Second, "1:30"},
		{45 * time.Minute, "45:00"},
		{time.Hour, "1:00:00"},
		{90 * time.Minute, "1:30:00"},
	}

	for _, tc := range tests {
		if got := FormatRemaining(tc.in); got != tc.want {
			t.Errorf("FormatRemaining(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{15 * time.Minute, "15 min"},
		{time.Hour, "1 h"},
		{90 * time.Minute, "1 h 30 min"},
	}

	for _, tc := range tests {
		if got := FormatDuration(tc.in); got != tc.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// hasMonotonic reports whether t carries a monotonic clock reading. Round(0)
// is the documented way to strip one, so a value that changes when stripped
// had one.
func hasMonotonic(t time.Time) bool {
	return t.String() != t.Round(0).String()
}

// A blocker must measure elapsed real time, not time the machine was awake.
//
// time.Now() carries a monotonic reading, now.Add(d) preserves it, and Go's
// Before/Sub use the monotonic clock whenever both operands have one. On
// macOS that clock is mach_absolute_time, which does not advance while the
// system is asleep — so a State built from an unstripped time.Now() freezes
// its countdown for as long as the lid is shut. Persisting and reloading
// happened to hide this (JSON drops the reading), so it only bit the process
// that started the blocker.
func TestStartUsesWallClockNotMonotonic(t *testing.T) {
	isolate(t)

	s, err := Start(90*time.Minute, time.Now())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if hasMonotonic(s.Until) {
		t.Errorf("Until carries a monotonic reading (%s); the countdown will pause while the machine sleeps", s.Until)
	}
	if hasMonotonic(s.StartedAt) {
		t.Errorf("StartedAt carries a monotonic reading (%s)", s.StartedAt)
	}
}

// Active and Remaining strip the reading themselves, so a State assembled by
// hand — as the TUI's in-memory copy is — gets the same wall-clock treatment.
func TestActiveAndRemainingStripMonotonic(t *testing.T) {
	now := time.Now() // carries a monotonic reading
	s := State{Until: now.Add(30 * time.Minute), StartedAt: now, Duration: 30 * time.Minute}

	// Compare against a purely wall-clock deadline: the two must agree, which
	// is only true if the monotonic reading is being ignored on both sides.
	wall := State{Until: now.Round(0).Add(30 * time.Minute)}

	for _, at := range []time.Time{now, now.Add(29 * time.Minute), now.Add(31 * time.Minute)} {
		if got, want := s.Active(at), wall.Active(at.Round(0)); got != want {
			t.Errorf("Active(%v) = %v, wall-clock says %v", at, got, want)
		}
		if got, want := s.Remaining(at), wall.Remaining(at.Round(0)); got != want {
			t.Errorf("Remaining(%v) = %v, wall-clock says %v", at, got, want)
		}
	}
}
