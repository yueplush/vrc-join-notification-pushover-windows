# VRChat Join Notification with Pushover

Cross-platform helper that watches VRChat logs and sends desktop and optional Pushover alerts when friends join. Windows now ships as a Go GUI built with Fyne, while Linux installs as a Python app with a Tk GUI.

## Features
- Tracks the newest `output_log_*.txt` / `Player.log` and follows roll-overs automatically.
- Emits one world join toast and one per unique player, ignoring the generic "A player joined your instance." message.
- Debounces duplicates with configurable cooldowns and mirrors behaviour across desktop and push notifications.
- Linux adds tray support when dependencies exist and can auto-hide on launch; Windows can be run directly or packaged as an `.exe`.

## Repository layout
| Path | Description |
| --- | --- |
| `main.go` | Windows Go GUI entry point built with Fyne (requires CGO/OpenGL). |
| `main_fallback.go` | Console-based entry point used when CGO is unavailable (e.g. Windows on ARM). |
| `src/vrchat-join-notification-with-pushover_linux.py` | Legacy shim that calls the packaged Linux app. |
| `src/vrchat_join_notification/` | Installable Python package with the Linux GUI and notifier logic. |
| `go.mod` | Module definition for the Windows build. |

---

# Getting the source (PowerShell/Bash)
Clone the repository and enter it:

```powershell/bash
git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
cd vrchat-join-notification-with-pushover
```

---

## Windows版のビルド手順 (Go/Fyne GUI)
1. Install [Go 1.21+](https://go.dev/dl/) and ensure `go` is available in **PowerShell**:
   ```powershell
   go version
   ```
2. Install the maintained Fyne tooling used for packaging:
   ```powershell
   go install fyne.io/tools/cmd/fyne@latest
   ```
   Ensure `%USERPROFILE%\go\bin` is on your `PATH` so the `fyne` command is available.
3. Restore Go dependencies:
   ```powershell
   go mod tidy
   ```
4. Build a development binary without a console window:
   ```powershell
   go build -ldflags="-H=windowsgui" -o VRChatJoinNotifier.exe .
   ```
   > On Windows on ARM (or when CGO is disabled) the build automatically falls back to a console UI and spawns its own console window when launched from Explorer.
   > Cross-compiling from another platform? Prefix the command with `GOOS=windows GOARCH=amd64`.
5. Package a distributable `.exe` with the embedded icon:
   ```powershell
   fyne package -os windows -icon src/notification.ico -name VRChatJoinNotifier --app-id com.vrchat.joinnotifier -release
   ```
   The packaged executable is written to `dist/VRChatJoinNotifier.exe`.
6. Run `VRChatJoinNotifier.exe`, enter your **Pushover App Token** and **User Key**, and click **Save**.
   Configuration is stored in `%AppData%\VRChatJoinNotifier\config.json` and the same folder holds `notifier.log`.
   Saving immediately starts the background log monitor and Pushover notifications.

---

## Windows版 (Pythonベース) の実行と .exe パッケージング
Linux 版と同じ Python/Tk アプリを Windows でも利用できます。Python のまま実行する方法と、PyInstaller で単体の `.exe` を生成する手順を以下にまとめています。

### 1. 前提条件
- [Python 3.8+ (64bit)](https://www.python.org/downloads/windows/) をインストールし、セットアップ時に「`Add python.exe to PATH`」を有効化してください。
- Tkinter は公式インストーラに同梱されています。インストール後、PowerShell でバージョンを確認します。
  ```powershell
  py --version
  ```

### 2. 依存関係のインストール
リポジトリのルートで仮想環境を作成し、Python 版アプリと任意でトレイ機能を入れます。

```powershell
py -3 -m venv .venv
.\.venv\Scripts\Activate.ps1
python -m pip install --upgrade pip
# 基本機能のみ
python -m pip install .
# システムトレイ対応が必要なら
python -m pip install '.[tray]'
```

> PowerShell の実行ポリシーでエラーになる場合は、一時的に `Set-ExecutionPolicy -Scope Process RemoteSigned` を実行してください。

### 3. Python スクリプトとして起動する
仮想環境を有効にしたまま、Python 版 GUI を直接起動できます。

```powershell
# Windows でも Linux と同じ GUI が立ち上がります
python -m vrchat_join_notification.app
```

> 設定ファイルやログは `%USERPROFILE%\.local\share\vrchat-join-notification-with-pushover` 配下に保存されます。必要に応じてアプリ内の「Install Dir」を Windows の任意のフォルダに変更してください。

### 4. PyInstaller で `.exe` を作成する
Python 版をそのまま Windows 用にバンドルしたい場合は [PyInstaller](https://pyinstaller.org/) を利用します。

```powershell
# 仮想環境を有効にした状態で実行
python -m pip install pyinstaller
pyinstaller ^
  --noconsole ^
  --name VRChatJoinNotifierPy ^
  --icon src/notification.ico ^
  --add-data "src/vrchat_join_notification/notification.ico;vrchat_join_notification" ^
  src/vrchat_join_notification/app.py
```

- ビルドが完了すると `dist/VRChatJoinNotifierPy/VRChatJoinNotifierPy.exe` が生成されます。
- 生成物は単体で動作しますが、初回起動時に Microsoft Defender SmartScreen による警告が表示される場合があります。その際は **詳細情報** → **実行** を選択してください。
- `.spec` ファイルを編集してカスタマイズしたい場合は、最初に `pyinstaller --name ... --onefile --icon ... --add-data ... --specpath buildspec` などでテンプレートを出力し、以後は `pyinstaller buildspec/VRChatJoinNotifierPy.spec` を実行します。

> PyInstaller の `--add-data` は Windows では `;` 区切りを使用します (Linux/macOS では `:`)。

PyInstaller で作成した `.exe` は Linux 版と同等の機能を提供し、同じ設定ファイルを共有します。

---

## Linux quick start (Python package)
Use the `vrchat-join-notifier` command provided by the installable Python package. The compatibility script simply forwards to it.

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

Set the install folder, VRChat log path, and optional Pushover keys in the GUI. **Add to Startup** and **Remove from Startup** manage the autostart entry.

### 4. Start automatically on login (optional)
The GUI writes `~/.config/autostart/vrchat-join-notifier.desktop`. To manage it manually:

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

Point `Exec` to your virtual environment if needed, then log out and back in to test.

---

# Uninstalling on Linux
Use the tool you installed with:

- **pip / pip install --user**
  ```bash
  python3 -m pip uninstall vrchat-join-notification-with-pushover
  ```
- **pipx**
  ```bash
  pipx uninstall vrchat-join-notification-with-pushover
  ```

Delete `~/.local/share/vrchat-join-notification-with-pushover` if you want to remove cached data.

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
