# VRChat Join Notification with Pushover

## 概要
VRChat Join Notification with Pushover は、VRChat のログを常時監視し、フレンドがインスタンスに参加したタイミングをデスクトップ通知と Pushover で知らせるクロスプラットフォームの常駐ツールです。GUI は Tkinter 製で、設定やログ保存先の管理、自動起動の切り替えまで一通りアプリ内で完結します。【F:src/vrchat_join_notification/app.py†L49-L58】【F:src/vrchat_join_notification/app.py†L1623-L1680】

## 主な機能
- VRChat の `Player.log` / `output_log_*.txt` を自動追跡し、ログがローテーションしても最新ファイルへ追従して監視を継続します。【F:src/vrchat_join_notification/app.py†L763-L779】【F:src/vrchat_join_notification/app.py†L1530-L1586】
- セッション単位で参加者を管理し、同一プレイヤーは 10 秒のクールダウン内では通知しないなどの重複抑制を行います。【F:src/vrchat_join_notification/app.py†L1170-L1240】【F:src/vrchat_join_notification/app.py†L1437-L1475】
- Windows では PowerShell トースト、Linux では `notify-send` を優先し、いずれも失敗時はローカルログへ記録します。Pushover が設定されていれば同じ本文をモバイルへ送信します。【F:src/vrchat_join_notification/app.py†L782-L930】
- 任意でシステムトレイに常駐し、開始・停止・GUI の再表示などをトレイメニューから操作できます（`pystray` と `Pillow` が必要）。【F:src/vrchat_join_notification/app.py†L932-L1103】
- アプリは単一インスタンスで動作し、ユーザー領域に設定 (`config.json`) とログ (`notifier.log`) を保存します。【F:src/vrchat_join_notification/app.py†L205-L324】【F:src/vrchat_join_notification/app.py†L326-L343】

## 仕組みの概要
1. **ログ監視スレッド** – `LogMonitor` が別スレッドで VRChat ログを tail し、参加・退室・ルーム移動などのイベントを解析して UI スレッドへ渡します。【F:src/vrchat_join_notification/app.py†L1513-L1620】
2. **セッショントラッカー** – `SessionTracker` が OnJoinedRoom/OnPlayerJoined の組み合わせや自分自身のイベントを整合させ、重複通知やプレースホルダー名を整理します。【F:src/vrchat_join_notification/app.py†L1160-L1510】
3. **通知パイプライン** – `DesktopNotifier` と `PushoverClient` が同じメッセージをデスクトップと Pushover に送付し、送信結果をアプリログへ残します。【F:src/vrchat_join_notification/app.py†L782-L930】
4. **システムトレイ／自動起動** – `TrayIconController` と `AppController` がトレイ常駐、ウィンドウの自動非表示、スタートアップ登録（Linux `.desktop` / Windows レジストリ）を制御します。【F:src/vrchat_join_notification/app.py†L932-L1133】【F:src/vrchat_join_notification/app.py†L2009-L2316】
5. **安全なランチャー** – Windows/Linux 用のラッパースクリプトは、メモリ監視・VRChat/EAC 保護・高権限ハンドル禁止などのガードを掛けたうえで本体を実行します。【F:src/vrchat-join-notification-with-pushover_windows.py†L1-L210】【F:src/vrchat-join-notification-with-pushover_linux.py†L1-L205】

## 必要環境
### 共通
- Python 3.8 以上。【F:pyproject.toml†L5-L23】
- VRChat のログ (`AppData/LocalLow/VRChat/VRChat`) へアクセスできること。【F:src/vrchat_join_notification/app.py†L138-L151】
- Pushover 通知を使う場合は API トークンとユーザーキー。

### Windows
- PowerShell (標準搭載) を利用したトースト通知に対応しています。【F:src/vrchat_join_notification/app.py†L782-L859】
- 任意: システムトレイを使う場合は `pip install "vrchat-join-notification-with-pushover[tray]"` で `pystray` と `Pillow` を追加してください。【F:pyproject.toml†L25-L29】【F:src/vrchat_join_notification/app.py†L932-L1103】

### Linux
- Python/Tk、および `notify-send` を提供する `libnotify` が必要です。【F:src/vrchat_join_notification/app.py†L782-L820】
- Wayland/X11 でトレイ機能を使う場合は `pystray` + `Pillow` に加えて、X11 バックエンドでは `python-xlib` が必要になるケースがあります。【F:src/vrchat_join_notification/app.py†L932-L1102】

## インストール
1. リポジトリを取得します。
   ```bash
   git clone https://github.com/yueplush/vrchat-join-notification-with-pushover.git
   cd vrchat-join-notification-with-pushover
   ```
2. 仮想環境を作成してアプリをインストールします。
   ```bash
   python -m venv .venv
   source .venv/bin/activate  # Windows は .venv\Scripts\Activate.ps1
   python -m pip install --upgrade pip
   python -m pip install .
   # トレイ機能込みで入れる場合
   python -m pip install '.[tray]'
   ```
3. `pipx` を利用する場合も同様に `pipx install .` / `pipx install '.[tray]'` で導入できます。【F:pyproject.toml†L25-L33】

