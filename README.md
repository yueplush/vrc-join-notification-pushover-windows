# VRChat Join Notification with Pushover

Resident desktop companion that watches your VRChat logs and pings you on the desktop (and optionally through Pushover) whenever a friend joins your instance. The GUI is built with Tkinter, so everything can be configured in-app.

## Highlights
- Tracks VRChat sessions safely, suppressing duplicates and ignoring your own joins.
- Sends native desktop notifications (PowerShell toast on Windows, `notify-send` on Linux) and mirrors them to Pushover when configured.
- Optional system tray with quick start/stop controls when `pystray` and `Pillow` are installed.

## Quick install from source
1. **Grab the code**
   ```bash
   git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
   cd vrchat-join-notification-with-pushover
   ```
2. **Create a virtual environment**
   ```bash
   python -m venv .venv
   source .venv/bin/activate        # bash/zsh
   ```
   ```bash
   # if you use fish (Garuda Linux, etc.)
   source .venv/bin/activate.fish
   ```
   On Windows (PowerShell): `python -m venv .venv; .\.venv\Scripts\Activate.ps1`.
3. **Install the package**
   ```bash
   python -m pip install --upgrade pip
   python -m pip install .           # add '.[tray]' for the optional tray icon
   ```
   Planning to bundle a Windows executable? Install PyInstaller alongside the project:
   ```powershell
   python -m pip install pyinstaller
   ```
4. **Run it**
   ```bash
   vrchat-join-notifier
   # or
   python -m vrchat_join_notification.app
   ```

5. **(Optional) Build a Windows executable**
   ```powershell
   .\tools\build-windows-exe.ps1
   ```
   The script drops a portable `.exe` in `dist/`. If you need to run PyInstaller yourself in PowerShell, quote the `--add-data`
   flag so the semicolon separator is preserved: `--add-data "src/vrchat_join_notification/notification.ico;vrchat_join_notification"`.

### Linux prerequisites
Install Python, Tk, and `libnotify` before running the steps above.
- **Ubuntu / Debian**
  ```bash
  sudo apt update
  sudo apt install python3 python3-venv python3-pip python3-tk libnotify-bin
  ```
- **Arch / Manjaro / Garuda**
  ```bash
  sudo pacman -Syu
  sudo pacman -S python python-pip tk libnotify
  ```
  Fish users (e.g., Garuda) should activate the venv with `source .venv/bin/activate.fish` as shown above.
- **Fedora**
  ```bash
  sudo dnf install python3 python3-pip python3-virtualenv python3-tkinter libnotify
  ```

### Windows prerequisites
Install Python 3.8 or newer from python.org (ensure “Add Python to PATH” is enabled). Launch PowerShell and follow the quick-install steps. Toast notifications work out of the box; install tray extras with `python -m pip install "vrchat-join-notification-with-pushover[tray]"`.

## Quick build targets
- **Python packages**: `python -m build` produces source and wheel archives in `dist/`.
- **Windows single-file executable**: run `tools\build-windows-exe.ps1` in PowerShell (PyInstaller must be installed).

### Build the Windows executable from source
If you prefer a standalone `.exe`, clone the repository on Windows and compile it with PyInstaller:

1. Launch PowerShell in the project root and install the build tools.
   ```powershell
   python -m pip install --upgrade pip
   python -m pip install .[tray] pyinstaller
   ```
   The optional `[tray]` extra brings in the system tray dependencies; omit it if you do not need the tray icon.
2. Execute the bundled build script (or call PyInstaller directly) to produce the executable under `dist/`.
   ```powershell
   .\tools\build-windows-exe.ps1
   # equivalent manual command
   pyinstaller --noconsole --name VRChatJoinNotificationWithPushover `
       --icon src/vrchat_join_notification/notification.ico `
       --add-data "src/vrchat_join_notification/notification.ico;vrchat_join_notification" `
       src/vrchat_join_notification/app.py
   ```
   After the run completes you will find `VRChatJoinNotificationWithPushover.exe` in `dist/` ready for distribution.

## Configuration basics
On first launch the app asks for your VRChat log folder, storage location, and Pushover credentials. Settings live in your user directory (`%LOCALAPPDATA%` on Windows, `~/.local/share` on Linux) together with the app log.
