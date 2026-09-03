#!/usr/bin/env python3
"""Where does this repo's knowledge belong?

Answers one question for every other part of the skill: given the directory I am
standing in, is the brain the STORE, a local file brain, or nothing yet?

    <config dir>/engram/config.json

is the settings file, and it is **shared with the `engram` binary**. The binary
owns the `store` section and writes it (`engram store set`); this script owns the
`brains` section — the file brain used when the store refuses a repo — and never
writes the other one. Two writers of one section was the mistake this layout
exists to avoid.

`<config dir>` is $ENGRAM_CONFIG_DIR, else $CLAUDE_CONFIG_DIR, else ~/.claude —
the same ladder the binary walks, so both halves land on one file without either
being told where the other put it. Override the whole path with $ENGRAM_CONFIG
(used by tests).

## Resolution

The store is asked FIRST and the answer stops there:

1. a store is designated -> `source="store"`. The brain is the store. `base` is
   only the fallback vault: where a document goes when the store answers 403,
   which happens for a repo whose owner group the store does not admit. Shared
   and repo-only knowledge are not two places — they are the `repo` coordinate
   inside the one store, decided per write.
2. no store, and cwd is inside the designated shared file brain -> `absorb`: that
   brain IS the base.
3. no store, a shared file brain is designated -> `shared`: use it from anywhere.
4. no store, no designation, a local `brain/` (or legacy `para/`, or PARA folders
   at the root) -> `local`.
5. nothing -> `none`. Ask; never invent a path.

Getting order 1 wrong is the classic failure: a repo the store admits takes the
file-vault branch merely because a not-yet-migrated `para/` is still sitting
there, and the session then hand-edits files nobody reads.

Importable: `from workspace import resolve_brain`. CLI:

    resolve [--repo P] [--json]
    list    [--repo P] [--json]
    set-brain <path> | unset-brain
    link    [--repo P] [--remove]
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

PARA_CATEGORIES = ("projects", "areas", "resources", "archives")

# One shared file brain per environment — always stored under this fixed name.
BRAIN_NAME = "shared"


# --------------------------------------------------------------------------- #
# config location + load/save
# --------------------------------------------------------------------------- #
def config_path() -> Path:
    override = os.environ.get("ENGRAM_CONFIG")
    if override:
        return Path(override).expanduser()
    base = os.environ.get("ENGRAM_CONFIG_DIR") or os.environ.get("CLAUDE_CONFIG_DIR")
    root = Path(base).expanduser() if base else (Path.home() / ".claude")
    return root / "engram" / "config.json"


def load_config() -> dict:
    p = config_path()
    if not p.is_file():
        return {"version": 1, "brains": {}, "store": {}}
    try:
        data = json.loads(p.read_text(encoding="utf-8"))
    except Exception:
        # A corrupt settings file must not crash a hook. Resolution degrades to
        # "nothing designated", which the caller already knows how to report.
        return {"version": 1, "brains": {}, "store": {}}
    data.setdefault("version", 1)
    data.setdefault("brains", {})
    data.setdefault("store", {})
    return data


def save_config(cfg: dict) -> Path:
    """Write back, preserving every key this script does not own — the binary's
    `store` section rides along untouched."""
    p = config_path()
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(json.dumps(cfg, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return p


# --------------------------------------------------------------------------- #
# path / git helpers
# --------------------------------------------------------------------------- #
def _norm(p: str | Path) -> str:
    """Comparison key: absolute, normalized case (Windows-safe)."""
    return os.path.normcase(os.path.abspath(os.path.expanduser(str(p))))


def _display(p: str | Path) -> str:
    """Readable, stable storage form: absolute with forward slashes."""
    return Path(os.path.expanduser(str(p))).resolve().as_posix()


def git_root(cwd: str | Path) -> str:
    """Repo root via git; fall back to walking up for a .git entry, else cwd."""
    cwd = os.path.abspath(str(cwd))
    try:
        out = subprocess.run(["git", "-C", cwd, "rev-parse", "--show-toplevel"],
                             capture_output=True, text=True, timeout=5)
        if out.returncode == 0 and out.stdout.strip():
            return os.path.abspath(out.stdout.strip())
    except Exception:
        pass
    cur = Path(cwd)
    for d in (cur, *cur.parents):
        if (d / ".git").exists():
            return str(d)
    return cwd


def owning_git_dir(path: str | Path) -> str | None:
    """The git repo root that owns `path` — itself or any ancestor with a .git."""
    p = Path(path)
    for d in (p, *p.parents):
        if (d / ".git").exists():
            return str(d)
    return None


def remote_url(path: str | Path, remote: str = "origin") -> str | None:
    root = owning_git_dir(path)
    if not root:
        return None
    try:
        out = subprocess.run(["git", "-C", root, "remote", "get-url", remote],
                             capture_output=True, text=True, timeout=5)
        if out.returncode == 0 and out.stdout.strip():
            return out.stdout.strip()
    except Exception:
        pass
    return None


_ORIGIN_RE = re.compile(r"[:/]([^/:]+)/([^/]+?)(?:\.git)?/*$")


def origin_coords(repo_root: str | Path) -> tuple[str, str]:
    """(owner, repo) per `origin`. ('', '') when unavailable.

    **Scope is derived, never chosen.** That is what turns a habit into a
    boundary: knowledge from a repo the store does not admit cannot be filed into
    it by someone forgetting where they were.
    """
    url = remote_url(repo_root)
    if not url:
        return "", ""
    m = _ORIGIN_RE.search(url)
    return (m.group(1), m.group(2)) if m else ("", "")


def local_base(repo_root: str | Path) -> tuple[Path, str] | None:
    """Detect a repo-local PARA base: brain/ -> para/ -> flat root."""
    root = Path(repo_root)
    if (root / "brain").is_dir():
        return (root / "brain").resolve(), "brain"
    if (root / "para").is_dir():
        return (root / "para").resolve(), "para"
    if any((root / c).is_dir() for c in PARA_CATEGORIES):
        return root.resolve(), "."
    return None


def brain_base(brain_path: str | Path) -> Path:
    """The PARA base inside a designated file brain's directory.

    A designated brain points at a container (usually its own git repo); its PARA
    base nests under brain/ by default — exactly like any repo, so a dedicated
    brain repo is not a special-cased flat exception. Registering the brain/
    folder itself still resolves to itself.
    """
    root = Path(os.path.expanduser(str(brain_path))).resolve()
    detected = local_base(root)
    return detected[0] if detected else (root / "brain").resolve()


# --------------------------------------------------------------------------- #
# resolution
# --------------------------------------------------------------------------- #
def the_brain(cfg: dict) -> tuple[str | None, dict | None]:
    """THE single designated file brain: (name, entry) or (None, None)."""
    brains = cfg.get("brains", {})
    if not brains:
        return None, None
    name = BRAIN_NAME if BRAIN_NAME in brains else next(iter(brains))
    return name, brains[name]


def resolve_brain(cwd: str | Path | None = None) -> dict:
    """Resolve the brain for a working directory. See the module docstring for
    the order; the store wins before any file question is asked."""
    cwd = os.path.abspath(str(cwd or os.getcwd()))
    repo_root = git_root(cwd)
    cfg = load_config()

    result = {"base": None, "label": None, "source": "none", "brain": None,
              "store": None, "owner": None, "repo": None, "in_scope": None,
              "shared_base": None, "repo_root": repo_root, "warning": None}

    store_url = (cfg.get("store") or {}).get("url") or os.environ.get("ENGRAM_STORE_URL", "")
    store_url = store_url.strip().rstrip("/")
    if store_url:
        owner, repo = origin_coords(repo_root)
        # The admitted groups come from the cache written by `engram store set`.
        # No network call happens here — hooks call this path. No cache means
        # None: "unknown", which is not the same as "refused".
        owners = (cfg.get("store") or {}).get("owners")
        in_scope = (owner in owners) if (owners and owner) else None
        lb = local_base(repo_root)
        result.update(
            source="store", store=store_url,
            owner=owner or None, repo=(repo or Path(repo_root).name), in_scope=in_scope,
            base=(str(lb[0]) if lb else None), label=(lb[1] if lb else None),
        )
        if in_scope is False:
            result["warning"] = (
                f"owner '{owner}' is outside the store's admitted groups "
                f"({', '.join(owners)}) — this repo's documents stay in a local file brain")
        elif lb:
            result["warning"] = (
                f"a local file vault `{lb[1]}/` is still here — the store is canonical, and "
                "this is either waiting to be migrated or the fallback for a 403. Do not read it as the brain")
        return result

    brain_name, brain = the_brain(cfg)
    if brain and brain.get("path"):
        sb = brain_base(brain["path"])
        container = _norm(brain["path"])
        inside = _norm(cwd) == container or _norm(cwd).startswith(container + os.sep)
        lb = local_base(repo_root)
        if inside or (lb and _norm(lb[0]) == _norm(sb)):
            # Working inside the shared brain itself: it IS the base.
            result.update(base=str(sb), label=brain_name, source="absorb", brain=brain_name)
            return result
        result.update(base=str(sb), label=brain_name, source="shared",
                      brain=brain_name, shared_base=str(sb))
        return result

    lb = local_base(repo_root)
    if lb:
        result.update(base=str(lb[0]), label=lb[1], source="local")
        return result

    return result


# --------------------------------------------------------------------------- #
# repo-side pointer (CLAUDE.md)
# --------------------------------------------------------------------------- #
# The designation lives only in the user-scope settings file, because a
# machine-specific absolute path must never be committed. The side effect: a
# plain (non-engram) session opening this repo has no signal a brain exists at
# all, and answers from the code alone. `link` drops a small PORTABLE pointer
# into the repo's CLAUDE.md, which every session loads. The block is
# marker-delimited, so re-running replaces it in place and `--remove` strips it.

POINTER_BEGIN = ("<!-- BEGIN engram:brain-pointer (managed by engram — regenerate with "
                 "`workspace.py link`; do not hand-edit) -->")
POINTER_END = "<!-- END engram:brain-pointer -->"
_POINTER_RE = re.compile(
    r"\n*<!-- BEGIN engram:brain-pointer.*?<!-- END engram:brain-pointer -->[ \t]*\n?",
    re.DOTALL)


def _pointer_store(url: str, owner: str, repo: str) -> str:
    scope = f"{owner}/{repo}" if owner else f"<owner>/{repo}"
    return (
        "## This repo's knowledge lives in an engram store\n\n"
        "Durable knowledge about this repo — design decisions, verified traps, runbooks,\n"
        "past investigations — is **not in these files**. It is in the shared store:\n\n"
        f"- Store: `{url}` (people browse it here; agents hit the same ranking)\n"
        f"- This repo's scope: `{scope}/<area>/…` (area = projects · areas · resources · archives)\n"
        f"- Knowledge that outlives any one repo — contracts, manuals, conventions — goes to\n"
        f"  `{owner or '<owner>'}/shared/<area>/…` instead\n\n"
        "**Search the store before grepping this repo.** It returns chunks with their\n"
        "heading path, so an answer costs a fraction of what a file sweep does. Use the\n"
        "`brain_search` MCP tool, or `engram search \"<your question>\"`.\n\n"
        "Scope is **derived from `origin`, never chosen**: a repo the store admits writes to\n"
        "it, one it does not is refused with 403 and keeps a local `brain/` instead. If the\n"
        "store is unreachable the client **fails loudly** — there is no local copy to fall\n"
        "back to, and a stale answer would be worse than none.\n")


def _pointer_files(brain: str, remote: str | None, subpath: str) -> str:
    remote_line = (f"- Brain (git remote): `{remote}`" if remote
                   else "- Brain: a designated engram file brain (see the engram settings)")
    return (
        "## This repo's knowledge lives in an engram brain\n\n"
        f"This repo's docs live in the engram **{brain}** brain — this environment's\n"
        "single shared brain. Its durable design knowledge, decisions and history live\n"
        "there, not in this repo's code.\n\n"
        f"{remote_line}\n"
        f"- This repo's notes within the brain: `{subpath}`\n\n"
        "Before answering architecture, history, or \"why is it built this way\" questions,\n"
        "read those brain docs first. The brain's local path is machine-specific, so it is\n"
        "deliberately not hard-coded here — resolve it with `workspace.py resolve` and read\n"
        "the `base` field.\n")


def build_pointer(repo_root: str | Path, r: dict) -> str:
    repo_name = Path(repo_root).name
    if r.get("source") == "store":
        body = _pointer_store(r["store"], r.get("owner") or "", r.get("repo") or repo_name)
    else:
        rurl = remote_url(r["base"]) if r.get("base") else None
        body = _pointer_files(r.get("brain") or r.get("label") or "shared", rurl,
                              f"projects/{repo_name}/")
    return f"{POINTER_BEGIN}\n{body}{POINTER_END}"


def write_repo_pointer(repo_root: str | Path, r: dict) -> tuple[str, Path]:
    """Create or refresh the pointer block in <repo>/CLAUDE.md. Idempotent."""
    f = Path(repo_root) / "CLAUDE.md"
    original = f.read_text(encoding="utf-8") if f.is_file() else None
    existed = original is not None
    stripped = _POINTER_RE.sub("\n", original).rstrip() if existed else ""
    block = build_pointer(repo_root, r)
    new = f"{stripped}\n\n{block}\n" if stripped else f"{block}\n"
    if existed and new == original:
        return "unchanged", f
    f.write_text(new, encoding="utf-8")
    return ("updated" if existed else "created"), f


def remove_repo_pointer(repo_root: str | Path) -> tuple[str, Path]:
    """Strip the pointer block. Deletes CLAUDE.md only if the block was all of it."""
    f = Path(repo_root) / "CLAUDE.md"
    if not f.is_file():
        return "absent", f
    original = f.read_text(encoding="utf-8")
    stripped = _POINTER_RE.sub("\n", original)
    if stripped == original:
        return "absent", f
    rest = stripped.strip()
    if rest:
        f.write_text(rest + "\n", encoding="utf-8")
        return "removed", f
    f.unlink()
    return "removed-empty", f


# --------------------------------------------------------------------------- #
# CLI
# --------------------------------------------------------------------------- #
def _emit(obj: dict) -> None:
    sys.stdout.buffer.write((json.dumps(obj, ensure_ascii=False, indent=2) + "\n").encode("utf-8"))
    sys.stdout.buffer.flush()


def _print(msg: str) -> None:
    # UTF-8 bytes regardless of console code page, so non-ASCII paths never crash a run.
    sys.stdout.buffer.write((msg + "\n").encode("utf-8"))
    sys.stdout.buffer.flush()


def cmd_set_brain(args) -> int:
    path = Path(os.path.expanduser(args.path)).resolve()
    if not path.is_dir():
        _print(f"error: not a directory: {path}")
        return 1
    cfg = load_config()
    _, prev = the_brain(cfg)
    # One shared file brain per environment: designating REPLACES, never adds.
    cfg["brains"] = {BRAIN_NAME: {"path": _display(path)}}
    save_config(cfg)
    _print(f"shared file brain designated: {_display(path)}")
    _print(f"  PARA base: {brain_base(path)}")
    if prev and _norm(prev.get("path", "")) != _norm(path):
        _print(f"  replaced: {prev.get('path')} (the directory is left untouched)")
    if (load_config().get("store") or {}).get("url"):
        _print("  note: a store is designated, so this is the FALLBACK vault — "
               "where documents go when the store refuses a repo (403)")
    return 0


def cmd_unset_brain(args) -> int:
    cfg = load_config()
    _, brain = the_brain(cfg)
    if not brain:
        _print("no shared file brain designated")
        return 0
    cfg["brains"] = {}
    save_config(cfg)
    _print(f"shared file brain un-designated: {brain.get('path')} (the directory is left untouched)")
    return 0


def cmd_link(args) -> int:
    repo = git_root(args.repo or os.getcwd())
    if args.remove:
        state, f = remove_repo_pointer(repo)
        _print(f"brain pointer {state}: {Path(f).as_posix()}")
        return 0
    r = resolve_brain(repo)
    if r["source"] == "none":
        _print("error: no brain designated. Point at a store with `engram store set <url>`, "
               "or designate a file brain with `workspace.py set-brain <path>`")
        return 1
    state, f = write_repo_pointer(repo, r)
    target = (f"store {r['store']} ({r['owner'] or '?'}/{r['repo']})"
              if r["source"] == "store" else f"{r.get('brain') or r.get('label')} ({r['base']})")
    _print(f"brain pointer {state}: {Path(f).as_posix()} (-> {target})")
    return 0


def cmd_list(args) -> int:
    cfg = load_config()
    _, brain = the_brain(cfg)
    r = resolve_brain(args.repo or os.getcwd())
    if args.json:
        _emit({"config": str(config_path()), "store": cfg.get("store"),
               "brain": brain, "resolved": r})
        return 0
    _print(f"config:      {config_path()}")
    _print(f"store:       {(cfg.get('store') or {}).get('url') or '(none — file brains only)'}")
    _print(f"file brain:  {brain.get('path') if brain else '(none designated)'}")
    if r["source"] == "store":
        _print(f"here:        source=store  scope={r['owner'] or '(no remote)'}/{r['repo']}"
               + (f"  fallback_vault={r['base']}" if r.get("base") else ""))
    else:
        _print(f"here:        source={r['source']}  base={r['base']}")
    if r["warning"]:
        _print(f"warning:     {r['warning']}")
    return 0


def cmd_resolve(args) -> int:
    r = resolve_brain(args.repo or os.getcwd())
    if args.json:
        _emit(r)
        return 0
    if r["source"] == "store":
        # `base` here is not the brain; naming it otherwise is how a session ends
        # up hand-editing a vault nobody reads.
        _print(f"store={r['store']} scope={r['owner'] or '(no remote)'}/{r['repo']} source=store"
               + (f" fallback_vault={r['base']}" if r.get("base") else ""))
    else:
        _print(f"base={r['base']} label={r['label']} source={r['source']}"
               + (f" brain={r['brain']}" if r["brain"] else ""))
    if r["warning"]:
        _print(f"warning: {r['warning']}")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description="engram brain resolution")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("resolve", help="resolve the brain for this directory")
    p.add_argument("--repo")
    p.add_argument("--json", action="store_true")
    p.set_defaults(func=cmd_resolve)

    p = sub.add_parser("list", help="show the designations and how this directory resolves")
    p.add_argument("--repo")
    p.add_argument("--json", action="store_true")
    p.set_defaults(func=cmd_list)

    p = sub.add_parser("set-brain", help="designate THE shared file brain of this environment "
                                         "(replaces any previous designation)")
    p.add_argument("path")
    p.set_defaults(func=cmd_set_brain)

    p = sub.add_parser("unset-brain", help="remove the file-brain designation "
                                           "(the directory is left untouched)")
    p.set_defaults(func=cmd_unset_brain)

    p = sub.add_parser("link", help="write or refresh (or --remove) this repo's CLAUDE.md pointer")
    p.add_argument("--repo")
    p.add_argument("--remove", action="store_true")
    p.set_defaults(func=cmd_link)

    args = ap.parse_args()
    return args.func(args)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except SystemExit:
        raise
    except Exception as e:  # never crash the skill
        sys.stderr.write(f"workspace error: {e}\n")
        raise SystemExit(1)
