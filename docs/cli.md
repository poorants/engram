# CLI and MCP reference

One binary, two surfaces over one transport client: the MCP tools a model calls
in a session, and `engram <verb>` for everything that is not one — a hook, a
scheduled job, the skill, a person at a terminal. They are the same code — two
clients would mean two places to put the token, two default authors, and two
copies of the path rules to drift apart.

Every command prints **for a person on stdout** and takes `--json` for a machine.
Errors go to stderr, and the exit code — not the message text — is the contract.

## Global flags

| Flag | Meaning |
|---|---|
| `--store <url>` | override the configured store for this one call |
| `--json` | machine-readable output instead of the human report |

Environment beats the config file for every setting: `ENGRAM_STORE_URL`,
`ENGRAM_TOKEN`, `ENGRAM_AUTHOR`, `ENGRAM_CONFIG_DIR`.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | ok |
| `1` | error — usage, validation, a bad or missing token (401), or the store said no for a reason of its own |
| `3` | the store **refused this path's owner group** (403) **and no local file brain took the document** |
| `4` | the store could not be **reached at all** |

`3` and `4` are separate on purpose, and a caller should treat them differently.
A refusal means the store is alive and declined: the document belongs in a local
file brain instead, and retrying will never help — so `engram put` writes it
there itself and exits `0`, because the document landed. `3` is what is left:
refused with nowhere to put it. Unreachable means try again later — there is no
queue and no cache, so nothing was written anywhere.

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
| `--chars <n>` | characters shown per chunk in the human report (default 400) |

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

**When the store refuses the owner group (403), the document is written to the
local file brain** — where it belonged — and the report says where it landed:

```
local: the store refused this path (…) → wrote the local file brain: /home/me/brain/resources/logging.md
```

The `<owner>/<repo>` coordinates are dropped on the way in: in a file brain the
directory IS the scope, so keeping them would nest the vault one repo deep inside
itself. With no file brain designated the write lands nowhere, and that is the
only case that exits `3`.

### `engram patch <path>`

Change **part** of a document. A put prices an edit by the size of the document;
this prices it by the size of the change, which for a one-line fix in a long
guide is the whole difference.

One edit per invocation — batches are the API's and the MCP tool's business,
because a shell carrying several addressed edits would need a file format of its
own, and "where does this edit go" must not be decided in two places.

Address it one of three ways, and only one per call:

| flag | addresses | notes |
|---|---|---|
| `--section` | a heading and everything under it | matches the text, the raw `## line`, or the full heading path search prints. Two matches is a refusal, never a guess |
| `--anchor` / `--anchor-file` | an exact substring | must occur **exactly once**; matched literally, not as a pattern |
| `--lines START:END` | a line range | `END` is **exclusive** — one line 12 is `12:13`, and `12:12` inserts before line 12 |

`--expect-file` holds the text you believe is there right now. It is compared
character for character, and it is what turns matching into proof: an edit
aimed at the wrong place is refused instead of applied. It is **required** with
`--lines`, because a line number by itself proves nothing about what is on it.

`--base` takes the `sha256` that `get --json` reported. It is the only thing
that catches an edit which is right about its own range and wrong about the
document — someone else changed the rest of it in between.

```bash
engram get acme/shared/areas/blog/writing-style.md --json | jq -r .sha256 > .base
sed -n '120,128p' current.md > .expect            # what is there now
engram patch acme/shared/areas/blog/writing-style.md \
  --section "## 옵트아웃 옵션 (향후)" --expect-file .expect --file new-section.md \
  --base "$(cat .base)" --note "retire the dead automation spec"
```

`--dry-run` prints a unified diff and writes nothing. `--file /dev/null` deletes
the addressed range; the store still refuses a patch that would leave the
document empty. Every edit lands or none does, and the result is **one ordinary
revision** — reversible exactly like a put.

Exit `1` covers both refusals: a malformed call (400) and a document that
disagrees with the request (409 — an ambiguous address, an `expect` that does
not match, a stale `--base`). The difference is in the message, and a 409 means
re-read before retrying rather than retrying unchanged.

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
| `--token <t>` | the store's token — reads and writes alike, stored at mode `0600` beside the config |
| `--author <a>` | the byline stamped on revisions from this machine |

