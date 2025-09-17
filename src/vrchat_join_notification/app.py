"""VRChat Join Notifier (Linux).

This module mirrors the behaviour of the Windows PowerShell implementation while
using a Tkinter GUI, libnotify desktop notifications and Pushover pushes.
"""
from __future__ import annotations

import importlib.util
import json
import os
import queue
import re
import shlex
import subprocess
import sys
import threading
import time
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from importlib import resources
from typing import Any, Dict, Optional, Tuple

import tkinter as tk
from tkinter import filedialog, messagebox, ttk

if importlib.util.find_spec("urllib.parse") and importlib.util.find_spec("urllib.request"):
    import urllib.parse
    import urllib.request
else:  # pragma: no cover - stdlib should always be present
    urllib = None  # type: ignore

_TRAY_SPEC = importlib.util.find_spec("pystray")
_PIL_SPEC = importlib.util.find_spec("PIL.Image")
_PIL_DRAW_SPEC = importlib.util.find_spec("PIL.ImageDraw")
if _TRAY_SPEC and _PIL_SPEC and _PIL_DRAW_SPEC:
    import pystray
    from PIL import Image, ImageDraw
else:  # pragma: no cover - optional dependency
    pystray = None  # type: ignore
    Image = None  # type: ignore
    ImageDraw = None  # type: ignore

APP_NAME = "VRChat Join Notifier"
CONFIG_FILE_NAME = "config.json"
POINTER_FILE_NAME = "config-location.txt"
APP_LOG_NAME = "notifier.log"
PO_URL = "https://api.pushover.net/1/messages.json"
NOTIFY_COOLDOWN_SECONDS = 10
SESSION_FALLBACK_GRACE_SECONDS = 30
SESSION_FALLBACK_MAX_CONTINUATION_SECONDS = 4
ICON_FILE_NAME = "notification.ico"
AUTOSTART_FILE_NAME = "vrchat-join-notifier.desktop"


def _expand_path(path: str) -> str:
    expanded = os.path.expanduser(os.path.expandvars(path.strip()))
    return os.path.abspath(expanded)


def _default_storage_root() -> str:
    root = os.path.join(
        os.path.expanduser("~/.local/share"), "vrchat-join-notification-with-pushover"
    )
    return _expand_path(root)


def _legacy_storage_roots() -> Tuple[str, ...]:
    legacy_root = os.path.join(os.path.expanduser("~/.local/share"), "VRChatJoinNotifier")
    return (_expand_path(legacy_root),)


def _guess_vrchat_log_dir() -> str:
    candidates = [
        "~/.steam/steam/steamapps/compatdata/438100/pfx/drive_c/users/steamuser/AppData/LocalLow/VRChat/VRChat",
        "~/.local/share/Steam/steamapps/compatdata/438100/pfx/drive_c/users/steamuser/AppData/LocalLow/VRChat/VRChat",
    ]
    for candidate in candidates:
        path = _expand_path(candidate)
        if os.path.isdir(path):
            return path
    return _expand_path(candidates[0])


@dataclass
class AppConfig:
    install_dir: str
    vrchat_log_dir: str
    pushover_user: str = ""
    pushover_token: str = ""
    first_run: bool = field(default=False, init=False)

    @classmethod
    def load(cls) -> Tuple["AppConfig", Optional[str]]:
        storage_root = _default_storage_root()
        os.makedirs(storage_root, exist_ok=True)
        install_dir = storage_root
        pointer_candidates = [os.path.join(storage_root, POINTER_FILE_NAME)]
        for legacy_root in _legacy_storage_roots():
            if legacy_root != storage_root:
                pointer_candidates.append(os.path.join(legacy_root, POINTER_FILE_NAME))

        for pointer_path in pointer_candidates:
            if not os.path.exists(pointer_path):
                continue
            try:
                with open(pointer_path, "r", encoding="utf-8") as handle:
                    raw = handle.read().strip()
                if raw:
                    candidate = _expand_path(raw)
                    if os.path.isdir(candidate):
                        install_dir = candidate
                        break
            except OSError:
                continue

        config_path = os.path.join(install_dir, CONFIG_FILE_NAME)
        fallback_path = os.path.join(storage_root, CONFIG_FILE_NAME)
        data: Dict[str, str] = {}
        load_error: Optional[str] = None

        config_exists = os.path.exists(config_path)
        fallback_exists = os.path.exists(fallback_path)

        if not config_exists and not fallback_exists:
            for legacy_root in _legacy_storage_roots():
                legacy_config = os.path.join(legacy_root, CONFIG_FILE_NAME)
                if os.path.exists(legacy_config):
                    install_dir = legacy_root
                    config_path = legacy_config
                    config_exists = True
                    break

        first_run = not config_exists and not fallback_exists

        if config_exists:
            try:
                with open(config_path, "r", encoding="utf-8") as handle:
                    data = json.load(handle)
            except Exception as exc:  # pragma: no cover - defensive
                load_error = f"Failed to load settings: {exc}"
                data = {}
        elif install_dir != storage_root and fallback_exists:
            try:
                with open(fallback_path, "r", encoding="utf-8") as handle:
                    data = json.load(handle)
                install_dir = storage_root
            except Exception as exc:  # pragma: no cover - defensive
                load_error = f"Failed to load settings: {exc}"
                data = {}

        cfg = cls(
            install_dir=_expand_path(data.get("InstallDir", install_dir)),
            vrchat_log_dir=_expand_path(data.get("VRChatLogDir", _guess_vrchat_log_dir())),
            pushover_user=str(data.get("PushoverUser", "")),
            pushover_token=str(data.get("PushoverToken", "")),
        )
        legacy_roots = _legacy_storage_roots()
        if legacy_roots:
            primary_legacy = legacy_roots[0]
            if (
                os.path.abspath(cfg.install_dir) == primary_legacy
                and primary_legacy != storage_root
            ):
                new_config_path = os.path.join(storage_root, CONFIG_FILE_NAME)
                if os.path.exists(new_config_path):
                    cfg.install_dir = storage_root
                else:
                    original_dir = cfg.install_dir
                    cfg.install_dir = storage_root
                    try:
                        cfg.save()
                    except Exception:
                        cfg.install_dir = original_dir
                    else:
                        cfg.install_dir = storage_root
        cfg.first_run = first_run
        cfg.ensure_install_dir()
        cfg._write_pointer()
        return cfg, load_error

    def ensure_install_dir(self) -> None:
        os.makedirs(self.install_dir, exist_ok=True)

    def config_path(self) -> str:
        return os.path.join(self.install_dir, CONFIG_FILE_NAME)

    def save(self) -> None:
        self.ensure_install_dir()
        payload = {
            "InstallDir": self.install_dir,
            "VRChatLogDir": self.vrchat_log_dir,
            "PushoverUser": self.pushover_user,
            "PushoverToken": self.pushover_token,
        }
        with open(self.config_path(), "w", encoding="utf-8") as handle:
            json.dump(payload, handle, indent=2, ensure_ascii=False)
        self._write_pointer()
        self.first_run = False

    def _write_pointer(self) -> None:
        storage_root = _default_storage_root()
        os.makedirs(storage_root, exist_ok=True)
        pointer_path = os.path.join(storage_root, POINTER_FILE_NAME)
        try:
            with open(pointer_path, "w", encoding="utf-8") as handle:
                handle.write(self.install_dir)
        except OSError:
            pass


