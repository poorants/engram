# CLI and MCP reference

One binary, three surfaces over one transport client: the MCP tools a model
calls in a session, `engram <verb>` for a subprocess, and the skill's Python
wrapper. They are the same code — two clients would mean two places to put the
token, two default authors, and two copies of the path rules to drift apart.

Every CLI command prints **JSON on stdout** and human-readable errors on stderr.
The caller is usually a program, so there is no second, prettier output format
to keep in agreement with the first.

## Global flags

| Flag | Meaning |
|---|---|
| `--store <url>` | override the configured store for this one call |

Environment beats the config file for every setting: `ENGRAM_STORE_URL`,
`ENGRAM_TOKEN`, `ENGRAM_AUTHOR`, `ENGRAM_CONFIG_DIR`.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | ok |
| `1` | error — usage, validation, or the store said no for a reason of its own |
| `3` | the store **refused this path's owner group** (403) |
| `4` | the store could not be **reached at all** |

`3` and `4` are separate on purpose, and a caller should treat them differently.
A refusal means the store is alive and declined: the document belongs in a local
file brain instead, and retrying will never help. Unreachable means try again
later — there is no queue and no cache, so nothing was written.

## Reading

### `engram search <question>`

Pass the question as a sentence, not keywords. Returns chunks with their heading
path, ranked by the one ranking — the same `/api/search` the web viewer uses.

| Flag | Meaning |
|---|---|
| `--limit <n>` | maximum results, 1..50 |
| `--archives` | also search archived documents (excluded by default) |
| `--boost-repo <repo>` | lift this repo's documents — a **boost**, not a filter |
| `--only-repo <a,b>` | restrict to these repos — a **filter** |
| `--only-owner <a,b>` | restrict to these owners |

```bash
engram search "how do we handle a token rotation" --limit 5
engram search "the postgres locale decision" --boost-repo webapp
```

Boost and filter are different tools. A boost says *this repo is more likely to
be relevant*; a filter says *nothing else may be returned*, which is how you
miss the answer that was written up in another repo.

### `engram get <path>`

One document: body, outgoing links, backlinks, recent history.

| Flag | Meaning |
|---|---|
| `--out <file>` | write the body to this file and omit it from the response |

`--out` exists so a large document can be handed to a tool that wants a file
without the body also passing through a model's context.

### `engram revisions <path>`

Who changed it, when, and the note they gave for why. `--limit <n>`.

### `engram integrity`

Broken links, orphans and weak nodes across the store. `--limit <n>` caps each
category. This is the health check for the graph — run it when the store starts
feeling like a folder tree again.

## Writing

### `engram put <path>`

Create and update are one upsert; the previous body is kept as a revision.

| Flag | Meaning |
|---|---|
| `--file <path>` | body file (stdin when omitted) |
| `--note <text>` | **required** — one line on why this revision exists |
| `--author <name>` | recorded author (resolved automatically when omitted) |
| `--dry-run` | validate the path and body without writing |

```bash
engram put acme/webapp/resources/logging.md \
  --file notes.md --note "structured logging decision, replaces the ad-hoc format"

echo "..." | engram put ./resources/logging.md --note "first pass"
```

`--note` is required because a history of changes with no reasons is a history
nobody can use. `--dry-run` checks the owner group and the address rules against
the live store, so it is the cheap way to find out whether a write would be
refused before assembling the body.

### `engram move <path> <new path>`

Rename, reclassify, archive. The old path **stays as an alias**, so links into
the document survive. `--author`, `--dry-run`.

```bash
engram move acme/webapp/projects/migration.md acme/webapp/archives/migration.md \
  --author "$(git config user.name)"
```

There is no delete. Archiving is the disposal path, and it keeps the reasons.

## Setup and diagnosis

### `engram store set <url>`

| Flag | Meaning |
|---|---|
| `--token <t>` | the write token — stored at mode `0600` beside the config |
| `--author <a>` | the byline stamped on revisions from this machine |

Without `--token` the machine is set up read-only, which is a legitimate
configuration and not an error.

### `engram store show`

Where every setting came from — file, environment, or default — without
touching the network.

### `engram store doctor`

Proves the store answers **and** that this machine can write to it. Those are
two different facts, and a check that proves only the first lets someone finish
a setup read-only and discover it at the end of a session, when a save fails.

### `engram store unset`

Remove the designation. `--forget-token` also deletes the token file.

### `engram status`

Connection, scope, and who you write as — the one-line version of `store show`
plus `scope`.

### `engram scope`

The `owner/repo` this directory's git origin resolves to. Nothing is chosen
here; this reports what the address rules derive. Run it when a write is refused
and you are not sure which repo the session thinks it is in.

### `engram version`

## `engram mcp`

Runs the MCP server over stdio. This is what a Claude Code session launches; it
is not a command to run by hand.

```bash
claude mcp add engram -- engram mcp                 # this project
claude mcp add --scope user engram -- engram mcp    # every project
```

The six tools it exposes:

| Tool | CLI equivalent |
|---|---|
| `brain_search` | `engram search` |
| `brain_get` | `engram get` |
| `brain_revisions` | `engram revisions` |
| `brain_integrity` | `engram integrity` |
| `brain_put` | `engram put` |
| `brain_move` | `engram move` |

There is deliberately no `brain_delete` — see [concepts](concepts.md#never-delete).

The MCP server reads the same config as the CLI, so a session inherits whatever
`engram store set` designated. It does not take a store URL or a token of its
own; there is one designation per machine and one place to change it.
