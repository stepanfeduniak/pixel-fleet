package notify

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/stepanfeduniak/pixel-fleet/internal/apps/builtin"
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
)

func TestIdleArmingWaitsForWorkAndDebouncesReadiness(t *testing.T) {
	now := time.Now()
	w := Watch{Armed: true}
	w.Observe("codex", session.StatusIdle, now)
	w.Observe("codex", session.StatusIdle, now.Add(time.Minute))
	if w.Pending != "" {
		t.Fatal("already idle session notified")
	}
	w.Observe("codex", session.StatusWorking, now)
	w.Observe("codex", session.StatusIdle, now.Add(time.Second))
	w.Observe("codex", session.StatusWorking, now.Add(2*time.Second))
	w.Observe("codex", session.StatusIdle, now.Add(3*time.Second))
	w.Observe("codex", session.StatusIdle, now.Add(12*time.Second))
	if w.Pending != "" {
		t.Fatal("transient idle counted toward readiness")
	}
	w.Observe("codex", session.StatusIdle, now.Add(13*time.Second))
	if w.Pending == "" {
		t.Fatal("sustained readiness did not notify")
	}
}

func TestRemoteShellWrapperDoesNotHideAgent(t *testing.T) {
	screen := "● Working on the task\n⏵⏵ auto mode on · esc to interrupt\n❯ "
	agent, status := classifySnapshot(screen, "zsh", true)
	if agent != "claude" || status != session.StatusWorking {
		t.Fatalf("remote wrapper hid agent: %s %s", agent, status)
	}
	agent, status = classifySnapshot(screen, "zsh", false)
	if agent != "terminal" {
		t.Fatalf("local shell should override old chrome: %s %s", agent, status)
	}
	_, status = classifySnapshot("Connection closed\nuser@host %", "zsh", true)
	if status != session.StatusError {
		t.Fatalf("lost connection counted as readiness: %s", status)
	}
}

func TestInputRequiresObservedWork(t *testing.T) {
	w := Watch{Armed: true}
	now := time.Now()
	w.Observe("claude", session.StatusWaitingInput, now)
	w.Observe("claude", session.StatusWaitingInput, now.Add(settleTime))
	if w.Pending != "" {
		t.Fatal("notified without a working transition")
	}
	w.Observe("claude", session.StatusWorking, now)
	w.Observe("claude", session.StatusWaitingInput, now)
	w.Observe("claude", session.StatusWaitingInput, now.Add(settleTime))
	if w.Pending != "appears to need your input" {
		t.Fatalf("pending = %q", w.Pending)
	}
}

func TestUnknownShellAndErrorsNeverMeanCompletion(t *testing.T) {
	for _, sample := range []struct {
		agent  string
		status session.Status
	}{
		{"terminal", session.StatusIdle}, {"", session.StatusIdle}, {"codex", session.StatusError}, {"claude", session.StatusUnknown},
	} {
		w := Watch{Armed: true, SeenWorking: true, Pending: "old readiness"}
		w.Observe(sample.agent, sample.status, time.Now())
		w.Observe(sample.agent, sample.status, time.Now().Add(time.Minute))
		if w.Pending != "" {
			t.Fatalf("notified for %+v", sample)
		}
	}
}

func TestBreakDigestAndAutomaticNextCycle(t *testing.T) {
	now := time.Now()
	state := map[string]Watch{}
	for _, name := range []string{"frontend", "backend"} {
		w := Watch{Name: name, Armed: true, SeenWorking: true}
		w.Observe("codex", session.StatusIdle, now)
		w.Observe("codex", session.StatusIdle, now.Add(settleTime))
		state[name] = w
	}
	calls := 0
	send := func(text string) error {
		calls++
		if !strings.Contains(text, "frontend") || !strings.Contains(text, "backend") {
			t.Fatal("not a combined digest")
		}
		return nil
	}
	Deliver(state, true, now, send)
	if calls != 0 {
		t.Fatal("interrupted break")
	}
	Deliver(state, false, now, send)
	Deliver(state, false, now.Add(time.Minute), send)
	if calls != 1 {
		t.Fatalf("sent %d notifications", calls)
	}
	for _, w := range state {
		if !w.LastSubmittedAt.Equal(now) {
			t.Fatal("successful submission time not recorded")
		}
		if !w.Armed || w.SeenWorking {
			t.Fatal("must remain enabled and wait for new work")
		}
	}
}

func TestResumedWorkCancelsQueuedHandoff(t *testing.T) {
	w := Watch{Name: "work", Armed: true, SeenWorking: true, Pending: "ready"}
	w.Observe("codex", session.StatusWorking, time.Now())
	Deliver(map[string]Watch{"work": w}, false, time.Now(), func(string) error { t.Fatal("sent stale handoff"); return nil })
	if !w.Armed {
		t.Fatal("must stay armed for next readiness")
	}
}

