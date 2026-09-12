package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/stepanfeduniak/pixel-fleet/internal/blocker"
	"github.com/stepanfeduniak/pixel-fleet/internal/notify"
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
)

func cmdNotify(args []string, tmuxSession string) error {
	s := notify.DefaultStore()
	if len(args) == 0 || args[0] == "status" {
		state, err := s.Read()
		if err != nil {
			return err
		}
		channel, err := s.Channel()
		if err != nil {
			return err
		}
		fmt.Printf("Notifications: %s\nConfigured: %t\nWatcher running: %t\nPaused: %t\n", channel, s.Ready() == nil, s.Running(), s.Paused())
		names := make([]string, 0, len(state))
		for name := range state {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			w := state[name]
			status := "muted"
			if w.Armed {
				status = "automatic"
			}
			if w.Pending != "" {
				status = "queued"
			}
			fmt.Printf("%s: %s", name, status)
			if w.LastObserved != "" {
				fmt.Printf("  observed %s at %s", w.LastObserved, w.LastObservedAt.Local().Format(time.RFC3339))
			}
			if !w.LastSubmittedAt.IsZero() {
				fmt.Printf("  last submitted %s", w.LastSubmittedAt.Local().Format(time.RFC3339))
			}
			if w.Issue != "" {
				fmt.Printf("  %s", w.Issue)
			}
			if w.LastError != "" {
				fmt.Printf("  %s", w.LastError)
			}
			fmt.Println()
		}
		return nil
	}
	switch args[0] {
	case "start":
		return s.EnsureWorker(tmuxSession)
	case "setup":
		if len(args) == 2 && args[1] == "macos" {
			if err := s.SetChannel("macos"); err != nil {
				return err
			}
			fmt.Println("Native macOS notifications selected. Run cs notify test and check Notification Center.")
			return nil
		}
		if len(args) > 1 && (len(args) != 2 || args[1] != "slack") {
			return fmt.Errorf("usage: cs notify setup [macos|slack]")
		}
		// Read from stdin, never argv (which main logs).
		fmt.Fprintln(os.Stderr, "Reading Slack webhook from stdin. Paste it and press Ctrl-D, or pipe it from your clipboard.")
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			return err
		}
		// Validate in a temporary store before replacing working credentials.
		tmp, err := os.MkdirTemp("", "cs-slack-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if err := os.WriteFile(filepath.Join(tmp, "slack-webhook"), []byte(strings.TrimSpace(string(data))), 0600); err != nil {
			return err
		}
		if _, err := (notify.Store{Dir: tmp}).Webhook(); err != nil {
			return err
		}
		if err := os.MkdirAll(s.Dir, 0700); err != nil {
			return err
		}
		path := filepath.Join(s.Dir, "slack-webhook")
		if err := os.WriteFile(path+".tmp", []byte(strings.TrimSpace(string(data))), 0600); err != nil {
			return err
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			return err
		}
		if err := s.SetChannel("slack"); err != nil {
			return err
		}
		fmt.Println("Slack configured. Run cs notify test to send a test message.")
		return nil
	case "test":
		if s.Paused() {
			return fmt.Errorf("notifications are paused; resume them from the Pixel Fleet menu bar")
		}
		if blocker.Load().Active(time.Now()) {
			return fmt.Errorf("break is active; run the notification test after it ends")
		}
		if len(args) > 1 {
			rec, ok := session.NewStore().Lookup(args[1])
			if !ok {
				return fmt.Errorf("unknown session %q", args[1])
			}
			if err := s.SendWatches([]notify.Watch{{Name: rec.Name, Machine: rec.Machine, CreatedAt: rec.CreatedAt, Pending: "is ready for a notification test — choose Open session"}}); err != nil {
				return err
			}
		} else if err := s.Send("Pixel Fleet is connected. Agents automatically notify you when they stop working and need your input."); err != nil {
			return err
		}
		channel, _ := s.Channel()
		if channel == "macos" {
			fmt.Println("Test submitted by Pixel Fleet.app. Choose Pixel Fleet → Persistent in System Settings > Notifications to keep it visible.")
		} else {
			fmt.Println("Test message delivered to Slack.")
		}
		return nil
	case "arm", "on", "off", "link":
		if len(args) < 2 {
			break
		}
		name := args[1]
		if args[0] == "off" {
			return s.Disarm(name)
		}
		rec, ok := session.NewStore().Lookup(name)
		if !ok {
			return fmt.Errorf("unknown session %q; run cs ls", name)
		}
		if args[0] == "link" {
			if len(args) != 3 {
				break
			}
			issue := args[2]
			if issue == "-" {
				issue = ""
			}
			if err := notify.ValidateIssue(issue); err != nil {
				return err
			}
			return s.Update(func(state map[string]notify.Watch) error {
				w := state[name]
				if !w.CreatedAt.Equal(rec.CreatedAt) {
					w = notify.Watch{Name: name, Machine: rec.Machine, CreatedAt: rec.CreatedAt}
				}
				w.Issue = issue
				state[name] = w
				return nil
			})
		}
		agent, status := notify.Snapshot(tmuxSession, rec)
		if agent != "claude" && agent != "codex" {
			return fmt.Errorf("no supported agent detected in %s; start Claude or Codex first", name)
		}
		if err := s.Arm(rec, nil, status); err != nil {
			return err
		}
		if err := s.EnsureWorker(tmuxSession); err != nil {
			return err
		}
		fmt.Printf("Automatic notifications enabled for %s; held during breaks.\n", name)
		return nil
	}
	return fmt.Errorf("usage: cs notify [status|setup|test|on <session>|off <session>|link <session> <linear-url-or-dash>]")
}
