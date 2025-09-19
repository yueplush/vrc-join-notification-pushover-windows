package app

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

type RoomEvent struct {
	World    string
	Instance string
	RawLine  string
}

type PlayerEvent struct {
	Name        string
	UserID      string
	Placeholder string
	RawLine     string
}

var (
	zeroWidthPattern      = regexp.MustCompile(`[\x{200b}-\x{200d}\x{feff}]`)
	joinSeparatorPattern  = regexp.MustCompile(`^[-:|\x{2013}\x{2014}]+$`)
	displayNamePattern    = regexp.MustCompile(`(?i)displayName\s*[:=]\s*([^,\]\)]+)`)
	namePattern           = regexp.MustCompile(`(?i)\bname\s*[:=]\s*([^,\]\)]+)`)
	userIDParenPattern    = regexp.MustCompile(`(?i)\(usr_[^\)\s]+\)`)
	userIDAssignment      = regexp.MustCompile(`(?i)userId\s*[:=]\s*(usr_[0-9a-f\-]+)`)
	cleanupUserIDPattern  = regexp.MustCompile(`(?i)\(usr_[^\)]*\)`)
	cleanupUserRawPattern = regexp.MustCompile(`(?i)\(userId[^\)]*\)`)
	cleanupBracketPattern = regexp.MustCompile(`\[[^\]]*\]`)
	cleanupBracePattern   = regexp.MustCompile(`\{[^\}]*\}`)
	cleanupAnglePattern   = regexp.MustCompile(`<[^>]*>`)
	roomWorldPattern      = regexp.MustCompile(`(?i)wrld_[0-9a-f\-]+`)
	markerPattern         = regexp.MustCompile(`(?i)\b(displayName|name|userId)\b`)
)

const (
	unicodeDashes      = "\u2013\u2014"
	joinSeparatorChars = ":|-" + unicodeDashes
	sessionCooldown    = time.Duration(NotifyCooldownSeconds) * time.Second
)

// SessionTracker mirrors the behaviour of the Python implementation but is
// intentionally pragmatic: it focuses on reliable notifications and log output
// rather than re-implementing every legacy edge case.
type SessionTracker struct {
	notifier *DesktopNotifier
	pushover *PushoverClient
	logger   *AppLogger

	mu                sync.Mutex
	sessionID         int
	ready             bool
	source            string
	seenPlayers       map[string]time.Time
	lastNotified      map[string]time.Time
	pendingRoom       *RoomEvent
	sessionStartedAt  time.Time
	sessionLastJoinAt time.Time
	lastJoinRaw       string
	localUserID       string
	lastEvent         string
}

func NewSessionTracker(n *DesktopNotifier, p *PushoverClient, logger *AppLogger) *SessionTracker {
	return &SessionTracker{
		notifier:     n,
		pushover:     p,
		logger:       logger,
		seenPlayers:  make(map[string]time.Time),
		lastNotified: make(map[string]time.Time),
	}
}

func (s *SessionTracker) Reset(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = false
	s.source = ""
	s.pendingRoom = nil
	s.sessionStartedAt = time.Time{}
	s.sessionLastJoinAt = time.Time{}
	s.lastJoinRaw = ""
	s.seenPlayers = make(map[string]time.Time)
	s.localUserID = ""
	s.lastEvent = ""
	if reason != "" && s.logger != nil {
		s.logger.Log(reason)
	}
}

func (s *SessionTracker) HandleLogSwitch(path string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = false
	s.source = ""
	s.pendingRoom = nil
	s.sessionStartedAt = time.Time{}
	s.sessionLastJoinAt = time.Time{}
	s.lastJoinRaw = ""
	s.seenPlayers = make(map[string]time.Time)
	s.localUserID = ""
	s.lastEvent = ""
	message := fmt.Sprintf("Switching to newest log: %s", path)
	if s.logger != nil {
		s.logger.Log(message)
	}
	return message
}

func (s *SessionTracker) HandleRoomEnter(event RoomEvent) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingRoom = &event
	var desc string
	if event.World != "" {
		desc = event.World
		if event.Instance != "" {
			desc += ":" + event.Instance
		}
	} else if event.RawLine != "" {
		desc = event.RawLine
	}
	if desc == "" {
		desc = "Room transition detected."
	} else {
		desc = "Room transition detected: " + desc
	}
	s.lastEvent = desc
	if s.logger != nil {
		s.logger.Log(desc)
	}
	return desc
}

