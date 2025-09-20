//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"vrchat-join-notification-with-pushover/internal/assets"
)

const (
	windowWidth  = 880
	windowHeight = 520

	startupRegistryPath        = "Software\\Microsoft\\Windows\\CurrentVersion\\Run"
	startupRegistryDescription = "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run"

	trayMenuOpenSettingsID uint16 = 1
	trayMenuStartID        uint16 = 2
	trayMenuStopID         uint16 = 3
	trayMenuResetID        uint16 = 4
	trayMenuExitID         uint16 = 5
)

// Controller owns the application window, widgets and background workers.
type Controller struct {
	cfg      *AppConfig
	logger   *AppLogger
	notifier *DesktopNotifier
	pushover *PushoverClient
	session  *SessionTracker

	app    fyne.App
	window fyne.Window

	iconData []byte

	installEntry *widget.Entry
	logEntry     *widget.Entry
	userEntry    *widget.Entry
	tokenEntry   *widget.Entry

	monitorLabel    *widget.Label
	currentLogLabel *widget.Label
	sessionLabel    *widget.Label
	lastEventLabel  *widget.Label
	statusLabel     *widget.Label

	saveRestartButton   *widget.Button
	startButton         *widget.Button
	stopButton          *widget.Button
	addStartupButton    *widget.Button
	removeStartupButton *widget.Button
	saveButton          *widget.Button
	quitButton          *widget.Button

	monitor    *LogMonitor
	eventCh    chan MonitorEvent
	eventMu    sync.Mutex
	eventQueue []MonitorEvent
	eventDone  chan struct{}

	loadNotice string
	quitting   bool

	tray *SystemTray

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	windowStateMu     sync.Mutex
	windowHandle      syscall.Handle
	windowMinimized   bool
	windowIconApplied bool
	windowIconHandle  syscall.Handle
}

// NewController constructs the Fyne based GUI controller.
func NewController(cfg *AppConfig, loadNotice string, logger *AppLogger) (*Controller, error) {
	controller := &Controller{
		cfg:        cfg,
		logger:     logger,
		notifier:   NewDesktopNotifier(logger),
		pushover:   NewPushoverClient(cfg, logger),
		session:    nil,
		eventCh:    make(chan MonitorEvent, 64),
		eventDone:  make(chan struct{}),
		loadNotice: loadNotice,
		stopCh:     make(chan struct{}),
	}
	controller.session = NewSessionTracker(controller.notifier, controller.pushover, controller.logger)

	controller.app = fyneapp.NewWithID("VRChatJoinNotificationWithPushover")
	controller.window = controller.app.NewWindow(AppName)
	controller.window.Resize(fyne.NewSize(windowWidth, windowHeight))
	controller.window.SetMaster()
	controller.window.SetOnClosed(func() {
		controller.quitting = true
	})

	controller.buildUI()
	controller.updateMonitoringButtons()
	controller.updateStartupButtons()
	controller.applyStartupState()

	controller.iconData = notificationIconData()
	if len(controller.iconData) > 0 {
		resource := fyne.NewStaticResource(assets.NotificationIconName(), controller.iconData)
		controller.app.SetIcon(resource)
		controller.window.SetIcon(resource)
	}

	controller.initSystemTray()

	go controller.consumeEvents()
	return controller, nil
}

// Run starts the UI event loop.
func (c *Controller) Run() error {
	defer c.cleanup()
	c.window.Show()
	c.app.Run()
	return nil
}

func (c *Controller) runOnMain(fn func()) {
	if fn == nil {
		return
	}
	if runner, ok := any(c.app).(interface{ RunOnMain(func()) }); ok {
		runner.RunOnMain(fn)
		return
	}
	if drv := c.app.Driver(); drv != nil {
		if runner, ok := any(drv).(interface{ RunOnMain(func()) }); ok {
			runner.RunOnMain(fn)
			return
		}
	}
	fn()
}

func (c *Controller) cleanup() {
	c.stopMonitoring()
	c.shutdownTray()
	c.releaseWindowIcon()
	close(c.eventCh)
	<-c.eventDone
}

func (c *Controller) consumeEvents() {
	defer close(c.eventDone)
	for ev := range c.eventCh {
		c.eventMu.Lock()
		c.eventQueue = append(c.eventQueue, ev)
		c.eventMu.Unlock()
		c.runOnMain(func() {
			c.flushEvents()
		})
	}
}

