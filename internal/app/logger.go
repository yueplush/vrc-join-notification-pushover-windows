package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// AppLogger appends timestamped log messages to notifier.log inside the
// installation directory. Logging is deliberately best-effort so that failures
// never interrupt monitoring behaviour.
type AppLogger struct {
	cfg *AppConfig
	mu  sync.Mutex
}

func NewAppLogger(cfg *AppConfig) *AppLogger {
	return &AppLogger{cfg: cfg}
}

func (l *AppLogger) Log(message string) {
	if l == nil || l.cfg == nil {
		return
	}
	if err := l.cfg.EnsureInstallDir(); err != nil {
		return
	}
	path := AppLogPath(l.cfg)
	if path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), message)
	_, _ = f.WriteString(line)
}

// Logf formats according to a format specifier and logs the resulting message.
func (l *AppLogger) Logf(format string, args ...interface{}) {
	l.Log(fmt.Sprintf(format, args...))
}

// OpenLogDirectory best-effort opens the installation directory using the
// default Windows file explorer.
func (l *AppLogger) OpenLogDirectory() {
	if l == nil || l.cfg == nil {
		return
	}
	path := l.cfg.InstallDir
	if path == "" {
		return
	}
	// Only attempt to launch explorer on Windows. The binary itself is
	// Windows-only, but guarding it keeps go vet and static analysis quiet when
	// building on other systems for testing.
	explorer, err := exec.LookPath("explorer.exe")
	if err != nil {
		return
	}
	proc, err := os.StartProcess(explorer, []string{"explorer.exe", filepath.Clean(path)}, &os.ProcAttr{})
	if err != nil {
		return
	}
	_ = proc.Release()
}
