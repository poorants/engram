# Resolution — which brain is this repo's brain?

`scripts/workspace.py` answers one question for every other part of the skill:
standing in this directory, is the brain the **store**, a **local file brain**,
or **nothing yet**?

```bash
python "<skill_dir>/scripts/workspace.py" resolve --json
```

## The settings file

```
<config dir>/engram/config.json
<config dir>/engram/store.token      # 0600, never in the JSON
```

`<config dir>` is `$ENGRAM_CONFIG_DIR`, else `$CLAUDE_CONFIG_DIR`, else
`~/.claude`. Override the whole path with `$ENGRAM_CONFIG` (tests use this).

```json
{
  "version": 1,
  "store": { "url": "http://brain.example:8081", "owners": ["acme"] },
  "brains": { "shared": { "path": "/home/me/brain" } }
}
```

**Two owners, no overlap.** The `engram` binary owns `store` and writes it
(`engram store set`); this script owns `brains` and never writes the other
section. Both read the whole file and preserve what they do not own, which is why
`store set` cannot wipe a file-brain designation and `set-brain` cannot wipe a
store address.

`store.owners` is a **cache** of the groups the store admits, written when the
store was designated. It exists so resolution — which hooks call — never touches
the network. Missing means `in_scope: null`, which is "unknown", not "refused";
the store itself decides, and it answers 403 when it disagrees.

## The order

The store is asked first and the answer stops there. Getting this order wrong is
the classic failure: an admitted repo takes the file branch merely because a
not-yet-migrated `para/` is still sitting there, and the session then hand-edits
files nobody reads.

| `source` | When | `base` means |
|---|---|---|
| `store` | a store is designated | the **fallback vault** for a 403 — not the brain |
| `absorb` | no store; cwd is inside the designated file brain | that brain's PARA base |
| `shared` | no store; a file brain is designated, cwd elsewhere | that brain's PARA base |
| `local` | no store, no designation, a local base exists | `brain/`, else `para/`, else the root (flat) |
| `none` | nothing | `null` — ask, never invent a path |

In `store` mode the result also carries `store` (the URL), `owner`/`repo` (from
`origin`), `in_scope`, and a `warning` when either the owner is outside the
admitted groups or a stale local vault is still present.

**There is no hybrid mode.** It used to mean two physical vaults kept in step by
a sync job; the store made both halves one row set and the sync a no-op. What
survived is not a mode but a routing decision made once per write: the `repo`
coordinate is either this repo or `shared`.

## Positional, not assigned

Resolution is positional — the local base anchors at the **nearest git repo root
above the working directory** (no repo above → the working directory itself). There
are no per-repo assignments to keep in sync, and the one designated file brain
applies everywhere once set.

```bash
workspace.py set-brain /home/me/brain    # designate; replaces any previous one
workspace.py unset-brain                 # the directory is left untouched
workspace.py list                        # designations + how here resolves
```

There is exactly one file brain per environment, stored under the fixed name
`shared`. Designating **replaces**; it never adds a second. The old directory is
left alone.

With a store designated, that file brain is the **fallback vault** — where
documents go when the store refuses a repo. Without one, it is the brain.

## The repo pointer

The designation lives only in the user-scope settings file, because a
machine-specific absolute path must never be committed. The side effect: a plain
session opening a repo has no signal a brain exists at all, and answers from the
code alone.

```bash
workspace.py link             # write/refresh the pointer in this repo's CLAUDE.md
workspace.py link --remove    # strip it
```

The block is marker-delimited and idempotent — re-running replaces it in place.
It carries the store URL and this repo's scope (or, for a file brain, the git
remote and the in-brain subpath) and deliberately **not** the local checkout
path, which is machine-specific and resolved on demand.

It is optional. engram always resolves positionally; the pointer is for every
other session.

## Organizing a brain

PARA folders own one axis, actionability. The repo axis is a folder **only** in
`projects/<repo>/`:

- `projects/<repo>/` — work with an end, per repo. Retiring a repo archives this
  and nothing else.
- `areas/` and `resources/` — mixed across repos by design. Knowledge that only
  ever mattered to one repo was rarely knowledge; knowledge that mattered to
  several is exactly what a shared brain is for.
- Domain knowledge stays **shallow** under a MOC rather than in deep folders.
  Depth is what links and MOC hubs replace.

In the store the same shape holds, with `<owner>/shared/` playing the role of
"belongs to no single repo".
