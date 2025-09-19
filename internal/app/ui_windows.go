//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
)

const (
	cmdSaveRestart   uint16 = 3001
	cmdSaveOnly      uint16 = 3002
	cmdStart         uint16 = 3004
	cmdRestart       uint16 = 3005
	cmdStop          uint16 = 3006
	cmdQuit          uint16 = 3007
	cmdBrowseInstall uint16 = 3008
	cmdBrowseLogs    uint16 = 3009
	cmdAddStartup    uint16 = 3010
	cmdRemoveStartup uint16 = 3011
)

const (
	trayCmdOpen    uint16 = 4001
	trayCmdStart   uint16 = 4002
	trayCmdRestart uint16 = 4003
	trayCmdStop    uint16 = 4004
	trayCmdQuit    uint16 = 4005
)

const startupRegistryPath = "Software\\Microsoft\\Windows\\CurrentVersion\\Run"
const startupRegistryDescription = "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run"

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
	hwnd, err := createWindow(mainClassName, AppName, 920, 560, 0)
	if err != nil {
		return nil, fmt.Errorf("create window: %w", err)
	}
	controller.hwnd = hwnd
	controller.createControls()
	controller.updateMonitoringButtons()
	controller.updateStartupButtons()
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
	client := getClientRect(c.hwnd)
	contentWidth := client.Right - client.Left
	margin := int32(18)
	gap := int32(12)
	rowHeight := int32(24)

	createStatic := func(text string, x, y, width int32) syscall.Handle {
		ctrl := createControl("STATIC", text, 0, x, y, width, rowHeight+4, c.hwnd, 0, 0)
		sendMessage(ctrl, wmSetFont, uintptr(font), 1)
		return ctrl
	}
	createButton := func(id uint16, text string, x, y, width int32) {
		ctrl := createControl("BUTTON", text, bsPushButton|wsTabStop, x, y, width, rowHeight+10, c.hwnd, id, 0)
		sendMessage(ctrl, wmSetFont, uintptr(font), 1)
		c.controls[id] = ctrl
	}

	labelWidth := int32(220)
	browseWidth := int32(110)
	editWidth := contentWidth - (margin*2 + labelWidth + browseWidth + gap)
	if editWidth < 240 {
		editWidth = 240
	}
	y := margin

	createStatic("Install Folder (logs/cache):", margin, y, labelWidth)
	installEdit := createControl("EDIT", c.cfg.InstallDir, esAutoHScroll|wsTabStop, margin+labelWidth, y-2, editWidth, rowHeight+6, c.hwnd, ctrlInstallEdit, wsExClientEdge)
	sendMessage(installEdit, wmSetFont, uintptr(font), 1)
	c.controls[ctrlInstallEdit] = installEdit
	createButton(cmdBrowseInstall, "Browse...", margin+labelWidth+editWidth+gap, y-4, browseWidth)

	y += rowHeight + 18

	createStatic("VRChat Log Folder:", margin, y, labelWidth)
	logEdit := createControl("EDIT", c.cfg.VRChatLogDir, esAutoHScroll|wsTabStop, margin+labelWidth, y-2, editWidth, rowHeight+6, c.hwnd, ctrlLogEdit, wsExClientEdge)
	sendMessage(logEdit, wmSetFont, uintptr(font), 1)
	c.controls[ctrlLogEdit] = logEdit
	createButton(cmdBrowseLogs, "Browse...", margin+labelWidth+editWidth+gap, y-4, browseWidth)

	y += rowHeight + 20

	credentialLabelWidth := int32(160)
	columnGap := int32(24)
	credentialFieldWidth := (contentWidth - (margin*2 + credentialLabelWidth*2 + columnGap)) / 2
	if credentialFieldWidth < 180 {
		credentialFieldWidth = 180
	}

	createStatic("Pushover User Key:", margin, y, credentialLabelWidth)
	userEdit := createControl("EDIT", c.cfg.PushoverUser, esAutoHScroll|wsTabStop, margin+credentialLabelWidth, y-2, credentialFieldWidth, rowHeight+6, c.hwnd, ctrlUserEdit, wsExClientEdge)
	sendMessage(userEdit, wmSetFont, uintptr(font), 1)
	c.controls[ctrlUserEdit] = userEdit

	tokenX := margin + credentialLabelWidth + credentialFieldWidth + columnGap
	createStatic("Pushover API Token:", tokenX, y, credentialLabelWidth)
	tokenEdit := createControl("EDIT", c.cfg.PushoverToken, esAutoHScroll|wsTabStop, tokenX+credentialLabelWidth, y-2, credentialFieldWidth, rowHeight+6, c.hwnd, ctrlTokenEdit, wsExClientEdge)
	sendMessage(tokenEdit, wmSetFont, uintptr(font), 1)
	c.controls[ctrlTokenEdit] = tokenEdit

	y += rowHeight + 28

	primaryButtonCount := int32(3)
	primaryAvailable := contentWidth - (margin*2 + gap*(primaryButtonCount-1))
	primaryWidth := primaryAvailable / primaryButtonCount
	if primaryWidth < 170 {
		primaryWidth = 170
	}
	buttonY := y
	x := margin
	createButton(cmdSaveRestart, "Save and Restart Monitoring", x, buttonY, primaryWidth)
	x += primaryWidth + gap
	createButton(cmdStart, "Start Monitoring", x, buttonY, primaryWidth)
	x += primaryWidth + gap
	createButton(cmdStop, "Stop Monitoring", x, buttonY, primaryWidth)

	y += rowHeight + 42

	secondaryButtonCount := int32(4)
	secondaryAvailable := contentWidth - (margin*2 + gap*(secondaryButtonCount-1))
	secondaryWidth := secondaryAvailable / secondaryButtonCount
	if secondaryWidth < 170 {
		secondaryWidth = 170
	}
	x = margin
	createButton(cmdAddStartup, "Add to Startup", x, y, secondaryWidth)
	x += secondaryWidth + gap
	createButton(cmdRemoveStartup, "Remove from Startup", x, y, secondaryWidth)
	x += secondaryWidth + gap
	createButton(cmdSaveOnly, "Save", x, y, secondaryWidth)
	x += secondaryWidth + gap
	createButton(cmdQuit, "Quit", x, y, secondaryWidth)

	y += rowHeight + 36

	infoLabelWidth := int32(110)
	infoValueWidth := contentWidth - (margin*2 + infoLabelWidth)
	if infoValueWidth < 260 {
		infoValueWidth = 260
	}
	makeInfoRow := func(caption string, id uint16) {
		createStatic(caption, margin, y, infoLabelWidth)
		value := createControl("STATIC", "", 0, margin+infoLabelWidth, y, infoValueWidth, rowHeight+4, c.hwnd, id, 0)
		sendMessage(value, wmSetFont, uintptr(font), 1)
		c.controls[id] = value
		y += rowHeight + 10
	}
	makeInfoRow("Monitor:", ctrlMonitorLabel)
	makeInfoRow("Current log:", ctrlCurrentLog)
	makeInfoRow("Session:", ctrlSessionLabel)
	makeInfoRow("Last event:", ctrlLastEvent)
	makeInfoRow("Status:", ctrlStatusLabel)

	c.setLabel(ctrlMonitorLabel, "Stopped")
	c.setLabel(ctrlCurrentLog, "(none)")
	c.setLabel(ctrlSessionLabel, "No active session")
	c.setLabel(ctrlLastEvent, "")
	c.setLabel(ctrlStatusLabel, "Idle")
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
		c.setStatus("Welcome! Configure folders and optional Pushover keys, then click Save and Restart Monitoring.")
		c.updateMonitoringButtons()
		return
	}
	if strings.TrimSpace(c.cfg.PushoverUser) != "" && strings.TrimSpace(c.cfg.PushoverToken) != "" {
		c.startMonitoring()
	} else {
		c.setStatus("Optional: enter your Pushover keys for push notifications, then click Save and Restart Monitoring when ready.")
	}
	c.updateStartupButtons()
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
	case cmdStart:
		if c.monitor != nil {
			c.setStatus("Monitoring is already running.")
		} else {
			c.startMonitoring()
		}
	case cmdRestart:
		c.restartMonitoring()
	case cmdStop:
		c.stopMonitoring()
	case cmdQuit:
		c.requestQuit()
	case cmdBrowseInstall, cmdBrowseLogs:
		c.setStatus("Enter the folder path manually in the text box.")
	case cmdAddStartup:
		c.addToStartup()
	case cmdRemoveStartup:
		c.removeFromStartup()
	case trayCmdOpen:
		c.showWindow()
	case trayCmdStart:
		if c.monitor != nil {
			c.setStatus("Monitoring is already running.")
		} else {
			c.startMonitoring()
		}
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
	wasRunning := c.monitor != nil
	if wasRunning {
		c.stopMonitoring()
	}
	c.startMonitoring()
	switch {
	case c.monitor != nil && wasRunning:
		c.setStatus("Settings saved. Monitoring restarted.")
	case c.monitor != nil:
		c.setStatus("Settings saved. Monitoring started.")
	default:
		c.setStatus("Settings saved.")
	}
	c.notifier.Send(AppName, "Settings saved.")
}

