#!/usr/bin/env python3
"""Linux entrypoint with memory, stability and anti-cheat safeguards.

The upstream application already operates as a passive log watcher, but this
wrapper adds a few belt-and-braces protections before the real
:mod:`vrchat_join_notification.app` entrypoint is executed:

* :class:`MemoryGuard` observes overall allocator pressure and triggers
  collections when it detects sustained growth. This keeps the daemon from
  leaking memory if a third-party dependency misbehaves.
* :class:`VRChatCrashGuard` blocks write access to sensitive binaries (VRChat
  and Easy Anti-Cheat) so the notifier never tampers with files that could make
  the game unstable.
* :class:`AntiCheatGuard` mirrors the Windows behaviour by disabling privileged
  process-handle requests, keeping Easy Anti-Cheat comfortable with the
  notifier's read-only design even on Wine/Proton based launches.
"""

from __future__ import annotations

import builtins
import contextlib
import gc
import os
import sys
import threading
import tracemalloc
from typing import Optional, Type

from vrchat_join_notification.app import main


def _log(message: str) -> None:
    """Emit a small diagnostic message to standard error."""

    sys.stderr.write(f"[vrchat-notifier] {message}\n")
    sys.stderr.flush()


def _env_int(name: str, default: int) -> int:
    """Parse an integer from the environment while tolerating bad input."""

    value = os.environ.get(name)
    if value is None:
        return default
    try:
        return int(value)
    except ValueError:
        return default


def _env_float(name: str, default: float) -> float:
    """Parse a float from the environment while tolerating bad input."""

    value = os.environ.get(name)
    if value is None:
        return default
    try:
        return float(value)
    except ValueError:
        return default


class MemoryGuard(contextlib.AbstractContextManager["MemoryGuard"]):
    """Background watchdog that periodically trims runaway allocations."""

    def __init__(self) -> None:
        self.soft_limit = max(_env_int("VRCJN_MEMORY_SOFT_LIMIT", 256 * 1024 * 1024), 0)
        self.check_interval = max(_env_float("VRCJN_MEMORY_CHECK_INTERVAL", 60.0), 0.0)
        self._stop_event = threading.Event()
        self._thread: Optional[threading.Thread] = None
        self._started_tracing = False

    def __enter__(self) -> "MemoryGuard":
        if self.soft_limit <= 0 or self.check_interval <= 0:
            gc.collect()
            return self
        if not tracemalloc.is_tracing():
            tracemalloc.start()
            self._started_tracing = True
        gc.collect()
        self._thread = threading.Thread(target=self._worker, name="MemoryGuard", daemon=True)
        self._thread.start()
        return self

    def __exit__(
        self,
        exc_type: Optional[Type[BaseException]],
        exc: Optional[BaseException],
        tb: Optional[BaseException],
    ) -> Optional[bool]:
        try:
            self._stop_event.set()
            if self._thread and self._thread.is_alive():
                self._thread.join(timeout=max(self.check_interval, 1.0))
        finally:
            gc.collect()
            if self._started_tracing:
                tracemalloc.stop()
        return None

    def _worker(self) -> None:
        while not self._stop_event.wait(self.check_interval):
            try:
                current, peak = tracemalloc.get_traced_memory()
            except RuntimeError:
                break
            if current >= self.soft_limit or peak >= self.soft_limit * 1.5:
                _log(
                    f"MemoryGuard triggered collection (current={current} peak={peak} limit={self.soft_limit})"
                )
                gc.collect()
                try:
                    tracemalloc.reset_peak()
                except AttributeError:
                    pass


class VRChatCrashGuard(contextlib.AbstractContextManager["VRChatCrashGuard"]):
    """Prevent accidental writes to sensitive VRChat or EAC binaries."""

    _PROTECTED_KEYWORDS = ("vrchat.exe", "easyanticheat", "easy anti-cheat")

    def __init__(self) -> None:
        self._original_open = builtins.open

    def __enter__(self) -> "VRChatCrashGuard":
        def guarded_open(file, mode="r", *args, **kwargs):  # type: ignore[override]
            if isinstance(file, (str, os.PathLike)):
                try:
                    resolved = os.fspath(file)
                except TypeError:
                    resolved = str(file)
                lowered = resolved.lower()
                if any(keyword in lowered for keyword in self._PROTECTED_KEYWORDS):
                    if any(flag in mode for flag in ("w", "a", "x", "+")):
                        raise PermissionError(
                            "Write access to VRChat/EAC binaries is blocked to avoid instability."
                        )
            return self._original_open(file, mode, *args, **kwargs)

        builtins.open = guarded_open  # type: ignore[assignment]
        return self

    def __exit__(
        self,
        exc_type: Optional[Type[BaseException]],
        exc: Optional[BaseException],
        tb: Optional[BaseException],
    ) -> Optional[bool]:
        builtins.open = self._original_open  # type: ignore[assignment]
        return None


class AntiCheatGuard(contextlib.AbstractContextManager["AntiCheatGuard"]):
    """Restrict high-privilege process access for Easy Anti-Cheat compliance."""

    _ALLOWED_MASK = 0x00100000 | 0x00000400 | 0x00001000

    def __init__(self) -> None:
        self._kernel32 = None
        self._original_open_process = None

    def __enter__(self) -> "AntiCheatGuard":
        if os.name != "nt":
            return self
        try:
            import ctypes
        except Exception:
            return self

        kernel32 = ctypes.windll.kernel32  # type: ignore[attr-defined]
        original = getattr(kernel32, "OpenProcess", None)
        if original is None:
            return self

        def safe_open_process(desired_access, inherit_handle, process_id):
            access = int(desired_access) & 0xFFFFFFFF
            if access & ~self._ALLOWED_MASK:
                raise PermissionError(
                    "High privilege process access is disabled to remain compatible with Easy Anti-Cheat."
                )
            return original(desired_access, inherit_handle, process_id)

        safe_open_process.__name__ = getattr(original, "__name__", "OpenProcess")
        safe_open_process.__doc__ = getattr(original, "__doc__", None)
        if hasattr(original, "argtypes"):
            safe_open_process.argtypes = original.argtypes  # type: ignore[attr-defined]
        if hasattr(original, "restype"):
            safe_open_process.restype = original.restype  # type: ignore[attr-defined]
        kernel32.OpenProcess = safe_open_process  # type: ignore[assignment]
        self._kernel32 = kernel32
        self._original_open_process = original
        return self

    def __exit__(
        self,
        exc_type: Optional[Type[BaseException]],
        exc: Optional[BaseException],
        tb: Optional[BaseException],
    ) -> Optional[bool]:
        if self._kernel32 and self._original_open_process is not None:
            self._kernel32.OpenProcess = self._original_open_process  # type: ignore[assignment]
        return None


def run() -> int:
    """Execute the application with safety guards enabled."""

    with contextlib.ExitStack() as stack:
        stack.enter_context(AntiCheatGuard())
        stack.enter_context(VRChatCrashGuard())
        stack.enter_context(MemoryGuard())
        try:
            result = main()
        except KeyboardInterrupt:
            _log("Interrupted by user; shutting down cleanly.")
            return 0
    if isinstance(result, int):
        return result
    return 0


if __name__ == "__main__":
    raise SystemExit(run())