## 実行方法
- インストール後はエントリーポイント `vrchat-join-notifier` で起動できます。
  ```bash
  vrchat-join-notifier
  ```
  このコマンドは `vrchat_join_notification.app:main` を呼び出します。【F:pyproject.toml†L31-L33】
- Python から直接実行する場合は `python -m vrchat_join_notification.app` としても同じです。【F:src/vrchat_join_notification/app.py†L2324-L2338】
- Windows / Linux のラッパーを使いたい場合は `python src/vrchat-join-notification-with-pushover_windows.py` や `python src/vrchat-join-notification-with-pushover_linux.py` を実行すると安全ガード付きで立ち上がります。【F:src/vrchat-join-notification-with-pushover_windows.py†L287-L305】【F:src/vrchat-join-notification-with-pushover_linux.py†L207-L224】

## 初回設定と保存先
- 初回起動時はインストールフォルダ（設定・ログ保存先）と VRChat ログフォルダ、Pushover のユーザーキー／API トークンを GUI で指定します。【F:src/vrchat_join_notification/app.py†L1623-L1680】【F:src/vrchat_join_notification/app.py†L1688-L1771】
- 設定は `config.json` として保存され、場所はデフォルトで次の通りです。
  - Windows: `%LOCALAPPDATA%\VRChatJoinNotificationWithPushover`
  - Linux: `~/.local/share/vrchat-join-notification-with-pushover`
  旧バージョンの保存場所に存在する設定は自動的に移行され、パスは `config-location.txt` で記憶されます。【F:src/vrchat_join_notification/app.py†L69-L111】【F:src/vrchat_join_notification/app.py†L205-L324】
- アプリケーションログは同じフォルダ内の `notifier.log` に追記されます。【F:src/vrchat_join_notification/app.py†L326-L343】

## 通知と Pushover 設定
- デスクトップ通知は OS ごとに最適な手段を選びます。Linux で `notify-send` が見つからない場合はログ出力のみとなります。【F:src/vrchat_join_notification/app.py†L782-L820】
- Pushover はユーザーキーとアプリトークンの両方が入力されているときだけ送信され、結果はレスポンスのステータスとともにアプリログへ記録されます。【F:src/vrchat_join_notification/app.py†L900-L929】
- VRChat クライアントが起動していないときに検出したイベントは無視し、不要な通知を抑えます。【F:src/vrchat_join_notification/app.py†L666-L733】【F:src/vrchat_join_notification/app.py†L1264-L1363】

## システムトレイ（任意機能）
- `pystray` と `Pillow` が利用可能な環境では、アプリ起動時にトレイアイコンが生成されます。サポートされていない場合は理由がステータスとログに表示されます。【F:src/vrchat_join_notification/app.py†L932-L1009】【F:src/vrchat_join_notification/app.py†L1675-L1679】
- トレイメニューから監視の開始／停止、設定ウィンドウの再表示、アプリ終了などが可能です。【F:src/vrchat_join_notification/app.py†L994-L1050】

## 自動起動の設定
- GUI の「Add to Startup」「Remove from Startup」ボタンで、Linux では `~/.config/autostart/vrchat-join-notifier.desktop`、Windows では `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` に登録／削除を行います。【F:src/vrchat_join_notification/app.py†L2009-L2278】
- 起動コマンドは実行中の形態（PyInstaller ビルド、`python -m` 実行など）に合わせて自動生成されます。【F:src/vrchat_join_notification/app.py†L2187-L2217】

## ログとトラブルシューティング
- 監視対象のログディレクトリが見つからない、または読み取りエラーが発生した場合はステータス表示とアプリログに理由が通知されます。【F:src/vrchat_join_notification/app.py†L1531-L1586】
- VRChat が起動していない、または自身の参加イベントと判断された場合は通知を抑制し、その旨をログへ出力します。【F:src/vrchat_join_notification/app.py†L1264-L1430】
- GUI を閉じるとウィンドウはトレイへ格納され、`Quit` を押すかトレイメニューから終了するまで監視は継続します。【F:src/vrchat_join_notification/app.py†L2114-L2166】

## パッケージングとビルド
- Python パッケージとしてビルドする場合は `python -m build` でソース配布物とホイールを生成できます（`setuptools`/`wheel` を使用）。【F:pyproject.toml†L1-L3】
- Windows 向けに単一ファイル実行形式へまとめたい場合は `tools/build-windows-exe.ps1` を利用して PyInstaller ビルドを自動化できます。【F:tools/build-windows-exe.ps1†L1-L20】

## リポジトリ構成
| パス | 説明 |
| --- | --- |
| `src/vrchat_join_notification/` | GUI・ログ監視・通知処理を含む Python パッケージ本体。【F:src/vrchat_join_notification/app.py†L1-L2338】 |
| `src/vrchat-join-notification-with-pushover_*.py` | Windows/Linux 向けの保護付きランチャー。【F:src/vrchat-join-notification-with-pushover_windows.py†L1-L305】【F:src/vrchat-join-notification-with-pushover_linux.py†L1-L224】 |
| `public/` | プロジェクト紹介用の静的サイトアセット。【F:public/index.html†L1-L188】 |
| `tools/` | ビルド補助スクリプト（Windows PyInstaller 用）。【F:tools/build-windows-exe.ps1†L1-L20】 |

## ライセンス
本プロジェクトは MIT License で提供されています。【F:pyproject.toml†L5-L21】
