# VRChat Join Notification with Pushover

A cross-platform helper that watches your VRChat logs and notifies you when players enter your instance. Windows users get a familiar PowerShell experience, while Linux users can install a native Python application that ships with a Tk GUI, desktop notifications, and optional Pushover pushes. Documentation is provided in English only.

## Features

- Monitors the most recent `output_log_*.txt` / `Player.log` and automatically follows log roll-overs.
- Emits a single notification when you enter a new world (`OnJoinedRoom`) and once per unique player join (`OnPlayerJoined`), with each join toast including the placeholder label VRChat logged (for example `Player`) so you can tell when the game is still resolving a display name.
- Silently ignores the redundant generic desktop toast (`A player joined your instance.`) so only the richer, name-aware notification remains on Windows and Linux.
- Debounces duplicate events with configurable cooldowns.
- Optional Pushover integration in addition to local desktop notifications.
- Simple tray-aware GUI on both Windows (PowerShell + WinForms) and Linux (Python + Tk + an optional system tray that disables itself automatically when prerequisites are missing). The Windows window now matches the Linux layout, including the live status fields and compact spacing.
- Windows builds guard against multiple launches and continue to raise desktop notifications (even when packaged as a `.exe`) by dispatching them through the UI thread.
- Native Windows builds now register an AppUserModelID and Start Menu shortcut automatically so Action Center toasts (with the system chime) accompany the tray balloon on supported versions of Windows.
- Linux build offers one-click login startup integration that writes/removes the `.desktop` autostart entry for you and confirms the action with a desktop notification.

## Repository layout

| Path | Description |
| --- | --- |
| `src/vrchat-join-notification-with-pushover.ps1` | Windows PowerShell implementation that mirrors the Linux app. |
| `src/vrchat-join-notification-with-pushover_linux.py` | Compatibility shim that launches the packaged Linux app. |
| `src/vrchat_join_notification/` | Installable Python package that provides the Linux GUI and notifier logic. |

---

# Getting the source (PowerShell/Bash)

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
　 if you want test, just Run the script directly:
   ```powershell
   .\src\vrchat-join-notification-with-pushover.ps1
   ```
2. or build an `.exe`:
   ```powershell
   Invoke-ps2exe -InputFile .\src\vrchat-join-notification-with-pushover.ps1 -OutputFile .\vrchat-join-notification-with-pushover.exe `
     -Title 'VRChat Join Notification with Pushover' -IconFile .\src\vrchat_join_notification\notification.ico -NoConsole -STA -x64
   ```
   The compiled build now searches next to the executable for both `notification.ico` and `src\notification.ico` (plus their
   `vrchat_join_notification` subfolder variants), so keep the icon alongside the `.exe` when redistributing a packaged copy.
3. Open **Settings** from the tray icon (or via the window) to configure the install/cache folder, VRChat log directory, and optional Pushover credentials.
   The WinForms UI now mirrors the Linux layout with live **Status**, **Monitoring**, **Current Log**, **Session**, and **Last Event** indicators so you can see exactly what the watcher is doing.
   The first-run window defaults to a more compact size that keeps every control visible (even on high DPI desktops), trims the extra padding around each field, and wraps long status text automatically, while the action buttons inherit the slimmer spacing used by the Linux port for a consistent look—the blank band that previously separated the middle controls is gone.
   Clicking **Start Monitoring** now launches the background follower without tripping the `ParameterizedThreadStart` constructor error on Windows PowerShell 5.1 builds.
   Launching the packaged `.exe` while another copy is running now surfaces an informational dialog instead of starting a duplicate instance, and desktop notifications are marshalled onto the UI thread so Windows toasts fire reliably again.

> [!NOTE]
> The rewritten Windows script uses the same parsing and session logic as the Linux edition, including Unicode-safe string handling. All non-ASCII characters (Japanese log phrases, dash variants, etc.) are emitted explicitly so Windows PowerShell 5.1 and `ps2exe` builds remain reliable on localized systems.
>
> If a previous build stopped at launch with a `The property 'Text' cannot be found` error, update to this release. The WinForms status table now registers its rows without leaking the intermediate index back to the caller, so StrictMode no longer halts the script.
>
> Likewise, we now suppress the column-style index values that PowerShell emits while wiring up the grid. This prevents `ps2exe` builds from hitting the `Cannot find an overload for 'Run' and the argument count: '1'` dialog at startup and lets the main form reach `Application.Run()` as intended.
> Action Center toasts are emitted via a registered AppUserModelID, so Windows 10 and newer play the native notification (with sound) alongside the tray balloon automatically.

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

> [!NOTE]
> The tray icon also requires a running desktop tray manager (for example an X11 Status Notifier host). If none is detected the notifier will disable the tray integration at runtime and fall back to the main window controls.

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

Clone this repository (if you have not already) and install it with `pip` (optionally enabling tray support):

```bash
python3 -m pip install --user .
# or include the system tray extras
python3 -m pip install --user '.[tray]'
```

> [!IMPORTANT]
> Some distributions (such as Arch, Manjaro, and derivatives like Garuda)
> mark Python as an [externally managed
> environment](https://peps.python.org/pep-0668/). On those systems the
> `--user` installs above fail with `error:
> externally-managed-environment`. To run the command in one shot, append
> `--break-system-packages`:
> ```bash
> python3 -m pip install --user --break-system-packages '.[tray]'
> ```
> Only use that flag if you understand the risks of overriding your system
> Python packages. As safer alternatives, create a virtual environment or
> install via `pipx` instead.

Using a virtual environment is also supported and avoids the
externally-managed-environment restriction:

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install '.[tray]'
```