func (c *Controller) flushEvents() {
	events := c.drainEvents()
	for _, ev := range events {
		c.handleEvent(ev)
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

func (c *Controller) buildUI() {
	c.installEntry = widget.NewEntry()
	c.installEntry.SetText(c.cfg.InstallDir)
	c.logEntry = widget.NewEntry()
	c.logEntry.SetText(c.cfg.VRChatLogDir)
	c.userEntry = widget.NewEntry()
	c.userEntry.SetText(c.cfg.PushoverUser)
	c.tokenEntry = widget.NewEntry()
	c.tokenEntry.SetText(c.cfg.PushoverToken)

	browseInstall := widget.NewButton("Browse...", func() {
		c.chooseFolder(c.installEntry)
	})
	browseLogs := widget.NewButton("Browse...", func() {
		c.chooseFolder(c.logEntry)
	})

	installRow := container.NewBorder(nil, nil, nil, browseInstall, c.installEntry)
	logRow := container.NewBorder(nil, nil, nil, browseLogs, c.logEntry)

	pathsForm := widget.NewForm(
		widget.NewFormItem("Install Folder (logs/cache):", installRow),
		widget.NewFormItem("VRChat Log Folder:", logRow),
	)

	userLabel := widget.NewLabel("Pushover User Key:")
	tokenLabel := widget.NewLabel("Pushover API Token:")
	userLabel.Alignment = fyne.TextAlignLeading
	tokenLabel.Alignment = fyne.TextAlignLeading

	pushoverRow := container.NewGridWithColumns(2,
		container.NewVBox(userLabel, c.userEntry),
		container.NewVBox(tokenLabel, c.tokenEntry),
	)

	c.saveRestartButton = widget.NewButton("Save and Restart Monitoring", func() {
		c.saveAndRestart()
	})
	c.startButton = widget.NewButton("Start Monitoring", func() {
		if c.monitor != nil {
			c.setStatus("Monitoring is already running.")
			return
		}
		c.startMonitoring()
	})
	c.stopButton = widget.NewButton("Stop Monitoring", func() {
		c.stopMonitoring()
	})

	primaryButtons := container.NewGridWithColumns(3,
		c.saveRestartButton,
		c.startButton,
		c.stopButton,
	)

	c.addStartupButton = widget.NewButton("Add to Startup", func() {
		c.addToStartup()
	})
	c.removeStartupButton = widget.NewButton("Remove from Startup", func() {
		c.removeFromStartup()
	})
	c.saveButton = widget.NewButton("Save", func() {
		c.saveOnly()
	})
	c.quitButton = widget.NewButton("Quit", func() {
		c.requestQuit()
	})

	secondaryButtons := container.NewGridWithColumns(4,
		c.addStartupButton,
		c.removeStartupButton,
		c.saveButton,
		c.quitButton,
	)

	c.monitorLabel = widget.NewLabel("Stopped")
	c.currentLogLabel = widget.NewLabel("(none)")
	c.sessionLabel = widget.NewLabel("No active session")
	c.lastEventLabel = widget.NewLabel("")
	c.statusLabel = widget.NewLabel("Idle")

	for _, lbl := range []*widget.Label{c.monitorLabel, c.currentLogLabel, c.sessionLabel, c.lastEventLabel, c.statusLabel} {
		lbl.Wrapping = fyne.TextWrapWord
	}

	infoForm := widget.NewForm(
		widget.NewFormItem("Monitor:", c.monitorLabel),
		widget.NewFormItem("Current log:", c.currentLogLabel),
		widget.NewFormItem("Session:", c.sessionLabel),
		widget.NewFormItem("Last event:", c.lastEventLabel),
		widget.NewFormItem("Status:", c.statusLabel),
	)

	content := container.NewVBox(
		pathsForm,
		widget.NewSeparator(),
		pushoverRow,
		widget.NewSeparator(),
		primaryButtons,
		secondaryButtons,
		widget.NewSeparator(),
		infoForm,
	)

	c.window.SetContent(container.NewPadded(content))
}

func (c *Controller) chooseFolder(target *widget.Entry) {
	chooser := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil {
			c.setStatus(fmt.Sprintf("Folder selection failed: %v", err))
			return
		}
		if uri == nil {
			return
		}
		path := uriToPath(uri)
		if path == "" {
			return
		}
		target.SetText(path)
	}, c.window)
	if chooser == nil {
		return
	}
	current := strings.TrimSpace(target.Text)
	if current != "" {
		if uri, err := storage.ListerForURI(storage.NewFileURI(current)); err == nil {
			chooser.SetLocation(uri)
		}
	}
	chooser.SetConfirmText("Select")
	chooser.Show()
}

