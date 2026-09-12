package notify

import (
	"context"
	"runtime"
	"testing"
)

func TestNativeMessageIsDataNotAppleScript(t *testing.T) {
	text := "a\" & do shell script \"touch /tmp/should-not-exist\"\n$(something) `anything`"
	cmd := nativeCommand(context.Background(), text)
	if len(cmd.Args) != 4 || cmd.Args[2] != notificationScript || cmd.Args[3] != text {
		t.Fatalf("message interpolated into command: %#v", cmd.Args)
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
