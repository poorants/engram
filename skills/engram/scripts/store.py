#!/usr/bin/env python3
"""The skill's wrapper over the `engram` binary.

**This script speaks no HTTP.** The transport, the token, the address and the
path rules all live in the binary; there is one client with three surfaces (the
`brain_*` MCP tools in a session, `engram <verb>` for a subprocess, and this
wrapper for the skill). Two clients would mean two places to put the token, two
default authors, and two copies of the path rules to drift apart.

What is left here is the skill's own two jobs:

  1. print results in a shape a person reads, and
  2. **write a refused document to the local file brain** — the vault's location
     is the skill's business, and a release-channel binary has no reason to know
     a plugin's layout.

Which knowledge goes to the store and which stays in files is not chosen: **the
git remote decides.** A repo whose owner group the store admits writes to the
store; one it does not is refused. There is no path by which knowledge from an
unadmitted repo lands in the store because somebody forgot where they were.

**An unreachable store fails on the spot, for reads and writes both.** No
fallback, no queue. A stale answer and a spool sitting somewhere both manufacture
the belief that it worked, and that belief outlives the outage.

A refusal is a different thing. **Scope refused (exit 3) -> write the local file
brain**: the store is alive and declined, and that knowledge belonged there
anyway.

    store.py status
    store.py search "why does the copy button copy nothing"
    store.py get   acme/shared/resources/x.md
    store.py put   acme/webapp/resources/x.md --file draft.md --note "why this exists"
    store.py revisions acme/shared/resources/x.md
    store.py move  <old> <new>
    store.py integrity
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from workspace import resolve_brain  # noqa: E402

TIMEOUT = float(os.environ.get("ENGRAM_TIMEOUT", "30"))

# The binary's exit-code contract. Telling 3 from 4 is the whole point: read an
# outage as a refusal and the document goes into a local file nobody reads while
# everyone believes it was recorded — a belief that outlives the outage by months.
EXIT_REFUSED = 3
EXIT_STORE_DOWN = 4

INSTALL_HINT = ("install it with:\n"
                "  curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh | sh")


class StoreDown(RuntimeError):
    """The store could not be reached. Do not fall back — say so."""


class ScopeRefused(PermissionError):
    """The store is alive and declined this path's owner. The local file brain takes it."""


def binary() -> str:
    """The `engram` executable: PATH first, then the installer's default location."""
    found = shutil.which("engram")
    if found:
        return found
    default = Path.home() / ".local/bin/engram"
    if default.is_file():
        return str(default)
    raise StoreDown(f"the `engram` binary is not on PATH — the store is only reached "
                    f"through it. {INSTALL_HINT}")


def last_line(text: str | None) -> str:
    """The last meaningful line of stderr. A child that also logs diagnostics
    should not bury its one human-readable line inside an exception message."""
    for line in reversed((text or "").strip().splitlines()):
        if line.strip():
            return line.strip()
    return ""


def run(*args: str, stdin: str | None = None) -> dict:
    """Run `engram <args>` and return its JSON.

    The exit code is the contract — 3 refused, 4 unreachable, anything else an
    error. Never branch on message text: split that way, a network failure is one
    day read as a scope refusal.
    """
    cmd = [binary(), *args]
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, input=stdin,
                           timeout=TIMEOUT)
    except subprocess.SubprocessError as e:
        raise StoreDown(str(e))

    if r.returncode == EXIT_REFUSED:
        raise ScopeRefused(last_line(r.stderr) or "the store declined this path's owner")
    if r.returncode == EXIT_STORE_DOWN:
        raise StoreDown(last_line(r.stderr) or "could not reach the store")
    if r.returncode != 0:
        raise RuntimeError(last_line(r.stderr) or last_line(r.stdout) or f"exit {r.returncode}")
    if not r.stdout.strip():
        return {}
    try:
        return json.loads(r.stdout)
    except json.JSONDecodeError as e:
        raise RuntimeError(f"could not read the engram binary's response: {e}")


