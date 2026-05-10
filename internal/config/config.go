package config

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// AppBins configures per-app overrides. Used by the generic `apps:` map
// for apps that don't have a hardcoded well-known config field (Claude /
// Codex still keep theirs for backward-compat). Despite the historical
// name, this struct now carries more than just bin paths.
type AppBins struct {
	LocalBin  string `yaml:"local_bin"`
	RemoteBin string `yaml:"remote_bin"`
	// RequiresPath, if non-nil, overrides the app's built-in
	// RequiresPath() default. Use a pointer so YAML can distinguish
	// "unset" (→ defer to the app) from explicit true/false. Example:
	//
	//   apps:
	//     claude:
	//       requires_path: false   # let me launch claude without picking a project
	RequiresPath *bool `yaml:"requires_path"`
}

type Config struct {
	RemoteBase        string        `yaml:"remote_base"`
	LocalBase         string        `yaml:"local_base"`
	LocalExtraPaths   []string      `yaml:"local_extra_paths"`
	RefreshInterval   time.Duration `yaml:"refresh_interval"`
	// Well-known per-app binary overrides. Kept for backward-compat with
	// existing config.yaml files; the generic Apps map below covers
	// anything else (including out-of-tree apps).
	ClaudeBin       string `yaml:"claude_bin"`
	RemoteClaudeBin string `yaml:"remote_claude_bin"`
	CodexBin        string `yaml:"codex_bin"`
	RemoteCodexBin  string `yaml:"remote_codex_bin"`
	// Apps is a generic per-app override map keyed by canonical app name.
	// Example:
	//   apps:
	//     aider:
	//       local_bin: aider
	//       remote_bin: /opt/aider/bin/aider
	Apps              map[string]AppBins `yaml:"apps"`
	SessionName       string             `yaml:"session_name"`
	ScanTimeout       time.Duration      `yaml:"scan_timeout"`
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

// RequiresPathFor returns the effective RequiresPath value for an app:
// the user's config override if set, otherwise the app's own default.
// Used by the dashboard form, CLI dispatch, and session launch so a
// single config knob (`apps.<name>.requires_path: false`) flips path
// requirement everywhere consistently.
func (c *Config) RequiresPathFor(appName string, defaultVal bool) bool {
	if c.Apps == nil {
		return defaultVal
	}
	o, ok := c.Apps[appName]
	if !ok || o.RequiresPath == nil {
		return defaultVal
	}
	return *o.RequiresPath
}

// BinFor returns the configured binary path for an app on either the local
// (remote=false) or remote machine. Returns "" when the user hasn't set an
// override — the caller (session.Manager) then falls back to the app's own
// DefaultLocalBin / DefaultRemoteBin.
//
// The well-known fields (ClaudeBin, CodexBin, …) take precedence so that
// existing config.yaml files keep working unchanged. The generic Apps map
// is the fallback path for new and out-of-tree apps.
func (c *Config) BinFor(appName string, remote bool) string {
	switch appName {
	case "claude":
		if remote {
			return c.RemoteClaudeBin
		}
		return c.ClaudeBin
	case "codex":
		if remote {
			return c.RemoteCodexBin
		}
		return c.CodexBin
	}
	if c.Apps == nil {
		return ""
	}
	bins, ok := c.Apps[appName]
	if !ok {
		return ""
	}
	if remote {
		return bins.RemoteBin
	}
	return bins.LocalBin
}
