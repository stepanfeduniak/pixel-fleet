package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	if _, err := os.Stat(AppPath()); err != nil {
		return errors.New("install Pixel Fleet.app with ./macos/install.sh")
	}
	return nil
}

func AppPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Applications", "Pixel Fleet.app")
}

func (s Store) Paused() bool {
	_, err := os.Stat(filepath.Join(s.Dir, "notifications-paused"))
	return err == nil
}

type NativeRequest struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	Session   string `json:"session"`
	CreatedAt string `json:"createdAt"`
}

func nativeRequest(text string, watches []Watch) NativeRequest {
	req := NativeRequest{Body: strings.TrimPrefix(text, "Pixel Fleet — ready when you are\n\n")}
	if len(watches) == 1 {
		req.Session = watches[0].Name
		req.CreatedAt = watches[0].CreatedAt.Format(time.RFC3339Nano)
	}
	return req
}

func (s Store) sendNative(text string, watches []Watch) error {
	if runtime.GOOS != "darwin" {
		return errors.New("native notifications require macOS")
	}
	if err := s.Ready(); err != nil {
		return err
	}
	inbox := filepath.Join(s.Dir, "app-inbox")
	if err := os.MkdirAll(inbox, 0700); err != nil {
		return err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	req := nativeRequest(text, watches)
	req.ID = hex.EncodeToString(id[:])
	path := filepath.Join(inbox, req.ID)
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	if err := os.Rename(path+".tmp", path+".json"); err != nil {
		return err
	}
	defer os.Remove(path + ".json")
	defer os.Remove(path + ".ack")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "/usr/bin/open", "-g", "-a", AppPath()).Run(); err != nil {
		return errors.New("could not launch Pixel Fleet.app")
	}
	for {
		if data, err := os.ReadFile(path + ".ack"); err == nil {
			if string(data) == "ok" {
				return nil
			}
			return fmt.Errorf("Pixel Fleet.app: %s", strings.TrimSpace(string(data)))
		}
		select {
		case <-ctx.Done():
			return errors.New("Pixel Fleet.app did not acknowledge notification; open the app and allow notifications")
		case <-time.After(100 * time.Millisecond):
		}
	}
}