func (c *Controller) saveOnly() {
	if err := c.saveConfig(); err != nil {
		c.setStatus(fmt.Sprintf("Failed to save settings: %v", err))
		return
	}
	c.setStatus("Settings saved.")
	c.notifier.Send(AppName, "Settings saved.")
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
	c.session.Reset("Monitoring VRChat logs...")
	c.setLabel(ctrlMonitorLabel, "Running")
	c.setStatus("Monitoring VRChat logs...")
	if c.logger != nil {
		c.logger.Log("Monitoring started.")
	}
	c.updateMonitoringButtons()
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
	c.updateMonitoringButtons()
	c.updateTray()
}

func (c *Controller) restartMonitoring() {
	running := c.monitor != nil
	if running {
		c.stopMonitoring()
	}
	c.startMonitoring()
	if c.monitor != nil {
		c.setStatus("Monitoring restarted.")
		if c.logger != nil {
			c.logger.Log("Monitoring restarted.")
		}
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

func (c *Controller) updateMonitoringButtons() {
	running := c.monitor != nil
	c.setControlEnabled(cmdStart, !running)
	c.setControlEnabled(cmdStop, running)
}

func (c *Controller) updateStartupButtons() {
	exists, err := startupEntryExists()
	if err != nil {
		if c.logger != nil {
			c.logger.Logf("Failed to query startup entry: %v", err)
		}
		c.setStatus(fmt.Sprintf("Failed to query startup entry: %v", err))
		return
	}
	c.setControlEnabled(cmdAddStartup, !exists)
	c.setControlEnabled(cmdRemoveStartup, exists)
}

func (c *Controller) setControlEnabled(id uint16, enabled bool) {
	if hwnd, ok := c.controls[id]; ok {
		enableWindow(hwnd, enabled)
	}
}

func (c *Controller) startupCommand() (string, error) {
	exe, err := osExecutable()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(exe) == "" {
		return "", fmt.Errorf("unable to determine executable path")
	}
	return strconv.Quote(exe), nil
}

func (c *Controller) addToStartup() {
	command, err := c.startupCommand()
	if err != nil {
		c.handleStartupError("Failed to determine startup command", err)
		return
	}
	if err := setStartupEntry(command); err != nil {
		c.handleStartupError("Failed to add to startup", err)
		return
	}
	c.setStatus("Added to startup.")
	if c.logger != nil {
		c.logger.Logf("Startup entry created at %s", startupRegistryDescription)
	}
	c.notifier.Send(AppName, "Added to startup.")
	c.updateStartupButtons()
}

func (c *Controller) removeFromStartup() {
	if err := removeStartupEntry(); err != nil {
		c.handleStartupError("Failed to remove from startup", err)
		return
	}
	c.setStatus("Removed from startup.")
	if c.logger != nil {
		c.logger.Logf("Startup entry removed from %s", startupRegistryDescription)
	}
	c.notifier.Send(AppName, "Removed from startup.")
	c.updateStartupButtons()
}

func (c *Controller) handleStartupError(action string, err error) {
	message := fmt.Sprintf("%s:\n%v", action, err)
	ShowMessage(message, AppName, MBOK|MBIconError)
	c.setStatus(fmt.Sprintf("%s: %v", action, err))
	if c.logger != nil {
		c.logger.Logf("%s: %v", action, err)
	}
	c.notifier.Send(AppName, fmt.Sprintf("%s: %v", action, err))
}

func startupEntryExists() (bool, error) {
	key, err := regOpenKey(hkeyCurrentUser, startupRegistryPath, keyQueryValue)
	if err != nil {
		if err == errFileNotFound {
			return false, nil
		}
		return false, err
	}
	defer regCloseKey(key)
	return regValueExists(key, AppName)
}

func setStartupEntry(command string) error {
	key, err := regCreateKey(hkeyCurrentUser, startupRegistryPath, keyRead|keyWrite)
	if err != nil {
		return err
	}
	defer regCloseKey(key)
	return regSetStringValue(key, AppName, command)
}

func removeStartupEntry() error {
	key, err := regOpenKey(hkeyCurrentUser, startupRegistryPath, keySetValue)
	if err != nil {
		if err == errFileNotFound {
			return nil
		}
		return err
	}
	defer regCloseKey(key)
	return regDeleteValue(key, AppName)
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