# --------------------------------------------------------------------------- #
# reads — the store only. Unreachable means failure.
# --------------------------------------------------------------------------- #
def cmd_search(args) -> int:
    try:
        cmd = ["search", args.q, "--limit", str(args.limit)]
        if args.archives:
            cmd.append("--archives")
        if args.only_repo:
            cmd += ["--only-repo", ",".join(args.only_repo)]
        res = run(*cmd)
    except StoreDown as e:
        # Do not sweep local files and answer from them instead. A stale answer
        # is worse than no answer, because nobody can tell it is stale.
        print(f"could not reach the store: {e}", file=sys.stderr)
        print("The brain cannot be searched until the store is back. "
              "Check the address with: store.py status", file=sys.stderr)
        return 1

    hits, idx = res.get("hits") or [], res.get("index", {})
    if not hits:
        print(f"no results: {args.q}")
        return 0
    for i, h in enumerate(hits, 1):
        loc = h["path"] + (f"  ¶ {h['heading_path']}" if h.get("heading_path") else "")
        print(f"\n[{i}] {loc}")
        print(f"    [{h.get('repo','?')}] score={h['score']:.4f}")
        print("    " + h["body"][:args.chars].replace("\n", "\n    "))
    if idx:
        # The boosted repo is echoed because a ranking nobody can explain is one
        # nobody trusts.
        boost = res.get("boostRepo")
        line = (f"\n— store: {idx.get('docs','?')} documents · "
                f"last write {idx.get('updated_at','?')[:16]}")
        print(line + (f" · boosted this repo '{boost}'" if boost else ""))
    return 0


def cmd_get(args) -> int:
    try:
        d = run("get", args.path)
    except StoreDown as e:
        print(f"could not reach the store: {e}", file=sys.stderr)
        return 1
    print(d.get("body", ""))
    if d.get("backlinks"):
        print(f"\n— linked from {len(d['backlinks'])} document(s): "
              + ", ".join(b["path"] for b in d["backlinks"][:10]))
    return 0


def cmd_revisions(args) -> int:
    r = run("revisions", args.path)
    if not r.get("revisions"):
        print(f"no history: {args.path}")
        return 0
    for rev in r["revisions"]:
        print(f"  {rev['id']:>5}  {rev['at'][:19]}  {rev['author']:<16} "
              f"{rev['chars']:>6} chars  {rev['note']}")
    return 0


def cmd_integrity(args) -> int:
    """Graph integrity, measured by the store itself.

    **The orphan check is the floor, not the goal** — a document hanging off a
    single MOC passes it while the graph is still a folder tree.
    """
    r = run("integrity", "--limit", str(args.limit))
    c = r["counts"]
    print(f"broken links {c['broken']} · orphans {c['orphans']} · weak nodes {c['weak']}")
    for k, v in sorted(c["by_kind"].items()):
        label = "contextual (wiki)" if k == "wiki" else "structural (md/MOC)"
        print(f"  {label:<20} {v['total']:>5} edges · {v['broken']} broken")
    if r["broken_links"]:
        print("\nbroken links")
        for b in r["broken_links"]:
            print(f"  {b['from']}  →  [[{b['to']}]]  ({b['kind']})")
    if r["orphans"]:
        print("\norphans — nothing links to these")
        for o in r["orphans"]:
            print(f"  {o}")
    if r["weak_nodes"]:
        print("\nweak nodes — reachable only from a MOC. Weave a contextual link into related prose")
        for w in r["weak_nodes"]:
            print(f"  {w}")
    return 0


# --------------------------------------------------------------------------- #
# writes — the store; the local file brain when refused; failure when unreachable
# --------------------------------------------------------------------------- #
def write_local(path: str, body: str) -> Path | None:
    """Write a scope-refused document to the local file brain — where it belonged."""
    r = resolve_brain()
    base = r.get("base")
    if not base:
        return None
    parts = [p for p in path.split("/") if p]
    # Drop the <owner>/<repo> coordinates: in a file brain the directory IS the
    # scope, so repeating them would nest the vault one repo deep inside itself.
    rel = "/".join(parts[2:]) if len(parts) > 2 else parts[-1]
    f = Path(base) / rel
    f.parent.mkdir(parents=True, exist_ok=True)
    f.write_text(body, encoding="utf-8")
    return f


def put(path: str, body: str, note: str, author: str) -> tuple[str, str]:
    """(status, explanation). An unreachable store is a failure — nothing is
    stashed anywhere."""
    cmd = ["put", path, "--note", note]
    if author:
        cmd += ["--author", author]
    try:
        res = run(*cmd, stdin=body)
        return res.get("status", "ok"), f"store: {path}  (author {res.get('author','?')})"
    except ScopeRefused as e:
        f = write_local(path, body)
        if f:
            return "local", f"the store refused this path ({e}) → wrote the local file brain: {f}"
        return "failed", (f"the store refused this path ({e}) and there is no local file brain. "
                          "Designate one: workspace.py set-brain <path>")
    except StoreDown as e:
        return "failed", (f"could not reach the store ({e}). Nothing was written — "
                          "a queued write would only manufacture the belief that it landed")


