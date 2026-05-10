package config

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	RemoteBase        string        `yaml:"remote_base"`
	LocalBase         string        `yaml:"local_base"`
	LocalExtraPaths   []string      `yaml:"local_extra_paths"`
	RefreshInterval   time.Duration `yaml:"refresh_interval"`
	ClaudeBin         string        `yaml:"claude_bin"`
	RemoteClaudeBin   string        `yaml:"remote_claude_bin"`
	CodexBin          string        `yaml:"codex_bin"`
	RemoteCodexBin    string        `yaml:"remote_codex_bin"`
	SessionName       string        `yaml:"session_name"`
	ScanTimeout       time.Duration `yaml:"scan_timeout"`
	// DiscoveryInterval controls how often the dashboard re-scans known
	// machines in the background for orphaned cs sessions (alive on a
	// remote, missing from the local store). Set to 0 to disable.
	DiscoveryInterval time.Duration `yaml:"discovery_interval"`
	RemoteTmuxSession string        `yaml:"remote_tmux_session"`
	// RemoteControl defaults to true. Use a pointer so YAML can distinguish
	// "unset" (→ default true) from "explicitly false".
	RemoteControl *bool `yaml:"remote_control"`
}

func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	enabled := true
	return &Config{
		RemoteBase:        "~",
		LocalBase:         home,
		RefreshInterval:   2 * time.Second,
		ClaudeBin:         "claude",
		RemoteClaudeBin:   "claude",
		CodexBin:          "codex",
		RemoteCodexBin:    "codex",
		SessionName:       "cs",
		ScanTimeout:       5 * time.Second,
		DiscoveryInterval: 60 * time.Second,
		RemoteTmuxSession: "cs-remote",
		RemoteControl:     &enabled,
	}
}

func Load() *Config {
	cfg := DefaultConfig()

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg
	}

	configPath := filepath.Join(home, ".config", "cs", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
	}

	_ = yaml.Unmarshal(data, cfg)

	// Ensure defaults for empty fields
	if cfg.SessionName == "" {
		cfg.SessionName = "cs"
	}
	if cfg.ClaudeBin == "" {
		cfg.ClaudeBin = "claude"
	}
	if cfg.RemoteClaudeBin == "" {
		cfg.RemoteClaudeBin = "claude"
	}
	if cfg.CodexBin == "" {
		cfg.CodexBin = "codex"
	}
	if cfg.RemoteCodexBin == "" {
		cfg.RemoteCodexBin = "codex"
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = 2 * time.Second
	}
	if cfg.ScanTimeout == 0 {
		cfg.ScanTimeout = 5 * time.Second
	}
	// DiscoveryInterval: 0 in YAML means "use default (60s)". Users who
	// genuinely want to disable auto-discovery should set a negative value
	// like -1s, which we preserve here as "off".
	if cfg.DiscoveryInterval == 0 {
		cfg.DiscoveryInterval = 60 * time.Second
	}
	if cfg.RemoteTmuxSession == "" {
		cfg.RemoteTmuxSession = "cs-remote"
	}
	if cfg.RemoteControl == nil {
		enabled := true
		cfg.RemoteControl = &enabled
	}

	return cfg
}

// IsRemoteControlEnabled returns whether Claude should be launched with the
// --remote-control flag by default for sessions on this config.
func (c *Config) IsRemoteControlEnabled() bool {
	if c.RemoteControl == nil {
		return true
	}
	return *c.RemoteControl
}
