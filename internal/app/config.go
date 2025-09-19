package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	configFileName  = "config.json"
	pointerFileName = "config-location.txt"
	appLogName      = "notifier.log"
)

// AppConfig mirrors the JSON configuration used by the original Python
// implementation. It stores persistent settings such as the installation
// directory, the VRChat log directory and optional Pushover credentials.
type AppConfig struct {
	InstallDir    string
	VRChatLogDir  string
	PushoverUser  string
	PushoverToken string
	FirstRun      bool
}

// LoadConfig restores the configuration from disk. It returns the populated
// configuration instance together with an optional warning string describing
// non fatal load issues.
func LoadConfig() (*AppConfig, string, error) {
	storageRoot := defaultStorageRoot()
	if err := os.MkdirAll(storageRoot, 0o755); err != nil {
		return nil, "", fmt.Errorf("create storage root: %w", err)
	}

	installDir := storageRoot
	pointerCandidates := []string{filepath.Join(storageRoot, pointerFileName)}
	for _, legacy := range legacyStorageRoots() {
		if legacy != storageRoot {
			pointerCandidates = append(pointerCandidates, filepath.Join(legacy, pointerFileName))
		}
	}
	for _, candidate := range pointerCandidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		resolved := expandPath(strings.TrimSpace(string(data)))
		if stat, err := os.Stat(resolved); err == nil && stat.IsDir() {
			installDir = resolved
			break
		}
	}

	configPath := filepath.Join(installDir, configFileName)
	fallbackPath := filepath.Join(storageRoot, configFileName)

	configExists := fileExists(configPath)
	fallbackExists := fileExists(fallbackPath)

	if !configExists && !fallbackExists {
		for _, legacy := range legacyStorageRoots() {
			legacyConfig := filepath.Join(legacy, configFileName)
			if fileExists(legacyConfig) {
				installDir = legacy
				configPath = legacyConfig
				configExists = true
				break
			}
		}
	}

	payload := map[string]string{}
	firstRun := !(configExists || fallbackExists)
	var loadWarning string

	if configExists {
		if err := loadConfigFile(configPath, payload); err != nil {
			loadWarning = err.Error()
			payload = map[string]string{}
		}
	} else if installDir != storageRoot && fallbackExists {
		if err := loadConfigFile(fallbackPath, payload); err != nil {
			loadWarning = err.Error()
			payload = map[string]string{}
		} else {
			installDir = storageRoot
		}
	}

	cfg := &AppConfig{
		InstallDir:    expandPath(valueOr(payload, "InstallDir", installDir)),
		VRChatLogDir:  expandPath(valueOr(payload, "VRChatLogDir", guessVRChatLogDir())),
		PushoverUser:  strings.TrimSpace(valueOr(payload, "PushoverUser", "")),
		PushoverToken: strings.TrimSpace(valueOr(payload, "PushoverToken", "")),
		FirstRun:      firstRun,
	}

	legacyRoots := legacyStorageRoots()
	if len(legacyRoots) > 0 {
		primaryLegacy := filepath.Clean(legacyRoots[0])
		if filepath.Clean(cfg.InstallDir) == primaryLegacy && primaryLegacy != filepath.Clean(storageRoot) {
			newConfig := filepath.Join(storageRoot, configFileName)
			if fileExists(newConfig) {
				cfg.InstallDir = storageRoot
			} else {
				original := cfg.InstallDir
				cfg.InstallDir = storageRoot
				if err := cfg.Save(); err != nil {
					cfg.InstallDir = original
				}
			}
		}
	}

	if err := cfg.EnsureInstallDir(); err != nil {
		return nil, "", err
	}
	_ = cfg.writePointer()

	return cfg, loadWarning, nil
}

func (c *AppConfig) ConfigPath() string {
	return filepath.Join(c.InstallDir, configFileName)
}

func (c *AppConfig) EnsureInstallDir() error {
	if c.InstallDir == "" {
		return errors.New("install directory is empty")
	}
	return os.MkdirAll(c.InstallDir, 0o755)
}