func TestDeliveryFailurePersistsAndRetries(t *testing.T) {
	s := Store{t.TempDir()}
	now := time.Now()
	err := s.Update(func(m map[string]Watch) error {
		m["work"] = Watch{Name: "work", Armed: true, Pending: "ready"}
		Deliver(m, false, now, func(string) error { return errors.New("offline") })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := (Store{s.Dir}).Read()
	if err != nil {
		t.Fatal(err)
	}
	if !m["work"].Armed || m["work"].LastError != "offline" {
		t.Fatal("retry state not persisted")
	}
	Deliver(m, false, now.Add(time.Second), func(string) error { t.Fatal("retried too soon"); return nil })
	Deliver(m, false, now.Add(time.Minute), func(string) error { return nil })
	if !m["work"].Armed || m["work"].Pending != "" {
		t.Fatal("retry did not clear pending event while staying enabled")
	}
}

func TestStoreConcurrentUpdatesAndCorruptState(t *testing.T) {
	s := Store{t.TempDir()}
	var wg sync.WaitGroup
	for _, name := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if err := s.Update(func(m map[string]Watch) error { m[name] = Watch{Name: name}; return nil }); err != nil {
				t.Error(err)
			}
		}(name)
	}
	wg.Wait()
	m, err := s.Read()
	if err != nil || len(m) != 4 {
		t.Fatalf("lost update: %v, %v", m, err)
	}
	path := filepath.Join(s.Dir, "notifications.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(); err == nil {
		t.Fatal("silently reset corrupt notification state")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSlackPayloadAndSecretRedaction(t *testing.T) {
	endpoint := "https://hooks.slack.com/services/SECRET"
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["unfurl_links"] != false || payload["unfurl_media"] != false {
			t.Fatal("unfurls enabled")
		}
		text := payload["text"].(string)
		if strings.Contains(text, "<!channel>") {
			t.Fatal("unescaped session name")
		}
		if !strings.Contains(text, "https://linear.app/team/issue/ENG-1/test") {
			t.Fatal("missing Linear link")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
	})}
	w := Watch{Name: "<!channel>", Issue: "https://linear.app/team/issue/ENG-1/test", Pending: "ready"}
	if err := post(client, endpoint, message([]Watch{w})); err != nil {
		t.Fatal(err)
	}
	client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New(endpoint) })
	err := post(client, endpoint, "test")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("webhook leaked or failure ignored")
	}
	client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(endpoint)), Header: http.Header{}}, nil
	})
	if err := post(client, endpoint, "test"); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("HTTP error leaked or ignored")
	}
}

func TestIssueValidationAndArming(t *testing.T) {
	for _, bad := range []string{"ENG-1", "https://evil.test/issue/ENG-1", "https://linear.app/", "https://linear.app/team/issue/ENG-1|oops"} {
		if ValidateIssue(bad) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	s := Store{t.TempDir()}
	rec := session.SessionRecord{Name: "work", CreatedAt: time.Now()}
	if err := s.SetChannel("slack"); err != nil {
		t.Fatal(err)
	}
	if err := s.Arm(rec, nil, session.StatusWorking); err == nil {
		t.Fatal("armed without Slack configured")
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "slack-webhook"), []byte("https://hooks.slack.com/services/test"), 0600); err != nil {
		t.Fatal(err)
	}
	issue := "https://linear.app/team/issue/ENG-1/test"
	if err := s.Arm(rec, &issue, session.StatusWorking); err != nil {
		t.Fatal(err)
	}
	if err := s.Arm(rec, nil, session.StatusWorking); err == nil {
		t.Fatal("duplicate arm accepted")
	}
	if err := s.Disarm(rec.Name); err != nil {
		t.Fatal(err)
	}
	if err := s.Arm(rec, nil, session.StatusIdle); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Read()
	if m[rec.Name].Issue != issue || m[rec.Name].SeenWorking {
		t.Fatal("rearming lost link or reused work signal")
	}
}

func TestRepeatedTransitionsNotifyWithoutRearming(t *testing.T) {
	now := time.Now()
	state := map[string]Watch{"work": {Name: "work", Armed: true}}
	calls := 0
	send := func(string) error { calls++; return nil }
	for cycle, ready := range []session.Status{session.StatusIdle, session.StatusWaitingInput, session.StatusIdle} {
		w := state["work"]
		at := now.Add(time.Duration(cycle) * time.Minute)
		w.Observe("codex", session.StatusWorking, at)
		w.Observe("codex", ready, at.Add(time.Second))
		w.Observe("codex", ready, at.Add(time.Second+settleTime))
		state["work"] = w
		Deliver(state, false, at.Add(time.Second+settleTime), send)
		w = state["work"]
		w.Observe("codex", ready, at.Add(30*time.Second))
		w.Observe("codex", session.StatusWaitingInput, at.Add(45*time.Second))
		state["work"] = w
		Deliver(state, false, at.Add(45*time.Second), send)
		if calls != cycle+1 {
			t.Fatalf("cycle %d: got %d deliveries", cycle, calls)
		}
	}
}

func TestSyncEnrollsSessionsAndPreservesMute(t *testing.T) {
	now := time.Now()
	records := []session.SessionRecord{{Name: "existing", CreatedAt: now}, {Name: "new", CreatedAt: now}}
	state := map[string]Watch{"existing": {Name: "existing", CreatedAt: now, Armed: false}, "removed": {Armed: true}}
	SyncSessions(state, records)
	if !state["existing"].Armed || !state["new"].Armed {
		t.Fatal("automatic enrollment failed")
	}
	if _, ok := state["removed"]; ok {
		t.Fatal("removed session retained")
	}
	w := state["existing"]
	w.Muted = true
	state["existing"] = w
	SyncSessions(state, records)
	if state["existing"].Armed {
		t.Fatal("mute was reset")
	}
	records[0].CreatedAt = now.Add(time.Second)
	SyncSessions(state, records)
	if !state["existing"].Armed || state["existing"].Muted {
		t.Fatal("replacement inherited old mute")
	}
}