def cmd_put(args) -> int:
    body = Path(args.file).read_text(encoding="utf-8") if args.file else sys.stdin.read()
    if not body.strip():
        print("the body is empty — an empty document is never written", file=sys.stderr)
        return 1
    if not args.note.strip():
        print("--note is required — one line on why this revision exists", file=sys.stderr)
        return 1
    # An empty author is passed through so the binary resolves it (ENGRAM_AUTHOR,
    # then git config user.name, then the OS user). Forcing "engram" here would
    # stamp the tool's name on work a person asked for.
    status, msg = put(args.path, body, args.note, args.author)
    print(f"{status}: {msg}")
    return 0 if status != "failed" else 1


def cmd_move(args) -> int:
    """Move a document. **Never `mv` a file for this** — the store keeps the old
    path as an alias, so the [[old-name]] somebody else wrote keeps resolving."""
    cmd = ["move", args.path, args.to]
    if args.author:
        cmd += ["--author", args.author]
    r = run(*cmd)
    if r.get("status") == "moved":
        print(f"moved: {args.path}\n    → {args.to}"
              + (f"  ({r['relinked']} dangling link(s) reconnected)" if r.get("relinked") else "")
              + "\n  The old path is kept as an alias, so existing links still reach it.")
        return 0
    print(f"{r.get('status')}: {args.path}", file=sys.stderr)
    return 1


def cmd_status(args) -> int:
    try:
        st = run("status")
    except StoreDown as e:
        print(f"connection  FAILED — {e}", file=sys.stderr)
        return 1

    print(f"store       {st.get('store') or '(not configured)'}  "
          f"token {'present' if st.get('canWrite') else 'absent'}")
    print(f"author      {st.get('author','?')}"
          + ("  (configured)" if st.get("authorSource") else "  (resolved automatically)"))
    if st.get("root"):
        print(f"here        owner={st['owner']}  repo={st['repo']}")
    else:
        print(f"here        {st.get('scopeError','(no git remote)')}")

    if not st.get("reachable"):
        print(f"connection  FAILED — {st.get('error','?')}")
        return 1
    print(f"connection  ok · {st.get('docs','?')} documents")
    if st.get("allowedOwners"):
        print(f"admitted    {', '.join(st['allowedOwners'])}")
    if "writesHere" in st:
        print("this repo   " + ("writes to the store" if st["writesHere"]
                                else "is refused by the store → local file brain"))
    for p in st.get("present") or []:
        print(f"    {p['owner']}/{p['repo']}: {p['docs']} documents")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description="engram store client (wraps the engram binary)")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("search", help="search the store (fails if it is unreachable)")
    p.add_argument("q")
    p.add_argument("--limit", type=int, default=6)
    p.add_argument("--chars", type=int, default=400, help="characters shown per chunk")
    p.add_argument("--archives", action="store_true")
    p.add_argument("--only-repo", dest="only_repo", action="append",
                   help="restrict to this repo (repeatable). Without it, everything is "
                        "searched and the current repo is boosted")
    p.set_defaults(func=cmd_search)

    p = sub.add_parser("get", help="print a document")
    p.add_argument("path")
    p.set_defaults(func=cmd_get)

    p = sub.add_parser("put", help="save a document")
    p.add_argument("path")
    p.add_argument("--file", help="body file (stdin when omitted)")
    p.add_argument("--note", default="", help="why this revision exists — recorded in the history (required)")
    p.add_argument("--author", default="",
                   help="omit to resolve automatically: ENGRAM_AUTHOR, then git config user.name, then the OS user")
    p.set_defaults(func=cmd_put)

    p = sub.add_parser("move", help="move a document (the old path stays as an alias)")
    p.add_argument("path")
    p.add_argument("to")
    p.add_argument("--author", default="")
    p.set_defaults(func=cmd_move)

    p = sub.add_parser("integrity", help="broken links · orphans · weak nodes")
    p.add_argument("--limit", type=int, default=50)
    p.set_defaults(func=cmd_integrity)

    p = sub.add_parser("revisions", help="a document's change history")
    p.add_argument("path")
    p.set_defaults(func=cmd_revisions)

    p = sub.add_parser("status", help="connection, scope, and who you write as")
    p.set_defaults(func=cmd_status)

    args = ap.parse_args()
    return args.func(args)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except SystemExit:
        raise
    except Exception as e:                       # never kill the skill
        sys.stderr.write(f"engram store error: {e}\n")
        raise SystemExit(1)