func (c *AppConfig) Save() error {
	if err := c.EnsureInstallDir(); err != nil {
		return err
	}
	payload := map[string]string{
		"InstallDir":   expandPath(c.InstallDir),
		"VRChatLogDir": expandPath(c.VRChatLogDir),
	}
	if strings.TrimSpace(c.PushoverUser) != "" {
		payload["PushoverUser"] = strings.TrimSpace(c.PushoverUser)
	}
	if strings.TrimSpace(c.PushoverToken) != "" {
		payload["PushoverToken"] = strings.TrimSpace(c.PushoverToken)
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.WriteFile(c.ConfigPath(), data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := c.writePointer(); err != nil {
		return err
	}
	c.FirstRun = false
	return nil
}

func (c *AppConfig) writePointer() error {
	storageRoot := defaultStorageRoot()
	if err := os.MkdirAll(storageRoot, 0o755); err != nil {
		return err
	}
	pointerPath := filepath.Join(storageRoot, pointerFileName)
	return os.WriteFile(pointerPath, []byte(expandPath(c.InstallDir)), 0o644)
}

func loadConfigFile(path string, payload map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("failed to parse settings: %w", err)
	}
	for key, value := range raw {
		switch v := value.(type) {
		case string:
			payload[key] = v
		case fmt.Stringer:
			payload[key] = v.String()
		default:
			payload[key] = fmt.Sprintf("%v", v)
		}
	}
	return nil
}

func valueOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func expandPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	expanded := os.ExpandEnv(trimmed)
	if strings.HasPrefix(expanded, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			expanded = filepath.Join(home, strings.TrimPrefix(expanded, "~"))
		}
	}
	if abs, err := filepath.Abs(expanded); err == nil {
		return abs
	}
	return filepath.Clean(expanded)
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	if stat, err := os.Stat(path); err == nil {
		return !stat.IsDir()
	}
	return false
}

func directoryExists(path string) bool {
	if path == "" {
		return false
	}
	if stat, err := os.Stat(path); err == nil {
		return stat.IsDir()
	}
	return false
}

func defaultStorageRoot() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(windowsLocalAppData(), "VRChatJoinNotificationWithPushover")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "vrchat-join-notification-with-pushover")
}

func legacyStorageRoots() []string {
	var roots []string
	if runtime.GOOS == "windows" {
		localAppData := windowsLocalAppData()
		roots = append(roots,
			filepath.Join(localAppData, "vrchat-join-notification-with-pushover"),
			filepath.Join(localAppData, "VRChatJoinNotifier"),
		)
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			roots = append(roots, expandPath(filepath.Join(appdata, "VRChatJoinNotifier")))
		}
		home, err := os.UserHomeDir()
		if err == nil {
			roots = append(roots, filepath.Join(home, ".local", "share", "vrchat-join-notification-with-pushover"))
		}
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			roots = append(roots, filepath.Join(home, ".local", "share", "VRChatJoinNotifier"))
		}
	}
	dedupe := map[string]struct{}{}
	var result []string
	for _, root := range roots {
		resolved := expandPath(root)
		if _, ok := dedupe[resolved]; ok {
			continue
		}
		dedupe[resolved] = struct{}{}
		result = append(result, resolved)
	}
	return result
}

func windowsLocalAppData() string {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return expandPath(v)
	}
	profile := os.Getenv("USERPROFILE")
	if profile == "" {
		if home, err := os.UserHomeDir(); err == nil {
			profile = home
		}
	}
	if profile == "" {
		return ""
	}
	return expandPath(filepath.Join(profile, "AppData", "Local"))
}

func windowsLocalLowDir() string {
	candidates := []string{}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		candidates = append(candidates, filepath.Join(local, "..", "LocalLow"))
	}
	if profile := os.Getenv("USERPROFILE"); profile != "" {
		candidates = append(candidates, filepath.Join(profile, "AppData", "LocalLow"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "AppData", "LocalLow"))
	}
	for _, candidate := range candidates {
		resolved := expandPath(candidate)
		if directoryExists(resolved) {
			return resolved
		}
	}
	if len(candidates) > 0 {
		return expandPath(candidates[0])
	}
	return ""
}

func guessVRChatLogDir() string {
	if runtime.GOOS == "windows" {
		base := windowsLocalLowDir()
		if base == "" {
			return ""
		}
		candidates := []string{
			filepath.Join(base, "VRChat", "VRChat"),
		}
		for _, candidate := range candidates {
			resolved := expandPath(candidate)
			if directoryExists(resolved) {
				return resolved
			}
		}
		if len(candidates) > 0 {
			return expandPath(candidates[0])
		}
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(home, ".steam", "steam", "steamapps", "compatdata", "438100", "pfx", "drive_c", "users", "steamuser", "AppData", "LocalLow", "VRChat", "VRChat"),
		filepath.Join(home, ".local", "share", "Steam", "steamapps", "compatdata", "438100", "pfx", "drive_c", "users", "steamuser", "AppData", "LocalLow", "VRChat", "VRChat"),
	}
	for _, candidate := range candidates {
		resolved := expandPath(candidate)
		if directoryExists(resolved) {
			return resolved
		}
	}
	if len(candidates) > 0 {
		return expandPath(candidates[0])
	}
	return ""
}

func AppLogPath(cfg *AppConfig) string {
	return filepath.Join(cfg.InstallDir, appLogName)
}
