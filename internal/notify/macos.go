package notify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func (s Store) Channel() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, "notification-channel"))
	if errors.Is(err, os.ErrNotExist) {
		if runtime.GOOS == "darwin" {
			return "macos", nil
		}
		return "slack", nil
	}
	if err != nil {
		return "", err
	}
	channel := strings.TrimSpace(string(data))
	if channel != "macos" && channel != "slack" {
		return "", errors.New("notification-channel must be macos or slack")
	}
	return channel, nil
}

func (s Store) SetChannel(channel string) error {
	if channel != "macos" && channel != "slack" {
		return errors.New("choose macos or slack")
	}
	if channel == "macos" && runtime.GOOS != "darwin" {
		return errors.New("native notifications require macOS")
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(s.Dir, "notification-channel")
	if err := os.WriteFile(path+".tmp", []byte(channel), 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func (s Store) Ready() error {
	channel, err := s.Channel()
	if err != nil {
		return err
	}
	if channel == "slack" {
		_, err := s.Webhook()
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("native notifications require macOS")
	}
	if _, err := os.Stat("/usr/bin/osascript"); err != nil {
		return errors.New("macOS notification helper /usr/bin/osascript is unavailable")
	}
	return nil
}

// Message content is passed as argv, never interpolated into AppleScript.
const notificationScript = `on run argv
 display notification (item 1 of argv) with title "Pixel Fleet"
end run`

func nativeCommand(ctx context.Context, text string) *exec.Cmd {
	return exec.CommandContext(ctx, "/usr/bin/osascript", "-e", notificationScript, text)
}

func sendNative(text string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("native notifications require macOS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	text = strings.TrimPrefix(text, "Pixel Fleet — ready when you are\n\n")
	if err := nativeCommand(ctx, text).Run(); err != nil {
		return fmt.Errorf("macOS notification failed; check notification permissions and try cs notify test")
	}
	// AppleScript confirms submission only. Focus and notification settings
	// can suppress presentation, which must be verified with a visible test.
	return nil
}
