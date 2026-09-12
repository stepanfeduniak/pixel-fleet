package main

import (
	"github.com/stepanfeduniak/pixel-fleet/internal/session"
	"testing"
	"time"
)

func TestNotificationTarget(t *testing.T) {
	now := time.Now()
	records := []session.SessionRecord{{Name: "agent's task:1", CreatedAt: now}}
	for _, tc := range []struct {
		name, created string
		blocked       bool
		want          string
	}{
		{records[0].Name, now.Format(time.RFC3339Nano), false, records[0].Name},
		{records[0].Name, now.Format(time.RFC3339Nano), true, dashboardWindow},
		{records[0].Name, now.Add(-time.Second).Format(time.RFC3339Nano), false, dashboardWindow},
		{"removed", now.Format(time.RFC3339Nano), false, dashboardWindow},
		{"", "", false, dashboardWindow},
	} {
		if got := notificationTarget(tc.name, tc.created, records, tc.blocked); got != tc.want {
			t.Fatalf("%+v: got %q", tc, got)
		}
	}
}
