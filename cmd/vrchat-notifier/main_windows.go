//go:build windows

package main

import (
	"errors"
	"fmt"

	"vrchat-join-notification-with-pushover/internal/app"
)

func main() {
	guard, err := app.AcquireSingleInstance("VRChatJoinNotificationWithPushover")
	if err != nil {
		if errors.Is(err, app.ErrAlreadyRunning) {
			app.ShowMessage(err.Error(), app.AppName, app.MBOK|app.MBIconWarning)
			return
		}
		app.ShowMessage(fmt.Sprintf("Failed to acquire single instance lock:\n%v", err), app.AppName, app.MBOK|app.MBIconError)
		return
	}
	defer guard.Release()

	cfg, loadNotice, err := app.LoadConfig()
	if err != nil {
		app.ShowMessage(fmt.Sprintf("Failed to load configuration:\n%v", err), app.AppName, app.MBOK|app.MBIconError)
		return
	}
	logger := app.NewAppLogger(cfg)
	logger.Log("Application started.")
	if loadNotice != "" {
		logger.Log(loadNotice)
	}

	controller, err := app.NewController(cfg, loadNotice, logger)
	if err != nil {
		app.ShowMessage(fmt.Sprintf("Failed to initialise UI:\n%v", err), app.AppName, app.MBOK|app.MBIconError)
		return
	}

	if err := controller.Run(); err != nil {
		logger.Logf("Application exited with error: %v", err)
	} else {
		logger.Log("Application exited cleanly.")
	}
}
