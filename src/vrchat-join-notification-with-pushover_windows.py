#!/usr/bin/env python3
"""Windows entrypoint with additional safety guards for the notifier.

This wrapper adds three layers of protection before handing control over to the
real application contained in :mod:`vrchat_join_notification.app`:

* :class:`MemoryGuard` keeps an eye on the Python heap and performs periodic
  garbage-collection passes when sustained growth is detected so that the
  long-running log watcher does not slowly leak memory.
* :class:`VRChatCrashGuard` prevents write access to the VRChat and
  EasyAntiCheat executables which could otherwise destabilise the game client.
* :class:`AntiCheatGuard` ensures that the process never requests powerful
  Windows process privileges that would trip Easy Anti-Cheat. Only
  query-level permissions remain available which is enough for our passive
  monitoring requirements.

In addition, the wrapper configures the task bar icon to match the tray icon so
that the frozen executable displays ``notification.ico`` instead of the default
Python logo.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Optional

from vrchat_join_notification.app import ensure_windows_app_user_model_id, main
from vrchat_join_notification.entrypoint_utils import run_guarded

def _locate_notification_icon() -> Optional[str]:
    """Locate ``notification.ico`` on disk for taskbar usage."""

    candidate_paths = []
    script_path = Path(__file__).resolve()
    candidate_paths.append(script_path.with_name("notification.ico"))
    candidate_paths.append(script_path.parent / "vrchat_join_notification" / "notification.ico")
    candidate_paths.append(Path.cwd() / "notification.ico")
    candidate_paths.append(Path.cwd() / "vrchat_join_notification" / "notification.ico")

    for candidate in candidate_paths:
        if candidate.exists():
            return str(candidate)

    try:
        from importlib import resources
    except Exception:
        return None

    package_name = "vrchat_join_notification"
    try:
        if hasattr(resources, "files"):
            icon = resources.files(package_name) / "notification.ico"  # type: ignore[attr-defined]
            with resources.as_file(icon) as handle:  # type: ignore[attr-defined]
                if handle.exists():
                    return os.fspath(handle)
        else:
            with resources.path(package_name, "notification.ico") as handle:  # type: ignore[attr-defined]
                return os.fspath(handle)
    except (FileNotFoundError, ModuleNotFoundError, AttributeError):
        return None
    return None


_ICON_PATCHED = False


def _apply_windows_taskbar_icon() -> None:
    """Force Tk windows to use ``notification.ico`` in the taskbar."""

    global _ICON_PATCHED
    if _ICON_PATCHED or os.name != "nt":
        return
    icon_path = _locate_notification_icon()
    if not icon_path:
        return
    ensure_windows_app_user_model_id()

    try:
        import tkinter as tk
    except Exception:
        return

    original_tk = tk.Tk

    def patched_tk(*args, **kwargs):  # type: ignore[override]
        root = original_tk(*args, **kwargs)
        try:
            root.iconbitmap(default=icon_path)
        except Exception:
            pass
        return root

    tk.Tk = patched_tk  # type: ignore[assignment]
    _ICON_PATCHED = True


def run() -> int:
    """Execute the application with safety guards enabled."""

    if os.name == "nt":
        _apply_windows_taskbar_icon()

    return run_guarded(main)


if __name__ == "__main__":
    raise SystemExit(run())
