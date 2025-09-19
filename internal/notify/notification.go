package notify

import (
	"fmt"

	"vrchat-join-notification-with-pushover/internal/logger"
)

// DesktopNotifier exposes desktop notification functionality.
type DesktopNotifier struct {
	log *logger.Logger
}

// New creates a notifier.
func New(log *logger.Logger) *DesktopNotifier {
	return &DesktopNotifier{log: log}
}

// Send attempts to show a desktop notification, falling back to logging on failure.
func (n *DesktopNotifier) Send(title, message string) {
	if title == "" && message == "" {
		return
	}
	if err := sendToast(title, message); err != nil {
		if n.log != nil {
			n.log.Log(fmt.Sprintf("Notification: %s - %s (fallback: %v)", title, message, err))
		}
		return
	}
	if n.log != nil {
		n.log.Log(fmt.Sprintf("Notification sent: %s - %s", title, message))
	}
}