func uriToPath(uri fyne.URI) string {
	if uri == nil {
		return ""
	}
	path := uri.Path()
	if runtime.GOOS == "windows" {
		path = strings.TrimPrefix(path, "//")
		if strings.HasPrefix(path, "/") && len(path) > 2 && path[2] == ':' {
			path = path[1:]
		}
	}
	if path == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(path))
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
		c.currentLogLabel.SetText(ev.Path)
		c.session.HandleLogSwitch(ev.Path)
		c.sessionLabel.SetText(c.session.Summary())
		c.setStatus("Monitoring " + filepath.Base(ev.Path))
	case EventError:
		c.setStatus(ev.Message)
	case EventRoomEnter:
		msg := c.session.HandleRoomEnter(ev.Room)
		c.setStatus(msg)
		c.sessionLabel.SetText(c.session.Summary())
	case EventRoomLeft:
		msg := c.session.HandleRoomLeft()
		c.setStatus(msg)
		c.sessionLabel.SetText(c.session.Summary())
	case EventSelfJoin:
		c.session.HandleSelfJoin(ev.Message)
		c.sessionLabel.SetText(c.session.Summary())
	case EventPlayerJoin:
		if msg := c.session.HandlePlayerJoin(ev.Player); msg != "" {
			c.setStatus(msg)
		}
	case EventPlayerLeft:
		if name := c.session.HandlePlayerLeft(ev.Player); name != "" {
			c.setStatus(fmt.Sprintf("%s left the instance.", name))
		}
	}
	c.lastEventLabel.SetText(c.session.LastEvent())
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
	c.cfg.InstallDir = expandPath(c.installEntry.Text)
	c.cfg.VRChatLogDir = expandPath(c.logEntry.Text)
	c.cfg.PushoverUser = strings.TrimSpace(c.userEntry.Text)
	c.cfg.PushoverToken = strings.TrimSpace(c.tokenEntry.Text)
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
	c.monitorLabel.SetText("Running")
	c.setStatus("Monitoring VRChat logs...")
	if c.logger != nil {
		c.logger.Log("Monitoring started.")
	}
	c.updateMonitoringButtons()
}

func (c *Controller) stopMonitoring() {
	if c.monitor == nil {
		return
	}
	c.monitor.Stop()
	c.monitor = nil
	c.session.Reset("Monitoring stopped by user.")
	c.monitorLabel.SetText("Stopped")
	c.setStatus("Monitoring stopped.")
	c.currentLogLabel.SetText("(none)")
	c.sessionLabel.SetText("No active session")
	c.lastEventLabel.SetText("")
	if c.logger != nil {
		c.logger.Log("Monitoring stopped.")
	}
	c.updateMonitoringButtons()
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
	c.statusLabel.SetText(text)
}

func (c *Controller) requestQuit() {
	if c.quitting {
		return
	}
	c.quitting = true
	c.stopMonitoring()
	c.window.Close()
	c.app.Quit()
}

func (c *Controller) updateMonitoringButtons() {
	running := c.monitor != nil
	if running {
		c.startButton.Disable()
		c.stopButton.Enable()
	} else {
		c.startButton.Enable()
		c.stopButton.Disable()
	}
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
	if exists {
		c.addStartupButton.Disable()
		c.removeStartupButton.Enable()
	} else {
		c.addStartupButton.Enable()
		c.removeStartupButton.Disable()
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
		if errors.Is(err, errFileNotFound) {
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
		if errors.Is(err, errFileNotFound) {
			return nil
		}
		return err
	}
	defer regCloseKey(key)
	return regDeleteValue(key, AppName)
}

func (c *Controller) initSystemTray() {
	if c.tray != nil {
		return
	}
	items := []TrayMenuItem{
		{ID: trayMenuOpenSettingsID, Title: "Open Settings", Action: c.openSettingsFromTray},
		{ID: trayMenuStartID, Title: "Start Monitoring", Action: c.startMonitoringFromTray},
		{ID: trayMenuStopID, Title: "Stop Monitoring", Action: c.stopMonitoringFromTray},
		{ID: trayMenuResetID, Title: "Reset Monitoring", Action: c.resetMonitoringFromTray},
		{ID: trayMenuExitID, Title: "Exit", Action: c.exitFromTray},
	}
	tray, err := NewSystemTray(c.iconData, AppName, c.openSettingsFromTray, items)
	if err != nil {
		if c.logger != nil {
			c.logger.Logf("Failed to initialise system tray: %v", err)
		}
		return
	}
	c.tray = tray
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.watchWindowMinimise()
	}()
}

