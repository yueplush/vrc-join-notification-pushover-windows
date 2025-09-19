//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

const (
	ctrlInstallEdit  uint16 = 1001
	ctrlLogEdit      uint16 = 1002
	ctrlUserEdit     uint16 = 1003
	ctrlTokenEdit    uint16 = 1004
	ctrlStatusLabel  uint16 = 2001
	ctrlMonitorLabel uint16 = 2002
	ctrlCurrentLog   uint16 = 2003
	ctrlSessionLabel uint16 = 2004
	ctrlLastEvent    uint16 = 2005

	cmdSaveRestart   uint16 = 3001
	cmdSaveOnly      uint16 = 3002
	cmdOpenInstall   uint16 = 3003
	cmdStart         uint16 = 3004
	cmdRestart       uint16 = 3005
	cmdStop          uint16 = 3006
	cmdQuit          uint16 = 3007
	cmdBrowseInstall uint16 = 3008
	cmdBrowseLogs    uint16 = 3009
)

const (
	trayCmdOpen    uint16 = 4001
	trayCmdStart   uint16 = 4002
	trayCmdRestart uint16 = 4003
	trayCmdStop    uint16 = 4004
	trayCmdQuit    uint16 = 4005
)

var (
	mainClassName    = "VRChatJoinNotifierWindow"
	windowProc       = syscall.NewCallback(wndProc)
	activeController *Controller
)

// Controller coordinates the Win32 window, tray icon and background workers.
type Controller struct {
	cfg      *AppConfig
	logger   *AppLogger
	notifier *DesktopNotifier
	pushover *PushoverClient
	session  *SessionTracker

	hwnd       syscall.Handle
	icon       syscall.Handle
	controls   map[uint16]syscall.Handle
	tray       *trayIcon
	monitor    *LogMonitor
	eventCh    chan MonitorEvent
	eventMu    sync.Mutex
	eventQueue []MonitorEvent

	loadNotice string
	quitting   bool
}

func NewController(cfg *AppConfig, loadNotice string, logger *AppLogger) (*Controller, error) {
	controller := &Controller{
		cfg:        cfg,
		logger:     logger,
		notifier:   NewDesktopNotifier(logger),
		pushover:   NewPushoverClient(cfg, logger),
		loadNotice: loadNotice,
		controls:   make(map[uint16]syscall.Handle),
		eventCh:    make(chan MonitorEvent, 64),
	}
	controller.session = NewSessionTracker(controller.notifier, controller.pushover, controller.logger)
	iconPath := locateNotificationIcon()
	controller.icon = loadIconFromFile(iconPath)
	if controller.icon == 0 {
		controller.icon = loadDefaultIcon()
	}
	activeController = controller
	if err := registerClass(mainClassName, windowProc, controller.icon); err != nil {
		return nil, fmt.Errorf("register window class: %w", err)
	}
	hwnd, err := createWindow(mainClassName, AppName, 820, 520, 0)
	if err != nil {
		return nil, fmt.Errorf("create window: %w", err)
	}
	controller.hwnd = hwnd
	controller.createControls()
	showWindow(controller.hwnd)
	tray, err := newTrayIcon(controller.hwnd, controller.icon)
	if err != nil {
		if controller.logger != nil {
			controller.logger.Logf("Tray icon disabled: %v", err)
		}
	} else {
		controller.tray = tray
	}
	go controller.processEvents()
	controller.applyStartupState()
	return controller, nil
}

func (c *Controller) Run() error {
	defer c.cleanup()
	returnCode := messageLoop()
	if c.logger != nil {
		c.logger.Logf("Message loop exited with code %d", returnCode)
	}
	return nil
}

func (c *Controller) cleanup() {
	if c.monitor != nil {
		c.monitor.Stop()
		c.monitor = nil
	}
	if c.tray != nil {
		c.tray.dispose()
		c.tray = nil
	}
	close(c.eventCh)
}

