package session

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"vrchat-join-notification-with-pushover/internal/core"
	"vrchat-join-notification-with-pushover/internal/logger"
	"vrchat-join-notification-with-pushover/internal/notify"
	"vrchat-join-notification-with-pushover/internal/pushover"
)

type pendingSelfJoin struct {
	SessionID   int
	Placeholder string
	Timestamp   time.Time
}

// Tracker mirrors the session tracking behaviour of the Linux implementation.
type Tracker struct {
	notifier *notify.DesktopNotifier
	pushover *pushover.Client
	log      *logger.Logger

	sessionID          int
	ready              bool
	source             string
	seenPlayers        map[string]time.Time
	pendingRoom        *core.RoomEvent
	sessionStartedAt   time.Time
	sessionLastJoinAt  *time.Time
	sessionLastJoinRaw string
	lastNotified       map[string]time.Time
	localUserID        string
	pendingSelf        *pendingSelfJoin
}

// checkVRChatRunning is overridden in tests.
var checkVRChatRunning = detectVRChatRunning

// SetVRChatRunningCheck overrides the VRChat process detector (useful for tests).
func SetVRChatRunningCheck(fn func() bool) {
	if fn == nil {
		fn = detectVRChatRunning
	}
	checkVRChatRunning = fn
}

// New creates a session tracker.
func New(notifier *notify.DesktopNotifier, po *pushover.Client, log *logger.Logger) *Tracker {
	return &Tracker{
		notifier:     notifier,
		pushover:     po,
		log:          log,
		seenPlayers:  make(map[string]time.Time),
		lastNotified: make(map[string]time.Time),
	}
}

// HandleStatus logs monitor status messages.
func (t *Tracker) HandleStatus(message string) {
	if t.log != nil {
		t.log.Log(message)
	}
}

// HandleError logs errors from the monitor.
func (t *Tracker) HandleError(message string) {
	if t.log != nil {
		t.log.Log("Monitor error: " + message)
	}
}

// HandleLogSwitch resets state for a new log file.
func (t *Tracker) HandleLogSwitch(path string) {
	if t.log != nil {
		t.log.Log(fmt.Sprintf("Switching to newest log: %s", path))
	}
	t.resetSessionState()
}

// HandleRoomEnter records a pending room transition.
func (t *Tracker) HandleRoomEnter(event *core.RoomEvent) {
	if event == nil {
		return
	}
	t.pendingRoom = event
	if t.log == nil {
		return
	}
	if event.World != "" {
		desc := event.World
		if event.Instance != "" {
			desc += ":" + event.Instance
		}
		t.log.Log(fmt.Sprintf("Room transition detected: %s", desc))
	} else if event.RawLine != "" {
		t.log.Log(fmt.Sprintf("Room transition detected: %s", event.RawLine))
	} else {
		t.log.Log("Room transition detected.")
	}
}

// HandleRoomLeft terminates the current session on OnLeftRoom events.
func (t *Tracker) HandleRoomLeft() {
	if t.log != nil {
		if t.ready {
			t.log.Log(fmt.Sprintf("Session %d ended (OnLeftRoom detected.)", t.sessionID))
		} else {
			t.log.Log("OnLeftRoom detected.")
		}
	}
	t.resetSessionState()
}

