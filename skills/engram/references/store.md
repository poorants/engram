# The store — full contract

The brain is a Postgres-backed service. There is **one client for it**, the
`engram` binary, and three surfaces over that one client: the `brain_*` MCP tools
inside a session, `engram <verb>` for anything that is not the model, and
[../scripts/store.py](../scripts/store.py) for this skill.

Keeping it to one client is not tidiness. When there were two, the cost was two
places to put the token, two default authors, and two copies of the path rules to
drift apart — and drift there is invisible, because both answers look plausible.

## Setup

```bash
engram store set <url> --token <write token>   # designate; caches the admitted groups
engram store doctor                            # prove it end to end
engram store show                              # where each setting came from
engram store unset [--forget-token]            # remove the designation
```

Settings live in `<config dir>/engram/config.json`; the write token lives beside
it at `store.token`, mode 0600, and **never in the JSON** — that file is one
people open and paste from, and a secret in it eventually gets copied somewhere
it should not be. `<config dir>` is `$ENGRAM_CONFIG_DIR`, else
`$CLAUDE_CONFIG_DIR`, else `~/.claude`. Environment beats file for every value:
`ENGRAM_STORE_URL`, `ENGRAM_TOKEN`, `ENGRAM_AUTHOR`.

The same file is read by this skill's `workspace.py`, which owns the `brains`
section (the file brain) and never writes the `store` section. One settings file,
two owners, no overlap.

**`doctor` checks the write token, not only the connection.** "The store is up"
and "I can write to it" are different facts, and a check that proves only the
first lets someone finish a setup read-only. The first thing they notice is a
save failing at the end of a session, which is the worst possible moment.

There is deliberately **no built-in store address**. A default address is a
machine that exists on one network and nowhere else; a client silently pointing
at it fails in a way that looks like an outage rather than a missing setting.
Unconfigured fails in place, with the remedy.

## Addresses

```
<owner>/<repo>/<area>/<name>.md
```

- `<owner>` and `<repo>` are the document root, and they are **columns in the
  store, not directory levels**. They are derived from `origin`, never chosen.
- `<area>` is one of `projects` · `areas` · `resources` · `archives`.
- A repo hub MOC is the exception with no area: `<owner>/<repo>/README.md`.
- At most **5 levels below the document root**. The rule is a depth CEILING, not
  a minimum segment count — written as a minimum it rejects the repo hub, which
  the store indexes and serves happily.
- Accepted extensions: `.md`, `.dbml`.

`./<area>/<name>.md` given to the CLI is filled in from the current directory's
`origin`. An explicit full path always passes through untouched, so writing
deliberately into another repo's scope is never silently redirected.

## Reads need no token, writes do

| Operation | Tool | Token |
|---|---|---|
| search | `brain_search` / `engram search` | no |
| read one document | `brain_get` / `engram get` | no |
| change history | `brain_revisions` / `engram revisions` | no |
| link-graph health | `brain_integrity` / `engram integrity` | no |
| save | `brain_put` / `engram put` | **yes** |
| move / archive | `brain_move` / `engram move` | **yes** |

There is deliberately **no delete tool**. The contract is *never delete, move to
archives*. The store's soft delete stays reachable for an operator with curl, not
for an agent.

`put` is an upsert: create and update are the same call, the previous body goes
to `revisions`, and an identical body answers `unchanged` instead of writing
again. A `note` is required — a history of "updated" tells you nothing a
timestamp did not.

`move` keeps the old path as an **alias**, so a `[[old-name]]` written elsewhere
keeps resolving. Edges point at an immutable document id, so a move breaks
nothing that had already resolved.

## Exit codes are a contract

| code | meaning | what to do |
|---|---|---|
| 0 | success | — |
| 1 | error — bad argument, malformed path, missing token | fix the call |
| 3 | the store REFUSED this path's owner (403) | write the local file brain |
| 4 | the store could not be REACHED | fail loudly; write nothing anywhere |

Never branch on message text. Split that way, a network failure is one day read
as a scope refusal, the document goes into a local file nobody reads, and
everyone believes it was recorded.

`store.py` turns these into `ScopeRefused` and `StoreDown`, and only the first
one triggers a local write.

## Scope is the confidentiality boundary

The server's `ENGRAM_OWNERS` lists the owner groups it admits; anything else is
refused with 403. It is a list of GROUPS rather than repos on purpose: enumerate
repos and the list falls behind the day someone creates one.

Reads are open, so `owner`/`repo` columns cannot protect confidentiality —
whatever the column says, a document is readable by anyone who can reach the
service. What must not be readable is therefore **never let in**, and that is
enforced at write time rather than by anyone remembering where they are standing.

An empty `ENGRAM_OWNERS` admits nothing. A deployment that forgot to configure it
closes rather than opens.

## The byline is a claim, not a proof

Every revision records an `author`, and the write token is one shared credential
— so the author is a claim the client makes. The design goal is to make it
**honest by default**, not provable; proving it means per-person tokens, which
means accounts, issuing and revocation.

Resolution order: an explicit argument → `ENGRAM_AUTHOR` (env or config) → `git
config user.name` → `$USER`/`$LOGNAME` → the literal `engram`. Every step below
the first is silent: a write must never fail, or even warn, because attribution
could not be resolved.

## No fallback, no queue

An unreachable store fails on the spot for reads and writes both. A stale cached
answer and a spool sitting somewhere both manufacture the belief that it worked,
and that belief outlives the outage. The honest answer to "the brain is down" is
to say so.

A **refusal is not an outage** and takes the other path: the store is alive and
declined, so the document goes to the local file brain, where knowledge from an
unadmitted repo belonged all along.

## Running a store

The server is one compose file — see `server/` in the engram repository.

```bash
cp .env.example .env      # POSTGRES_PASSWORD, ENGRAM_INGEST_TOKEN, ENGRAM_OWNERS
docker compose up -d
```

Exactly one port is published. Seed an existing tree of notes with
`bin/import_tree.py`. Back it up with `deploy/backup.sh`: the canonical copy is
in the database and there is no copy of it anywhere else.
