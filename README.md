# VRChat Join Notification with Pushover

A helper for Windows and Linux that watches the latest VRChat log, tells you when you join a world, and announces every new
player with a toast and (optionally) a Pushover push.

## Highlights
- Follows `output_log_*.txt` / `Player.log` automatically and debounces duplicate join events.
- Desktop notifications show the placeholder name that VRChat logged so you can spot unresolved display names.
- Windows ships as a PowerShell script that you can run directly or package as a standalone `.exe`.
- Linux installs as a Python application with a Tk GUI, system tray support (when available), and optional Pushover integration.

# git clone
   ```bash/powershell
git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
cd vrchat-join-notification-with-pushover
   ```

## Windows quick start
1. Clone the repository and install the optional packaging tool:
   ```powershell
   Install-Module -Name ps2exe -Scope CurrentUser   # only if you want an .exe
   Import-Module ps2exe                              # load it for this session
   ```
   > **Note**
   > If `Invoke-ps2exe` says the module could not be loaded, make sure the
   > `Import-Module ps2exe` command above has completed successfully or start a
   > new PowerShell session after installing the module.
2. If you want to test the app, run the script:
   ```powershell
   # run directly
   .\src\vrchat-join-notification-with-pushover.ps1
   ```
or build an executable (after importing the module):
   ```powershell
   # or package
   Invoke-ps2exe -InputFile .\src\vrchat-join-notification-with-pushover.ps1 -OutputFile .\vrchat-join-notification-with-pushover.exe `
     -Title 'VRChat Join Notification with Pushover' -IconFile .\src\vrchat_join_notification\notification.ico -NoConsole -STA -x64
   ```
3. Use **Settings** to pick your VRChat log folder, toggle "Hide window on launch" if you prefer a tray-first start, and add
   Pushover credentials when you want push alerts.

## Linux quick start

The Linux build installs as a Python package and exposes the `vrchat-join-notifier` command.

### 1. Install prerequisites

You need Python 3.8+, Tk bindings, `libnotify`, `procps`, and either `pip` or `pipx`.

- **Debian / Ubuntu**
  ```bash
  sudo apt update
  sudo apt install python3 python3-venv python3-tk python3-pip libnotify-bin procps
  ```
- **Fedora**
  ```bash
  sudo dnf install python3 python3-virtualenv python3-tkinter python3-pip libnotify procps-ng pipx
  ```
- **Arch / Manjaro / Garuda**
  ```bash
  sudo pacman -Syu
  sudo pacman -S python python-pip python-virtualenv tk libnotify procps-ng
  yay -S python-pipx
  ```
  Fish users can export the binary path once with `set -Ux fish_user_paths $fish_user_paths ~/.local/bin`.

### 2. Install the package

Run the commands that match your workflow—each one mirrors the long-form documentation.

- **User install**
  ```bash
  python3 -m pip install --user .
  python3 -m pip install --user '.[tray]'
  ```
  For externally managed Python environments, append:
  ```bash
  python3 -m pip install --user --break-system-packages '.[tray]'
  ```
- **Virtual environment (Bourne shells)**
  ```bash
  python3 -m venv .venv
  source .venv/bin/activate
  pip install '.[tray]'
  ```
- **Virtual environment (fish)**
  ```bash
  python3 -m venv .venv
  source .venv/bin/activate.fish
  pip install '.[tray]'
  ```
- **pipx**
  ```bash
  pipx install .
  pipx install '.[tray]'
  ```
  Remove any previous `pip` copy first (`python3 -m pip uninstall vrchat-join-notification-with-pushover` or delete `~/.local/bin/vrchat-join-notifier`).

### 3. Launch the notifier

Use the CLI that each install method places on your `$PATH`:

```bash
vrchat-join-notifier
```

### 4. Start on login (optional)

The GUI provides **Add to Startup** / **Remove from Startup**, but you can also write the autostart file manually:

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

Point `Exec` at your virtual environment if you installed it there (for example `Exec=/home/you/vrchat-join-notification-with-pushover/.venv/bin/vrchat-join-notifier`) and delete the file to remove the autostart entry. The GUI keeps its data under `~/.local/share/vrchat-join-notification-with-pushover` and enables the tray icon automatically when `pystray`/`Pillow` and a compatible tray host are available.

## Uninstalling
- **Windows:** delete the script or packaged executable.
- **Linux:** uninstall the Python package with `python3 -m pip uninstall vrchat-join-notification-with-pushover` or
  `pipx uninstall vrchat-join-notification-with-pushover`, then remove the data directory if you no longer need cached logs.

## Support
If this project helps you, consider buying me a coffee (JPY): <https://yueplushdev.booth.pm/items/7434788>
