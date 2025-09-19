package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type MonitorEventType string

const (
	EventStatus     MonitorEventType = "status"
	EventLogSwitch  MonitorEventType = "log_switch"
	EventError      MonitorEventType = "error"
	EventRoomEnter  MonitorEventType = "room_enter"
	EventRoomLeft   MonitorEventType = "room_left"
	EventSelfJoin   MonitorEventType = "self_join"
	EventPlayerJoin MonitorEventType = "player_join"
	EventPlayerLeft MonitorEventType = "player_left"
)

type MonitorEvent struct {
	Type    MonitorEventType
	Message string
	Room    RoomEvent
	Player  PlayerEvent
	Path    string
}

// LogMonitor tails the VRChat log files and emits parsed events to the GUI.
type LogMonitor struct {
	cfg    *AppConfig
	logger *AppLogger
	events chan<- MonitorEvent

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

func NewLogMonitor(cfg *AppConfig, logger *AppLogger, events chan<- MonitorEvent) *LogMonitor {
	return &LogMonitor{
		cfg:    cfg,
		logger: logger,
		events: events,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

func (m *LogMonitor) Start() {
	go m.run()
}

func (m *LogMonitor) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
	select {
	case <-m.doneCh:
	case <-time.After(2 * time.Second):
	}
}

func (m *LogMonitor) emit(event MonitorEvent) {
	select {
	case <-m.stopCh:
		return
	case m.events <- event:
	}
}

var (
	behaviourSelfRegex  = regexp.MustCompile(`(?i)\[Behaviour\].*OnJoinedRoom\b`)
	behaviourJoinRegex  = regexp.MustCompile(`(?i)\[Behaviour\].*OnPlayerJoined\b`)
	behaviourLeaveRegex = regexp.MustCompile(`(?i)\[Behaviour\].*OnPlayerLeft\b`)
)

func (m *LogMonitor) run() {
	defer close(m.doneCh)
	var lastDirWarning time.Time
	var lastNoFileWarning time.Time
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}
		logDir := strings.TrimSpace(m.cfg.VRChatLogDir)
		if logDir == "" || !directoryExists(logDir) {
			if time.Since(lastDirWarning) > 10*time.Second {
				m.emit(MonitorEvent{Type: EventStatus, Message: "Waiting for VRChat log directory at " + logDir})
				lastDirWarning = time.Now()
			}
			if m.waitForStop(1 * time.Second) {
				return
			}
			continue
		}
		newest := getNewestLogPath(logDir)
		if newest == "" {
			if time.Since(lastNoFileWarning) > 10*time.Second {
				m.emit(MonitorEvent{Type: EventStatus, Message: "No log files found in " + logDir})
				lastNoFileWarning = time.Now()
			}
			if m.waitForStop(1 * time.Second) {
				return
			}
			continue
		}
		if m.followFile(newest, logDir) {
			return
		}
	}
}

func (m *LogMonitor) waitForStop(d time.Duration) bool {
	select {
	case <-m.stopCh:
		return true
	case <-time.After(d):
		return false
	}
}

