#!/usr/bin/env python3
"""engram capture-loop hook — wrap-up detection (UserPromptSubmit) + a Stop backstop.

Nudges the model to look back over the session and capture durable new concepts,
decisions and traps into the brain. Ships with the plugin, so nothing has to be
set up per machine.

**The hooks are not the engine.** Three layers, and only the first one does the
judging:

  - Engine (primary): the model saves things as they crystallize, during the
    work. See SKILL.md, "Capture loop". A shell hook cannot judge what is worth
    keeping; it can only fire the reflection at the right moment.
  - Primary trigger — UserPromptSubmit: when the user's message looks like the
    end of a session ("wrap up", "thanks, that's it", …), inject a
    reflect-and-save instruction. It fires at the natural closing moment, before
    the last ideas are gone.
  - Backstop — Stop: a time-throttled nudge for long sessions that never got a
    sign-off. Stop fires every turn, so it is heavily gated (loop guard plus a
    cooldown).

Scope: repos with a brain — a designated store (the normal case), a designated
file brain, or a local `brain/`. The hook never fails a session: any error exits 0.

Knobs:
  ENGRAM_CAPTURE_DISABLE=1        turn these hooks off entirely
  ENGRAM_CAPTURE_COOLDOWN_MIN=30  minutes between Stop-backstop nudges
  ENGRAM_CAPTURE_PHRASES="a,b,c"  override the wrap-up phrases (comma-separated,
                                  case-insensitive substring match)
"""

from __future__ import annotations

import json
import os
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
try:
    from workspace import load_config, resolve_brain
except Exception:  # degrade rather than break a session
    resolve_brain = None
    load_config = None

# Wrap-up signals. Matched as case-insensitive substrings, so short forms are
# deliberately avoided — "done" would fire on "I'm done reading that file".
DEFAULT_PHRASES = [
    # English
    "wrap up", "wrap it up", "that's all", "thats all", "that's it", "thats it",
    "done for today", "call it a day", "good night", "goodnight", "see you",
    "good work", "well done", "great work", "nice work", "thanks for the help",
    "let's stop", "lets stop", "stop here", "that will do",
    # Korean
    "고생했", "수고했", "수고하", "오늘은 여기까지", "여기까지 하", "여기까지만",
    "마무리하", "마무리 하", "마치자", "끝내자", "오늘 그만", "그만하자",
    "푹 쉬", "내일 보", "다음에 보", "이만",
]


def brain_info(cwd: str) -> dict | None:
    """{display, store} for the brain feeding this directory, or None when there
    is none to feed.

    **The store is asked first.** resolve_brain answers `source="store"` whenever
    one is designated, so a not-yet-migrated local `para/` cannot hijack an
    admitted repo into the file-vault instruction — which is exactly how a
    session ends up hand-editing a hub MOC nobody reads.
    """
    if resolve_brain is not None:
        try:
            r = resolve_brain(cwd)
            if r.get("source") == "store" and r.get("store"):
                if r.get("in_scope") is False:
                    # Admitted nowhere: this repo's knowledge lives in files.
                    return ({"display": f"local file brain ({r['base']})", "store": False}
                            if r.get("base") else None)
                scope = f"{r.get('owner') or '?'}/{r.get('repo') or '?'}"
                return {"display": f"the shared store {r['store']} ({scope})", "store": True}
            if r.get("source") in ("absorb", "shared", "local") and r.get("base") \
                    and Path(r["base"]).is_dir():
                return {"display": f"the file brain at {r['base']}", "store": False}
        except Exception:
            pass
    # The resolver could not be imported. Read the designation directly rather
    # than guessing from directories — a configured store IS the brain.
    url = os.environ.get("ENGRAM_STORE_URL", "").strip()
    if not url and load_config is not None:
        try:
            url = ((load_config().get("store") or {}).get("url") or "").strip()
        except Exception:
            url = ""
    if url:
        return {"display": f"the shared store {url}", "store": True}
    for name in ("brain", "para"):
        if (Path(cwd) / name).is_dir():
            return {"display": f"the file brain at {name}/", "store": False}
    return None


