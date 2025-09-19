package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"vrchat-join-notification-with-pushover/internal/config"
	"vrchat-join-notification-with-pushover/internal/core"
	"vrchat-join-notification-with-pushover/internal/logger"
	"vrchat-join-notification-with-pushover/internal/logwatcher"
	"vrchat-join-notification-with-pushover/internal/notify"
	"vrchat-join-notification-with-pushover/internal/pushover"
	"vrchat-join-notification-with-pushover/internal/session"
)

func main() {
	configureOnly := flag.Bool("configure", false, "Run interactive configuration and exit.")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration load warning: %v\n", err)
	}
	if cfg == nil {
		fmt.Fprintln(os.Stderr, "Failed to load configuration; aborting.")
		os.Exit(1)
	}

	log := logger.New(cfg)
	if err != nil && log != nil {
		log.Log(fmt.Sprintf("Configuration load warning: %v", err))
	}

	if *configureOnly {
		if err := runInteractiveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Configuration failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Configuration saved to %s\n", cfg.ConfigPath())
		return
	}

	if cfg.FirstRun() {
		fmt.Println("First run detected. Launching interactive configuration...")
		if err := runInteractiveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Configuration failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Configuration saved to %s\n", cfg.ConfigPath())
	}

	log.Log(fmt.Sprintf("Starting %s (Windows)", core.AppName))
	events := make(chan logwatcher.Event, 128)
	monitor := logwatcher.New(cfg, log, events)
	notifier := notify.New(log)
	po := pushover.New(cfg, log)
	tracker := session.New(notifier, po, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		if log != nil {
			log.Log(fmt.Sprintf("Received signal %s; shutting down...", sig))
		}
		cancel()
	}()

	go monitor.Run(ctx)

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

	log.Log("Monitor stopped; exiting.")
}

func runInteractiveConfig(cfg *config.Config) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("=== VRChat Join Notification with Pushover (Windows) ===")
	fmt.Println("Configure the install directory, VRChat log location, and optional Pushover keys.")
	fmt.Println("Press Enter to keep the current value shown in brackets.")

	fmt.Printf("Install directory [%s]: ", cfg.InstallDir)
	installDir, _ := reader.ReadString('\n')
	installDir = strings.TrimSpace(installDir)
	if installDir != "" {
		cfg.InstallDir = config.ExpandPath(installDir)
	}

	fmt.Printf("VRChat log directory [%s]: ", cfg.VRChatLogDir)
	logDir, _ := reader.ReadString('\n')
	logDir = strings.TrimSpace(logDir)
	if logDir != "" {
		cfg.VRChatLogDir = config.ExpandPath(logDir)
	}

	fmt.Printf("Pushover user key [%s]: ", cfg.PushoverUser)
	userKey, _ := reader.ReadString('\n')
	userKey = strings.TrimSpace(userKey)
	if userKey != "" {
		cfg.PushoverUser = userKey
	}

	fmt.Printf("Pushover API token [%s]: ", maskSecret(cfg.PushoverToken))
	token, _ := reader.ReadString('\n')
	token = strings.TrimSpace(token)
	if token != "" {
		cfg.PushoverToken = token
	}

	if err := cfg.Save(); err != nil {
		return err
	}

	if cfg.VRChatLogDir != "" {
		if info, err := os.Stat(cfg.VRChatLogDir); err != nil || !info.IsDir() {
			fmt.Printf("Warning: VRChat log directory '%s' does not exist yet.\n", cfg.VRChatLogDir)
		}
	}

	fmt.Println("Configuration complete. Restart the application to apply changes if needed.")
	return nil
}

func maskSecret(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 4 {
		return "****"
	}
	return trimmed[:2] + strings.Repeat("*", len(trimmed)-4) + trimmed[len(trimmed)-2:]
}