func (m *LogMonitor) followFile(path string, logDir string) bool {
	normalized := filepath.Clean(path)
	m.emit(MonitorEvent{Type: EventLogSwitch, Path: normalized})
	var lastSize int64
	if info, err := os.Stat(normalized); err == nil {
		lastSize = info.Size()
	}
	for {
		select {
		case <-m.stopCh:
			return true
		default:
		}
		file, err := os.Open(normalized)
		if err != nil {
			if m.logger != nil {
				m.logger.Logf("Failed reading log '%s': %v", normalized, err)
			}
			m.emit(MonitorEvent{Type: EventError, Message: "Log read error: " + err.Error()})
			if m.waitForStop(2 * time.Second) {
				return true
			}
			continue
		}
		reader := bufio.NewReader(file)
		if _, err := file.Seek(lastSize, io.SeekStart); err != nil {
			lastSize = 0
			file.Seek(0, io.SeekStart)
		}
		for {
			select {
			case <-m.stopCh:
				file.Close()
				return true
			default:
			}
			position, _ := file.Seek(0, io.SeekCurrent)
			line, err := reader.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					if m.waitForStop(600 * time.Millisecond) {
						file.Close()
						return true
					}
					info, statErr := os.Stat(normalized)
					if statErr != nil {
						file.Close()
						time.Sleep(600 * time.Millisecond)
						break
					}
					if info.Size() < lastSize {
						lastSize = 0
						file.Seek(0, io.SeekStart)
						reader.Reset(file)
						continue
					}
					newest := getNewestLogPath(logDir)
					if newest != "" && filepath.Clean(newest) != normalized {
						file.Close()
						return false
					}
					file.Seek(position, io.SeekStart)
					reader.Reset(file)
					continue
				}
				file.Close()
				if m.logger != nil {
					m.logger.Logf("Failed reading log '%s': %v", normalized, err)
				}
				m.emit(MonitorEvent{Type: EventError, Message: "Log read error: " + err.Error()})
				if m.waitForStop(2 * time.Second) {
					return true
				}
				break
			}
			lastSize += int64(len(line))
			trimmed := strings.TrimRight(line, "\r\n")
			m.processLine(trimmed)
		}
		file.Close()
	}
}

func (m *LogMonitor) processLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	safeLine := strings.ReplaceAll(stripZeroWidth(line), "||", "|")
	lowerLine := strings.ToLower(safeLine)
	if strings.Contains(lowerLine, "onleftroom") {
		m.emit(MonitorEvent{Type: EventRoomLeft, Message: safeLine})
		return
	}
	if room, ok := parseRoomTransitionLine(safeLine); ok {
		m.emit(MonitorEvent{Type: EventRoomEnter, Room: room})
		return
	}
	if behaviourSelfRegex.MatchString(safeLine) {
		m.emit(MonitorEvent{Type: EventSelfJoin, Message: safeLine})
		return
	}
	if behaviourLeaveRegex.MatchString(safeLine) {
		if player, ok := parsePlayerEventLine(safeLine, "OnPlayerLeft"); ok {
			m.emit(MonitorEvent{Type: EventPlayerLeft, Player: player})
		} else {
			m.emit(MonitorEvent{Type: EventPlayerLeft, Player: PlayerEvent{RawLine: safeLine}})
		}
		return
	}
	if behaviourJoinRegex.MatchString(safeLine) {
		if player, ok := parsePlayerEventLine(safeLine, "OnPlayerJoined"); ok {
			m.emit(MonitorEvent{Type: EventPlayerJoin, Player: player})
		} else {
			m.emit(MonitorEvent{Type: EventPlayerJoin, Player: PlayerEvent{RawLine: safeLine}})
		}
		return
	}
}

var logTimestampPattern = regexp.MustCompile(`(?i)output_log_([0-9]{4})-([0-9]{2})-([0-9]{2})_([0-9]{2})-([0-9]{2})-([0-9]{2})`)

func getNewestLogPath(logDir string) string {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return ""
	}
	var bestPath string
	var bestScore float64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if name == "player.log" || strings.HasPrefix(name, "output_log_") {
			path := filepath.Join(logDir, entry.Name())
			score := scoreLogFile(path)
			if score > bestScore {
				bestScore = score
				bestPath = path
			}
		}
	}
	return bestPath
}

func scoreLogFile(path string) float64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	best := float64(info.ModTime().Unix())
	if matches := logTimestampPattern.FindStringSubmatch(strings.ToLower(filepath.Base(path))); len(matches) == 7 {
		layout := "2006-01-02 15:04:05"
		candidate := fmt.Sprintf("%s-%s-%s %s:%s:%s", matches[1], matches[2], matches[3], matches[4], matches[5], matches[6])
		if t, err := time.ParseInLocation(layout, candidate, time.Local); err == nil {
			if float64(t.Unix()) > best {
				best = float64(t.Unix())
			}
		}
	}
	return best
}