def instruction(info: dict, wrapup: bool) -> str:
    head = "[engram — session wrap-up detected] " if wrapup else "[engram — brain reflection] "
    if info["store"]:
        record = (
            "If there is, record it through the engram skill into the right PARA area — "
            "**via the store** (the brain_put MCP tool, or store.py put; a note is required), "
            "never by writing a file. Weave links into the prose where the idea comes up, "
            "and check brain_integrity afterwards. ")
    else:
        # File-brain wording. MOC updating survives here because a file vault has
        # no index and no search — the folder README is its only discovery
        # mechanism. In the store, search fills that role, which is why the
        # branch above has no MOC step. That is design, not an omission.
        record = (
            "If there is, record it through the engram skill into the right PARA folder, "
            "weave links into the prose, update that folder's MOC (README.md), and run the "
            "engram lint to check integrity. ")
    body = (
        f"This repo is connected to an engram brain — {info['display']}. Look back over this "
        "session and judge whether anything worth keeping came out of it: a concept that got "
        "pinned down, a design decision, a research conclusion, a trap or constraint someone "
        "will hit again. " + record +
        "Be selective: skip small talk, progress checks, anything already captured by the code "
        "or its history, and anything already documented. Over-documenting and forced links are "
        "both failures. If there is nothing worth keeping, say so in one line and move on."
    )
    if wrapup:
        body += " The user is closing the session, so do this before you reply."
    return head + body


def emit(obj: dict) -> None:
    # UTF-8 bytes: printing non-ASCII on a non-UTF-8 locale raises
    # UnicodeEncodeError and emits broken JSON, which the host cannot parse.
    sys.stdout.buffer.write(json.dumps(obj, ensure_ascii=False).encode("utf-8"))
    sys.stdout.buffer.flush()


def main() -> int:
    if os.environ.get("ENGRAM_CAPTURE_DISABLE") == "1":
        return 0
    try:
        # stdin is read as UTF-8 bytes: the host sends UTF-8 JSON, but
        # sys.stdin's text decoder follows the locale, which corrupts or raises
        # on non-ASCII prompts — exactly the wrap-up case in some languages.
        data = json.loads(sys.stdin.buffer.read().decode("utf-8"))
    except Exception:
        return 0

    cwd = data.get("cwd") or os.getcwd()
    info = brain_info(cwd)
    if info is None:
        return 0

    event = data.get("hook_event_name", "")

    if event == "UserPromptSubmit":
        prompt = (data.get("prompt") or "").lower()
        raw = os.environ.get("ENGRAM_CAPTURE_PHRASES")
        phrases = [p.strip().lower() for p in raw.split(",")] if raw else DEFAULT_PHRASES
        if any(p and p in prompt for p in phrases):
            emit({"hookSpecificOutput": {
                "hookEventName": "UserPromptSubmit",
                "additionalContext": instruction(info, wrapup=True)}})
        return 0

    if event == "Stop":
        if data.get("stop_hook_active"):
            return 0
        session_id = data.get("session_id") or "nosession"
        try:
            cooldown_s = max(0.0, float(os.environ.get("ENGRAM_CAPTURE_COOLDOWN_MIN", "30")) * 60.0)
        except ValueError:
            cooldown_s = 1800.0
        marker = Path(tempfile.gettempdir()) / f"engram-capture-{session_id}.marker"
        now = time.time()
        try:
            if not marker.exists():
                marker.write_text(str(now), encoding="utf-8")   # first encounter: start the clock
                return 0
            if now - marker.stat().st_mtime < cooldown_s:
                return 0
            os.utime(marker, (now, now))
        except Exception:
            return 0
        emit({"decision": "block", "reason": instruction(info, wrapup=False)})
        return 0

    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except SystemExit:
        raise
    except Exception:
        raise SystemExit(0)  # never break a session