class AppLogger:
    def __init__(self, config: AppConfig) -> None:
        self._config = config
        self._lock = threading.Lock()

    def log(self, message: str) -> None:
        timestamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        line = f"[{timestamp}] {message}"
        try:
            self._config.ensure_install_dir()
            path = os.path.join(self._config.install_dir, APP_LOG_NAME)
            with self._lock:
                with open(path, "a", encoding="utf-8") as handle:
                    handle.write(line + "\n")
        except OSError:
            pass


def strip_zero_width(text: str) -> str:
    return re.sub(r"[\u200b-\u200d\ufeff]", "", text)


def normalize_join_fragment(text: str) -> str:
    clean = strip_zero_width(text or "")
    clean = clean.replace("\u3000", " ")
    clean = clean.strip().strip('"').strip("'")
    clean = clean.replace("||", "|")
    clean = clean.lstrip("-—–:|").strip()
    if len(clean) > 160:
        clean = clean[:160].strip()
    if re.fullmatch(r"[-:|–—]+", clean):
        return ""
    return clean


def normalize_join_name(name: str) -> str:
    clean = normalize_join_fragment(name)
    return clean or ""


def is_placeholder_name(name: str) -> bool:
    if not name:
        return True
    trimmed = name.strip().lower()
    return trimmed in {"player", "you", "someone", "a player"}


def get_short_hash(text: str) -> str:
    import hashlib

    if not text:
        return ""
    digest = hashlib.md5(text.encode("utf-8", "ignore")).hexdigest()
    return digest[:8]


def parse_player_event_line(line: str, event_token: str = "OnPlayerJoined") -> Optional[Dict[str, str]]:
    if not line:
        return None
    lower_line = line.lower()
    needle = event_token.lower()
    index = lower_line.find(needle)
    if index < 0:
        return None
    after = strip_zero_width(line[index + len(needle) :])
    after = after.strip()
    while after and after[0] in ":|-–—":
        after = after[1:].lstrip()

    placeholder = ""
    if after:
        candidate = re.split(r"(?i)\b(displayName|name|userId)\b", after, maxsplit=1)[0]
        candidate = candidate.split("(", 1)[0]
        candidate = candidate.split("[", 1)[0]
        candidate = candidate.split("{", 1)[0]
        candidate = candidate.split("<", 1)[0]
        candidate = normalize_join_fragment(candidate)
        if is_placeholder_name(candidate):
            placeholder = candidate

    display_name = None
    match = re.search(r"(?i)displayName\s*[:=]\s*([^,\]\)]+)", after)
    if match:
        display_name = normalize_join_fragment(match.group(1))

    if not display_name:
        match = re.search(r"(?i)\bname\s*[:=]\s*([^,\]\)]+)", after)
        if match:
            display_name = normalize_join_fragment(match.group(1))

    user_id = None
    match = re.search(r"(?i)\(usr_[^\)\s]+\)", after)
    if match:
        user_id = match.group(0).strip("() ")
    if not user_id:
        match = re.search(r"(?i)userId\s*[:=]\s*(usr_[0-9a-f\-]+)", after)
        if match:
            user_id = match.group(1)

    if not display_name:
        tmp = after
        if user_id:
            tmp = re.sub(re.escape(f"({user_id})"), "", tmp, flags=re.IGNORECASE)
        tmp = re.sub(r"(?i)\(usr_[^\)]*\)", "", tmp)
        tmp = re.sub(r"(?i)\(userId[^\)]*\)", "", tmp)
        tmp = re.sub(r"\[[^\]]*\]", "", tmp)
        tmp = re.sub(r"\{[^\}]*\}", "", tmp)
        tmp = re.sub(r"<[^>]*>", "", tmp)
        tmp = tmp.replace("||", "|")
        display_name = normalize_join_fragment(tmp)

    if not display_name and user_id:
        display_name = user_id

    safe_line = strip_zero_width(line).replace("||", "|").strip()
    return {
        "name": display_name or "",
        "user_id": user_id or "",
        "placeholder": placeholder or "",
        "raw_line": safe_line,
    }


def parse_room_transition_line(line: str) -> Optional[Dict[str, str]]:
    if not line:
        return None
    clean = strip_zero_width(line).strip()
    if not clean:
        return None
    lower = clean.lower()
    indicators = [
        "joining or creating room",
        "entering room",
        "joining room",
        "creating room",
        "created room",
        "rejoining room",
        "re-joining room",
        "reentering room",
        "re-entering room",
        "joining instance",
        "creating instance",
        "entering instance",
    ]
    matched = any(indicator in lower for indicator in indicators)
    if not matched:
        jp_sets = [
            {"key": "ルーム", "terms": ["参加", "作成", "入室", "移動", "入場"]},
            {"key": "インスタンス", "terms": ["参加", "作成", "入室", "移動", "入場"]},
        ]
        for jp in jp_sets:
            if jp["key"] in clean:
                if any(term in clean for term in jp["terms"]):
                    matched = True
                    break
    if not matched:
        if re.search(r"(?i)wrld_[0-9a-f\-]+", clean):
            if "room" in lower or "instance" in lower or "インスタンス" in clean or "ルーム" in clean:
                matched = True
    if not matched:
        return None

    world_id = ""
    instance_id = ""
    world_match = re.search(r"(?i)wrld_[0-9a-f\-]+", clean)
    if world_match:
        world_id = world_match.group(0)
        after_world = clean[world_match.end() :]
        after_world = after_world.lstrip(": \t-")
        if after_world:
            inst_match = re.match(r"[^\s,]+", after_world)
            if inst_match:
                instance_id = inst_match.group(0)
    if not instance_id:
        inst_alt = re.search(r"(?i)instance\s*[:=]\s*([^\s,]+)", clean)
        if inst_alt:
            instance_id = inst_alt.group(1)

    return {"world": world_id, "instance": instance_id, "raw_line": clean}


def is_vrchat_running() -> bool:
    patterns = ["VRChat.exe", "VRChat"]
    for pattern in patterns:
        try:
            result = subprocess.run(
                ["pgrep", "-f", pattern],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                check=False,
            )
            if result.returncode == 0:
                return True
        except FileNotFoundError:
            break
        except Exception:
            pass
    return False


def score_log_file(path: str) -> float:
    try:
        stat = os.stat(path)
    except OSError:
        return 0.0
    best = max(stat.st_mtime, stat.st_ctime)
    match = re.search(
        r"output_log_(\d{4})-(\d{2})-(\d{2})_(\d{2})-(\d{2})-(\d{2})\.txt$",
        os.path.basename(path),
        re.IGNORECASE,
    )
    if match:
        try:
            dt = datetime(
                int(match.group(1)),
                int(match.group(2)),
                int(match.group(3)),
                int(match.group(4)),
                int(match.group(5)),
                int(match.group(6)),
            )
            best = max(best, dt.timestamp())
        except ValueError:
            pass
    return best


def get_newest_log_path(log_dir: str) -> Optional[str]:
    if not log_dir or not os.path.isdir(log_dir):
        return None
    candidates = []
    try:
        for entry in os.scandir(log_dir):
            if not entry.is_file():
                continue
            name = entry.name.lower()
            if name == "player.log" or name.startswith("output_log_"):
                candidates.append(entry.path)
    except OSError:
        return None
    if not candidates:
        return None
    candidates.sort(key=score_log_file, reverse=True)
    return candidates[0]


