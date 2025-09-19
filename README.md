
# VRChat Join Notification with Pushover

Cross-platform helper that watches VRChat logs and sends desktop alerts when friends join. Optional Pushover support lets you receive the same notifications on mobile devices.

## Features
- Tracks the newest `output_log_*.txt` / `Player.log` file and follows roll-overs automatically.
- Emits one world join toast and one per unique player, ignoring the generic "A player joined your instance." message.
- Debounces duplicates with configurable cooldowns and mirrors behaviour across desktop and push notifications.
- The Linux build adds tray support when dependencies exist and can auto-hide on launch; Windows can run directly from Python or be packaged with PyInstaller.

## Repository layout
| Path | Description |
| --- | --- |
| `src/vrchat-join-notification-with-pushover_linux.py` | Legacy shim that calls the packaged Linux app. |
| `src/vrchat_join_notification/` | Installable Python package with the GUI and notifier logic. |
| `public/` | Static assets used by the GUI for onboarding tips. |
| `pyproject.toml` | Python packaging metadata. |

---

# Getting the source (PowerShell/Bash)
Clone the repository and enter it:

```powershell/bash
git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
cd vrchat-join-notification-with-pushover
```

---

## Windows (Python GUI)
The same Python/Tk application used on Linux is also available on Windows. The steps below cover running it directly and packaging it into a standalone `.exe` with PyInstaller.

### 1. Prerequisites
- Install [Python 3.1.3+ (64-bit)](https://www.python.org/downloads/windows/) and enable **Add python.exe to PATH** during setup.
- Tkinter ships with the official installer. Confirm the installation in PowerShell:
  ```powershell
  py --version
  ```

### 2. Install dependencies
Create a virtual environment in the repository root and install the application. Add the tray extra if you need Windows tray support.

```powershell
py -3 -m venv .venv
.\.venv\Scripts\Activate.ps1
python -m pip install --upgrade pip
# Base functionality
python -m pip install .
# Enable optional system tray integration
python -m pip install '.[tray]'
```

> If PowerShell blocks script execution, run `Set-ExecutionPolicy -Scope Process RemoteSigned` for the current session.

### 3. Run as a Python script
With the virtual environment active you can launch the GUI directly:

```powershell
# Launches the same interface shipped on Linux
python -m vrchat_join_notification.app
```

> Configuration files and logs live under `%USERPROFILE%\.local\share\vrchat-join-notification-with-pushover`. Update the **Install Dir** in-app if you want to store assets elsewhere on Windows.

### 4. Package with PyInstaller
Bundle the Python build into a single-file `.exe` when you want to redistribute it.

```powershell
# With the virtual environment still active
python -m pip install pyinstaller
pyinstaller --noconsole --name VRChatJoinNotificationWithPushover `
  --icon src/vrchat_join_notification/notification.ico `
  --add-data "src/vrchat_join_notification/notification.ico;vrchat_join_notification" `
  src/vrchat_join_notification/app.py
```

- PowerShell uses the backtick (`` ` ``) for line continuations. If you are running
  the command from **Command Prompt**, replace each trailing backtick with a caret
  (`^`).
- Prefer to avoid manual copy/paste? Run the helper script instead:

  ```powershell
  # Still inside the virtual environment
  python -m pip install pyinstaller
  .\tools\build-windows-exe.ps1
  ```

- The build produces `dist/VRChatJoinNotificationWithPushover/VRChatJoinNotificationWithPushover.exe`.
- The executable runs standalone, but Microsoft Defender SmartScreen may warn on first launch. Choose **More info** → **Run anyway**.
- If you want to customise the build, generate a template first (`pyinstaller --name ... --onefile --icon ... --add-data ... --specpath buildspec`) and then run `pyinstaller buildspec/VRChatJoinNotificationWithPushover.spec`.

> PyInstaller uses `;` to separate `--add-data` entries on Windows (Linux/macOS use `:`).

