package notify

import (
	"runtime"
	"testing"
	"time"
)

func TestNativeRequestPreservesExactTarget(t *testing.T) {
	now := time.Now()
	name := "agent's task; $(anything)"
	req := nativeRequest("message", []Watch{{Name: name, CreatedAt: now}})
	if req.Session != name || req.CreatedAt != now.Format(time.RFC3339Nano) {
		t.Fatal("target identity lost")
	}
	if req := nativeRequest("digest", []Watch{{Name: "one"}, {Name: "two"}}); req.Session != "" {
		t.Fatal("digest must open dashboard")
	}
}

func TestDefaultChannelAndExplicitSlack(t *testing.T) {
	s := Store{t.TempDir()}
	want := "slack"
	if runtime.GOOS == "darwin" {
		want = "macos"
	}
	channel, err := s.Channel()
	if err != nil || channel != want {
		t.Fatalf("default = %q, %v", channel, err)
	}
	if err := s.SetChannel("slack"); err != nil {
		t.Fatal(err)
	}
	channel, err = s.Channel()
	if err != nil || channel != "slack" {
		t.Fatalf("explicit = %q, %v", channel, err)
	}
	if err := s.Ready(); err == nil {
		t.Fatal("Slack ready without webhook")
	}
	if err := s.SetChannel("arbitrary"); err == nil {
		t.Fatal("invalid channel accepted")
	}
}
