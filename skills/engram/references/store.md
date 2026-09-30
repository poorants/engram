# The store — full contract

The brain is a Postgres-backed service. A session reaches it through **one
surface**: the remote MCP server `engram serve`, registered in Claude Code as
`engram`. There is no CLI, no local MCP server, no local brain and no
interpreter — this skill ships no scripts.

Keeping it to one surface is not tidiness. When there were several (a Python
helper, a CLI, a local MCP server, a file-brain fallback), the cost was several
places to keep a token, several default authors, and several copies of the path
rules to drift apart — and drift there is invisible, because every answer looks
plausible.

## Connection

```
claude mcp add --transport http --scope user --callback-port 33418 engram https://<host>/mcp
```

Then `/mcp` → engram → **Authenticate**: the server is its own OAuth
authorization server, so Claude Code registers, sends the person through a
Google login once per machine, and keeps and refreshes the token itself. Then
`/mcp__engram__setup` in a session finishes the machine (the plugin and the
hook binary). **Register at user scope**: the brain is not a property of one
checkout, and per-project registration makes the tools vanish the first time
someone opens a different repo.

The client machine holds **no store address, no store credential and no
settings file**. The login is the person's; the store's own credential lives
only in the `engram serve` process next to the store.

- **The tools are absent** from a session — `claude mcp list`; `engram` must be
  an HTTP server and connected. A leftover local (stdio) `engram mcp` entry from
  engram 0.11 or earlier no longer works: remove it
  (`claude mcp remove engram -s user`) and re-add.
- **"Needs authentication"** — `/mcp` → engram → Authenticate. A login that
  succeeds and still shows the state may be Claude Code's cached answer in
  `~/.claude/mcp-needs-auth-cache.json`.
- **403 at login** — the person is not on the server's allow-list
  (`ENGRAM_SERVE_ALLOW`); that is the operator's to change.

Never ask anyone to paste a token into a chat.

## Addresses

```
<owner>/<repo>/<area>/<name>.md
```

- `<owner>` and `<repo>` are the document root, and they are **columns in the
  store, not directory levels**. They come from the working repo's `origin`,
  never chosen. The MCP server cannot see which checkout a call is about, so the
  model always sends the **full** path.
- `<area>` is one of `projects` · `areas` · `resources` · `archives`.
- A repo hub MOC is the exception with no area: `<owner>/<repo>/README.md`.
- At most **5 levels below the document root**. The rule is a depth CEILING, not
  a minimum segment count — written as a minimum it rejects the repo hub, which
  the store indexes and serves happily.
- Accepted extensions: `.md`, `.dbml`.

## The tools

| Operation | Tool |
|---|---|
| search | `brain_search` (`query`, `tier`, `boostRepo`, `onlyRepos`, `archives`, `limit`) |
| read one document | `brain_get` (`path`) — body, `sha256`, links, backlinks, recent history |
| change history | `brain_revisions` (`path`, `limit`) |
| link-graph health | `brain_integrity` (`limit`) |
| create / replace | `brain_put` (`path`, `body`, `note`, `dryRun`) |
| change a part | `brain_patch` (`path`, `edits`, `note`, `baseSha256`, `dryRun`) |
| move / archive | `brain_move` (`path`, `to`, `dryRun`) |
| vote a document useful / noise | `brain_feedback` (`paths`, `kind`, `note`, `searchId`) |

What sessions cost and the tier-1 hit rate are on the web viewer's `/usage`
page, not a tool.

**Search answers in tiers.** Tier 1 (the default) is up to four documents as
snippets, one chunk each — a quarter of a full page's tokens with the same
recall on the bench. If the answer is not on it, call again with the **same
question** and the tier the result names (`next`): 2 is the full chunks, 3 adds
archives and drops the repo boost. Raise the tier before rephrasing; a
rephrased question is a new search and a vote on it teaches the ranking
nothing about the first one.

**A vote is how the ranking learns.** When a brain document settled something,
`brain_feedback` with its path (the session's last search is attached by
itself). The document then comes first for that question and ones like it;
opening a hit with `brain_get` is recorded as a weaker vote without any call.
It is a bonus, never a filter — the document must still match the question.

There is deliberately **no delete tool**. The contract is *never delete, move to
archives*. The store's soft delete stays reachable for an operator, not for an
agent.

`brain_put` is an upsert: create and update are the same call, the previous body
goes to `revisions`, and an identical body answers `unchanged` instead of
writing again. A `note` is required — a history of "updated" tells you nothing a
timestamp did not.

`brain_patch` edits in place: each edit addresses a `section`, a unique
`anchor`, or a line range, with `expect` (the text you believe is there) and
`baseSha256` (from `brain_get`). Edits apply together or not at all, and a
mismatch is refused rather than guessed — read again and retry.

`brain_move` keeps the old path as an **alias**, so a `[[old-name]]` written
elsewhere keeps resolving. Edges point at an immutable document id, so a move
breaks nothing that had already resolved.

## Scope is the confidentiality boundary

The store's `ENGRAM_OWNERS` lists the owner groups it admits; anything else is
refused with **403**. It is a list of GROUPS rather than repos on purpose:
enumerate repos and the list falls behind the day someone creates one. An empty
`ENGRAM_OWNERS` admits nothing — a deployment that forgot to configure it closes
rather than opens.

Everyone who can log in reads everything in the store, so the `owner`/`repo`
columns cannot protect confidentiality — what must not be readable is therefore
**never let in**, and that is enforced at write time rather than by anyone
remembering where they are standing.

A 403 is not an error to retry and not a cue to write somewhere else. The store
is alive and declined: knowledge from an unadmitted repo does not go into this
brain, and there is no local brain to take it either. Say so.

## The byline is the login

Every revision records an `author`, and the remote server stamps it with the
logged-in person (the part of their email before `@`, or the server's fixed
`ENGRAM_SERVE_AUTHOR`). The tools' `author` argument is ignored. Attribution is
therefore authenticated, not claimed.

## No fallback, no queue

An unreachable store fails on the spot for reads and writes both. A stale cached
answer and a spool sitting somewhere both manufacture the belief that it worked,
and that belief outlives the outage. The honest answer to "the brain is down" is
to say so.

## Running a store

The server is one compose file — see `server/` in the engram repository — and
the remote MCP is `engram serve` beside it (`server/deploy/engram-serve.service`,
its settings in an environment file).

```bash
cp .env.example .env      # POSTGRES_PASSWORD, ENGRAM_INGEST_TOKEN, ENGRAM_OWNERS
docker compose up -d
```

Seed an existing tree of notes with `bin/import_tree.py`. Back it up with
`deploy/backup.sh`: the canonical copy is in the database and there is no copy
of it anywhere else.