func (s *SessionTracker) HandleRoomLeft() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var message string
	if s.ready {
		message = fmt.Sprintf("Session %d ended (OnLeftRoom detected.)", s.sessionID)
	} else {
		message = "OnLeftRoom detected."
	}
	s.ready = false
	s.source = ""
	s.pendingRoom = nil
	s.seenPlayers = make(map[string]time.Time)
	s.lastEvent = message
	if s.logger != nil {
		s.logger.Log(message)
	}
	return message
}

func (s *SessionTracker) HandleSelfJoin(rawLine string) string {
	if runtime.GOOS == "windows" && !isVRChatRunning() {
		if s.logger != nil {
			s.logger.Log("Ignored self join while VRChat is not running.")
		}
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ensureSessionReadyLocked("OnJoinedRoom detected") {
		if s.logger != nil {
			s.logger.Log(fmt.Sprintf("Session %d started (OnJoinedRoom detected.)", s.sessionID))
		}
	}
	s.lastJoinRaw = rawLine
	s.lastEvent = "OnJoinedRoom"
	return ""
}

func (s *SessionTracker) HandlePlayerJoin(event PlayerEvent) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureSessionReadyLocked("OnPlayerJoined fallback")
	cleanedName := normalizeJoinName(event.Name)
	cleanedUser := strings.TrimSpace(event.UserID)
	wasPlaceholder := isPlaceholderName(cleanedName)
	if wasPlaceholder && cleanedUser != "" {
		if s.localUserID != "" && strings.EqualFold(s.localUserID, cleanedUser) {
			wasPlaceholder = false
			cleanedName = cleanedUser
		} else {
			cleanedName = cleanedUser
			wasPlaceholder = false
		}
	}
	if cleanedName == "" && cleanedUser != "" {
		cleanedName = cleanedUser
		wasPlaceholder = false
	}
	if cleanedName == "" {
		cleanedName = "Unknown VRChat user"
	}
	keyBase := strings.ToLower(cleanedUser)
	if keyBase == "" {
		keyBase = strings.ToLower(cleanedName)
	}
	hashSuffix := ""
	if keyBase == "" {
		hashSuffix = getShortHash(event.RawLine)
		keyBase = hashSuffix
	}
	joinKey := fmt.Sprintf("join:%d:%s", s.sessionID, keyBase)
	if hashSuffix != "" {
		joinKey += ":" + hashSuffix
	}
	if _, exists := s.seenPlayers[joinKey]; exists {
		return ""
	}
	s.seenPlayers[joinKey] = time.Now()
	placeholderName := normalizeJoinName(event.Placeholder)
	if placeholderName == "" && wasPlaceholder {
		placeholderName = event.Name
	}
	if placeholderName == "" {
		placeholderName = "Someone"
	}
	messageName := cleanedName
	if messageName == "" {
		messageName = placeholderName
	}
	message := fmt.Sprintf("%s joined your instance.", messageName)
	desktopNotification := true
	if wasPlaceholder && cleanedUser == "" {
		lowerPlaceholder := strings.TrimSpace(strings.ToLower(placeholderName))
		if lowerPlaceholder == "a player" {
			desktopNotification = false
		}
	}
	pushoverNotification := !wasPlaceholder
	s.notifyAll(joinKey, AppName, message, desktopNotification, pushoverNotification)
	if s.logger != nil {
		logLine := fmt.Sprintf("Session %d: player joined '%s'", s.sessionID, messageName)
		if cleanedUser != "" {
			logLine += fmt.Sprintf(" (%s)", cleanedUser)
		}
		logLine += "."
		s.logger.Log(logLine)
	}
	s.sessionLastJoinAt = time.Now()
	s.lastJoinRaw = event.RawLine
	s.lastEvent = message
	return message
}

func (s *SessionTracker) HandlePlayerLeft(event PlayerEvent) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cleanedName := normalizeJoinName(event.Name)
	cleanedUser := strings.TrimSpace(event.UserID)
	if cleanedName == "" && cleanedUser != "" {
		cleanedName = cleanedUser
	}
	if cleanedName == "" {
		cleanedName = "Unknown VRChat user"
	}
	if cleanedUser != "" {
		s.localUserID = cleanedUser
		prefix := fmt.Sprintf("join:%d:%s", s.sessionID, strings.ToLower(cleanedUser))
		for key := range s.seenPlayers {
			if strings.HasPrefix(key, prefix) {
				delete(s.seenPlayers, key)
			}
		}
	}
	if s.logger != nil {
		logLine := fmt.Sprintf("Session %d: player left '%s'", s.sessionID, cleanedName)
		if cleanedUser != "" {
			logLine += fmt.Sprintf(" (%s)", cleanedUser)
		}
		logLine += "."
		s.logger.Log(logLine)
	}
	s.lastEvent = fmt.Sprintf("%s left the instance.", cleanedName)
	return cleanedName
}