func (c *Controller) createControls() {
	font := defaultUIFont()
	xMargin := int32(16)
	y := int32(18)
	labelWidth := int32(150)
	editWidth := int32(420)
	buttonWidth := int32(140)
	rowHeight := int32(24)
	gap := int32(8)

	createLabel := func(text string, x, y, width int32) {
		ctrl := createControl("STATIC", text, 0, x, y, width, rowHeight, c.hwnd, 0, 0)
		sendMessage(ctrl, wmSetFont, uintptr(font), 1)
	}
	createButton := func(id uint16, text string, x, y int32) {
		ctrl := createControl("BUTTON", text, bsPushButton|wsTabStop, x, y, buttonWidth, rowHeight+6, c.hwnd, id, 0)
		sendMessage(ctrl, wmSetFont, uintptr(font), 1)
		c.controls[id] = ctrl
	}

	createLabel("Install directory", xMargin, y, labelWidth)
	installEdit := createControl("EDIT", c.cfg.InstallDir, esAutoHScroll|wsTabStop, xMargin+labelWidth, y-2, editWidth, rowHeight+4, c.hwnd, ctrlInstallEdit, wsExClientEdge)
	sendMessage(installEdit, wmSetFont, uintptr(font), 1)
	c.controls[ctrlInstallEdit] = installEdit
	createButton(cmdBrowseInstall, "Browse...", xMargin+labelWidth+editWidth+gap, y-4)

	y += rowHeight + 18

	createLabel("VRChat log directory", xMargin, y, labelWidth)
	logEdit := createControl("EDIT", c.cfg.VRChatLogDir, esAutoHScroll|wsTabStop, xMargin+labelWidth, y-2, editWidth, rowHeight+4, c.hwnd, ctrlLogEdit, wsExClientEdge)
	sendMessage(logEdit, wmSetFont, uintptr(font), 1)
	c.controls[ctrlLogEdit] = logEdit
	createButton(cmdBrowseLogs, "Browse...", xMargin+labelWidth+editWidth+gap, y-4)

	y += rowHeight + 18

	createLabel("Pushover user key", xMargin, y, labelWidth)
	userEdit := createControl("EDIT", c.cfg.PushoverUser, esAutoHScroll|wsTabStop, xMargin+labelWidth, y-2, editWidth, rowHeight+4, c.hwnd, ctrlUserEdit, wsExClientEdge)
	sendMessage(userEdit, wmSetFont, uintptr(font), 1)
	c.controls[ctrlUserEdit] = userEdit

	y += rowHeight + 12

	createLabel("Pushover API token", xMargin, y, labelWidth)
	tokenEdit := createControl("EDIT", c.cfg.PushoverToken, esAutoHScroll|wsTabStop, xMargin+labelWidth, y-2, editWidth, rowHeight+4, c.hwnd, ctrlTokenEdit, wsExClientEdge)
	sendMessage(tokenEdit, wmSetFont, uintptr(font), 1)
	c.controls[ctrlTokenEdit] = tokenEdit

	y += rowHeight + 24

	createButton(cmdSaveRestart, "Save & Restart", xMargin, y)
	createButton(cmdSaveOnly, "Save Only", xMargin+buttonWidth+gap, y)
	createButton(cmdOpenInstall, "Open Install Folder", xMargin+2*(buttonWidth+gap), y)

	y += rowHeight + 30

	createButton(cmdStart, "Start Monitoring", xMargin, y)
	createButton(cmdRestart, "Restart Monitoring", xMargin+buttonWidth+gap, y)
	createButton(cmdStop, "Stop Monitoring", xMargin+2*(buttonWidth+gap), y)

	y += rowHeight + 30

	statusLabel := createControl("STATIC", "Idle", 0, xMargin, y, editWidth+labelWidth+buttonWidth, rowHeight+4, c.hwnd, ctrlStatusLabel, 0)
	sendMessage(statusLabel, wmSetFont, uintptr(font), 1)
	c.controls[ctrlStatusLabel] = statusLabel

	y += rowHeight + 8
	monitorLabel := createControl("STATIC", "Stopped", 0, xMargin, y, editWidth+labelWidth+buttonWidth, rowHeight+4, c.hwnd, ctrlMonitorLabel, 0)
	sendMessage(monitorLabel, wmSetFont, uintptr(font), 1)
	c.controls[ctrlMonitorLabel] = monitorLabel

	y += rowHeight + 8
	logLabel := createControl("STATIC", "(none)", 0, xMargin, y, editWidth+labelWidth+buttonWidth, rowHeight+4, c.hwnd, ctrlCurrentLog, 0)
	sendMessage(logLabel, wmSetFont, uintptr(font), 1)
	c.controls[ctrlCurrentLog] = logLabel

	y += rowHeight + 8
	sessionLabel := createControl("STATIC", "No active session", 0, xMargin, y, editWidth+labelWidth+buttonWidth, rowHeight+4, c.hwnd, ctrlSessionLabel, 0)
	sendMessage(sessionLabel, wmSetFont, uintptr(font), 1)
	c.controls[ctrlSessionLabel] = sessionLabel

	y += rowHeight + 8
	lastEvent := createControl("STATIC", "", 0, xMargin, y, editWidth+labelWidth+buttonWidth, rowHeight+4, c.hwnd, ctrlLastEvent, 0)
	sendMessage(lastEvent, wmSetFont, uintptr(font), 1)
	c.controls[ctrlLastEvent] = lastEvent
}

