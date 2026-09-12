package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stepanfeduniak/pixel-fleet/internal/session"
)

const settleTime = 10 * time.Second

func ValidateIssue(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "linear.app" || u.User != nil || !strings.Contains(u.Path, "/issue/") || strings.ContainsAny(raw, "<>|\n\r ") {
		return errors.New("use a full https://linear.app/<workspace>/issue/<id>/... issue URL")
	}
	return nil
}

// Webhook reads a file so an already-running dashboard needs no new env vars.
func (s Store) Webhook() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, "slack-webhook"))
	if err != nil {
		return "", errors.New("Slack is not configured; see docs/notifications.md (create ~/.config/cs/slack-webhook)")
	}
	raw := strings.TrimSpace(string(data))
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || (u.Host != "hooks.slack.com" && u.Host != "hooks.slack-gov.com") || u.User != nil || !strings.HasPrefix(u.Path, "/services/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("slack-webhook must contain a Slack incoming webhook HTTPS URL")
	}
	return raw, nil
}

func (s Store) Arm(rec session.SessionRecord, issue *string, observed session.Status) error {
	if err := s.Ready(); err != nil {
		return err
	}
	if issue != nil {
		if err := ValidateIssue(*issue); err != nil {
			return err
		}
	}
	return s.Update(func(state map[string]Watch) error {
		old := state[rec.Name]
		if old.Armed {
			return fmt.Errorf("automatic notifications are already enabled for %s", rec.Name)
		}
		link := old.Issue
		if !old.CreatedAt.Equal(rec.CreatedAt) {
			link = ""
		}
		if issue != nil {
			link = *issue
		}
		state[rec.Name] = Watch{Name: rec.Name, Machine: rec.Machine, CreatedAt: rec.CreatedAt, Armed: true, ArmedAt: time.Now(), SeenWorking: observed == session.StatusWorking, Issue: link}
		return nil
	})
}

func (s Store) Disarm(name string) error {
	return s.Update(func(state map[string]Watch) error {
		w := state[name]
		w.Armed, w.Pending, w.Candidate, w.LastError = false, "", "", ""
		w.Muted = true
		w.SeenWorking = false
		state[name] = w
		return nil
	})
}

// Observe requires sustained readiness, never interpreting a shell or a
// disconnected pane as a completed turn. Arming while idle waits for work.
func (w *Watch) Observe(agent string, status session.Status, now time.Time) {
	if !w.Armed {
		return
	}
	if agent != "claude" && agent != "codex" {
		status = session.StatusUnknown
	}
	observed := agent + ": " + status.String()
	if observed != w.LastObserved {
		w.LastObserved, w.LastObservedAt = observed, now
		log.Printf("notification observation %q: %s", w.Name, observed)
	}
	if status == session.StatusWorking {
		w.SeenWorking = true
		w.Pending, w.Candidate, w.LastError = "", "", ""
		w.Since = time.Time{}
		w.RetryAt = time.Time{}
		return
	}
	reason := ""
	if status == session.StatusWaitingInput && w.SeenWorking {
		reason = "appears to need your input"
	}
	if status == session.StatusIdle && w.SeenWorking {
		reason = "appears ready for your next instruction"
	}
	if reason == "" {
		if status == session.StatusUnknown || status == session.StatusError {
			w.SeenWorking = false
		}
		w.Pending, w.Candidate = "", ""
		w.Since = time.Time{}
		return
	}
	if reason != w.Candidate {
		w.Candidate, w.Pending, w.Since = reason, "", now
		return
	}
	if now.Sub(w.Since) >= settleTime {
		w.Pending = reason
	}
}

func message(watches []Watch) string {
	parts := []string{"Pixel Fleet — ready when you are"}
	for _, w := range watches {
		line := fmt.Sprintf("%s (%s) %s.", w.Name, w.Machine, w.Pending)
		if w.Issue != "" {
			line += "\n" + w.Issue
		}
		parts = append(parts, line)
	}
	parts = append(parts, "Open cs and return to the session when convenient.")
	return strings.Join(parts, "\n\n")
}

var slackClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
}

type retryError struct{ delay time.Duration }

func (e retryError) Error() string {
	return "Slack rate limited delivery; will retry after Retry-After"
}

func post(client *http.Client, endpoint, text string) error {
	text = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
	body, _ := json.Marshal(map[string]any{"text": text, "unfurl_links": false, "unfurl_media": false})
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	// net/http errors include the secret URL; never return or log them.
	if err != nil {
		return errors.New("Slack delivery failed (network or timeout); will retry")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		delay := time.Minute
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 60 && seconds <= 86400 {
			delay = time.Duration(seconds) * time.Second
		}
		return retryError{delay}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil || resp.StatusCode != http.StatusOK || strings.TrimSpace(string(data)) != "ok" {
		return fmt.Errorf("Slack delivery failed (HTTP %d); check webhook/channel; will retry", resp.StatusCode)
	}
	return nil
}

func (s Store) Send(text string) error {
	channel, err := s.Channel()
	if err != nil {
		return err
	}
	if channel == "macos" {
		return sendNative(text)
	}
	endpoint, err := s.Webhook()
	if err != nil {
		return err
	}
	return post(slackClient, endpoint, text)
}

// Deliver holds the state lock through sending so disarm and send have a
// defined order. A failed or ambiguous delivery remains armed for retry.
func Deliver(state map[string]Watch, blocked bool, now time.Time, send func(string) error) {
	if blocked {
		return
	}
	var ready []Watch
	for _, w := range state {
		if w.Armed && w.Pending != "" && !now.Before(w.RetryAt) {
			ready = append(ready, w)
			if len(ready) == 10 {
				break
			}
		}
	}
	if len(ready) == 0 {
		return
	}
	err := send(message(ready))
	for _, w := range ready {
		if err != nil {
			delay := time.Minute
			var retry retryError
			if errors.As(err, &retry) {
				delay = retry.delay
			}
			w.LastError, w.RetryAt = err.Error(), now.Add(delay)
			log.Printf("notification submission failed for %q: %v", w.Name, err)
		} else {
			w.LastSubmittedAt = now
			log.Printf("notification submitted for %q", w.Name)
			w.Pending, w.LastError, w.Candidate = "", "", ""
			w.SeenWorking = false
			w.Since, w.RetryAt = time.Time{}, time.Time{}
		}
		state[w.Name] = w
	}
}
