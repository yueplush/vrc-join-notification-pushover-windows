# VRChat Join Notification with Pushover

A cross-platform helper that watches your VRChat logs and notifies you when players enter your instance. Windows users get a familiar PowerShell experience, while Linux users can install a native Python application that ships with a Tk GUI, desktop notifications, and optional Pushover pushes. Documentation is provided in English only.

## Features

- Monitors the most recent `output_log_*.txt` / `Player.log` and automatically follows log roll-overs.
- Emits a single notification when you enter a new world (`OnJoinedRoom`) and once per unique player join (`OnPlayerJoined`).
- Debounces duplicate events with configurable cooldowns.
- Optional Pushover integration in addition to local desktop notifications.
- Simple tray-aware GUI on both Windows (PowerShell + WinForms) and Linux (Python + Tk + optional system tray).

## Repository layout

| Path | Description |
| --- | --- |
| `src/vrchat-join-notification-with-pushover.ps1` | Original Windows PowerShell implementation. |
| `src/vrchat-join-notification-with-pushover_linux.py` | Compatibility shim that launches the packaged Linux app. |
| `src/vrchat_join_notification/` | Installable Python package that provides the Linux GUI and notifier logic. |

---

## Getting the source (PowerShell/Bash)

Clone the repository and enter it:

```powershell/bash
git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
cd vrchat-join-notification-with-pushover
```

---

## Windows quick start (PowerShell)

1. Install the `ps2exe` module if you plan to build a standalone executable:
   ```powershell
   Install-Module -Name ps2exe -Scope CurrentUser
   ```
2. Run the script directly:
   ```powershell
   .\src\vrchat-join-notification-with-pushover.ps1
   ```
   or build an `.exe`:
   ```powershell
   Invoke-ps2exe -InputFile .\src\vrchat-join-notification-with-pushover.ps1 -OutputFile .\vrchat-join-notification-with-pushover.exe `
     -Title 'VRChat Join Notification with Pushover' -IconFile .\src\vrchat_join_notification\notification.ico -NoConsole -STA -x64
   ```
3. Use the tray icon to open **Settings**, configure your VRChat log directory and (optionally) Pushover credentials, then start monitoring.

---

## Linux quick start (Python package)

The Linux port is published as an installable Python package that exposes the `vrchat-join-notifier` command. The legacy script `src/vrchat-join-notification-with-pushover_linux.py` simply calls into that package for backwards compatibility.

### 1. Prerequisites

Ensure the following are available on your system:

| Requirement | Why it is needed |
| --- | --- |
| Python 3.8+ with Tk bindings | Powers the GUI. Usually provided by `python3` + `python3-tk`. |
| `pip` or `pipx` | Installs the package. |
| `libnotify` (`notify-send`) | Provides desktop notifications. Package name examples: `libnotify-bin` (Debian/Ubuntu), `libnotify` (Fedora/Arch). |
| `procps` (`pgrep`) | Detects a running `VRChat.exe`. |
| *(Optional)* `pystray`, `Pillow` | Enables the system tray icon. These are installed automatically when using the `[tray]` extra. |

Example package installs:

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

### 2. Install the package

Clone this repository and install it with `pip` (optionally enabling tray support):

```bash
cd vrchat-join-notification-with-pushover
python3 -m pip install --user .
# or include the system tray extras
python3 -m pip install --user '.[tray]'
```

Using a virtual environment is also supported:

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install '.[tray]'
```

If you prefer to keep the app isolated from your system Python, `pipx` works out of the box:

```bash
pipx install .
# or, for tray support
pipx install '.[tray]'
```

### 3. Launch the notifier

Run the command that gets added to your `$PATH`:

```bash
vrchat-join-notifier
```

The GUI opens automatically on the first launch. Configure the following:

- **Install Folder (logs/cache):** Location where the app stores its config and log files (`~/.local/share/vrchat-join-notification-with-pushover` by default).
- **VRChat Log Folder:** Your Proton prefix path containing the VRChat logs. Common Steam installs are detected automatically, but you can browse to a custom directory if needed.
- **Pushover User/Token:** Optional, required only if you want push notifications.

Click **Save & Restart Monitoring** to begin watching the log file. Settings persist in `config.json` within the chosen install folder. A pointer file (`config-location.txt`) keeps track of custom locations, so you can move the data directory without losing preferences.

