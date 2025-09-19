# VRChat Join Notification with Pushover

## Overview
VRChat Join Notification with Pushover is a cross-platform resident tool that continuously watches VRChat logs and notifies you via desktop alerts and Pushover whenever a friend joins your instance. The GUI is built with Tkinter, letting you manage configuration, log locations, and auto-start entirely within the app.【F:src/vrchat_join_notification/app.py†L49-L58】【F:src/vrchat_join_notification/app.py†L1623-L1680】

## Key Features
- Automatically tails VRChat's `Player.log` / `output_log_*.txt`, seamlessly following the latest file even when logs rotate.【F:src/vrchat_join_notification/app.py†L763-L779】【F:src/vrchat_join_notification/app.py†L1530-L1586】
- Tracks participants per session and suppresses duplicates, including a 10-second cooldown for the same player.【F:src/vrchat_join_notification/app.py†L1170-L1240】【F:src/vrchat_join_notification/app.py†L1437-L1475】
- Prefers PowerShell toast notifications on Windows and `notify-send` on Linux, logging locally if either fails. When Pushover is configured, the same message is pushed to mobile.【F:src/vrchat_join_notification/app.py†L782-L930】
- Optionally resides in the system tray so you can start/stop monitoring or reopen the GUI from the tray menu (requires `pystray` and `Pillow`).【F:src/vrchat_join_notification/app.py†L932-L1103】
- Runs as a single instance, storing settings (`config.json`) and logs (`notifier.log`) in the user area.【F:src/vrchat_join_notification/app.py†L205-L324】【F:src/vrchat_join_notification/app.py†L326-L343】

## How It Works
1. **Log Monitoring Thread** – `LogMonitor` tails VRChat logs in a separate thread, parses join/leave/room move events, and passes them to the UI thread.【F:src/vrchat_join_notification/app.py†L1513-L1620】
2. **Session Tracker** – `SessionTracker` reconciles combinations of OnJoinedRoom/OnPlayerJoined events with your own activity to prevent duplicates and clean up placeholder names.【F:src/vrchat_join_notification/app.py†L1160-L1510】
3. **Notification Pipeline** – `DesktopNotifier` and `PushoverClient` send identical messages to the desktop and Pushover while recording delivery results in the app log.【F:src/vrchat_join_notification/app.py†L782-L930】
4. **System Tray / Auto-Start** – `TrayIconController` and `AppController` manage tray residency, auto-hiding the window, and registering startup entries (Linux `.desktop` / Windows Registry).【F:src/vrchat_join_notification/app.py†L932-L1133】【F:src/vrchat_join_notification/app.py†L2009-L2316】
5. **Safe Launchers** – The Windows/Linux wrapper scripts enforce guards such as memory monitoring, VRChat/EAC protection, and blocking high-privilege handles before launching the app.【F:src/vrchat-join-notification-with-pushover_windows.py†L1-L210】【F:src/vrchat-join-notification-with-pushover_linux.py†L1-L205】

## Requirements
### Common
- Python 3.8 or newer.【F:pyproject.toml†L5-L23】
- Access to VRChat logs (`AppData/LocalLow/VRChat/VRChat`).【F:src/vrchat_join_notification/app.py†L138-L151】
- Pushover API token and user key if you want mobile notifications.

### Windows
- Supports toast notifications via built-in PowerShell.【F:src/vrchat_join_notification/app.py†L782-L859】
- Optional: install tray dependencies with `pip install "vrchat-join-notification-with-pushover[tray]"` to enable `pystray` and `Pillow`.【F:pyproject.toml†L25-L29】【F:src/vrchat_join_notification/app.py†L932-L1103】

### Linux
- Requires Python/Tk and `libnotify` providing `notify-send`.【F:src/vrchat_join_notification/app.py†L782-L820】
- For tray usage on Wayland/X11, you need `pystray` + `Pillow`; some X11 environments may also require `python-xlib`.【F:src/vrchat_join_notification/app.py†L932-L1102】

## Installation
1. Clone the repository.
   ```bash
   git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
   cd vrchat-join-notification-with-pushover
   ```
2. Create a virtual environment and install the app.
   ```bash
   python -m venv .venv
   source .venv/bin/activate  # On Windows, use .venv\Scripts\Activate.ps1
   python -m pip install --upgrade pip
   python -m pip install .
   # Install with tray extras if needed
   python -m pip install '.[tray]'
   ```
3. Using `pipx` works the same way with `pipx install .` / `pipx install '.[tray]'`.【F:pyproject.toml†L25-L33】

