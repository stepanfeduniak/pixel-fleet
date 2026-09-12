package main

import (
	"os"
	"strconv"
	"time"

	"github.com/stepanfeduniak/pixel-fleet/internal/blocker"
	"github.com/stepanfeduniak/pixel-fleet/internal/config"
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
	"github.com/stepanfeduniak/pixel-fleet/internal/tmux"
)

// Notification clicks must not open a replacement session with a reused name,
// or bypass the coding blocker. Missing targets return to the dashboard.
func notificationTarget(name, createdAt string, records []session.SessionRecord, blocked bool) string {
	if blocked || name == "" {
		return dashboardWindow
	}
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return dashboardWindow
	}
	for _, rec := range records {
		if rec.Name == name && rec.CreatedAt.Equal(created) {
			return name
		}
	}
	return dashboardWindow
}

func cmdFocus(mgr *session.Manager, cfg *config.Config, args []string) {
	ensureDashboard(mgr, cfg)
	name, created := "", ""
	if len(args) == 2 {
		name, created = args[0], args[1]
	}
	target := notificationTarget(name, created, session.NewStore().Load(), blocker.Load().Active(time.Now()))
	windows, _ := tmux.ListWindows(cfg.SessionName)
	_ = tmux.SelectWindow(cfg.SessionName, dashboardWindow)
	for _, window := range windows {
		if window.Name == target {
			_ = tmux.SelectWindow(cfg.SessionName, strconv.Itoa(window.Index))
			break
		}
	}
	if os.Getenv("TMUX") == "" {
		attach(cfg.SessionName)
	}
}
