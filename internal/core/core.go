package core

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	AppName                            = "VRChat Join Notification with Pushover"
	ConfigFileName                     = "config.json"
	AppLogName                         = "notifier.log"
	PushoverURL                        = "https://api.pushover.net/1/messages.json"
	NotifyCooldownSeconds              = 10
	SessionFallbackGraceSeconds        = 30
	SessionFallbackMaxContinuationSecs = 4
)

var (
	unicodeDashes       = "\u2013\u2014"
	joinSeparatorChars  = ":|-" + unicodeDashes
	joinSeparatorRegexp = regexp.MustCompile("[-:|" + unicodeDashes + "]+")
	zeroWidthRegexp     = regexp.MustCompile("[\u200B-\u200D\uFEFF]")
	displayNameRegexp   = regexp.MustCompile(`(?i)displayName\s*[:=]\s*([^,\]\)]+)`)
	nameRegexp          = regexp.MustCompile(`(?i)\bname\s*[:=]\s*([^,\]\)]+)`)
	inlineUserIDRegexp  = regexp.MustCompile(`(?i)\(usr_[^\)\s]+\)`)
	userIDRegexp        = regexp.MustCompile(`(?i)userId\s*[:=]\s*(usr_[0-9a-f\-]+)`)
	worldIDRegexp       = regexp.MustCompile(`(?i)wrld_[0-9a-f\-]+`)
	instanceInlineRegex = regexp.MustCompile(`(?i)instance\s*[:=]\s*([^\s,]+)`)
	firstFieldRegexp    = regexp.MustCompile(`(?i)\b(displayName|name|userId)\b`)
	nonSpaceCommaRegexp = regexp.MustCompile(`[^\s,]+`)
	bracketContentRegex = regexp.MustCompile(`\[[^\]]*\]`)
	braceContentRegex   = regexp.MustCompile(`\{[^\}]*\}`)
	angleContentRegex   = regexp.MustCompile(`<[^>]*>`)
	outputLogRegex      = regexp.MustCompile(`output_log_(\d{4})-(\d{2})-(\d{2})_(\d{2})-(\d{2})-(\d{2})\.txt$`)
)

// PlayerEvent captures parsed information from an OnPlayerJoined / OnPlayerLeft log line.
type PlayerEvent struct {
	Name        string
	UserID      string
	Placeholder string
	RawLine     string
}

// RoomEvent captures parsed information about a world transition.
type RoomEvent struct {
	World    string
	Instance string
	RawLine  string
}

// StripZeroWidth removes zero-width Unicode characters from a string.
func StripZeroWidth(text string) string {
	if text == "" {
		return ""
	}
	return zeroWidthRegexp.ReplaceAllString(text, "")
}

// NormalizeJoinFragment cleans a join-related fragment for comparisons.
func NormalizeJoinFragment(text string) string {
	clean := StripZeroWidth(text)
	clean = strings.ReplaceAll(clean, "\u3000", " ")
	clean = strings.TrimSpace(clean)
	clean = strings.Trim(clean, "\"'")
	clean = strings.ReplaceAll(clean, "||", "|")
	clean = strings.TrimLeft(clean, joinSeparatorChars)
	clean = strings.TrimSpace(clean)
	if len(clean) > 160 {
		clean = strings.TrimSpace(clean[:160])
	}
	if clean == "" {
		return ""
	}
	if joinSeparatorRegexp.MatchString(clean) && joinSeparatorRegexp.ReplaceAllString(clean, "") == "" {
		return ""
	}
	return clean
}

// NormalizeJoinName returns a cleaned join name, or an empty string if no meaningful content remains.
func NormalizeJoinName(name string) string {
	return NormalizeJoinFragment(name)
}