func (c *Controller) processEvents() {
	for ev := range c.eventCh {
		c.eventMu.Lock()
		c.eventQueue = append(c.eventQueue, ev)
		c.eventMu.Unlock()
		postMessage(c.hwnd, wmEventNotify, 0, 0)
	}
}

func (c *Controller) drainEvents() []MonitorEvent {
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	events := make([]MonitorEvent, len(c.eventQueue))
	copy(events, c.eventQueue)
	c.eventQueue = c.eventQueue[:0]
	return events
}

func (c *Controller) applyStartupState() {
	if c.loadNotice != "" {
		c.setStatus(c.loadNotice)
	}
	if c.cfg.FirstRun {
		c.setStatus("Welcome! Configure folders and optional Pushover keys, then click Save & Restart Monitoring.")
		return
	}
	if strings.TrimSpace(c.cfg.PushoverUser) != "" && strings.TrimSpace(c.cfg.PushoverToken) != "" {
		c.startMonitoring()
	} else {
		c.setStatus("Optional: add your Pushover keys then click Save & Restart Monitoring when ready.")
	}
}

func (c *Controller) handleEvent(ev MonitorEvent) {
	switch ev.Type {
	case EventStatus:
		c.setStatus(ev.Message)
	case EventLogSwitch:
		c.setLabel(ctrlCurrentLog, ev.Path)
		c.session.HandleLogSwitch(ev.Path)
		c.setLabel(ctrlSessionLabel, c.session.Summary())
		c.setStatus("Monitoring " + filepath.Base(ev.Path))
	case EventError:
		c.setStatus(ev.Message)
	case EventRoomEnter:
		msg := c.session.HandleRoomEnter(ev.Room)
		c.setStatus(msg)
		c.setLabel(ctrlSessionLabel, c.session.Summary())
	case EventRoomLeft:
		msg := c.session.HandleRoomLeft()
		c.setStatus(msg)
		c.setLabel(ctrlSessionLabel, c.session.Summary())
	case EventSelfJoin:
		c.session.HandleSelfJoin(ev.Message)
		c.setLabel(ctrlSessionLabel, c.session.Summary())
	case EventPlayerJoin:
		if msg := c.session.HandlePlayerJoin(ev.Player); msg != "" {
			c.setStatus(msg)
		}
	case EventPlayerLeft:
		if name := c.session.HandlePlayerLeft(ev.Player); name != "" {
			c.setStatus(fmt.Sprintf("%s left the instance.", name))
		}
	}
	c.setLabel(ctrlLastEvent, c.session.LastEvent())
	c.updateTray()
}