// HandleSelfJoin processes OnJoinedRoom events.
func (t *Tracker) HandleSelfJoin(raw string) {
	if !checkVRChatRunning() {
		if t.log != nil {
			t.log.Log("Ignored self join while VRChat is not running.")
		}
		return
	}
	now := time.Now().UTC()
	reuseFallback := false
	var elapsedSinceFallback time.Duration
	var lastJoinGap time.Duration
	fallbackJoinCount := 0
	if t.ready && t.source == "OnPlayerJoined fallback" {
		fallbackJoinCount = len(t.seenPlayers)
		if !t.sessionStartedAt.IsZero() {
			elapsedSinceFallback = now.Sub(t.sessionStartedAt)
		}
		if fallbackJoinCount > 0 {
			if t.sessionLastJoinAt != nil {
				lastJoinGap = now.Sub(*t.sessionLastJoinAt)
			} else if len(t.seenPlayers) > 0 {
				var latest time.Time
				for _, ts := range t.seenPlayers {
					if ts.After(latest) {
						latest = ts
					}
				}
				if !latest.IsZero() {
					lastJoinGap = now.Sub(latest)
				}
			}
		}
		withinGrace := elapsedSinceFallback > 0 && elapsedSinceFallback < time.Duration(core.SessionFallbackGraceSeconds)*time.Second
		withinJoinGap := false
		if withinGrace {
			if fallbackJoinCount <= 0 {
				withinJoinGap = true
			} else if lastJoinGap > 0 {
				withinJoinGap = lastJoinGap <= time.Duration(core.SessionFallbackMaxContinuationSecs)*time.Second
			}
		}
		if withinGrace && withinJoinGap {
			reuseFallback = true
			t.source = "OnJoinedRoom"
			var details []string
			if lastJoinGap > 0 {
				details = append(details, fmt.Sprintf("last join gap %.1fs", safeSeconds(lastJoinGap)))
			} else if fallbackJoinCount > 0 {
				details = append(details, "last join gap unknown")
			}
			if fallbackJoinCount > 0 {
				details = append(details, fmt.Sprintf("tracked players %d", fallbackJoinCount))
			}
			if t.log != nil {
				t.log.Log(fmt.Sprintf("Session %d confirmed by OnJoinedRoom.%s", t.sessionID, wrapDetails(details)))
			}
		}
	}
	if !reuseFallback {
		var details []string
		if elapsedSinceFallback > 0 {
			details = append(details, fmt.Sprintf("after %.1fs", safeSeconds(elapsedSinceFallback)))
		}
		if fallbackJoinCount > 0 {
			if lastJoinGap > 0 {
				details = append(details, fmt.Sprintf("last join gap %.1fs", safeSeconds(lastJoinGap)))
			} else {
				details = append(details, "last join gap unavailable")
			}
			details = append(details, fmt.Sprintf("tracked players %d", fallbackJoinCount))
		}
		if t.ready && t.source == "OnPlayerJoined fallback" && t.log != nil {
			t.log.Log(fmt.Sprintf("Session %d fallback expired%s; starting new session for OnJoinedRoom.", t.sessionID, wrapDetails(details)))
		}
		pending := t.pendingRoom
		t.resetSessionState()
		if pending != nil {
			t.pendingRoom = pending
		}
		t.ensureSessionReady("OnJoinedRoom")
	}
	parsedName := ""
	parsedUser := ""
	parsedPlaceholder := ""
	if raw != "" {
		if parsed := core.ParsePlayerEventLine(raw, "OnJoinedRoom"); parsed != nil {
			parsedName = core.NormalizeJoinName(parsed.Name)
			parsedUser = strings.TrimSpace(parsed.UserID)
			parsedPlaceholder = core.NormalizeJoinName(parsed.Placeholder)
		}
	}
	if parsedUser != "" {
		lowerUser := strings.ToLower(parsedUser)
		if t.localUserID == "" || t.localUserID != lowerUser {
			t.localUserID = lowerUser
			if t.log != nil {
				t.log.Log(fmt.Sprintf("Learned local userId from OnJoinedRoom event: %s", parsedUser))
			}
		}
	}
	displayName := parsedName
	if strings.EqualFold(displayName, "you") && parsedName != "" {
		displayName = parsedName
	}
	if displayName == "" {
		if parsedUser != "" {
			displayName = parsedUser
		} else {
			displayName = "You"
		}
	}
	placeholderLabel := parsedPlaceholder
	if placeholderLabel == "" {
		placeholderLabel = "Player"
	}
	if strings.EqualFold(placeholderLabel, "you") {
		placeholderLabel = "Player"
	}
	messageBase := displayName
	if messageBase == "" {
		messageBase = placeholderLabel
	} else if placeholderLabel != "" {
		messageBase = fmt.Sprintf("%s(%s)", messageBase, placeholderLabel)
	}
	message := fmt.Sprintf("%s joined your instance.", messageBase)
	key := fmt.Sprintf("self:%d", t.sessionID)
	t.notifyAll(key, core.AppName, message, true, true)
	t.pendingSelf = &pendingSelfJoin{SessionID: t.sessionID, Placeholder: placeholderLabel, Timestamp: now}
}