Without `--token` the machine can only reach a store that has
`ENGRAM_PUBLIC_READS=true`, and cannot write to any store. That is a legitimate
configuration and not an error — but on a store with the default settings it
means the machine cannot do anything at all, which `store doctor` will say.

The token is the store's one credential, not a write-specific one: holding it
grants reads and writes alike. There is nothing to hand out that permits
reading without also permitting writing.

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

The version, the platform it was built for, and the Go toolchain that built it.

## The file brain

A file brain is what covers a repo the store does not admit, and a machine that
has no store at all. These commands are pure filesystem work — nothing here
touches the network.

### `engram resolve`

Which brain feeds this directory, and why. Every other part of engram asks this
first, so it is also the command to run when something wrote to a place you did
not expect.

```
store=https://brain.example scope=acme/webapp source=store fallback_vault=/repo/brain
base=/home/me/brain/brain label=brain source=shared
```

`source` is one of `store` · `absorb` (you are inside the designated file brain)
· `shared` (it is designated and you are elsewhere) · `local` (an undesignated
`brain/` in this repo) · `none`. **The store is asked first and the answer stops
there**: a not-yet-migrated `para/` sitting in an admitted repo is a leftover,
not a second brain, and `warning` says so.

### `engram brain show | set <path> | unset`

The designation of THE shared file brain — one per environment, stored under the
fixed name `shared`. `set` **replaces** rather than adds: two vaults means a coin
flip about where a refused document went. Un-designating leaves the directory
completely alone.

### `engram init`

Create the four PARA folders with a `.gitkeep` in each. Idempotent — running it
twice is a no-op, not a reset.

| Flag | Meaning |
|---|---|
| `--output <dir>` | base directory (default: here) |
| `--flat` | categories at the base, with no nested prefix |
| `--nested-dir <name>` | nested folder name (default `brain`; reuses a legacy `para/` if present) |

### `engram lint`

Broken links, orphans, **weak nodes** and the density metrics of a file brain.
Exit is always `0` — a `[[wikilink]]` may point at a note not written yet, so
problems are reported and never block work.

| Flag | Meaning |
|---|---|
| `--base <path>` | force the PARA base instead of resolving it |
| `--all` | print the summary even when the graph is clean |
| `--json` | the full report, metrics included |

In store mode it says so and scans nothing: the link graph is a table there, and
`engram integrity` is the check that reads it. The exception is a repo the store
refuses — there the local vault really is that repo's brain, and it is linted
normally.

### `engram weave`

The concrete moves that would raise the density, ranked. Advisory only: it never
touches a file.

- **missing_links** — a document already names another note in prose but does not
  link it. The cheapest way to dissolve a lonely spoke, and spokes rank first.
- **concept_candidates** — a term recurring across documents in *different*
  folders with no note of its own.

### `engram link`

Write or refresh this repo's `CLAUDE.md` brain pointer, so a session without the
engram skill still knows a brain exists and where to look. The block is
marker-delimited and idempotent; `--remove` strips it, and deletes `CLAUDE.md`
only when the block was the whole of it.

## `engram hook`

The capture-loop hook. It reads a Claude Code hook payload on stdin and, at the
right moment, injects an instruction to reflect on the session and save what is
worth keeping. The plugin registers it on `UserPromptSubmit` and `Stop`; it is
not a command to run by hand.

It never fails a session — every path exits `0`, including a panic, malformed
stdin and an unknown event. A hook that exits non-zero puts an error in front of
the user on every single prompt, which is worse than a missed reflection.

| Variable | Default | Effect |
|---|---|---|
| `ENGRAM_CAPTURE_DISABLE` | unset | `1` turns both hooks off |
| `ENGRAM_CAPTURE_COOLDOWN_MIN` | `30` | minutes between `Stop`-backstop nudges |
| `ENGRAM_CAPTURE_PHRASES` | built-in list | comma-separated wrap-up phrases |

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
| `brain_patch` | `engram patch` |
| `brain_move` | `engram move` |

There is deliberately no `brain_delete` — see [concepts](concepts.md#never-delete).

The MCP server reads the same config as the CLI, so a session inherits whatever
`engram store set` designated. It does not take a store URL or a token of its
own; there is one designation per machine and one place to change it.
