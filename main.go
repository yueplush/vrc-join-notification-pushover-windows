package main

import (
	"context"
	"embed"
	"fmt"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	fyneApp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"vrchat-join-notification-with-pushover/internal/config"
	"vrchat-join-notification-with-pushover/internal/logger"
	"vrchat-join-notification-with-pushover/internal/logwatcher"
	"vrchat-join-notification-with-pushover/internal/notify"
	"vrchat-join-notification-with-pushover/internal/pushover"
	"vrchat-join-notification-with-pushover/internal/session"
)

//go:embed src/notification.ico
var iconData []byte

func main() {
	cfg, err := config.Load()
	if cfg == nil {
		cfg = &config.Config{
			InstallDir:   config.DefaultInstallDir(),
			VRChatLogDir: config.GuessVRChatLogDir(),
		}
	}

	application := fyneApp.NewWithID("com.vrchat.joinnotifier")
	if len(iconData) > 0 {
		application.SetIcon(fyne.NewStaticResource("notification.ico", iconData))
	}

	window := application.NewWindow("VRChat Join Notifier")
	window.Resize(fyne.NewSize(480, 360))

	tokenEntry := widget.NewPasswordEntry()
	tokenEntry.SetPlaceHolder("Enter your Pushover App Token")
	tokenEntry.SetText(strings.TrimSpace(cfg.PushoverToken))

	userEntry := widget.NewEntry()
	userEntry.SetPlaceHolder("Enter your Pushover User Key")
	userEntry.SetText(strings.TrimSpace(cfg.PushoverUser))

	statusLabel := widget.NewLabel("Idle")
	statusLabel.Wrapping = fyne.TextWrapWord

	logOutput := widget.NewMultiLineEntry()
	logOutput.SetPlaceHolder("Status messages will appear here.")
	logOutput.Disable()

	uiLogger := newUILog(application, logOutput, statusLabel)

	log := logger.New(cfg)
	log.SetObserver(uiLogger.Append)

	if err != nil {
		log.Log(fmt.Sprintf("Configuration load warning: %v", err))
	}

	service := newMonitorService(application, log, func(running bool) {
		if running {
			uiLogger.setStatus("Monitoring VRChat logs...")
		} else {
			uiLogger.setStatus("Monitoring stopped.")
		}
	})

	saveButton := widget.NewButton("Save", func() {
		cfg.PushoverToken = strings.TrimSpace(tokenEntry.Text)
		cfg.PushoverUser = strings.TrimSpace(userEntry.Text)
		if err := cfg.Save(); err != nil {
			dialog.ShowError(err, window)
			return
		}
		log.Log(fmt.Sprintf("Configuration saved to %s", cfg.ConfigPath()))
		service.Start(cfg)
	})

	content := container.NewVBox(
		widget.NewLabel("Configure Pushover credentials and start monitoring."),
		widget.NewForm(
			widget.NewFormItem("Pushover App Token", tokenEntry),
			widget.NewFormItem("Pushover User Key", userEntry),
		),
		saveButton,
		widget.NewSeparator(),
		widget.NewLabel("Status"),
		statusLabel,
		widget.NewLabel("Log"),
		container.NewMax(logOutput),
	)

	if strings.TrimSpace(cfg.PushoverToken) != "" && strings.TrimSpace(cfg.PushoverUser) != "" {
		service.Start(cfg)
	}

	window.SetContent(content)
	window.SetCloseIntercept(func() {
		service.Stop()
		window.SetCloseIntercept(nil)
		window.Close()
	})

	window.ShowAndRun()
}

type monitorService struct {
	app     fyne.App
	log     *logger.Logger
	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	notify  func(bool)
}

func newMonitorService(app fyne.App, log *logger.Logger, notify func(bool)) *monitorService {
	return &monitorService{app: app, log: log, notify: notify}
}

func (s *monitorService) Start(cfg *config.Config) {
	if cfg == nil {
		return
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true
	s.mu.Unlock()

	runOnUI(s.app, func() {
		if s.notify != nil {
			s.notify(true)
		}
	})

	events := make(chan logwatcher.Event, 128)
	monitor := logwatcher.New(cfg, s.log, events)
	notifier := notify.New(s.log)
	po := pushover.New(cfg, s.log)
	tracker := session.New(notifier, po, s.log)

	if s.log != nil {
		s.log.Log("Monitoring started.")
	}

	go monitor.Run(ctx)

	go func() {
		for event := range events {
			switch event.Type {
			case logwatcher.EventStatus:
				tracker.HandleStatus(event.Message)
			case logwatcher.EventError:
				tracker.HandleError(event.Message)
			case logwatcher.EventLogSwitch:
				tracker.HandleLogSwitch(event.Path)
			case logwatcher.EventRoomEnter:
				tracker.HandleRoomEnter(event.Room)
			case logwatcher.EventRoomLeft:
				tracker.HandleRoomLeft()
			case logwatcher.EventSelfJoin:
				tracker.HandleSelfJoin(event.Raw)
			case logwatcher.EventPlayerJoin:
				tracker.HandlePlayerJoin(event.Player)
			case logwatcher.EventPlayerLeft:
				tracker.HandlePlayerLeft(event.Player)
			}
		}

		if s.log != nil {
			s.log.Log("Monitor stopped; awaiting restart.")
		}

		s.mu.Lock()
		s.cancel = nil
		s.running = false
		s.mu.Unlock()

		runOnUI(s.app, func() {
			if s.notify != nil {
				s.notify(false)
			}
		})
	}()
}

func (s *monitorService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

type uiLog struct {
	app    fyne.App
	entry  *widget.Entry
	status *widget.Label
	mu     sync.Mutex
	lines  []string
}

func newUILog(app fyne.App, entry *widget.Entry, status *widget.Label) *uiLog {
	return &uiLog{app: app, entry: entry, status: status}
}

func (l *uiLog) Append(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	l.mu.Lock()
	l.lines = append(l.lines, line)
	if len(l.lines) > 200 {
		l.lines = l.lines[len(l.lines)-200:]
	}
	text := strings.Join(l.lines, "\n")
	l.mu.Unlock()

	runOnUI(l.app, func() {
		l.entry.SetText(text)
		l.entry.ScrollToBottom()
		if l.status != nil {
			l.status.SetText(line)
		}
	})
}

func (l *uiLog) setStatus(message string) {
	runOnUI(l.app, func() {
		if l.status != nil {
			l.status.SetText(message)
		}
	})
}

func runOnUI(app fyne.App, fn func()) {
	if app == nil || fn == nil {
		return
	}
	if driver := app.Driver(); driver != nil {
		driver.RunOnMain(fn)
	}
}