// HandlePlayerJoin processes OnPlayerJoined log entries.
func (t *Tracker) HandlePlayerJoin(event *core.PlayerEvent) {
	if event == nil {
		return
	}
	if !checkVRChatRunning() {
		if t.log != nil {
			t.log.Log("Ignored player join while VRChat is not running.")
		}
		return
	}
	if !t.ready {
		t.ensureSessionReady("OnPlayerJoined fallback")
	}
	if !t.ready {
		return
	}
	eventTime := time.Now().UTC()
	t.sessionLastJoinAt = &eventTime
	t.sessionLastJoinRaw = event.RawLine
	cleanedName := core.NormalizeJoinName(event.Name)
	originalName := cleanedName
	cleanedPlaceholder := core.NormalizeJoinName(event.Placeholder)
	cleanedUser := strings.TrimSpace(event.UserID)
	userKey := strings.ToLower(cleanedUser)
	if userKey != "" && t.localUserID != "" && userKey == t.localUserID {
		if t.log != nil {
			t.log.Log(fmt.Sprintf("Skipping join for known local userId '%s'.", cleanedUser))
		}
		t.pendingSelf = nil
		return
	}
	if pending := t.pendingSelf; pending != nil && pending.SessionID == t.sessionID {
		pendingPlaceholder := core.NormalizeJoinName(pending.Placeholder)
		pendingLower := strings.ToLower(pendingPlaceholder)
		eventPlaceholderLower := strings.ToLower(cleanedPlaceholder)
		if eventPlaceholderLower == "" && core.IsPlaceholderName(originalName) {
			eventPlaceholderLower = strings.ToLower(originalName)
		}
		ageOK := eventTime.Sub(pending.Timestamp) < 10*time.Second
		if ageOK && (pendingLower == "player" || pendingLower == "you") && (pendingLower == eventPlaceholderLower || (eventPlaceholderLower == "" && core.IsPlaceholderName(originalName))) {
			if userKey != "" && t.localUserID == "" {
				t.localUserID = userKey
			}
			t.pendingSelf = nil
			if t.log != nil {
				t.log.Log("Skipping join matched pending self event.")
			}
			return
		}
	}
	wasPlaceholder := core.IsPlaceholderName(cleanedName)
	isFallbackSession := t.source == "OnPlayerJoined fallback"
	if wasPlaceholder && userKey != "" {
		if isFallbackSession && t.localUserID == "" {
			t.localUserID = userKey
			if t.log != nil {
				t.log.Log(fmt.Sprintf("Learned local userId from join event: %s", cleanedUser))
				t.log.Log(fmt.Sprintf("Skipping initial local join placeholder for userId '%s'.", cleanedUser))
			}
			t.pendingSelf = nil
			return
		}
		if t.localUserID != "" && t.localUserID == userKey {
			if t.log != nil {
				t.log.Log(fmt.Sprintf("Skipping local join placeholder for userId '%s'.", cleanedUser))
			}
			t.pendingSelf = nil
			return
		}
	}
	if cleanedName == "" && cleanedUser != "" {
		cleanedName = cleanedUser
		wasPlaceholder = false
	} else if wasPlaceholder && cleanedUser != "" {
		cleanedName = cleanedUser
		wasPlaceholder = false
	}
	if cleanedName == "" {
		cleanedName = "Unknown VRChat user"
	}
	keyBase := userKey
	if keyBase == "" {
		keyBase = strings.ToLower(cleanedName)
	}
	hashSuffix := ""
	if cleanedUser == "" && event.RawLine != "" {
		hashSuffix = core.GetShortHash(event.RawLine)
	}
	joinKey := fmt.Sprintf("join:%d:%s", t.sessionID, keyBase)
	if hashSuffix != "" {
		joinKey += ":" + hashSuffix
	}
	if _, exists := t.seenPlayers[joinKey]; exists {
		return
	}
	t.seenPlayers[joinKey] = eventTime
	placeholderMessage := cleanedPlaceholder
	if placeholderMessage == "" && wasPlaceholder {
		placeholderMessage = originalName
	}
	if placeholderMessage == "" {
		placeholderMessage = "Someone"
	} else if strings.EqualFold(placeholderMessage, "you") {
		placeholderMessage = "Player"
	}
	messageName := cleanedName
	if cleanedName != "" {
		messageName = fmt.Sprintf("%s(%s)", cleanedName, placeholderMessage)
	} else {
		messageName = placeholderMessage
	}
	desktopNotification := true
	if wasPlaceholder && cleanedUser == "" {
		if strings.EqualFold(strings.TrimSpace(placeholderMessage), "a player") {
			desktopNotification = false
		}
	}
	message := fmt.Sprintf("%s joined your instance.", messageName)
	pushoverNotification := !wasPlaceholder
	t.notifyAll(joinKey, core.AppName, message, desktopNotification, pushoverNotification)
	logLine := fmt.Sprintf("Session %d: player joined '%s'", t.sessionID, cleanedName)
	if cleanedUser != "" {
		logLine += fmt.Sprintf(" (%s)", cleanedUser)
	}
	logLine += "."
	if t.log != nil {
		t.log.Log(logLine)
	}
}

