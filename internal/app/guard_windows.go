//go:build windows

package app

import (
	"errors"
	"fmt"
	"syscall"
)

// ErrAlreadyRunning indicates that another copy of the application is already
// holding the single-instance mutex.
var ErrAlreadyRunning = errors.New(AppName + " is already running.")

// InstanceGuard prevents multiple copies of the notifier from running at the
// same time by relying on a named Windows mutex.
type InstanceGuard struct {
	handle syscall.Handle
	name   string
}

// AcquireSingleInstance attempts to create the named mutex. When another copy
// is already running it returns ErrAlreadyRunning.
func AcquireSingleInstance(name string) (*InstanceGuard, error) {
	if name == "" {
		name = "VRChatJoinNotificationWithPushover"
	}
	handle, errno := createNamedMutex(name)
	if errno != 0 {
		if errno == syscall.ERROR_ALREADY_EXISTS {
			closeHandle(handle)
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("create mutex: %w", errno)
	}
	return &InstanceGuard{handle: handle, name: name}, nil
}

// Release frees the underlying mutex handle.
func (g *InstanceGuard) Release() {
	if g == nil {
		return
	}
	if g.handle != 0 {
		releaseMutex(g.handle)
		closeHandle(g.handle)
		g.handle = 0
	}
}
