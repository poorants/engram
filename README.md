# engram

**A networked PARA knowledge brain for coding agents.**

Documents are managed by PARA (Projects / Areas / Resources / Archives) and woven
into one connected graph rather than a folder tree. Search is Postgres full-text,
not grep. Every document keeps a revision history. Broken links, orphans and
weakly-connected notes are caught by a lint.

> A session dies; the knowledge should not. The measure is not investigating the
> same question twice.

An agent puts what it concluded into the store, and the next session — a different
repo, a different machine, a different person — searches it and reads it back. A
file brain is limited by grep; a wiki is too heavy for an agent to write to. This
sits in between.

## Install

One static binary, from GitHub Releases. No toolchain, no runtime, nothing to
build:

```bash
curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh | sh
```

Then point it at a store and prove it works:

```bash
engram store set http://<host>:8081 --token <write token>
engram store doctor
```

Add the skill to Claude Code:

```bash
claude plugin marketplace add poorants/engram
claude plugin install engram@engram
```

Register the MCP server so a session gets the `brain_*` tools:

```bash
claude mcp add engram -- engram mcp
```

No store yet? One comes up with Docker on any machine — see [server/](server/).

## What it is made of

Three deliverables in one repository. The layers are not collapsed, because each
one is a different kind of thing.

| | What | Who installs it |
|---|---|---|
| **`engram`** | one static binary: MCP server + CLI. The whole client. | a person, per machine — `install.sh` |
| **`server/`** | the store: FastAPI + Postgres 17, one compose file | once, on a machine everyone can reach |
| **`skills/engram/`** | the Claude Code skill — the judgement and the workflows | the plugin marketplace |

A skill cannot be an MCP server: a skill is instructions a model reads, an MCP
server is a process. A server cannot be a client: there is one store and as many
clients as there are people. And there is exactly **one** transport client with
three surfaces over it — the MCP tools for a model in a session, `engram <verb>`
for a subprocess, and the skill's Python wrapper. Two clients would mean two
places to put the token, two default authors, and two copies of the path rules to
drift apart.

## How a document is addressed

```
<owner>/<repo>/<area>/<name>.md
  acme / webapp / resources / logging.md
```

`owner` and `repo` are **derived from the git remote, never chosen**. That is the
confidentiality boundary: the server admits a list of owner groups, and a repo
outside them is refused with 403 — so knowledge from a repo that should not be in
the store cannot get in because somebody forgot where they were. A refused
document goes to a local file brain instead, which is where it belonged.

`area` is one of `projects` · `areas` · `resources` · `archives`, and at most five
levels sit below the document root. A repo hub MOC is the one address with no
area: `<owner>/<repo>/README.md`.

## The tools an agent gets

| Tool | What it does |
|---|---|
| `brain_search` | the one ranking — returns chunks with their heading path, not whole files |
| `brain_get` | one document: body, outgoing links, backlinks, recent history |
| `brain_revisions` | who changed it, when, and why |
| `brain_integrity` | broken links, orphans, weak nodes |
| `brain_put` | save (create and update are one upsert; the previous body is kept) |
| `brain_move` | rename, reclassify, archive — the old path stays as an alias |

There is deliberately no `brain_delete`. The contract is *never delete, move to
archives*.

## Design decisions worth knowing before you rely on it

**One ranking.** The web viewer and `brain_search` hit the same `/api/search`. If
there were two, the order a person saw and the order an agent received would
drift and nobody could reason about either.

**No fallback and no queue.** If the store is unreachable, reads and writes both
fail on the spot. A stale cached answer and a spool sitting somewhere both
manufacture the belief that it worked, and that belief outlives the outage. A
scope refusal is a different thing and takes the other path — the store is alive
and declined, so the document goes to the local file brain.

**The byline is a claim, not a proof.** The write token is one shared credential,
so the recorded author is what the client says it is. The goal is to make it
honest by default (`ENGRAM_AUTHOR` → `git config user.name` → `$USER`), not
provable — proving it means accounts, issuing and revocation, which is a
different system.

**Lexical search, not vectors, by default.** Measured with and without a vector
channel, recall was the same and the failures were the same questions. The
production image does not carry the extension, and the deployment stays one
compose file. That property decides the installation barrier.

**Not multi-tenant.** One service on a LAN or a personal server.

## Repository layout

```
cmd/engram/          the binary — MCP server, CLI, store setup
pkg/brain/           the HTTP client and the address rules
pkg/config/          settings, shared with the skill
pkg/identity/        who a revision says wrote it
server/              the store: app, schema, compose file, bench
skills/engram/       the Claude Code skill
install.sh           one-line installer, from GitHub Releases
```

Development:

```bash
make build test lint     # the binary
make server-up           # a store on localhost:8081
make bench               # search regression check
```

## Status

Extracted from three private repositories and made general, then measured
against its own bench before release. What was cut, what was kept, and what was
deliberately left out is written up in the example brain that ships with the
server: [`server/bench/corpus/projects/public-release.md`](server/bench/corpus/projects/public-release.md).

## License

MIT — see [LICENSE](LICENSE).