// HandlePlayerLeft processes OnPlayerLeft log entries.
func (t *Tracker) HandlePlayerLeft(event *core.PlayerEvent) {
	if event == nil {
		return
	}
	cleanedName := core.NormalizeJoinName(event.Name)
	cleanedUser := strings.TrimSpace(event.UserID)
	userKey := strings.ToLower(cleanedUser)
	isPlaceholder := core.IsPlaceholderName(cleanedName)
	if isPlaceholder && userKey != "" {
		if t.localUserID == "" {
			t.localUserID = userKey
			if t.log != nil {
				t.log.Log(fmt.Sprintf("Learned local userId from leave event: %s", cleanedUser))
			}
		} else if t.localUserID == userKey {
			cleanedName = cleanedUser
			isPlaceholder = false
		}
	}
	if cleanedName == "" && cleanedUser != "" {
		cleanedName = cleanedUser
		isPlaceholder = false
	} else if isPlaceholder && cleanedUser != "" {
		cleanedName = cleanedUser
		isPlaceholder = false
	}
	if cleanedName == "" {
		cleanedName = "Unknown VRChat user"
	}
	removedCount := 0
	if userKey != "" {
		prefix := fmt.Sprintf("join:%d:%s", t.sessionID, userKey)
		for key := range t.seenPlayers {
			if strings.HasPrefix(key, prefix) {
				delete(t.seenPlayers, key)
				removedCount++
			}
		}
	}
	logLine := fmt.Sprintf("Session %d: player left '%s'", t.sessionID, cleanedName)
	if cleanedUser != "" {
		logLine += fmt.Sprintf(" (%s)", cleanedUser)
	}
	if removedCount > 0 {
		logLine += " [cleared join tracking]"
	}
	logLine += "."
	if t.log != nil {
		t.log.Log(logLine)
	}
}

func (t *Tracker) ensureSessionReady(reason string) bool {
	if t.ready {
		return false
	}
	if strings.TrimSpace(reason) == "" {
		reason = "unknown trigger"
	}
	t.sessionID++
	t.ready = true
	t.source = reason
	t.seenPlayers = make(map[string]time.Time)
	t.sessionStartedAt = time.Now().UTC()
	t.sessionLastJoinAt = nil
	t.sessionLastJoinRaw = ""
	t.pendingSelf = nil
	t.localUserID = ""
	message := fmt.Sprintf("Session %d started (%s)", t.sessionID, reason)
	if t.pendingRoom != nil {
		desc := t.pendingRoom.World
		if desc == "" {
			desc = t.pendingRoom.RawLine
		}
		if desc != "" {
			if t.pendingRoom.Instance != "" {
				if desc != t.pendingRoom.RawLine {
					desc += ":" + t.pendingRoom.Instance
				}
			}
			message += fmt.Sprintf(" [%s]", desc)
		}
	}
	message += "."
	if t.log != nil {
		t.log.Log(message)
	}
	return true
}

func (t *Tracker) resetSessionState() {
	t.ready = false
	t.source = ""
	t.seenPlayers = make(map[string]time.Time)
	t.pendingRoom = nil
	t.sessionStartedAt = time.Time{}
	t.sessionLastJoinAt = nil
	t.sessionLastJoinRaw = ""
	t.localUserID = ""
	t.pendingSelf = nil
}

func (t *Tracker) notifyAll(key, title, message string, desktop, push bool) {
	now := time.Now().UTC()
	if prev, ok := t.lastNotified[key]; ok {
		if now.Sub(prev) < time.Duration(core.NotifyCooldownSeconds)*time.Second {
			if t.log != nil {
				t.log.Log(fmt.Sprintf("Suppressed '%s' within cooldown.", key))
			}
			return
		}
	}
	t.lastNotified[key] = now
	if desktop && t.notifier != nil {
		t.notifier.Send(title, message)
	}
	if push && t.pushover != nil {
		t.pushover.Send(title, message)
	}
}

func safeSeconds(d time.Duration) float64 {
	return math.Max(0, d.Seconds())
}

func wrapDetails(details []string) string {
	if len(details) == 0 {
		return ""
	}
	return " (" + strings.Join(details, "; ") + ")"
}

func detectVRChatRunning() bool {
	if runtime.GOOS == "windows" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "tasklist", "/FI", "IMAGENAME eq VRChat.exe")
		output, err := cmd.Output()
		if err != nil {
			return true
		}
		return bytes.Contains(bytes.ToLower(output), []byte("vrchat.exe"))
	}
	if _, err := exec.LookPath("pgrep"); err != nil {
		return true
	}
	patterns := []string{"VRChat.exe", "VRChat"}
	for _, pattern := range patterns {
		cmd := exec.Command("pgrep", "-f", pattern)
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}