class DesktopNotifier:
    def __init__(self, logger: AppLogger) -> None:
        self._logger = logger
        self._notify_send = shutil_which("notify-send")

    def send(self, title: str, message: str) -> None:
        if self._notify_send:
            try:
                subprocess.run(
                    [
                        self._notify_send,
                        "--app-name",
                        APP_NAME,
                        "--icon",
                        "dialog-information",
                        title,
                        message,
                    ],
                    check=False,
                )
                return
            except Exception as exc:
                self._logger.log(f"notify-send failed: {exc}")
        self._logger.log(f"Notification: {title} - {message}")


def shutil_which(executable: str) -> Optional[str]:
    for directory in os.environ.get("PATH", "").split(os.pathsep):
        candidate = os.path.join(directory.strip(), executable)
        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate
    return None


class PushoverClient:
    def __init__(self, config: AppConfig, logger: AppLogger) -> None:
        self._config = config
        self._logger = logger

    def send(self, title: str, message: str) -> None:
        token = (self._config.pushover_token or "").strip()
        user = (self._config.pushover_user or "").strip()
        if not token or not user or urllib is None:
            if not token or not user:
                self._logger.log("Pushover not configured; skipping.")
            return
        try:
            data = urllib.parse.urlencode(
                {
                    "token": token,
                    "user": user,
                    "title": title,
                    "message": message,
                    "priority": "0",
                }
            ).encode("utf-8")
            request = urllib.request.Request(PO_URL, data=data)
            with urllib.request.urlopen(request, timeout=20) as response:
                body = response.read()
            try:
                payload = json.loads(body.decode("utf-8"))
                status = payload.get("status", "?")
                self._logger.log(f"Pushover sent: {status}")
            except Exception:
                self._logger.log("Pushover sent; response parsing failed.")
        except Exception as exc:
            self._logger.log(f"Pushover error: {exc}")


class TrayIconController:
    def __init__(self, app: "AppController", logger: AppLogger) -> None:
        self.app = app
        self.logger = logger
        self.available = bool(pystray and Image and ImageDraw)
        self.icon: Optional["pystray.Icon"] = None
        self._icon_idle: Optional["Image.Image"] = None
        self._icon_active: Optional["Image.Image"] = None
        self._base_icon: Optional["Image.Image"] = None
        self._thread: Optional[threading.Thread] = None
        self._ready = threading.Event()
        self._active = False
        self._tooltip = APP_NAME
        self.disabled_reason: Optional[str] = None
        if not self.available:
            self.disabled_reason = "install 'pystray' and 'Pillow' to enable it."

    def start(self) -> None:
        if not self.available:
            reason = self.disabled_reason or "install 'pystray' and 'Pillow' to enable it."
            self.disabled_reason = reason
            self.logger.log(f"Tray icon disabled: {reason}")
            return
        if self.icon is not None:
            return
        if sys.platform.startswith("linux"):
            reason = self._probe_linux_tray_support()
            if reason:
                self.available = False
                self.disabled_reason = reason
                self.logger.log(f"Tray icon disabled: {reason}")
                return
        self._icon_idle = self._create_icon(False)
        self._icon_active = self._create_icon(True)
        menu = pystray.Menu(
            pystray.MenuItem("Open Settings", self._menu_open, default=True),
            pystray.MenuItem("Start Monitoring", self._menu_start),
            pystray.MenuItem("Stop Monitoring", self._menu_stop),
            pystray.MenuItem("Quit", self._menu_quit),
        )
        self.icon = pystray.Icon(
            "vrchat-join-notifier",
            self._icon_idle,
            APP_NAME,
            menu,
        )
        self._thread = threading.Thread(target=self._run, daemon=True)
        self._thread.start()

    def stop(self) -> None:
        if self.icon is not None and self._ready.is_set():
            try:
                self.icon.stop()
            except Exception:
                pass
        if self._thread and self._thread.is_alive():
            self._thread.join(timeout=2.0)
        self.icon = None
        self._thread = None
        self._ready.clear()

    def update_state(self, monitoring: bool, tooltip: str) -> None:
        if not self.available:
            return
        self._active = monitoring
        self._tooltip = tooltip
        self._apply_state()

    def _run(self) -> None:
        if self.icon is None:
            return
        icon = self.icon
        try:
            icon.run(self._on_setup)
        except Exception as exc:
            error_text = str(exc).strip() or exc.__class__.__name__
            reason = f"failed to initialise system tray ({error_text})."
            self.available = False
            self.disabled_reason = reason
            self.logger.log(f"Tray icon disabled: {reason}")
        finally:
            self._on_teardown(icon)
            self._thread = None

    def _on_setup(self, icon: "pystray.Icon") -> None:
        self._ready.set()
        icon.visible = True
        self._apply_state()

    def _on_teardown(self, icon: "pystray.Icon") -> None:
        self._ready.clear()
        if not self.available:
            self.icon = None

    def _apply_state(self) -> None:
        if not self.available or self.icon is None or not self._ready.is_set():
            return
        if self._active and self._icon_active is not None:
            self.icon.icon = self._icon_active
        elif self._icon_idle is not None:
            self.icon.icon = self._icon_idle
        self.icon.title = self._tooltip

    def _menu_open(self, icon: "pystray.Icon", item: "pystray.MenuItem") -> None:
        self.app.root.after(0, self.app.show_window)

    def _menu_start(self, icon: "pystray.Icon", item: "pystray.MenuItem") -> None:
        self.app.root.after(0, self.app.start_monitoring)

    def _menu_stop(self, icon: "pystray.Icon", item: "pystray.MenuItem") -> None:
        self.app.root.after(0, self.app.stop_monitoring)

    def _menu_quit(self, icon: "pystray.Icon", item: "pystray.MenuItem") -> None:
        self.app.root.after(0, self.app.request_quit)

    def _create_icon(self, active: bool) -> Optional["Image.Image"]:
        if not self.available:
            return None
        base = self._load_base_icon()
        if base is None:
            size = 64
            base = Image.new("RGBA", (size, size), (0, 0, 0, 0))
            draw = ImageDraw.Draw(base)
            draw.ellipse((4, 4, size - 4, size - 4), fill=(46, 134, 222, 255))
            draw.ellipse((16, 16, size - 16, size - 16), fill=(255, 255, 255, 80))
        image = base.copy()
        if active:
            overlay = Image.new("RGBA", image.size, (0, 180, 0, 90))
        else:
            overlay = Image.new("RGBA", image.size, (0, 0, 0, 110))
        image = Image.alpha_composite(image, overlay)
        return image

    def _probe_linux_tray_support(self) -> Optional[str]:
        if not pystray:
            return "install 'pystray' and 'Pillow' to enable it."
        display = os.environ.get("DISPLAY")
        wayland_display = os.environ.get("WAYLAND_DISPLAY")
        if not display and not wayland_display:
            return "no graphical display detected (DISPLAY/WAYLAND_DISPLAY not set)."
        backend_module = getattr(pystray.Icon, "__module__", "")
        if backend_module.endswith("._xorg"):
            if not display:
                return "X11 system tray backend selected but DISPLAY is not set."
            try:
                from Xlib import X, display as xdisplay  # type: ignore[import-not-found]
            except Exception as exc:  # pragma: no cover - optional dependency guard
                return f"unable to access X11 libraries: {exc}"
            try:
                connection = xdisplay.Display(display)
            except Exception as exc:
                return f"failed to connect to X11 display '{display}': {exc}"
            try:
                screen_index = connection.get_default_screen()
                atom_name = f"_NET_SYSTEM_TRAY_S{screen_index}"
                selection_atom = connection.intern_atom(atom_name)
                owner = connection.get_selection_owner(selection_atom)
            except Exception as exc:
                return f"failed to query X11 system tray availability: {exc}"
            finally:
                try:
                    connection.close()
                except Exception:
                    pass
            owner_id = getattr(owner, "id", None)
            if not owner_id or owner_id == getattr(X, "None", 0):
                return "no system tray manager is running."
        return None

    def _load_base_icon(self) -> Optional["Image.Image"]:
        if not self.available:
            return None
        if self._base_icon is not None:
            return self._base_icon
        opener = None
        identifier = ICON_FILE_NAME
        try:
            icon_resource = resources.files(__package__).joinpath(ICON_FILE_NAME)
        except AttributeError:  # pragma: no cover - fallback for older Python
            icon_resource = None
        if icon_resource is not None:
            opener = icon_resource.open
            identifier = str(icon_resource)
        else:  # pragma: no cover - fallback for Python < 3.9
            opener = lambda mode="rb": resources.open_binary(__package__, ICON_FILE_NAME)
        try:
            with opener("rb") as handle:  # type: ignore[misc]
                with Image.open(handle) as image_handle:
                    self._base_icon = image_handle.convert("RGBA").resize((64, 64))
        except FileNotFoundError:
            self.logger.log(f"Tray icon resource missing: '{identifier}'")
            self._base_icon = None
        except Exception as exc:
            self.logger.log(f"Failed to load tray icon '{identifier}': {exc}")
            self._base_icon = None
        return self._base_icon