func (s *SessionTracker) ensureSessionReadyLocked(reason string) bool {
	if s.ready {
		return false
	}
	if strings.TrimSpace(reason) == "" {
		reason = "unknown trigger"
	}
	s.sessionID++
	s.ready = true
	s.source = reason
	s.seenPlayers = make(map[string]time.Time)
	s.sessionStartedAt = time.Now()
	s.sessionLastJoinAt = time.Time{}
	s.lastJoinRaw = ""
	var roomDesc string
	if s.pendingRoom != nil {
		if s.pendingRoom.World != "" {
			roomDesc = s.pendingRoom.World
			if s.pendingRoom.Instance != "" {
				roomDesc += ":" + s.pendingRoom.Instance
			}
		} else if s.pendingRoom.RawLine != "" {
			roomDesc = s.pendingRoom.RawLine
		}
	}
	message := fmt.Sprintf("Session %d started (%s)", s.sessionID, reason)
	if roomDesc != "" {
		message += fmt.Sprintf(" [%s]", roomDesc)
	}
	message += "."
	if s.logger != nil {
		s.logger.Log(message)
	}
	s.lastEvent = message
	return true
}

func (s *SessionTracker) notifyAll(key, title, message string, desktop, push bool) {
	now := time.Now()
	if previous, ok := s.lastNotified[key]; ok {
		if now.Sub(previous) < sessionCooldown {
			if s.logger != nil {
				s.logger.Logf("Suppressed '%s' within cooldown.", key)
			}
			return
		}
	}
	s.lastNotified[key] = now
	if desktop && s.notifier != nil {
		s.notifier.Send(title, message)
	}
	if push && s.pushover != nil {
		s.pushover.Send(title, message)
	}
}

func (s *SessionTracker) Summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return "No active session"
	}
	desc := fmt.Sprintf("Session %d (%s)", s.sessionID, s.source)
	if s.pendingRoom != nil {
		var room string
		if s.pendingRoom.World != "" {
			room = s.pendingRoom.World
			if s.pendingRoom.Instance != "" {
				room += ":" + s.pendingRoom.Instance
			}
		} else if s.pendingRoom.RawLine != "" {
			room = s.pendingRoom.RawLine
		}
		if room != "" {
			desc += " [" + room + "]"
		}
	}
	return desc
}

func (s *SessionTracker) LastEvent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastEvent
}

func stripZeroWidth(text string) string {
	return zeroWidthPattern.ReplaceAllString(text, "")
}

func normalizeJoinFragment(text string) string {
	clean := stripZeroWidth(text)
	clean = strings.ReplaceAll(clean, "\u3000", " ")
	clean = strings.TrimSpace(strings.Trim(strings.Trim(clean, "\""), "'"))
	clean = strings.ReplaceAll(clean, "||", "|")
	clean = strings.TrimLeft(clean, joinSeparatorChars)
	clean = strings.TrimSpace(clean)
	if len(clean) > 160 {
		clean = strings.TrimSpace(clean[:160])
	}
	if joinSeparatorPattern.MatchString(clean) {
		return ""
	}
	return clean
}

func normalizeJoinName(name string) string {
	return normalizeJoinFragment(name)
}

func isPlaceholderName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return true
	}
	trimmed := strings.ToLower(strings.TrimSpace(name))
	switch trimmed {
	case "player", "you", "someone", "a player":
		return true
	default:
		return false
	}
}

func getShortHash(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	sum := md5.Sum([]byte(text))
	hexed := hex.EncodeToString(sum[:])
	if len(hexed) > 8 {
		return hexed[:8]
	}
	return hexed
}

