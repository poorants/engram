#!/usr/bin/env python3
"""Seed a store from a tree of markdown files.

This script **reads and sends, and nothing else.** Chunking, lexemes and the
inserts all happen on the server, which is what structurally guarantees that the
index and the search build lexemes with the same code. The server holds no
files, no git, and no credentials.

    python bin/import_tree.py ~/notes --owner acme --repo shared \
        --url http://localhost:8081 --token "$ENGRAM_TOKEN"

Every file becomes ``<owner>/<repo>/<its path under the tree>``, so a tree laid
out as ``projects/ areas/ resources/ archives/`` lands with its PARA areas
intact. When the tree is a git repo, each document carries its last commit date
as ``updated_at`` — without that, two hundred imported documents all share the
moment of the import and "recently changed" means nothing.

The import is an **upsert and never deletes**: documents absent from the payload
are left alone, and anything born in the store keeps its history. It is still
not a thing to run out of habit — the store is the canonical copy, and these
files are a snapshot of some earlier moment. The server refuses to overwrite a
newer body with an older one and reports it as ``skipped_newer``, which is a
backstop, not a licence.
"""
from __future__ import annotations

import argparse
import gzip
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

SUFFIXES = (".md", ".dbml")


def last_commit_dates(root: Path) -> dict[str, str]:
    """Each file's last commit time, keyed by path relative to ``root``.

    The log is walked ONCE rather than running git log per file (hundreds of
    files that way costs seconds; this costs milliseconds). Returns {} when the
    tree is not a git repo.

    ``--relative`` is not a nicety: git prints paths from the REPOSITORY root,
    and the tree being imported is often a subdirectory of one (a brain kept in
    ``<repo>/brain``). Without it every key is prefixed, nothing matches, and
    the import silently succeeds with all two hundred documents stamped at the
    moment it ran — the exact failure this function exists to prevent, and one
    that looks like nothing went wrong.
    """
    try:
        out = subprocess.run(["git", "-C", str(root), "log", "--relative",
                              "--format=%x01%cI", "--name-only", "--", "."],
                             capture_output=True, text=True, timeout=60)
    except (OSError, subprocess.SubprocessError):
        return {}
    if out.returncode != 0:
        return {}
    dates: dict[str, str] = {}
    when = ""
    for line in out.stdout.splitlines():
        if line.startswith("\x01"):
            when = line[1:]
        elif line.strip():
            dates.setdefault(line.strip(), when)   # the newest commit comes first
    return dates


def main() -> int:
    ap = argparse.ArgumentParser(description="seed an engram store from a directory of markdown")
    ap.add_argument("tree", help="the directory to import")
    ap.add_argument("--owner", required=True, help="owner coordinate — must be one the store admits")
    ap.add_argument("--repo", default="shared",
                    help="repo coordinate ('shared' for knowledge that belongs to no single repo)")
    ap.add_argument("--url", default=os.environ.get("ENGRAM_URL", "http://127.0.0.1:8081"))
    ap.add_argument("--token", default=os.environ.get("ENGRAM_TOKEN", ""))
    ap.add_argument("--note", default="bulk import", help="recorded on every revision this creates")
    ap.add_argument("--dry-run", action="store_true", help="list what would be sent and stop")
    args = ap.parse_args()

    root = Path(args.tree).expanduser().resolve()
    if not root.is_dir():
        print(f"not a directory: {root}", file=sys.stderr)
        return 2
    if not args.token and not args.dry_run:
        print("no token — pass --token or set ENGRAM_TOKEN", file=sys.stderr)
        return 2

    dates = last_commit_dates(root)
    docs = []
    for p in sorted(q for q in root.rglob("*")
                    if q.is_file() and q.suffix in SUFFIXES and ".git" not in q.parts):
        rel = p.relative_to(root).as_posix()
        docs.append({"path": f"{args.owner}/{args.repo}/{rel}",
                     "body": p.read_text(encoding="utf-8", errors="replace"),
                     "updated_at": dates.get(rel)})
    if not docs:
        print(f"no markdown under {root} — an empty import never overwrites the index", file=sys.stderr)
        return 1

    if args.dry_run:
        for d in docs:
            print(d["path"])
        print(f"\n{len(docs)} documents would be sent to {args.url}")
        return 0

    payload = json.dumps({"note": args.note, "docs": docs}, ensure_ascii=False).encode()
    body = gzip.compress(payload, 6)
    url = args.url.rstrip("/")
    print(f"{len(docs)} documents · {len(payload)/1024/1024:.1f}MB → gzip {len(body)/1024/1024:.1f}MB → {url}")

    req = urllib.request.Request(f"{url}/api/index", data=body, method="POST", headers={
        "Content-Type": "application/json", "Content-Encoding": "gzip",
        "X-Engram-Token": args.token})
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=300) as r:
            res = json.load(r)
    except urllib.error.HTTPError as e:
        print(f"failed {e.code}: {e.read().decode(errors='replace')[:400]}", file=sys.stderr)
        return 1
    except urllib.error.URLError as e:
        print(f"could not reach {url}: {e}", file=sys.stderr)
        return 1

    print(f"imported · created {res['created']} · updated {res['updated']} · unchanged {res['unchanged']}"
          + (f" · retimed {res['touched']}" if res.get("touched") else "")
          + (f" · skipped {res['skipped_newer']}" if res.get("skipped_newer") else "")
          + (f" · refused {res['denied']}" if res.get("denied") else ""))
    for d in res.get("skipped_paths", []):
        print(f"  skipped (the store's copy is newer): {d}")
    for d in res.get("denied_paths", []):
        # Never silent: a refused document dropped quietly is one everyone
        # believes landed.
        print(f"  refused (owner not admitted): {d}")
    print(f"  store now holds {res['docs']} documents · {res['chunks']} chunks"
          f" · {res['broken_links']} broken links · server {res['seconds']}s"
          f" · round trip {time.time()-t0:.1f}s")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