func (c *Controller) handleCommand(id uint16) {
	switch id {
	case cmdSaveRestart:
		c.saveAndRestart()
	case cmdSaveOnly:
		c.saveOnly()
	case cmdOpenInstall:
		if c.logger != nil {
			c.logger.OpenLogDirectory()
		}
	case cmdStart:
		c.startMonitoring()
	case cmdRestart:
		c.restartMonitoring()
	case cmdStop:
		c.stopMonitoring()
	case cmdQuit:
		c.requestQuit()
	case cmdBrowseInstall, cmdBrowseLogs:
		c.setStatus("Enter the folder path manually in the text box.")
	case trayCmdOpen:
		c.showWindow()
	case trayCmdStart:
		c.startMonitoring()
	case trayCmdRestart:
		c.restartMonitoring()
	case trayCmdStop:
		c.stopMonitoring()
	case trayCmdQuit:
		c.requestQuit()
	}
}

func (c *Controller) saveAndRestart() {
	if err := c.saveConfig(); err != nil {
		c.setStatus(fmt.Sprintf("Failed to save settings: %v", err))
		return
	}
	c.startMonitoring()
	c.setStatus("Settings saved & monitoring restarted.")
}

func (c *Controller) saveOnly() {
	if err := c.saveConfig(); err != nil {
		c.setStatus(fmt.Sprintf("Failed to save settings: %v", err))
		return
	}
	c.setStatus("Settings saved.")
}

func (c *Controller) saveConfig() error {
	c.cfg.InstallDir = expandPath(c.getText(ctrlInstallEdit))
	c.cfg.VRChatLogDir = expandPath(c.getText(ctrlLogEdit))
	c.cfg.PushoverUser = strings.TrimSpace(c.getText(ctrlUserEdit))
	c.cfg.PushoverToken = strings.TrimSpace(c.getText(ctrlTokenEdit))
	if err := c.cfg.Save(); err != nil {
		return err
	}
	if c.logger != nil {
		c.logger.Log("Settings saved.")
	}
	return nil
}

func (c *Controller) startMonitoring() {
	if c.monitor != nil {
		return
	}
	c.monitor = NewLogMonitor(c.cfg, c.logger, c.eventCh)
	c.monitor.Start()
	c.setLabel(ctrlMonitorLabel, "Running")
	c.setStatus("Monitoring started.")
	if c.logger != nil {
		c.logger.Log("Monitoring started.")
	}
	c.updateTray()
}

func (c *Controller) stopMonitoring() {
	if c.monitor == nil {
		return
	}
	c.monitor.Stop()
	c.monitor = nil
	c.session.Reset("Monitoring stopped by user.")
	c.setLabel(ctrlMonitorLabel, "Stopped")
	c.setStatus("Monitoring stopped.")
	c.setLabel(ctrlCurrentLog, "(none)")
	c.setLabel(ctrlSessionLabel, "No active session")
	c.setLabel(ctrlLastEvent, "")
	if c.logger != nil {
		c.logger.Log("Monitoring stopped.")
	}
	c.updateTray()
}

func (c *Controller) restartMonitoring() {
	c.stopMonitoring()
	c.startMonitoring()
	c.setStatus("Monitoring restarted.")
	if c.logger != nil {
		c.logger.Log("Monitoring restarted.")
	}
}

func (c *Controller) setStatus(text string) {
	c.setLabel(ctrlStatusLabel, text)
}

func (c *Controller) setLabel(id uint16, text string) {
	if hwnd, ok := c.controls[id]; ok {
		setWindowText(hwnd, text)
	}
}

func (c *Controller) getText(id uint16) string {
	if hwnd, ok := c.controls[id]; ok {
		return strings.TrimSpace(getWindowText(hwnd))
	}
	return ""
}

func (c *Controller) updateTray() {
	if c.tray == nil {
		return
	}
	monitoring := c.monitor != nil
	summary := c.session.Summary()
	tooltip := AppName
	if monitoring {
		tooltip = AppName + " - Monitoring"
	} else {
		tooltip = AppName + " - Stopped"
	}
	if summary != "" {
		tooltip += "\n" + summary
	}
	_ = c.tray.setTooltip(tooltip)
	c.tray.setMonitoring(monitoring)
}

func (c *Controller) showWindow() {
	showWindow(c.hwnd)
}

func (c *Controller) hideWindow() {
	hideWindow(c.hwnd)
}