> [!TIP]
> The activation script above targets Bourne-style shells such as Bash or Zsh.
> If you use fish, run `source .venv/bin/activate.fish` instead.
>```bash
> python3 -m venv .venv
> source .venv/bin/activate.fish
> pip install '.[tray]'
>```

If you prefer to keep the app isolated from your system Python, `pipx` works out of the box:

```bash
pipx install .
# or, for tray support
pipx install '.[tray]'
```

> [!NOTE]
> If you previously installed the package with `pip` (including
> `pip install --user`), remove that copy before switching to `pipx` so
> the launcher can be created without warnings. Uninstall with `pip
> uninstall vrchat-join-notification-with-pushover` or delete the old
> `~/.local/bin/vrchat-join-notifier` script and re-run the `pipx`
> command.

### 3. Launch the notifier

Run the command that gets added to your `$PATH`:

```bash
vrchat-join-notifier
```

The GUI opens automatically on the first launch and smartly sizes itself so every control stays visible, even with larger desktop scaling. The Linux build now identifies itself as **VRChat Join Notification with Pushover (Linux)**, and its window switcher entry shows up as **VRC-Notifier** so it's easy to spot while Alt+Tabbing. Configure the following:

- **Install Folder (logs/cache):** Location where the app stores its config and log files (`~/.local/share/vrchat-join-notification-with-pushover` by default).
- **VRChat Log Folder:** Your Proton prefix path containing the VRChat logs. Common Steam installs are detected automatically, but you can browse to a custom directory if needed.
- **Pushover User/Token:** Optional, required only if you want push notifications.

Use the **Add to Startup** / **Remove from Startup** buttons if you want the app to manage your desktop login entry automatically.

Click **Save & Restart Monitoring** to begin watching the log file. Settings persist in `config.json` within the chosen install folder. A pointer file (`config-location.txt`) keeps track of custom locations, so you can move the data directory without losing preferences. If you simply click **Save** while valid Pushover keys are configured, the Linux build also confirms the change with a desktop notification. Clearing either Pushover field and saving likewise removes the stored key and treats you to a confirmation toast so you know it was wiped.

When the tray extras are installed **and** a tray manager is available, the notifier adds a tray icon with quick actions to open the settings window, start/stop monitoring, and exit. Closing the main window simply hides it, allowing the app to continue monitoring in the background. If the environment is missing tray support (no `pystray`/`Pillow` extras or no system tray manager), the app logs the reason, leaves the tray disabled, and you can continue operating it from the main window instead.

### 4. Start automatically on login (optional)

The GUI's **Add to Startup** button creates the autostart entry under `~/.config/autostart/vrchat-join-notifier.desktop`, while **Remove from Startup** deletes it.

Both buttons also trigger a desktop notification so you immediately know whether the action succeeded.

If you prefer to manage it manually, Linux desktop environments follow the [freedesktop.org autostart specification](https://specifications.freedesktop.org/autostart-spec/latest/), so you can launch the notifier automatically by adding a `.desktop` entry to `~/.config/autostart/`. The basic steps are:

```bash
mkdir -p ~/.config/autostart
cat > ~/.config/autostart/vrchat-join-notifier.desktop <<'EOF'
[Desktop Entry]
Type=Application
Name=VRChat Join Notification with Pushover
Comment=Watch VRChat logs and notify when friends join.
Exec=vrchat-join-notifier
Terminal=false
EOF
```

The `Exec` line assumes `vrchat-join-notifier` is on your `$PATH` (which is the case for installs performed with `pipx` or `pip install --user`). If you installed it into a virtual environment, point `Exec` to that environment's binary, for example `Exec=/home/you/vrchat-join-notification-with-pushover/.venv/bin/vrchat-join-notifier`.

After saving the file, log out and back in (or reboot) and your session will automatically start the notifier. You can remove the autostart entry at any time by deleting the `.desktop` file.

# Uninstalling on Linux

Remove the Python package with the same tool you used to install it:

- **pip / pip install --user**
  ```bash
  python3 -m pip uninstall vrchat-join-notification-with-pushover
  ```
- **pipx**
  ```bash
  pipx uninstall vrchat-join-notification-with-pushover
  ```


Once you know the owning tool, run the corresponding uninstall command above.

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