func parsePlayerEventLine(line string, eventToken string) (PlayerEvent, bool) {
	if strings.TrimSpace(line) == "" {
		return PlayerEvent{}, false
	}
	lower := strings.ToLower(line)
	needle := strings.ToLower(eventToken)
	idx := strings.Index(lower, needle)
	if idx < 0 {
		return PlayerEvent{}, false
	}
	after := stripZeroWidth(line[idx+len(eventToken):])
	after = strings.TrimSpace(after)
	for len(after) > 0 {
		r, size := utf8.DecodeRuneInString(after)
		if strings.ContainsRune(joinSeparatorChars, r) {
			after = strings.TrimSpace(after[size:])
			continue
		}
		break
	}
	placeholder := ""
	if after != "" {
		candidate := after
		if loc := markerPattern.FindStringIndex(after); loc != nil {
			candidate = candidate[:loc[0]]
		}
		for _, cut := range []string{"(", "[", "{", "<"} {
			if idx := strings.Index(candidate, cut); idx >= 0 {
				candidate = candidate[:idx]
			}
		}
		candidate = normalizeJoinFragment(candidate)
		if isPlaceholderName(candidate) {
			placeholder = candidate
		}
	}
	displayName := ""
	if match := displayNamePattern.FindStringSubmatch(after); len(match) > 1 {
		displayName = normalizeJoinFragment(match[1])
	}
	if displayName == "" {
		if match := namePattern.FindStringSubmatch(after); len(match) > 1 {
			displayName = normalizeJoinFragment(match[1])
		}
	}
	userID := ""
	if match := userIDParenPattern.FindString(after); match != "" {
		userID = strings.Trim(match, "() ")
	}
	if userID == "" {
		if match := userIDAssignment.FindStringSubmatch(after); len(match) > 1 {
			userID = strings.TrimSpace(match[1])
		}
	}
	if displayName == "" {
		tmp := after
		if userID != "" {
			tmp = strings.ReplaceAll(tmp, "("+userID+")", "")
		}
		tmp = cleanupUserIDPattern.ReplaceAllString(tmp, "")
		tmp = cleanupUserRawPattern.ReplaceAllString(tmp, "")
		tmp = cleanupBracketPattern.ReplaceAllString(tmp, "")
		tmp = cleanupBracePattern.ReplaceAllString(tmp, "")
		tmp = cleanupAnglePattern.ReplaceAllString(tmp, "")
		tmp = strings.ReplaceAll(tmp, "||", "|")
		displayName = normalizeJoinFragment(tmp)
	}
	if displayName == "" && userID != "" {
		displayName = userID
	}
	safeLine := strings.TrimSpace(strings.ReplaceAll(stripZeroWidth(line), "||", "|"))
	return PlayerEvent{
		Name:        displayName,
		UserID:      userID,
		Placeholder: placeholder,
		RawLine:     safeLine,
	}, true
}

func parseRoomTransitionLine(line string) (RoomEvent, bool) {
	clean := strings.TrimSpace(stripZeroWidth(line))
	if clean == "" {
		return RoomEvent{}, false
	}
	lower := strings.ToLower(clean)
	indicators := []string{
		"joining or creating room",
		"entering room",
		"joining room",
		"creating room",
		"created room",
		"rejoining room",
		"re-joining room",
		"reentering room",
		"re-entering room",
		"joining instance",
		"creating instance",
		"entering instance",
	}
	matched := false
	for _, indicator := range indicators {
		if strings.Contains(lower, indicator) {
			matched = true
			break
		}
	}
	if !matched {
		jpSets := []struct {
			Key   string
			Terms []string
		}{
			{Key: "ルーム", Terms: []string{"参加", "作成", "入室", "移動", "入場"}},
			{Key: "インスタンス", Terms: []string{"参加", "作成", "入室", "移動", "入場"}},
		}
		for _, jp := range jpSets {
			if strings.Contains(clean, jp.Key) {
				for _, term := range jp.Terms {
					if strings.Contains(clean, term) {
						matched = true
						break
					}
				}
			}
			if matched {
				break
			}
		}
	}
	if !matched {
		if roomWorldPattern.MatchString(clean) {
			if strings.Contains(lower, "room") || strings.Contains(lower, "instance") ||
				strings.Contains(clean, "インスタンス") || strings.Contains(clean, "ルーム") {
				matched = true
			}
		}
	}
	if !matched {
		return RoomEvent{}, false
	}
	world := ""
	instance := ""
	if match := roomWorldPattern.FindStringIndex(clean); match != nil {
		world = clean[match[0]:match[1]]
		after := strings.TrimSpace(clean[match[1]:])
		after = strings.TrimLeft(after, ": \t-")
		if after != "" {
			if idx := strings.IndexAny(after, " ,\r\n"); idx >= 0 {
				instance = after[:idx]
			} else {
				instance = after
			}
		}
	}
	if instance == "" {
		if match := regexp.MustCompile(`(?i)instance\s*[:=]\s*([^\s,]+)`).FindStringSubmatch(clean); len(match) > 1 {
			instance = strings.TrimSpace(match[1])
		}
	}
	return RoomEvent{World: world, Instance: instance, RawLine: clean}, true
}

func isVRChatRunning() bool {
	tasklist, err := exec.LookPath("tasklist.exe")
	if err != nil {
		tasklist, err = exec.LookPath("tasklist")
		if err != nil {
			return true // best effort fallback
		}
	}
	cmd := exec.Command(tasklist, "/FI", "IMAGENAME eq VRChat.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.Output()
	if err != nil {
		return true
	}
	return strings.Contains(strings.ToLower(string(output)), "vrchat.exe")
}