// IsPlaceholderName reports whether the provided name looks like a placeholder emitted by VRChat.
func IsPlaceholderName(name string) bool {
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

// GetShortHash returns an eight character MD5 hash of the provided text.
func GetShortHash(text string) string {
	if text == "" {
		return ""
	}
	digest := md5.Sum([]byte(text))
	encoded := hex.EncodeToString(digest[:])
	if len(encoded) < 8 {
		return encoded
	}
	return encoded[:8]
}

// ParsePlayerEventLine extracts display name, userId and placeholder information from a log line.
func ParsePlayerEventLine(line, eventToken string) *PlayerEvent {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	lower := strings.ToLower(line)
	needle := strings.ToLower(eventToken)
	idx := strings.Index(lower, needle)
	if idx < 0 {
		return nil
	}
	after := StripZeroWidth(line[idx+len(needle):])
	after = strings.TrimSpace(after)
	for after != "" {
		r, size := rune(after[0]), 1
		if len(after) > 0 {
			r, size = utf8DecodeRuneInString(after)
		}
		if !strings.ContainsRune(joinSeparatorChars, r) {
			break
		}
		after = strings.TrimSpace(after[size:])
	}

	placeholder := ""
	if after != "" {
		candidate := after
		if loc := firstFieldRegexp.FindStringIndex(candidate); loc != nil {
			candidate = candidate[:loc[0]]
		}
		candidate = splitAndHead(candidate, "(")
		candidate = splitAndHead(candidate, "[")
		candidate = splitAndHead(candidate, "{")
		candidate = splitAndHead(candidate, "<")
		candidate = NormalizeJoinFragment(candidate)
		if IsPlaceholderName(candidate) {
			placeholder = candidate
		}
	}

	var displayName string
	if match := displayNameRegexp.FindStringSubmatch(after); len(match) > 1 {
		displayName = NormalizeJoinFragment(match[1])
	}
	if displayName == "" {
		if match := nameRegexp.FindStringSubmatch(after); len(match) > 1 {
			displayName = NormalizeJoinFragment(match[1])
		}
	}

	var userID string
	if match := inlineUserIDRegexp.FindString(after); match != "" {
		userID = strings.Trim(strings.TrimSpace(match), "() ")
	}
	if userID == "" {
		if match := userIDRegexp.FindStringSubmatch(after); len(match) > 1 {
			userID = match[1]
		}
	}

	if displayName == "" {
		tmp := after
		if userID != "" {
			tmp = strings.ReplaceAll(tmp, "("+userID+")", "")
		}
		tmp = inlineUserIDRegexp.ReplaceAllString(tmp, "")
		tmp = userIDRegexp.ReplaceAllString(tmp, "")
		tmp = bracketContentRegex.ReplaceAllString(tmp, "")
		tmp = braceContentRegex.ReplaceAllString(tmp, "")
		tmp = angleContentRegex.ReplaceAllString(tmp, "")
		tmp = strings.ReplaceAll(tmp, "||", "|")
		displayName = NormalizeJoinFragment(tmp)
	}

	if displayName == "" && userID != "" {
		displayName = userID
	}

	safeLine := strings.TrimSpace(strings.ReplaceAll(StripZeroWidth(line), "||", "|"))
	return &PlayerEvent{
		Name:        displayName,
		UserID:      userID,
		Placeholder: placeholder,
		RawLine:     safeLine,
	}
}

// ParseRoomTransitionLine extracts world and instance identifiers from a potential transition line.
func ParseRoomTransitionLine(line string) *RoomEvent {
	clean := strings.TrimSpace(StripZeroWidth(line))
	if clean == "" {
		return nil
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
			{Key: "\u30eb\u30fc\u30e0", Terms: []string{"\u53c2\u52a0", "\u4f5c\u6210", "\u5165\u5ba4", "\u79fb\u52d5", "\u5165\u5834"}},
			{Key: "\u30a4\u30f3\u30b9\u30bf\u30f3\u30b9", Terms: []string{"\u53c2\u52a0", "\u4f5c\u6210", "\u5165\u5ba4", "\u79fb\u52d5", "\u5165\u5834"}},
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
		if worldIDRegexp.MatchString(clean) {
			if strings.Contains(lower, "room") || strings.Contains(lower, "instance") || strings.Contains(clean, "\u30a4\u30f3\u30b9\u30bf\u30f3\u30b9") || strings.Contains(clean, "\u30eb\u30fc\u30e0") {
				matched = true
			}
		}
	}
	if !matched {
		return nil
	}

	worldID := ""
	instanceID := ""
	if match := worldIDRegexp.FindStringIndex(clean); match != nil {
		worldID = clean[match[0]:match[1]]
		after := strings.TrimLeft(clean[match[1]:], ": \t-")
		if after != "" {
			instMatch := nonSpaceCommaRegexp.FindString(after)
			instanceID = instMatch
		}
	}
	if instanceID == "" {
		if match := instanceInlineRegex.FindStringSubmatch(clean); len(match) > 1 {
			instanceID = match[1]
		}
	}

	return &RoomEvent{World: worldID, Instance: instanceID, RawLine: clean}
}

// ScoreLogFile ranks log files by timestamp embedded in their name or their filesystem metadata.
func ScoreLogFile(path string) float64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	best := float64(info.ModTime().Unix())
	base := filepath.Base(path)
	match := outputLogRegex.FindStringSubmatch(base)
	if len(match) == 7 {
		ts := match[1] + "-" + match[2] + "-" + match[3] + "_" + match[4] + "-" + match[5] + "-" + match[6]
		if dt, err := time.Parse("2006-01-02_15-04-05", ts); err == nil {
			if ts := float64(dt.Unix()); ts > best {
				best = ts
			}
		}
	}
	return best
}

// GetNewestLogPath returns the most recent Player.log / output_log_* in a directory.
func GetNewestLogPath(logDir string) string {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return ""
	}
	var candidates []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if name == "player.log" || strings.HasPrefix(name, "output_log_") {
			candidates = append(candidates, filepath.Join(logDir, entry.Name()))
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		return ScoreLogFile(candidates[i]) > ScoreLogFile(candidates[j])
	})
	return candidates[0]
}

// Helper to decode runes without importing unicode/utf8 in multiple places.
func utf8DecodeRuneInString(s string) (rune, int) {
	if s == "" {
		return rune(0), 0
	}
	r := rune(s[0])
	if r < utf8.RuneSelf {
		return r, 1
	}
	rr, size := utf8.DecodeRuneInString(s)
	if rr == utf8.RuneError && size == 1 {
		return rune(s[0]), 1
	}
	return rr, size
}

func splitAndHead(text, sep string) string {
	parts := strings.Split(text, sep)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