## Running the App
- After installation, launch the entry point `vrchat-join-notifier`.
  ```bash
  vrchat-join-notifier
  ```
  This command invokes `vrchat_join_notification.app:main`.【F:pyproject.toml†L31-L33】
- You can also run it directly via Python with `python -m vrchat_join_notification.app`.【F:src/vrchat_join_notification/app.py†L2324-L2338】
- To use the Windows/Linux wrappers, run `python src/vrchat-join-notification-with-pushover_windows.py` or `python src/vrchat-join-notification-with-pushover_linux.py` for guarded startup.【F:src/vrchat-join-notification-with-pushover_windows.py†L287-L305】【F:src/vrchat-join-notification-with-pushover_linux.py†L207-L224】

## First-Time Setup and Storage
- On first launch, use the GUI to specify the installation folder (for settings/log storage), VRChat log directory, and Pushover user key/API token.【F:src/vrchat_join_notification/app.py†L1623-L1680】【F:src/vrchat_join_notification/app.py†L1688-L1771】
- Settings are saved as `config.json` in these default locations:
  - Windows: `%LOCALAPPDATA%\VRChatJoinNotificationWithPushover`
  - Linux: `~/.local/share/vrchat-join-notification-with-pushover`
  Existing configs from older versions are migrated automatically, and the path is stored in `config-location.txt`.【F:src/vrchat_join_notification/app.py†L69-L111】【F:src/vrchat_join_notification/app.py†L205-L324】
- Application logs are appended to `notifier.log` in the same directory.【F:src/vrchat_join_notification/app.py†L326-L343】

## Notifications and Pushover Setup
- Desktop notifications choose the optimal channel per OS. If `notify-send` is unavailable on Linux, the app falls back to log output only.【F:src/vrchat_join_notification/app.py†L782-L820】
- Pushover only sends when both the user key and API token are set; results are recorded in the app log along with response status.【F:src/vrchat_join_notification/app.py†L900-L929】
- Events detected while the VRChat client is offline are ignored to reduce unnecessary alerts.【F:src/vrchat_join_notification/app.py†L666-L733】【F:src/vrchat_join_notification/app.py†L1264-L1363】

## System Tray (Optional)
- When `pystray` and `Pillow` are available, the app spawns a tray icon at startup. Unsupported environments are reported in the status area and logs.【F:src/vrchat_join_notification/app.py†L932-L1009】【F:src/vrchat_join_notification/app.py†L1675-L1679】
- The tray menu lets you start/stop monitoring, reopen the settings window, and quit the app.【F:src/vrchat_join_notification/app.py†L994-L1050】

## Auto-Start Configuration
- Use the GUI buttons “Add to Startup” and “Remove from Startup” to register/unregister startup entries at `~/.config/autostart/vrchat-join-notifier.desktop` on Linux and `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` on Windows.【F:src/vrchat_join_notification/app.py†L2009-L2278】
- The launch command automatically adapts to the current runtime (PyInstaller build, `python -m`, etc.).【F:src/vrchat_join_notification/app.py†L2187-L2217】

## Logs and Troubleshooting
- If the target log directory is missing or read errors occur, the status area and app log report the reason.【F:src/vrchat_join_notification/app.py†L1531-L1586】
- Notifications are suppressed—and a message is logged—if VRChat is not running or the event is identified as your own join.【F:src/vrchat_join_notification/app.py†L1264-L1430】
- Closing the GUI minimizes the window to the tray; monitoring continues until you select `Quit` or exit via the tray menu.【F:src/vrchat_join_notification/app.py†L2114-L2166】

## Packaging and Build
- Build standard Python distributions with `python -m build` to generate source archives and wheels (`setuptools`/`wheel`).【F:pyproject.toml†L1-L3】
- For a single-file Windows executable, use `tools/build-windows-exe.ps1` to automate the PyInstaller build.【F:tools/build-windows-exe.ps1†L1-L20】

## Repository Structure
| Path | Description |
| --- | --- |
| `src/vrchat_join_notification/` | Core Python package containing the GUI, log monitoring, and notification logic.【F:src/vrchat_join_notification/app.py†L1-L2338】 |
| `src/vrchat-join-notification-with-pushover_*.py` | Windows/Linux wrapper scripts with protective guards.【F:src/vrchat-join-notification-with-pushover_windows.py†L1-L305】【F:src/vrchat-join-notification-with-pushover_linux.py†L1-L224】 |
| `public/` | Static site assets for the project overview.【F:public/index.html†L1-L188】 |
| `tools/` | Helper scripts for building (PyInstaller for Windows).【F:tools/build-windows-exe.ps1†L1-L20】 |

## License
This project is distributed under the MIT License.【F:pyproject.toml†L5-L21】