PyInstaller packages provide the same features as the Linux version and share configuration data.

---

## Linux quick start (Python package)
Use the `vrchat-join-notifier` command installed with the Python package. The compatibility script in `src/` simply forwards to it.

### 1. Prerequisites
Install Python 3.8+, Tk bindings, `libnotify`, and `procps`.

- **Debian/Ubuntu**
  ```bash
  sudo apt update
  sudo apt install python3 python3-venv python3-tk python3-pip libnotify-bin procps
  ```
- **Fedora**
  ```bash
  sudo dnf install python3 python3-tkinter python3-pip libnotify procps-ng
  ```
- **Arch / Manjaro**
  ```bash
  sudo pacman -S python python-pip tk libnotify procps-ng
  ```

Optional tray extras install automatically when the `[tray]` extra is used. A running desktop tray manager is still required.

### 2. Install the package
Run one of the following in the cloned repository:

```bash
python3 -m pip install --user .
# or include the system tray extras
python3 -m pip install --user '.[tray]'
```

> Some distributions mark Python as an externally managed environment. Append `--break-system-packages` if needed:
> ```bash
> python3 -m pip install --user --break-system-packages '.[tray]'
> ```

Virtual environments avoid the restriction:

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install '.[tray]'
```

> Fish shell users should activate with:
> ```bash
> python3 -m venv .venv
> source .venv/bin/activate.fish
> pip install '.[tray]'
> ```

`pipx` is also supported:

```bash
pipx install .
# or, for tray support
pipx install '.[tray]'
```

> Uninstall any previous `pip install --user` copy before switching to `pipx` to avoid launcher conflicts.

### 3. Launch the notifier
Run the command added to your `$PATH`:

```bash
vrchat-join-notifier
```

Configure the install folder, VRChat log path, and optional Pushover keys in the GUI. **Add to Startup** and **Remove from Startup** manage the autostart entry.

### 4. Start automatically on login (optional)
The GUI writes `~/.config/autostart/vrchat-join-notifier.desktop`. To manage it manually:

```bash
mkdir -p ~/.config/autostart
cat > ~/.config/autostart/vrchat-join-notifier.desktop <<'AUTOSTART'
[Desktop Entry]
Type=Application
Name=VRChat Join Notification with Pushover
Comment=Watch VRChat logs and notify when friends join.
Exec=vrchat-join-notifier
Terminal=false
AUTOSTART
```

Point `Exec` to your virtual environment if needed, then log out and back in to test.

---

# Uninstalling on Linux
Use the same tool you installed with:

- **pip / pip install --user**
  ```bash
  python3 -m pip uninstall vrchat-join-notification-with-pushover
  ```
- **pipx**
  ```bash
  pipx uninstall vrchat-join-notification-with-pushover
  ```

Delete `~/.local/share/vrchat-join-notification-with-pushover` if you also want to remove cached data.

---

## Building on Linux
Builds use `setuptools` and `wheel` via `python3 -m build`.

1. Install build dependencies:
   ```bash
   python3 -m pip install --upgrade pip
   python3 -m pip install --upgrade build setuptools wheel
   ```
2. Run the build:
   ```bash
   python3 -m build
   ```
3. Inspect the artifacts:
   ```bash
   ls -1 dist/
   # vrchat-join-notification-with-pushover-<version>.tar.gz
   # vrchat-join-notification-with-pushover-<version>-py3-none-any.whl
   ```
4. Test the wheel (optional):
   ```bash
   python3 -m venv .venv-test
   source .venv-test/bin/activate
   pip install dist/vrchat-join-notification-with-pushover-<version>-py3-none-any.whl
   vrchat-join-notifier --help
   deactivate
   ```

Share the contents of `dist/` when you are ready to distribute the package. Installers can add `.[tray]` if they need tray support.

---

## Support
If this project helps you, consider buying me a coffee (JPY): https://yueplushdev.booth.pm/items/7434788