class SessionTracker:
    def __init__(
        self,
        notifier: DesktopNotifier,
        pushover: PushoverClient,
        logger: AppLogger,
    ) -> None:
        self.notifier = notifier
        self.pushover = pushover
        self.logger = logger
        self.session_id = 0
        self.ready = False
        self.source = ""
        self.seen_players: Dict[str, datetime] = {}
        self.pending_room: Optional[Dict[str, str]] = None
        self.session_started_at: Optional[datetime] = None
        self.session_last_join_at: Optional[datetime] = None
        self.session_last_join_raw: Optional[str] = None
        self.last_notified: Dict[str, datetime] = {}
        self.local_user_id: Optional[str] = None
        self.pending_self_join: Optional[Dict[str, Any]] = None

    def reset_session_state(self) -> None:
        self.ready = False
        self.source = ""
        self.seen_players = {}
        self.session_started_at = None
        self.session_last_join_at = None
        self.session_last_join_raw = None
        self.pending_room = None
        self.local_user_id = None
        self.pending_self_join = None

    def ensure_session_ready(self, reason: str) -> bool:
        if self.ready:
            return False
        if not reason.strip():
            reason = "unknown trigger"
        self.session_id += 1
        self.ready = True
        self.source = reason
        self.seen_players = {}
        now = datetime.utcnow()
        self.session_started_at = now
        self.session_last_join_at = None
        self.session_last_join_raw = None
        room_desc = None
        if self.pending_room:
            world = self.pending_room.get("world", "").strip()
            instance = self.pending_room.get("instance", "").strip()
            if world:
                room_desc = world
                if instance:
                    room_desc += f":{instance}"
        message = f"Session {self.session_id} started ({reason})"
        if room_desc:
            message += f" [{room_desc}]"
        message += "."
        self.logger.log(message)
        return True

    def notify_all(self, key: str, title: str, message: str) -> None:
        now = datetime.utcnow()
        previous = self.last_notified.get(key)
        if previous and (now - previous) < timedelta(seconds=NOTIFY_COOLDOWN_SECONDS):
            self.logger.log(f"Suppressed '{key}' within cooldown.")
            return
        self.last_notified[key] = now
        self.notifier.send(title, message)
        self.pushover.send(title, message)

    def handle_log_switch(self, path: str) -> None:
        self.logger.log(f"Switching to newest log: {path}")
        self.reset_session_state()

    def handle_room_enter(self, world: str, instance: str, raw_line: str) -> None:
        self.pending_room = {"world": world or "", "instance": instance or "", "raw": raw_line or ""}
        if world:
            desc = world
            if instance:
                desc += f":{instance}"
            self.logger.log(f"Room transition detected: {desc}")
        elif raw_line:
            self.logger.log(f"Room transition detected: {raw_line}")
        else:
            self.logger.log("Room transition detected.")

    def handle_room_left(self) -> None:
        if self.ready:
            self.logger.log(f"Session {self.session_id} ended (OnLeftRoom detected.)")
        else:
            self.logger.log("OnLeftRoom detected.")
        self.reset_session_state()

    def handle_self_join(self, raw_line: str) -> None:
        if not is_vrchat_running():
            self.logger.log("Ignored self join while VRChat is not running.")
            return
        now = datetime.utcnow()
        reuse_fallback = False
        elapsed_since_fallback: Optional[timedelta] = None
        last_join_gap: Optional[timedelta] = None
        fallback_join_count = 0
        if self.ready and self.source == "OnPlayerJoined fallback":
            fallback_join_count = len(self.seen_players)
            if self.session_started_at:
                elapsed_since_fallback = now - self.session_started_at
            if fallback_join_count > 0:
                last_join = self.session_last_join_at
                if not last_join and self.seen_players:
                    last_join = max(self.seen_players.values())
                if last_join:
                    last_join_gap = now - last_join
            within_grace = (
                elapsed_since_fallback is not None
                and elapsed_since_fallback.total_seconds() < SESSION_FALLBACK_GRACE_SECONDS
            )
            within_join_gap = False
            if within_grace:
                if fallback_join_count <= 0:
                    within_join_gap = True
                elif last_join_gap:
                    within_join_gap = last_join_gap.total_seconds() <= SESSION_FALLBACK_MAX_CONTINUATION_SECONDS
            if within_grace and within_join_gap:
                reuse_fallback = True
                self.source = "OnJoinedRoom"
                details = []
                if last_join_gap:
                    details.append(
                        f"last join gap {round(max(0.0, last_join_gap.total_seconds()), 1)}s"
                    )
                elif fallback_join_count > 0:
                    details.append("last join gap unknown")
                if fallback_join_count > 0:
                    details.append(f"tracked players {fallback_join_count}")
                detail_text = f" ({'; '.join(details)})" if details else ""
                self.logger.log(f"Session {self.session_id} confirmed by OnJoinedRoom.{detail_text}")
        if not reuse_fallback:
            details = []
            if elapsed_since_fallback:
                details.append(
                    f"after {round(max(0.0, elapsed_since_fallback.total_seconds()), 1)}s"
                )
            if fallback_join_count > 0:
                if last_join_gap:
                    details.append(
                        f"last join gap {round(max(0.0, last_join_gap.total_seconds()), 1)}s"
                    )
                else:
                    details.append("last join gap unavailable")
                details.append(f"tracked players {fallback_join_count}")
            if self.ready and self.source == "OnPlayerJoined fallback":
                detail_text = f" ({'; '.join(details)})" if details else ""
                self.logger.log(
                    f"Session {self.session_id} fallback expired{detail_text}; starting new session for OnJoinedRoom."
                )
            pending = self.pending_room
            self.reset_session_state()
            if pending:
                self.pending_room = pending
            self.ensure_session_ready("OnJoinedRoom")
        parsed_name = ""
        parsed_user = ""
        parsed_placeholder = ""
        if raw_line:
            parsed = parse_player_event_line(raw_line, "OnJoinedRoom")
            if parsed:
                parsed_name = normalize_join_name(parsed.get("name", ""))
                parsed_user = (parsed.get("user_id") or "").strip()
                parsed_placeholder = normalize_join_name(parsed.get("placeholder", ""))
        if parsed_user:
            lower_user = parsed_user.lower()
            if not self.local_user_id or self.local_user_id != lower_user:
                self.local_user_id = lower_user
                self.logger.log(
                    f"Learned local userId from OnJoinedRoom event: {parsed_user}"
                )
        display_name = parsed_name or parsed_user or "You"
        placeholder_label = parsed_placeholder or "Player"
        if placeholder_label.lower() == "you":
            placeholder_label = "Player"
        if display_name.lower() == "you" and parsed_name:
            display_name = parsed_name
        message_base = display_name or placeholder_label or "You"
        if placeholder_label:
            if not message_base:
                message_base = placeholder_label
            else:
                message_base = f"{message_base}({placeholder_label})"
        message = f"{message_base} joined your instance."
        key = f"self:{self.session_id}"
        self.notify_all(key, APP_NAME, message)
        self.pending_self_join = {
            "session_id": self.session_id,
            "placeholder": placeholder_label,
            "timestamp": now,
        }

    def handle_player_join(
        self, name: str, user_id: str, raw_line: str, placeholder: str
    ) -> None:
        if not is_vrchat_running():
            self.logger.log("Ignored player join while VRChat is not running.")
            return
        if not self.ready:
            self.ensure_session_ready("OnPlayerJoined fallback")
        if not self.ready:
            return
        event_time = datetime.utcnow()
        self.session_last_join_at = event_time
        self.session_last_join_raw = raw_line
        cleaned_name = normalize_join_name(name)
        original_name = cleaned_name
        cleaned_placeholder = normalize_join_name(placeholder)
        cleaned_user = user_id.strip()
        user_key = cleaned_user.lower() if cleaned_user else ""
        if user_key and self.local_user_id and user_key == self.local_user_id:
            self.logger.log(f"Skipping join for known local userId '{cleaned_user}'.")
            self.pending_self_join = None
            return
        pending_self = self.pending_self_join
        if pending_self and pending_self.get("session_id") == self.session_id:
            pending_placeholder = normalize_join_name(
                str(pending_self.get("placeholder", ""))
            )
            pending_lower = (pending_placeholder or "").lower()
            event_placeholder_lower = (cleaned_placeholder or "").lower()
            if not event_placeholder_lower and is_placeholder_name(original_name):
                event_placeholder_lower = original_name.lower()
            timestamp = pending_self.get("timestamp")
            age_ok = False
            if isinstance(timestamp, datetime):
                age_ok = (event_time - timestamp).total_seconds() < 10
            if (
                age_ok
                and pending_lower in {"player", "you"}
                and (
                    pending_lower == event_placeholder_lower
                    or (
                        not event_placeholder_lower
                        and is_placeholder_name(original_name)
                    )
                )
            ):
                if user_key and not self.local_user_id:
                    self.local_user_id = user_key
                self.pending_self_join = None
                self.logger.log("Skipping join matched pending self event.")
                return
        was_placeholder = is_placeholder_name(cleaned_name)
        is_fallback_session = self.source == "OnPlayerJoined fallback"
        if was_placeholder and user_key:
            if is_fallback_session and not self.local_user_id:
                self.local_user_id = user_key
                self.logger.log(f"Learned local userId from join event: {cleaned_user}")
                self.logger.log(
                    f"Skipping initial local join placeholder for userId '{cleaned_user}'."
                )
                self.pending_self_join = None
                return
            if self.local_user_id and self.local_user_id == user_key:
                self.logger.log(
                    f"Skipping local join placeholder for userId '{cleaned_user}'."
                )
                self.pending_self_join = None
                return
        if not cleaned_name and cleaned_user:
            cleaned_name = cleaned_user
            was_placeholder = False
        elif was_placeholder and cleaned_user:
            cleaned_name = cleaned_user
            was_placeholder = False
        if not cleaned_name:
            cleaned_name = "Unknown VRChat user"
        key_base = user_key or cleaned_name.lower()
        hash_suffix = ""
        if not cleaned_user and raw_line:
            hash_suffix = get_short_hash(raw_line)
        join_key = f"join:{self.session_id}:{key_base}"
        if hash_suffix:
            join_key += f":{hash_suffix}"
        if join_key in self.seen_players:
            return
        self.seen_players[join_key] = event_time
        placeholder_for_message = cleaned_placeholder
        if not placeholder_for_message and was_placeholder:
            placeholder_for_message = original_name
        if not placeholder_for_message:
            placeholder_for_message = "Someone"
        elif placeholder_for_message.lower() == "you":
            placeholder_for_message = "Player"
        message_name = cleaned_name or placeholder_for_message or "Unknown VRChat user"
        if cleaned_name:
            message_name = f"{cleaned_name}({placeholder_for_message})"
        else:
            message_name = placeholder_for_message
        message = f"{message_name} joined your instance."
        self.notify_all(join_key, APP_NAME, message)
        log_line = f"Session {self.session_id}: player joined '{cleaned_name}'"
        if cleaned_user:
            log_line += f" ({cleaned_user})"
        log_line += "."
        self.logger.log(log_line)

    def handle_player_left(self, name: str, user_id: str, raw_line: str) -> None:
        cleaned_name = normalize_join_name(name)
        cleaned_user = user_id.strip()
        user_key = cleaned_user.lower() if cleaned_user else ""
        is_placeholder = is_placeholder_name(cleaned_name)
        if is_placeholder and user_key:
            if not self.local_user_id:
                self.local_user_id = user_key
                self.logger.log(f"Learned local userId from leave event: {cleaned_user}")
            elif self.local_user_id == user_key:
                cleaned_name = cleaned_user
                is_placeholder = False
        if not cleaned_name and cleaned_user:
            cleaned_name = cleaned_user
            is_placeholder = False
        elif is_placeholder and cleaned_user:
            cleaned_name = cleaned_user
            is_placeholder = False
        if not cleaned_name:
            cleaned_name = "Unknown VRChat user"
        removed_count = 0
        if user_key:
            prefix = f"join:{self.session_id}:{user_key}"
            keys_to_remove = [key for key in self.seen_players if key.startswith(prefix)]
            for key in keys_to_remove:
                self.seen_players.pop(key, None)
                removed_count += 1
        log_line = f"Session {self.session_id}: player left '{cleaned_name}'"
        if cleaned_user:
            log_line += f" ({cleaned_user})"
        if removed_count:
            log_line += " [cleared join tracking]"
        log_line += "."
        self.logger.log(log_line)