When the tray extras are installed, the notifier adds a tray icon with quick actions to open the settings window, start/stop monitoring, and exit. Closing the main window simply hides it, allowing the app to continue monitoring in the background.

### 4. Start automatically on login (optional)

Linux desktop environments follow the [freedesktop.org autostart specification](https://specifications.freedesktop.org/autostart-spec/latest/), so you can launch the notifier automatically by adding a `.desktop` entry to `~/.config/autostart/`. The basic steps are:

```bash
mkdir -p ~/.config/autostart
cat > ~/.config/autostart/vrchat-join-notifier.desktop <<'EOF'
[Desktop Entry]
Type=Application
Name=VRChat Join Notifier
Comment=Watch VRChat logs and notify when friends join.
Exec=vrchat-join-notifier
Terminal=false
EOF
```

The `Exec` line assumes `vrchat-join-notifier` is on your `$PATH` (which is the case for installs performed with `pipx` or `pip install --user`). If you installed it into a virtual environment, point `Exec` to that environment's binary, for example `Exec=/home/you/vrchat-join-notification-with-pushover/.venv/bin/vrchat-join-notifier`.

After saving the file, log out and back in (or reboot) and your session will automatically start the notifier. You can remove the autostart entry at any time by deleting the `.desktop` file.

### Uninstalling on Linux

Remove the Python package with the same tool you used to install it:

- **pip / pip install --user**
  ```bash
  python3 -m pip uninstall vrchat-join-notification-with-pushover
  ```
- **pipx**
  ```bash
  pipx uninstall vrchat-join-notification-with-pushover
  ```

After uninstalling, you can delete the app's data directory (defaults to `~/.local/share/vrchat-join-notification-with-pushover`) if you no longer need your saved settings or cached logs.

### Desktop & push notifications

- Linux desktop notifications mirror the Windows behaviour and obey the same cooldowns—one toast per unique event.
- Pushover pushes use the same debounce logic to avoid duplicate alerts across devices.

---

## Building on Linux

The project uses `setuptools` and `wheel` under the hood, driven by the `python3 -m build` frontend. Building locally lets you create both a source distribution (`sdist`) and a universal wheel that installs the `vrchat-join-notifier` console script.

1. **Prepare build dependencies.** Ensure Python, Tk bindings, and compiler prerequisites are installed as described in the quick start above. Then install the build tooling (isolated in a virtual environment if you prefer):
   ```bash
   python3 -m pip install --upgrade pip
   python3 -m pip install --upgrade build setuptools wheel
   ```
2. **Run the build.** From the repository root, invoke:
   ```bash
   python3 -m build
   ```
   This creates fresh artifacts under `dist/`.
3. **Verify the artifacts.** Inspect the directory to confirm both the wheel and source archive exist and match the project version:
   ```bash
   ls -1 dist/
   # vrchat-join-notification-with-pushover-<version>.tar.gz
   # vrchat-join-notification-with-pushover-<version>-py3-none-any.whl
   ```
4. **Test the wheel locally (optional but recommended).** Install it into a virtual environment or with `--user` to ensure the console script is produced correctly:
   ```bash
   python3 -m venv .venv-test
   source .venv-test/bin/activate
   pip install dist/vrchat-join-notification-with-pushover-<version>-py3-none-any.whl
   vrchat-join-notifier --help
   deactivate
   ```
5. **Package for distribution (optional).** Share the artifacts by copying the `dist/` contents to your release location or another machine. Consumers can install directly with `pip install dist/vrchat-join-notification-with-pushover-<version>-py3-none-any.whl` or `pip install dist/vrchat-join-notification-with-pushover-<version>.tar.gz`. To include tray support, add `.[tray]` when installing from the source tree or install `pystray`/`Pillow` alongside the wheel.

After installation, launch the notifier using `vrchat-join-notifier` as described in the quick-start section.

---

## Development notes

- The Python package metadata lives in `pyproject.toml`.
- Packaging uses `setuptools`; run `python3 -m build` to produce wheels/sdist.
- Linting/tests are not bundled; feel free to use your preferred tooling.

---

## Support

If this project helps you, consider buying me a coffee (JPY):
https://yueplushdev.booth.pm/items/7434788
