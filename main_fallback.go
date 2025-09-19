//go:build !cgo || (windows && arm64)

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"vrchat-join-notification-with-pushover/internal/config"
	"vrchat-join-notification-with-pushover/internal/logger"
	"vrchat-join-notification-with-pushover/internal/logwatcher"
	"vrchat-join-notification-with-pushover/internal/notify"
	"vrchat-join-notification-with-pushover/internal/pushover"
	"vrchat-join-notification-with-pushover/internal/session"
)

func main() {
	fmt.Println("VRChat Join Notifier (console mode)")

	cfg, err := config.Load()
	if cfg == nil {
		cfg = &config.Config{
			InstallDir:   config.DefaultInstallDir(),
			VRChatLogDir: config.GuessVRChatLogDir(),
		}
	}
	if err != nil {
		fmt.Printf("Configuration load warning: %v\n", err)
	}

	reader := bufio.NewReader(os.Stdin)
	if updated := promptForConfiguration(reader, cfg); updated {
		if err := cfg.Save(); err != nil {
			fmt.Printf("Failed to save configuration: %v\n", err)
		} else {
			fmt.Printf("Configuration saved to %s\n", cfg.ConfigPath())
		}
	}

	log := logger.New(cfg)
	log.Log("Console mode activated. Press Ctrl+C to stop.")

	events := make(chan logwatcher.Event, 128)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	monitor := logwatcher.New(cfg, log, events)
	notifier := notify.New(log)
	po := pushover.New(cfg, log)
	tracker := session.New(notifier, po, log)

	go monitor.Run(ctx)

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signalCh)
	go func() {
		sig, ok := <-signalCh
		if !ok {
			return
		}
		log.Log(fmt.Sprintf("Received %s, shutting down...", sig))
		cancel()
	}()

	for event := range events {
		handleEvent(tracker, event)
	}

	log.Log("Monitor stopped.")
}

func promptForConfiguration(reader *bufio.Reader, cfg *config.Config) bool {
	if reader == nil || cfg == nil {
		return false
	}

	updated := false

	token := strings.TrimSpace(cfg.PushoverToken)
	if token == "" {
		fmt.Print("Enter your Pushover App Token: ")
		input, _ := reader.ReadString('\n')
		token = strings.TrimSpace(input)
		if token != "" {
			cfg.PushoverToken = token
			updated = true
		}
	}

	user := strings.TrimSpace(cfg.PushoverUser)
	if user == "" {
		fmt.Print("Enter your Pushover User Key: ")
		input, _ := reader.ReadString('\n')
		user = strings.TrimSpace(input)
		if user != "" {
			cfg.PushoverUser = user
			updated = true
		}
	}

	dir := strings.TrimSpace(cfg.VRChatLogDir)
	if dir == "" || !isDir(dir) {
		if dir != "" {
			fmt.Printf("VRChat log directory not found at %s.\n", dir)
		}
		fmt.Print("Enter the VRChat log directory path: ")
		input, _ := reader.ReadString('\n')
		dir = strings.TrimSpace(input)
		if dir != "" {
			cfg.VRChatLogDir = config.ExpandPath(dir)
			updated = true
		}
	}

	installDir := strings.TrimSpace(cfg.InstallDir)
	if installDir == "" {
		cfg.InstallDir = config.DefaultInstallDir()
		updated = true
	}

	return updated
}

func handleEvent(tracker *session.Tracker, event logwatcher.Event) {
	if tracker == nil {
		return
	}

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

func isDir(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		if !filepath.IsAbs(path) {
			info, err = os.Stat(config.ExpandPath(path))
		}
	}
	return err == nil && info.IsDir()
}