class LogMonitor(threading.Thread):
    def __init__(self, config: AppConfig, event_queue: "queue.Queue[Tuple]", logger: AppLogger) -> None:
        super().__init__(daemon=True)
        self._config = config
        self._queue = event_queue
        self._logger = logger
        self._stop_event = threading.Event()
        self._re_self = re.compile(r"(?i)\[Behaviour\].*OnJoinedRoom\b")
        self._re_join = re.compile(r"(?i)\[Behaviour\].*OnPlayerJoined\b")
        self._re_leave = re.compile(r"(?i)\[Behaviour\].*OnPlayerLeft\b")

    def stop(self) -> None:
        self._stop_event.set()

    def run(self) -> None:  # pragma: no cover - requires log files
        last_dir_warning = 0.0
        last_no_file_warning = 0.0
        while not self._stop_event.is_set():
            log_dir = self._config.vrchat_log_dir
            if not log_dir or not os.path.isdir(log_dir):
                if time.time() - last_dir_warning > 10:
                    self._queue.put(("status", f"Waiting for VRChat log directory at {log_dir or '(unset)'}"))
                    last_dir_warning = time.time()
                if self._stop_event.wait(1.0):
                    break
                continue
            newest = get_newest_log_path(log_dir)
            if not newest:
                if time.time() - last_no_file_warning > 10:
                    self._queue.put(("status", f"No log files found in {log_dir}"))
                    last_no_file_warning = time.time()
                if self._stop_event.wait(1.0):
                    break
                continue
            self._follow_file(newest, log_dir)

    def _follow_file(self, path: str, log_dir: str) -> None:
        normalized = os.path.abspath(path)
        self._queue.put(("log_switch", normalized))
        try:
            last_size = os.path.getsize(normalized)
        except OSError:
            last_size = 0
        while not self._stop_event.is_set():
            try:
                with open(normalized, "r", encoding="utf-8", errors="ignore") as handle:
                    handle.seek(last_size)
                    while not self._stop_event.is_set():
                        position = handle.tell()
                        line = handle.readline()
                        if line:
                            last_size = handle.tell()
                            self._process_line(line.rstrip())
                            continue
                        if self._stop_event.wait(0.6):
                            return
                        try:
                            current_size = os.path.getsize(normalized)
                        except OSError:
                            time.sleep(0.6)
                            break
                        if current_size < last_size:
                            handle.seek(0)
                            last_size = 0
                            continue
                        newest = get_newest_log_path(log_dir)
                        if newest and os.path.abspath(newest) != normalized:
                            return
                        handle.seek(position)
            except (OSError, UnicodeDecodeError) as exc:
                self._logger.log(f"Failed reading log '{normalized}': {exc}")
                self._queue.put(("error", f"Log read error: {exc}"))
                if self._stop_event.wait(2.0):
                    return

    def _process_line(self, line: str) -> None:
        if not line:
            return
        safe_line = strip_zero_width(line).replace("||", "|")
        lower_line = safe_line.lower()
        if "onleftroom" in lower_line:
            self._queue.put(("room_left", safe_line))
            return
        room_event = parse_room_transition_line(safe_line)
        if room_event:
            self._queue.put(("room_enter", room_event))
            return
        if self._re_self.search(safe_line):
            self._queue.put(("self_join", safe_line))
            return
        if self._re_leave.search(safe_line):
            parsed = parse_player_event_line(safe_line, "OnPlayerLeft") or {
                "name": "",
                "user_id": "",
                "placeholder": "",
                "raw_line": safe_line,
            }
            self._queue.put(("player_left", parsed))
            return
        if self._re_join.search(safe_line):
            parsed = parse_player_event_line(safe_line, "OnPlayerJoined") or {
                "name": "",
                "user_id": "",
                "placeholder": "",
                "raw_line": safe_line,
            }
            self._queue.put(("player_join", parsed))
            return


