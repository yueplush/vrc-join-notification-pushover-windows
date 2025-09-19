# VRChat Join Notification with Pushover

Resident desktop companion that watches your VRChat logs and pings you on the desktop (and optionally through Pushover) whenever a friend joins your instance. The original release ships a Python/Tkinter GUI, and the repository now additionally contains a native Go-based tray companion for Windows with a self-contained settings window.

## Highlights
- Tracks VRChat sessions safely, suppressing duplicates and ignoring your own joins.
- Sends native desktop notifications (PowerShell toast on Windows, `notify-send` on Linux) and mirrors them to Pushover when configured.
- Optional system tray with quick start/stop controls when `pystray` and `Pillow` are installed.

## Quick install from source

### Windows native tray companion (Go)

Prefer a small self-contained `.exe` with a built-in settings UI and task-tray controls? Compile the Go implementation that lives in `cmd/vrchat-notifier`.

**Prerequisites**

- Windows 10/11.
- [Go 1.21 or newer](https://go.dev/dl/).

**Build & run**

1. **Clone the repository**
   ```powershell
   git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
   Set-Location vrchat-join-notification-with-pushover
   ```
2. **Compile the tray application**
   ```powershell
   go build -o bin/vrchat-notifier.exe ./cmd/vrchat-notifier
   ```
   The build embeds no external assets – just ensure `notification.ico` sits next to the executable (it is already present in the repository root).
3. **Launch the watcher**
   ```powershell
   .\bin\vrchat-notifier.exe
   ```

The Go binary stores configuration files in `%LOCALAPPDATA%\VRChatJoinNotificationWithPushover`, mirroring the Python application's layout. A system-tray icon exposes quick commands (Open Settings, Start/Stop/Restart Monitoring and Quit) and the window may be re-opened at any time from the tray. Desktop notifications and Pushover integration behave just like the original implementation.

### Windows (PowerShell)

**Prerequisites**

- Python 3.8 or newer from [python.org](https://www.python.org/downloads/) with “Add Python to PATH” enabled during setup.
- Git for Windows.

**Install & run**

1. **Grab the code**
   ```powershell
   git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
   Set-Location vrchat-join-notification-with-pushover
   ```
2. **Create and activate a virtual environment**
   ```powershell
   python -m venv .venv
   .\.venv\Scripts\Activate.ps1
   ```
3. **Install the project**
   ```powershell
   python -m pip install --upgrade pip
   python -m pip install .            # add '.[tray]' to include the optional system tray extras
   ```
4. **Run the desktop companion**
   ```powershell
   vrchat-join-notifier
   # or
   python -m vrchat_join_notification.app
   ```

**Compile & bundle (optional)**

Create a portable `.exe` with the bundled PowerShell script (PyInstaller is installed automatically if you used the `[tray]` extra above, otherwise add it first with `python -m pip install pyinstaller`).

```powershell
python -m pip install pyinstaller     # skip if already installed
./tools/build-windows-exe.ps1
```

The script writes `VRChatJoinNotificationWithPushover.exe` to `dist/`. If you prefer to run PyInstaller manually, remember to quote the `--add-data` argument so the semicolon separator survives PowerShell parsing: `--add-data "src/vrchat_join_notification/notification.ico;vrchat_join_notification"`.

### Linux (Bash)

**Prerequisites**

Install Python, Tk, and `libnotify` before continuing.

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
  Fish users (e.g., Garuda) should activate the venv with `source .venv/bin/activate.fish` as shown below.
- **Fedora**
  ```bash
  sudo dnf install python3 python3-pip python3-virtualenv python3-tkinter libnotify
  ```

**Install & run**

1. **Grab the code**
   ```bash
   git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
   cd vrchat-join-notification-with-pushover
   ```
2. **Create and activate a virtual environment**
   ```bash
   python -m venv .venv
   source .venv/bin/activate          # bash/zsh
   ```
   ```bash
   # for fish shells
   source .venv/bin/activate.fish
   ```
3. **Install the project**
   ```bash
   python -m pip install --upgrade pip
   python -m pip install .             # include '.[tray]' for the optional tray icon support
   ```
4. **Run the desktop companion**
   ```bash
   vrchat-join-notifier
   # or
   python -m vrchat_join_notification.app
   ```

**Build artifacts (optional)**

Package the project for redistribution with the standard Python build backend:

```bash
python -m build
```

Source and wheel archives will appear in `dist/`. If you need a Windows `.exe`, perform the Windows build on a Windows machine as described above.

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
