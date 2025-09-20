//go:build windows

package app

import (
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
)

type TrayMenuItem struct {
	ID     uint16
	Title  string
	Action func()
}

type SystemTray struct {
	hwnd syscall.Handle
	icon syscall.Handle
	menu syscall.Handle

	tooltip string

	onDoubleClick func()
	items         []TrayMenuItem
	callbacks     map[uint16]func()

	nid notifyIconData

	ready chan error
	done  chan struct{}
}

const (
	trayEventShutdown     = 1
	errClassAlreadyExists = syscall.Errno(1410)
)

var trayInstances sync.Map

func NewSystemTray(iconData []byte, tooltip string, onDoubleClick func(), items []TrayMenuItem) (*SystemTray, error) {
	tray := &SystemTray{
		tooltip:       tooltip,
		onDoubleClick: onDoubleClick,
		items:         items,
		callbacks:     make(map[uint16]func()),
		ready:         make(chan error, 1),
		done:          make(chan struct{}),
	}
	go tray.run(iconData)
	err := <-tray.ready
	if err != nil {
		<-tray.done
		return nil, err
	}
	return tray, nil
}

func (t *SystemTray) Close() {
	if t == nil {
		return
	}
	if t.hwnd != 0 {
		postMessage(t.hwnd, wmEventNotify, uintptr(trayEventShutdown), 0)
	}
	<-t.done
}

func (t *SystemTray) run(iconData []byte) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)
	defer close(t.ready)

	icon := loadIconFromBytes(iconData)
	if icon == 0 {
		icon = loadDefaultIcon()
	}
	t.icon = icon
	if icon != 0 {
		defer destroyIcon(icon)
	}

	wndProc := syscall.NewCallback(trayWindowProc)
	className := "VRChatJoinNotificationWithPushoverTray"
	if err := registerClass(className, wndProc, icon); err != nil {
		if errno, ok := err.(syscall.Errno); !ok || errno != errClassAlreadyExists {
			t.ready <- err
			return
		}
	}
	hwnd, err := createWindow(className, tooltipOrDefault(t.tooltip), 0, 0, 0, false)
	if err != nil {
		t.ready <- err
		return
	}
	t.hwnd = hwnd
	trayInstances.Store(hwnd, t)

	if err := t.initialiseMenu(); err != nil {
		trayInstances.Delete(hwnd)
		destroyWindow(hwnd)
		t.ready <- err
		return
	}

	t.nid = notifyIconData{
		HWnd:        hwnd,
		ID:          1,
		Flags:       nifMessage | nifIcon | nifTip,
		CallbackMsg: wmTrayMessage,
		HIcon:       icon,
	}
	writeUTF16String(t.nid.Tip[:], tooltipOrDefault(t.tooltip))

	if err := shellNotifyIcon(nidAdd, &t.nid); err != nil {
		trayInstances.Delete(hwnd)
		destroyWindow(hwnd)
		t.ready <- err
		return
	}

	t.ready <- nil
	messageLoop()
	trayInstances.Delete(hwnd)
}

func (t *SystemTray) initialiseMenu() error {
	menu := makeMenu()
	if menu == 0 {
		return errNotifyIcon
	}
	for _, item := range t.items {
		if item.Title == "" {
			appendMenu(menu, mfSeparator, 0, "")
			continue
		}
		appendMenu(menu, mfString, item.ID, item.Title)
		if item.Action != nil {
			t.callbacks[item.ID] = item.Action
		}
	}
	t.menu = menu
	return nil
}

func (t *SystemTray) showMenu() {
	if t.menu == 0 {
		return
	}
	pos, ok := getCursorPos()
	if !ok {
		return
	}
	trackPopupMenu(t.menu, tpmLeftAlign|tpmBottomAlign|tpmRightButton, pos.X, pos.Y, t.hwnd)
}

func (t *SystemTray) handleCommand(id uint16) {
	if cb, ok := t.callbacks[id]; ok && cb != nil {
		go cb()
	}
}

func (t *SystemTray) handleDoubleClick() {
	if t.onDoubleClick != nil {
		go t.onDoubleClick()
	}
}

func trayWindowProc(hwnd syscall.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	if value, ok := trayInstances.Load(hwnd); ok {
		tray := value.(*SystemTray)
		switch msg {
		case wmTrayMessage:
			switch uint32(lparam) {
			case wmRButtonUp, wmContextMenu:
				tray.showMenu()
			case wmLButtonDblClk:
				tray.handleDoubleClick()
			}
			return 0
		case wmCommand:
			tray.handleCommand(uint16(wparam & 0xffff))
			return 0
		case wmEventNotify:
			if wparam == trayEventShutdown {
				destroyWindow(hwnd)
			}
			return 0
		case wmDestroy:
			shellNotifyIcon(nidDelete, &tray.nid)
			trayInstances.Delete(hwnd)
			postQuitMessage(0)
			return 0
		}
	}
	return defWindowProc(hwnd, msg, wparam, lparam)
}

func tooltipOrDefault(text string) string {
	if strings.TrimSpace(text) == "" {
		return AppName
	}
	return text
}

func writeUTF16String(dst []uint16, value string) {
	if len(dst) == 0 {
		return
	}
	encoded := utf16.Encode([]rune(value))
	if len(encoded) >= len(dst) {
		encoded = encoded[:len(dst)-1]
	}
	n := copy(dst, encoded)
	if n < len(dst) {
		dst[n] = 0
	} else {
		dst[len(dst)-1] = 0
	}
}