class AppController:
    def __init__(
        self,
        root: tk.Tk,
        config: AppConfig,
        logger: AppLogger,
        load_error: Optional[str] = None,
    ) -> None:
        self.root = root
        self.config = config
        self.logger = logger
        self._load_error = bool(load_error)
        self.notifier = DesktopNotifier(logger)
        self.pushover = PushoverClient(config, logger)
        self.session = SessionTracker(self.notifier, self.pushover, logger)
        self.event_queue: queue.Queue = queue.Queue()
        self.monitor: Optional[LogMonitor] = None
        self._quitting = False

        self.install_var = tk.StringVar(value=self.config.install_dir)
        self.log_dir_var = tk.StringVar(value=self.config.vrchat_log_dir)
        self.user_var = tk.StringVar(value=self.config.pushover_user)
        self.token_var = tk.StringVar(value=self.config.pushover_token)
        self.status_var = tk.StringVar(value="Idle")
        self.monitor_status_var = tk.StringVar(value="Stopped")
        self.current_log_var = tk.StringVar(value="(none)")
        self.session_var = tk.StringVar(value="No active session")
        self.last_event_var = tk.StringVar(value="")

        self._build_ui()
        self.tray = TrayIconController(self, logger)
        self.tray.start()
        self.root.after(200, self._process_events)
        self.apply_startup_state()
        self._update_tray_state()
        self._maybe_hide_initially()

    def apply_startup_state(self) -> None:
        if self._load_error:
            return
        if self.config.first_run:
            self.status_var.set(
                "Welcome! Configure the install folder, VRChat log folder, and optional "
                "Pushover keys, then click Save & Restart Monitoring."
            )
        elif self.config.pushover_user and self.config.pushover_token:
            self.start_monitoring()
        else:
            self.status_var.set(
                "Optional: enter your Pushover keys for push notifications, then click "
                "Save & Restart Monitoring when you're ready."
            )
        if not self.tray.available:
            reason = self.tray.disabled_reason or "install 'pystray' and 'Pillow' to enable it."
            self.status_var.set(
                f"{self.status_var.get()} Tray icon disabled: {reason}"
            )

    def _build_ui(self) -> None:
        self.root.title(f"{APP_NAME} (Linux)")
        main = ttk.Frame(self.root, padding=12)
        main.pack(fill=tk.BOTH, expand=True)

        main.columnconfigure(1, weight=1)
        main.columnconfigure(3, weight=1)
        main.rowconfigure(4, weight=1)

        ttk.Label(main, text="Install Folder (logs/cache):").grid(row=0, column=0, sticky=tk.W)
        install_entry = ttk.Entry(main, textvariable=self.install_var)
        install_entry.grid(row=0, column=1, columnspan=3, sticky=tk.EW, padx=(0, 6))
        ttk.Button(main, text="Browse…", command=self._browse_install).grid(row=0, column=4, sticky=tk.E)

        ttk.Label(main, text="VRChat Log Folder:").grid(row=1, column=0, sticky=tk.W, pady=(8, 0))
        log_entry = ttk.Entry(main, textvariable=self.log_dir_var)
        log_entry.grid(row=1, column=1, columnspan=3, sticky=tk.EW, padx=(0, 6), pady=(8, 0))
        ttk.Button(main, text="Browse…", command=self._browse_logs).grid(row=1, column=4, sticky=tk.E, pady=(8, 0))

        ttk.Label(main, text="Pushover User Key:").grid(row=2, column=0, sticky=tk.W, pady=(12, 0))
        user_entry = ttk.Entry(main, textvariable=self.user_var, show="*")
        user_entry.grid(row=2, column=1, sticky=tk.EW, padx=(0, 6), pady=(12, 0))

        ttk.Label(main, text="Pushover API Token:").grid(row=2, column=2, sticky=tk.W, pady=(12, 0))
        token_entry = ttk.Entry(main, textvariable=self.token_var, show="*")
        token_entry.grid(row=2, column=3, sticky=tk.EW, padx=(0, 6), pady=(12, 0))

        button_frame = ttk.Frame(main)
        button_frame.grid(row=3, column=0, columnspan=5, sticky=tk.EW, pady=(16, 0))
        for column in range(4):
            button_frame.columnconfigure(column, weight=1)

        self.save_restart_button = ttk.Button(
            button_frame,
            text="Save & Restart Monitoring",
            command=self.save_and_restart,
        )
        self.save_restart_button.grid(row=0, column=0, padx=4, sticky=tk.EW)

        self.start_button = ttk.Button(
            button_frame,
            text="Start Monitoring",
            command=self.start_monitoring,
        )
        self.start_button.grid(row=0, column=1, padx=4, sticky=tk.EW)

        self.stop_button = ttk.Button(
            button_frame,
            text="Stop Monitoring",
            command=self.stop_monitoring,
        )
        self.stop_button.grid(row=0, column=2, padx=4, sticky=tk.EW)

        self.add_startup_button = ttk.Button(
            button_frame,
            text="Add to Startup",
            command=self.add_to_startup,
        )
        self.add_startup_button.grid(row=1, column=0, padx=4, pady=(8, 0), sticky=tk.EW)

        self.remove_startup_button = ttk.Button(
            button_frame,
            text="Remove from Startup",
            command=self.remove_from_startup,
        )
        self.remove_startup_button.grid(row=1, column=1, padx=4, pady=(8, 0), sticky=tk.EW)

        self.save_button = ttk.Button(
            button_frame,
            text="Save",
            command=self.save_only,
        )
        self.save_button.grid(row=1, column=2, padx=4, pady=(8, 0), sticky=tk.EW)

        self.quit_button = ttk.Button(
            button_frame,
            text="Quit",
            command=self.request_quit,
        )
        self.quit_button.grid(row=1, column=3, padx=4, pady=(8, 0), sticky=tk.EW)

        button_frame.grid_rowconfigure(1, weight=1)

        status_frame = ttk.Frame(main, padding=(0, 12, 0, 0))
        status_frame.grid(row=4, column=0, columnspan=5, sticky=tk.NSEW)
        status_frame.columnconfigure(1, weight=1)

        ttk.Label(status_frame, text="Monitor:").grid(row=0, column=0, sticky=tk.W)
        ttk.Label(status_frame, textvariable=self.monitor_status_var).grid(row=0, column=1, sticky=tk.W)

        ttk.Label(status_frame, text="Current log:").grid(row=1, column=0, sticky=tk.W, pady=(4, 0))
        ttk.Label(status_frame, textvariable=self.current_log_var).grid(row=1, column=1, sticky=tk.W, pady=(4, 0))

        ttk.Label(status_frame, text="Session:").grid(row=2, column=0, sticky=tk.W, pady=(4, 0))
        ttk.Label(status_frame, textvariable=self.session_var).grid(row=2, column=1, sticky=tk.W, pady=(4, 0))

        ttk.Label(status_frame, text="Last event:").grid(row=3, column=0, sticky=tk.W, pady=(4, 0))
        last_event_label = ttk.Label(status_frame, textvariable=self.last_event_var)
        last_event_label.grid(row=3, column=1, sticky=tk.W, pady=(4, 0))

        ttk.Label(status_frame, text="Status:").grid(row=4, column=0, sticky=tk.W, pady=(4, 0))
        status_value_label = ttk.Label(status_frame, textvariable=self.status_var)
        status_value_label.grid(row=4, column=1, sticky=tk.W, pady=(4, 0))

        def _sync_wrap_lengths(_: Optional[tk.Event] = None) -> None:
            try:
                rows = status_frame.grid_size()[1]
                if rows <= 0:
                    return
                _, _, col_width, _ = status_frame.grid_bbox(1, 0, 1, rows - 1)
            except tk.TclError:
                return
            wrap = max(col_width - 16, 160)
            for widget in (last_event_label, status_value_label):
                widget.configure(wraplength=wrap)

        status_frame.bind("<Configure>", _sync_wrap_lengths)
        _sync_wrap_lengths(None)

        self.root.protocol("WM_DELETE_WINDOW", self.on_close)
        self.root.bind("<Unmap>", self._on_unmap)
        self._update_startup_buttons()

        self.root.update_idletasks()
        required_width = max(720, self.root.winfo_reqwidth())
        required_height = self.root.winfo_reqheight()
        self.root.minsize(required_width, required_height)
        self.root.geometry(f"{required_width}x{required_height}")

    def _browse_install(self) -> None:
        directory = filedialog.askdirectory(initialdir=self.install_var.get() or os.getcwd())
        if directory:
            self.install_var.set(directory)

    def _browse_logs(self) -> None:
        directory = filedialog.askdirectory(initialdir=self.log_dir_var.get() or os.getcwd())
        if directory:
            self.log_dir_var.set(directory)

    def save_and_restart(self) -> None:
        self._save_config()
        self.start_monitoring()
        self.status_var.set("Settings saved & monitoring restarted.")

    def save_only(self) -> None:
        self._save_config()
        if self.config.pushover_user and self.config.pushover_token:
            self.notifier.send(APP_NAME, "Settings saved.")
        self.status_var.set("Settings saved.")

    def _save_config(self) -> None:
        self.config.install_dir = _expand_path(self.install_var.get())
        self.config.vrchat_log_dir = _expand_path(self.log_dir_var.get())
        self.config.pushover_user = self.user_var.get().strip()
        self.config.pushover_token = self.token_var.get().strip()
        try:
            self.config.save()
            self.logger.log("Settings saved.")
        except Exception as exc:
            messagebox.showerror(APP_NAME, f"Failed to save settings:\n{exc}")
            self.logger.log(f"Failed to save settings: {exc}")

    def start_monitoring(self) -> None:
        self.stop_monitoring()
        self.config.vrchat_log_dir = _expand_path(self.log_dir_var.get())
        self.config.install_dir = _expand_path(self.install_var.get())
        self.config.ensure_install_dir()
        self.monitor = LogMonitor(self.config, self.event_queue, self.logger)
        self.monitor.start()
        self.monitor_status_var.set("Running")
        self.status_var.set("Monitoring VRChat logs…")
        self.logger.log("Monitoring started.")
        self._update_tray_state()

    def stop_monitoring(self) -> None:
        if self.monitor:
            self.monitor.stop()
            self.monitor.join(timeout=2.0)
            self.monitor = None
            self.logger.log("Monitoring stopped.")
        self.monitor_status_var.set("Stopped")
        self.status_var.set("Monitoring stopped.")
        self._update_tray_state()

    def add_to_startup(self) -> None:
        entry_path = self._autostart_entry_path()
        try:
            command, workdir = self._launcher_command()
            if not command:
                raise RuntimeError("Unable to determine launch command.")
            os.makedirs(os.path.dirname(entry_path), exist_ok=True)
            lines = [
                "[Desktop Entry]",
                "Type=Application",
                "Version=1.0",
                f"Name={APP_NAME}",
                "Comment=Monitor VRChat joins and send notifications.",
                f"Exec={command}",
                "Terminal=false",
                "X-GNOME-Autostart-enabled=true",
            ]
            if workdir:
                lines.append(f"Path={workdir}")
            with open(entry_path, "w", encoding="utf-8") as handle:
                handle.write("\n".join(lines) + "\n")
        except Exception as exc:
            messagebox.showerror(APP_NAME, f"Failed to add to startup:\n{exc}")
            self.logger.log(f"Failed to add startup entry: {exc}")
            self.notifier.send(APP_NAME, f"Failed to add to startup: {exc}")
        else:
            self.status_var.set("Added to startup.")
            self.logger.log(f"Startup entry created at {entry_path}")
            self.notifier.send(APP_NAME, "Added to startup.")
        finally:
            self._update_startup_buttons()

    def remove_from_startup(self) -> None:
        entry_path = self._autostart_entry_path()
        try:
            if os.path.exists(entry_path):
                os.remove(entry_path)
        except Exception as exc:
            messagebox.showerror(APP_NAME, f"Failed to remove from startup:\n{exc}")
            self.logger.log(f"Failed to remove startup entry: {exc}")
            self.notifier.send(APP_NAME, f"Failed to remove from startup: {exc}")
        else:
            self.status_var.set("Removed from startup.")
            self.logger.log(f"Startup entry removed from {entry_path}")
            self.notifier.send(APP_NAME, "Removed from startup.")
        finally:
            self._update_startup_buttons()

    def _process_events(self) -> None:
        try:
            while True:
                event = self.event_queue.get_nowait()
                self._handle_event(event)
        except queue.Empty:
            pass
        self.root.after(200, self._process_events)

    def _handle_event(self, event: Tuple) -> None:
        etype = event[0]
        if etype == "status":
            self.status_var.set(str(event[1]))
        elif etype == "error":
            self.status_var.set(str(event[1]))
        elif etype == "log_switch":
            path = str(event[1])
            self.current_log_var.set(path)
            self.session.handle_log_switch(path)
            self.session_var.set(self._session_description())
            self.last_event_var.set("Switched to new log file.")
        elif etype == "room_enter":
            info = event[1]
            world = info.get("world", "")
            instance = info.get("instance", "")
            raw = info.get("raw_line", "")
            self.session.handle_room_enter(world, instance, raw)
            desc = world or raw or "(unknown room)"
            if world and instance:
                desc = f"{world}:{instance}"
            self.last_event_var.set(f"Room transition detected: {desc}")
        elif etype == "room_left":
            self.session.handle_room_left()
            self.session_var.set(self._session_description())
            self.last_event_var.set("Left current room.")
        elif etype == "self_join":
            self.session.handle_self_join(str(event[1]))
            self.session_var.set(self._session_description())
            self.last_event_var.set("OnJoinedRoom detected.")
        elif etype == "player_join":
            info = event[1]
            self.session.handle_player_join(
                info.get("name", ""),
                info.get("user_id", ""),
                info.get("raw_line", ""),
                info.get("placeholder", ""),
            )
            self.session_var.set(self._session_description())
            display = info.get("name") or info.get("user_id") or "Unknown VRChat user"
            self.last_event_var.set(f"Player joined: {display}")
        elif etype == "player_left":
            info = event[1]
            self.session.handle_player_left(
                info.get("name", ""), info.get("user_id", ""), info.get("raw_line", "")
            )
            self.session_var.set(self._session_description())
            display = info.get("name") or info.get("user_id") or "Unknown VRChat user"
            self.last_event_var.set(f"Player left: {display}")
        self.status_var.set(self.status_var.get())
        self._update_tray_state()

    def _session_description(self) -> str:
        if self.session.ready:
            source = self.session.source or "unknown"
            return f"Session {self.session.session_id} – {source}"
        return "No active session"

    def on_close(self) -> None:
        if self._quitting:
            self.root.destroy()
            return
        self.hide_window()
        self.status_var.set(
            "Settings window hidden. Use the tray icon to reopen or quit the notifier."
        )

    def _on_unmap(self, event: tk.Event) -> None:
        if self._quitting:
            return
        if event.widget is self.root and self.root.state() == "iconic":
            self.hide_window()

    def show_window(self) -> None:
        if self._quitting:
            return
        self.root.deiconify()
        try:
            self.root.state("normal")
        except tk.TclError:
            pass
        self.root.lift()
        self.root.focus_force()
        try:
            self.root.attributes("-topmost", True)
            self.root.after(100, lambda: self.root.attributes("-topmost", False))
        except tk.TclError:
            pass

    def hide_window(self) -> None:
        if self._quitting:
            return
        self.root.withdraw()

    def request_quit(self) -> None:
        if self._quitting:
            return
        self._quitting = True
        self.stop_monitoring()
        if hasattr(self, "tray") and self.tray is not None:
            self.tray.stop()
        self.root.after(0, self.root.destroy)

    def _update_tray_state(self) -> None:
        monitoring = self.monitor is not None
        tooltip = f"{APP_NAME} – {'Monitoring' if monitoring else 'Stopped'}"
        session_desc = self._session_description()
        if self.session.ready:
            tooltip += f"\n{session_desc}"
        if hasattr(self, "tray") and self.tray is not None:
            self.tray.update_state(monitoring, tooltip)

    def _maybe_hide_initially(self) -> None:
        if self.config.first_run or self._load_error:
            return
        if not getattr(self, "tray", None) or not self.tray.available:
            return
        if len(sys.argv) > 1:
            return
        try:
            if sys.stdin is not None and sys.stdin.isatty():
                return
        except Exception:
            pass
        self.root.after(400, self.hide_window)

    def _autostart_entry_path(self) -> str:
        config_home = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
        autostart_dir = _expand_path(os.path.join(config_home, "autostart"))
        return os.path.join(autostart_dir, AUTOSTART_FILE_NAME)

    def _launcher_command(self) -> Tuple[str, Optional[str]]:
        args: list[str]
        workdir: Optional[str]
        if getattr(sys, "frozen", False):
            exe_path = os.path.abspath(sys.executable)
            args = [exe_path, *sys.argv[1:]]
            workdir = os.path.dirname(exe_path)
        else:
            script = os.path.abspath(sys.argv[0]) if sys.argv and sys.argv[0] else ""
            python_exe = sys.executable or "python3"
            if script and script.lower().endswith(('.py', '.pyw')) and os.path.isfile(script):
                args = [python_exe, script, *sys.argv[1:]]
                workdir = os.path.dirname(script)
            elif script:
                args = [script, *sys.argv[1:]]
                workdir = os.path.dirname(script)
            else:
                args = [python_exe, "-m", "vrchat_join_notification.app", *sys.argv[1:]]
                workdir = None
        command = " ".join(shlex.quote(arg) for arg in args if arg)
        return command, workdir

    def _autostart_entry_exists(self) -> bool:
        try:
            return os.path.exists(self._autostart_entry_path())
        except Exception:
            return False

    def _update_startup_buttons(self) -> None:
        exists = self._autostart_entry_exists()
        if hasattr(self, "add_startup_button"):
            if exists:
                self.add_startup_button.state(["disabled"])
            else:
                self.add_startup_button.state(["!disabled"])
        if hasattr(self, "remove_startup_button"):
            if exists:
                self.remove_startup_button.state(["!disabled"])
            else:
                self.remove_startup_button.state(["disabled"])


def main() -> None:
    config, load_error = AppConfig.load()
    logger = AppLogger(config)
    root = tk.Tk()
    controller = AppController(root, config, logger, load_error)
    if load_error:
        messagebox.showerror(APP_NAME, load_error)
        controller.status_var.set(load_error)
    root.mainloop()


if __name__ == "__main__":
    main()