func (c *Controller) requestQuit() {
	if c.quitting {
		return
	}
	c.quitting = true
	c.stopMonitoring()
	destroyWindow(c.hwnd)
}

func wndProc(hwnd syscall.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	if activeController != nil && activeController.hwnd == hwnd {
		return activeController.handleMessage(msg, wparam, lparam)
	}
	if msg == wmDestroy {
		postQuitMessage(0)
		return 0
	}
	return defWindowProc(hwnd, msg, wparam, lparam)
}

func (c *Controller) handleMessage(msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmCommand:
		id := uint16(wparam & 0xFFFF)
		c.handleCommand(id)
		return 0
	case wmTrayMessage:
		switch lparam {
		case 0x0202: // WM_LBUTTONUP
			c.showWindow()
		case 0x0205: // WM_RBUTTONUP
			c.showTrayMenu()
		}
		return 0
	case wmEventNotify:
		events := c.drainEvents()
		for _, ev := range events {
			c.handleEvent(ev)
		}
		return 0
	case wmClose:
		if !c.quitting {
			c.hideWindow()
			return 0
		}
	case wmDestroy:
		activeController = nil
		postQuitMessage(0)
		return 0
	}
	return defWindowProc(c.hwnd, msg, wparam, lparam)
}

func (c *Controller) showTrayMenu() {
	if c.tray == nil {
		return
	}
	c.tray.showMenu()
}

// locateNotificationIcon searches common paths for notification.ico.
func locateNotificationIcon() string {
	candidates := []string{}
	if exe, err := osExecutable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, IconFileName),
			filepath.Join(dir, "vrchat_join_notification", IconFileName),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, IconFileName),
			filepath.Join(cwd, "vrchat_join_notification", IconFileName),
		)
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func osExecutable() (string, error) {
	return os.Executable()
}

type trayIcon struct {
	hwnd       syscall.Handle
	icon       syscall.Handle
	menu       syscall.Handle
	added      bool
	monitoring bool
}

func newTrayIcon(hwnd syscall.Handle, icon syscall.Handle) (*trayIcon, error) {
	t := &trayIcon{hwnd: hwnd, icon: icon}
	menu := makeMenu()
	appendMenu(menu, mfString, trayCmdOpen, "Open Settings")
	appendMenu(menu, mfString, trayCmdStart, "Start Monitoring")
	appendMenu(menu, mfString, trayCmdRestart, "Restart Monitoring")
	appendMenu(menu, mfString, trayCmdStop, "Stop Monitoring")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, trayCmdQuit, "Quit")
	t.menu = menu
	if err := t.setTooltip(AppName); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *trayIcon) setMonitoring(active bool) {
	t.monitoring = active
}

func (t *trayIcon) setTooltip(text string) error {
	var data notifyIconData
	data.HWnd = t.hwnd
	data.ID = 1
	data.Flags = nifMessage | nifIcon | nifTip
	data.CallbackMsg = wmTrayMessage
	data.HIcon = t.icon
	copyUTF16(data.Tip[:], text)
	if !t.added {
		if err := shellNotifyIcon(nidAdd, &data); err != nil {
			return err
		}
		data.TimeoutOrVersion = 4
		_ = shellNotifyIcon(0x00000004, &data)
		t.added = true
	} else {
		if err := shellNotifyIcon(nidModify, &data); err != nil {
			return err
		}
	}
	return nil
}

func (t *trayIcon) showMenu() {
	if t.menu == 0 {
		return
	}
	p, ok := getCursorPos()
	if !ok {
		return
	}
	trackPopupMenu(t.menu, 0x0000|0x0002, p.X, p.Y, t.hwnd)
}

func (t *trayIcon) dispose() {
	if t.added {
		var data notifyIconData
		data.HWnd = t.hwnd
		data.ID = 1
		_ = shellNotifyIcon(nidDelete, &data)
		t.added = false
	}
}

func copyUTF16(dest []uint16, text string) {
	runes := syscall.StringToUTF16(text)
	max := len(dest)
	for i := 0; i < max && i < len(runes)-1; i++ {
		dest[i] = runes[i]
	}
	if max > 0 {
		dest[max-1] = 0
	}
}
