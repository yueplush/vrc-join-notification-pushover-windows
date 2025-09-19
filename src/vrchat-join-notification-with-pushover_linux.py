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

from vrchat_join_notification.app import main
from vrchat_join_notification.entrypoint_utils import run_guarded

def run() -> int:
    """Execute the application with safety guards enabled."""

    return run_guarded(main)


if __name__ == "__main__":
    raise SystemExit(run())
