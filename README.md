# VRChat Join Notification with Pushover

This repository contains cross-platform helpers that watch your VRChat logs and
notify you when players join your instance. Notifications are delivered locally
and (optionally) through Pushover.

## Features

- Watches the most recent `output_log_*.txt` or `Player.log` file and switches
  automatically when VRChat rolls over to a new log.
- Sends a single notification when you enter an instance (`OnJoinedRoom`) and
  one per unique player that joins you (`OnPlayerJoined`).
- Debounces duplicate events with configurable cooldowns.
- Optional Pushover push notifications alongside local toasts.
- Simple GUI on both Windows (PowerShell/WinForms) and Linux (Python/Tk).

## Windows (PowerShell)

The original script lives in `VRChatJoinNotifier.ps1`.

```powershell
# Convert to an EXE (requires ps2exe)
Invoke-ps2exe .\vrchat-join-notification-with-pushover.ps1 .\vrchat-join-notification-with-pushover.exe `
  -NoConsole -Title "VRChat Join Notification with Pushover" -Icon .\notification.ico
```

Run the script (or compiled EXE) and use the tray icon to open the settings
window, configure your VRChat log directory and Pushover credentials, then
start monitoring.

## Linux (Python)

A native Linux port with a Tk GUI is provided in
`VRChatJoinNotifier_linux.py`.

### Requirements

- Python 3 with Tk bindings (present on most distributions).
- `notify-send` (from `libnotify-bin`) for desktop notifications. When it is
  missing the script falls back to logging the messages.
- `pgrep` (usually part of `procps`) is used to detect a running VRChat.exe.

### Running

```bash
python3 VRChatJoinNotifier_linux.py
```

On the first launch the settings window opens automatically. Configure:

- **Install Folder (logs/cache):** Where the notifier stores its own log and
  configuration file (`~/.local/share/VRChatJoinNotifier` by default).
- **VRChat Log Folder:** The Proton prefix that contains the VRChat logs. The
  script tries the typical Steam locations automatically.
- **Pushover keys:** Optional, but required for push notifications.

Click **Save & Restart Monitoring** once the folders and keys are set. The
application remembers the settings in
`~/.local/share/VRChatJoinNotifier/config.json` (a pointer file keeps track of
custom install folders).

Desktop notifications mirror the Windows behaviour and Pushover pushes share
the same cooldown logic, so you will only see one alert per unique event.
