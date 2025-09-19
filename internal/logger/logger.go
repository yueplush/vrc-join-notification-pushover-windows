package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vrchat-join-notification-with-pushover/internal/config"
	"vrchat-join-notification-with-pushover/internal/core"
)

// Logger writes timestamped lines to the notifier log.
type Logger struct {
	mu   sync.Mutex
	path string
}

// New creates a logger bound to the configuration install directory.
func New(cfg *config.Config) *Logger {
	path := ""
	if cfg != nil {
		if err := config.EnsureDir(cfg.InstallDir); err == nil {
			path = filepath.Join(cfg.InstallDir, core.AppLogName)
		}
	}
	return &Logger{path: path}
}

// Log writes a line to the log file and prints it to stdout.
func (l *Logger) Log(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("[%s] %s", timestamp, message)
	fmt.Println(line)
	if l.path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line + "\n")
}
