package logwatcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"vrchat-join-notification-with-pushover/internal/config"
	"vrchat-join-notification-with-pushover/internal/core"
	"vrchat-join-notification-with-pushover/internal/logger"
)

// EventType classifies monitor events.
type EventType int

const (
	EventStatus EventType = iota
	EventError
	EventLogSwitch
	EventRoomEnter
	EventRoomLeft
	EventSelfJoin
	EventPlayerJoin
	EventPlayerLeft
)

// Event carries monitor events to the session tracker.
type Event struct {
	Type    EventType
	Message string
	Path    string
	Room    *core.RoomEvent
	Player  *core.PlayerEvent
	Raw     string
}

// Monitor tails VRChat logs and emits structured events.
type Monitor struct {
	cfg    *config.Config
	log    *logger.Logger
	events chan Event

	reSelf  *regexp.Regexp
	reJoin  *regexp.Regexp
	reLeave *regexp.Regexp
}

// New creates a new log monitor.
func New(cfg *config.Config, log *logger.Logger, events chan Event) *Monitor {
	return &Monitor{
		cfg:     cfg,
		log:     log,
		events:  events,
		reSelf:  regexp.MustCompile(`(?i)\[Behaviour\].*OnJoinedRoom\b`),
		reJoin:  regexp.MustCompile(`(?i)\[Behaviour\].*OnPlayerJoined\b`),
		reLeave: regexp.MustCompile(`(?i)\[Behaviour\].*OnPlayerLeft\b`),
	}
}

// Run begins monitoring until the context is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	defer close(m.events)
	var lastDirWarning time.Time
	var lastNoFileWarning time.Time
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		logDir := ""
		if m.cfg != nil {
			logDir = strings.TrimSpace(m.cfg.VRChatLogDir)
		}
		if logDir == "" || !isDir(logDir) {
			if time.Since(lastDirWarning) > 10*time.Second {
				message := fmt.Sprintf("Waiting for VRChat log directory at %s", valueOrUnset(logDir))
				m.emit(Event{Type: EventStatus, Message: message})
				lastDirWarning = time.Now()
			}
			if waitFor(ctx, time.Second) {
				return
			}
			continue
		}
		newest := core.GetNewestLogPath(logDir)
		if newest == "" {
			if time.Since(lastNoFileWarning) > 10*time.Second {
				m.emit(Event{Type: EventStatus, Message: fmt.Sprintf("No log files found in %s", logDir)})
				lastNoFileWarning = time.Now()
			}
			if waitFor(ctx, time.Second) {
				return
			}
			continue
		}
		if err := m.followFile(ctx, newest, logDir); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			m.emit(Event{Type: EventError, Message: err.Error()})
			if waitFor(ctx, 2*time.Second) {
				return
			}
		}
	}
}

func (m *Monitor) followFile(ctx context.Context, path, logDir string) error {
	normalized, err := filepath.Abs(path)
	if err != nil {
		normalized = filepath.Clean(path)
	}
	m.emit(Event{Type: EventLogSwitch, Path: normalized})
	var lastSize int64
	if info, err := os.Stat(normalized); err == nil {
		lastSize = info.Size()
	}
	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
		}
		file, err := os.Open(normalized)
		if err != nil {
			m.emit(Event{Type: EventError, Message: fmt.Sprintf("Failed to open log '%s': %v", normalized, err)})
			if waitFor(ctx, time.Second) {
				return context.Canceled
			}
			continue
		}
		if lastSize > 0 {
			if _, err := file.Seek(lastSize, io.SeekStart); err != nil {
				lastSize = 0
				_, _ = file.Seek(0, io.SeekStart)
			}
		}
		reader := bufio.NewReader(file)
		for {
			select {
			case <-ctx.Done():
				file.Close()
				return context.Canceled
			default:
			}
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				trimmed := strings.TrimRight(line, "\r\n")
				lastSize += int64(len(line))
				m.processLine(trimmed)
			}
			if errors.Is(err, io.EOF) {
				fileStat, statErr := os.Stat(normalized)
				if statErr != nil {
					file.Close()
					if waitFor(ctx, 600*time.Millisecond) {
						return context.Canceled
					}
					break
				}
				currentSize := fileStat.Size()
				if currentSize < lastSize {
					lastSize = 0
					file.Close()
					break
				}
				newest := core.GetNewestLogPath(logDir)
				if newest != "" && !sameFile(normalized, newest) {
					file.Close()
					return nil
				}
				if waitFor(ctx, 600*time.Millisecond) {
					file.Close()
					return context.Canceled
				}
				continue
			}
			if err != nil {
				file.Close()
				m.emit(Event{Type: EventError, Message: fmt.Sprintf("Log read error: %v", err)})
				if waitFor(ctx, 2*time.Second) {
					return context.Canceled
				}
				break
			}
		}
		file.Close()
	}
}

func (m *Monitor) processLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	safeLine := core.StripZeroWidth(line)
	safeLine = strings.ReplaceAll(safeLine, "||", "|")
	lower := strings.ToLower(safeLine)
	if strings.Contains(lower, "onleftroom") {
		m.emit(Event{Type: EventRoomLeft})
		return
	}
	if room := core.ParseRoomTransitionLine(safeLine); room != nil {
		m.emit(Event{Type: EventRoomEnter, Room: room})
		return
	}
	if m.reSelf.MatchString(safeLine) {
		m.emit(Event{Type: EventSelfJoin, Raw: safeLine})
		return
	}
	if m.reLeave.MatchString(safeLine) {
		parsed := core.ParsePlayerEventLine(safeLine, "OnPlayerLeft")
		if parsed == nil {
			parsed = &core.PlayerEvent{}
		}
		if parsed.RawLine == "" {
			parsed.RawLine = safeLine
		}
		m.emit(Event{Type: EventPlayerLeft, Player: parsed})
		return
	}
	if m.reJoin.MatchString(safeLine) {
		parsed := core.ParsePlayerEventLine(safeLine, "OnPlayerJoined")
		if parsed == nil {
			parsed = &core.PlayerEvent{}
		}
		if parsed.RawLine == "" {
			parsed.RawLine = safeLine
		}
		m.emit(Event{Type: EventPlayerJoin, Player: parsed})
	}
}

func (m *Monitor) emit(event Event) {
	if m.events == nil {
		return
	}
	m.events <- event
}

func waitFor(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func sameFile(a, b string) bool {
	aClean, _ := filepath.Abs(a)
	bClean, _ := filepath.Abs(b)
	return aClean == bClean
}

func valueOrUnset(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(unset)"
	}
	return value
}
