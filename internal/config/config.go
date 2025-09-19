package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"vrchat-join-notification-with-pushover/internal/core"
)

// Config holds persisted application settings.
type Config struct {
	InstallDir    string `json:"InstallDir"`
	VRChatLogDir  string `json:"VRChatLogDir"`
	PushoverUser  string `json:"PushoverUser,omitempty"`
	PushoverToken string `json:"PushoverToken,omitempty"`

	firstRun bool
	path     string
}

// Load reads the configuration, creating defaults if necessary.
func Load() (*Config, error) {
	cfg := &Config{}
	cfg.InstallDir = DefaultInstallDir()
	cfg.VRChatLogDir = GuessVRChatLogDir()
	cfg.path = filepath.Join(cfg.InstallDir, core.ConfigFileName)

	if err := EnsureDir(cfg.InstallDir); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(cfg.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.firstRun = true
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	if len(data) == 0 {
		cfg.firstRun = true
		return cfg, nil
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse config: %w", err)
	}
	cfg.InstallDir = ExpandPath(cfg.InstallDir)
	cfg.VRChatLogDir = ExpandPath(cfg.VRChatLogDir)
	cfg.path = filepath.Join(cfg.InstallDir, core.ConfigFileName)
	if err := EnsureDir(cfg.InstallDir); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Save writes the configuration to disk.
func (c *Config) Save() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if strings.TrimSpace(c.InstallDir) == "" {
		return errors.New("install directory is required")
	}
	c.InstallDir = ExpandPath(c.InstallDir)
	c.VRChatLogDir = ExpandPath(c.VRChatLogDir)
	if err := EnsureDir(c.InstallDir); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(struct {
		InstallDir    string `json:"InstallDir"`
		VRChatLogDir  string `json:"VRChatLogDir"`
		PushoverUser  string `json:"PushoverUser,omitempty"`
		PushoverToken string `json:"PushoverToken,omitempty"`
	}{
		InstallDir:    c.InstallDir,
		VRChatLogDir:  c.VRChatLogDir,
		PushoverUser:  strings.TrimSpace(c.PushoverUser),
		PushoverToken: strings.TrimSpace(c.PushoverToken),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	path := c.path
	if path == "" {
		path = filepath.Join(c.InstallDir, core.ConfigFileName)
		c.path = path
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	c.firstRun = false
	return nil
}

// FirstRun indicates whether this is the initial configuration load.
func (c *Config) FirstRun() bool {
	if c == nil {
		return true
	}
	return c.firstRun
}

// ConfigPath returns the full path to the settings file.
func (c *Config) ConfigPath() string {
	if c == nil {
		return ""
	}
	if c.path != "" {
		return c.path
	}
	return filepath.Join(c.InstallDir, core.ConfigFileName)
}

// EnsureDir creates the provided directory if necessary.
func EnsureDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("empty path")
	}
	return os.MkdirAll(path, 0o755)
}

// ExpandPath expands environment variables, ~ and returns an absolute path.
func ExpandPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	expanded := os.ExpandEnv(trimmed)
	if strings.HasPrefix(expanded, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			expanded = filepath.Join(home, strings.TrimPrefix(expanded, "~"))
		}
	}
	expanded = filepath.Clean(expanded)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return expanded
	}
	return abs
}

// DefaultInstallDir returns the platform-specific default configuration root.
func DefaultInstallDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if runtime.GOOS == "windows" {
			if home := os.Getenv("USERPROFILE"); home != "" {
				base = filepath.Join(home, "AppData", "Local")
			}
		}
	}
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".local", "share")
		}
	}
	return ExpandPath(filepath.Join(base, "VRChatJoinNotificationWithPushover"))
}

// GuessVRChatLogDir attempts to locate the VRChat log directory on Windows installations.
func GuessVRChatLogDir() string {
	localLow := getLocalLowFolder()
	doc := getDocumentsFolder()
	candidates := []string{
		filepath.Join(localLow, "VRChat", "VRChat"),
		filepath.Join(doc, "VRChat", "VRChat"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ExpandPath(filepath.Join(localLow, "VRChat", "VRChat"))
}

func getLocalLowFolder() string {
	local := os.Getenv("LOCALAPPDATA")
	if local != "" {
		if strings.EqualFold(filepath.Base(local), "Local") {
			return ExpandPath(filepath.Join(filepath.Dir(local), "LocalLow"))
		}
	}
	home := os.Getenv("USERPROFILE")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	if home == "" {
		return ExpandPath(filepath.Join("C:\\", "Users", "Public", "AppData", "LocalLow"))
	}
	return ExpandPath(filepath.Join(home, "AppData", "LocalLow"))
}

func getDocumentsFolder() string {
	doc := os.Getenv("USERPROFILE")
	if doc != "" {
		return ExpandPath(filepath.Join(doc, "Documents"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		return ExpandPath(filepath.Join(home, "Documents"))
	}
	return ExpandPath(filepath.Join("C:\\", "Users", "Public", "Documents"))
}