func (c *Controller) shutdownTray() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	c.wg.Wait()
	if c.tray != nil {
		c.tray.Close()
		c.tray = nil
	}
}

func (c *Controller) watchWindowMinimise() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
		}
		hwnd := c.getWindowHandle()
		if hwnd == 0 {
			hwnd = findWindowByTitle(AppName)
			if hwnd != 0 {
				c.setWindowHandle(hwnd)
			}
			continue
		}
		if !isWindowHandleValid(hwnd) {
			c.setWindowHandle(0)
			continue
		}
		if isWindowIconic(hwnd) {
			if c.setWindowMinimized(true) {
				c.runOnMain(func() {
					c.window.Hide()
				})
			}
			continue
		}
		c.setWindowMinimized(false)
	}
}

func (c *Controller) setWindowHandle(hwnd syscall.Handle) {
	var previous syscall.Handle
	var alreadyApplied bool

	c.windowStateMu.Lock()
	previous = c.windowHandle
	alreadyApplied = c.windowIconApplied
	c.windowHandle = hwnd
	if hwnd == 0 {
		c.windowIconApplied = false
	}
	c.windowStateMu.Unlock()

	if hwnd != 0 && (previous != hwnd || !alreadyApplied) {
		c.applyWindowIcon(hwnd)
	}
}

func (c *Controller) getWindowHandle() syscall.Handle {
	c.windowStateMu.Lock()
	defer c.windowStateMu.Unlock()
	return c.windowHandle
}

func (c *Controller) applyWindowIcon(hwnd syscall.Handle) {
	if len(c.iconData) == 0 {
		return
	}
	c.windowStateMu.Lock()
	iconHandle := c.windowIconHandle
	if iconHandle == 0 {
		iconHandle = loadIconFromBytes(c.iconData)
		if iconHandle != 0 {
			c.windowIconHandle = iconHandle
		}
	}
	c.windowStateMu.Unlock()
	if iconHandle == 0 {
		return
	}
	if setWindowIconHandle(hwnd, iconHandle) {
		c.windowStateMu.Lock()
		c.windowIconApplied = true
		c.windowStateMu.Unlock()
	}
}

func (c *Controller) releaseWindowIcon() {
	c.windowStateMu.Lock()
	iconHandle := c.windowIconHandle
	c.windowIconHandle = 0
	c.windowIconApplied = false
	c.windowStateMu.Unlock()
	if iconHandle != 0 {
		destroyIcon(iconHandle)
	}
}

func (c *Controller) setWindowMinimized(min bool) bool {
	c.windowStateMu.Lock()
	changed := c.windowMinimized != min
	c.windowMinimized = min
	c.windowStateMu.Unlock()
	return changed
}

func (c *Controller) openSettingsFromTray() {
	c.runOnMain(func() {
		c.window.Show()
		if hwnd := c.getWindowHandle(); hwnd != 0 {
			restoreWindow(hwnd)
		}
	})
	c.setWindowMinimized(false)
}

func (c *Controller) startMonitoringFromTray() {
	c.runOnMain(func() {
		c.startMonitoring()
	})
}

func (c *Controller) stopMonitoringFromTray() {
	c.runOnMain(func() {
		c.stopMonitoring()
	})
}

func (c *Controller) resetMonitoringFromTray() {
	c.runOnMain(func() {
		c.restartMonitoring()
	})
}

func (c *Controller) exitFromTray() {
	c.runOnMain(func() {
		c.requestQuit()
	})
}

// locateNotificationIcon searches common paths for notification.ico.
func notificationIconData() []byte {
	if len(assets.NotificationIcon) > 0 {
		return assets.NotificationIcon
	}
	if path := locateNotificationIcon(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return data
		}
	}
	return nil
}

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
