# VRChat Join Notification with Pushover

A helper for Windows and Linux that watches the latest VRChat log, tells you when you join a world, and announces every new
player with a toast and (optionally) a Pushover push.

## Highlights
- Follows `output_log_*.txt` / `Player.log` automatically and debounces duplicate join events.
- Desktop notifications show the placeholder name that VRChat logged so you can spot unresolved display names.
- Windows ships as a PowerShell script that you can run directly or package as a standalone `.exe`.
- Linux installs as a Python application with a Tk GUI, system tray support (when available), and optional Pushover integration.

## Windows quick start
1. Clone the repository and install the optional packaging tool:
   ```powershell
   git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
   cd vrchat-join-notification-with-pushover
   Install-Module -Name ps2exe -Scope CurrentUser   # only if you want an .exe
   ```
2. Run the script or build an executable:
   ```powershell
   # run directly
   .\src\vrchat-join-notification-with-pushover.ps1

   # or package
   Invoke-ps2exe -InputFile .\src\vrchat-join-notification-with-pushover.ps1 -OutputFile .\vrchat-join-notification-with-pushover.exe `
     -Title 'VRChat Join Notification with Pushover' -IconFile .\src\vrchat_join_notification\notification.ico -NoConsole -STA -x64
   ```
3. Use **Settings** to pick your VRChat log folder, toggle "Hide window on launch" if you prefer a tray-first start, and add
   Pushover credentials when you want push alerts.

## Linux quick start
1. Install prerequisites (Python 3.8+, Tk bindings, `libnotify`, and `procps`). On Debian/Ubuntu:
   ```bash
   sudo apt update
   sudo apt install python3 python3-venv python3-tk python3-pip libnotify-bin procps
   ```
2. Install the app. Use whichever tool you prefer:
   ```bash
   python3 -m pip install --user '.[tray]'   # or omit [tray] if you do not need the system tray
   # alternatively
   pipx install '.[tray]'
   ```
   If your distribution enforces an "externally managed" policy, append `--break-system-packages` or use a virtual environment.
3. Launch the notifier:
   ```bash
   vrchat-join-notifier
   ```
   The GUI stores its settings under `~/.local/share/vrchat-join-notification-with-pushover`, supports optional autostart, and
   enables the tray icon automatically when `pystray`/`Pillow` and a tray host are present.

## Uninstalling
- **Windows:** delete the script or packaged executable.
- **Linux:** uninstall the Python package with `python3 -m pip uninstall vrchat-join-notification-with-pushover` or
  `pipx uninstall vrchat-join-notification-with-pushover`, then remove the data directory if you no longer need cached logs.

## Support
If this project helps you, consider buying me a coffee (JPY): <https://yueplushdev.booth.pm/items/7434788>
