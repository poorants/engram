<h1 align="center">engram</h1>

<p align="center">
  <b>A networked PARA knowledge brain for coding agents.</b><br>
  Documents managed by PARA, woven into one connected graph, searched instead of grepped.
</p>

<p align="center">
  <a href="https://github.com/poorants/engram/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/poorants/engram/ci.yml?branch=main&style=flat-square&label=ci" alt="CI"></a>
  <a href="https://github.com/poorants/engram/releases/latest"><img src="https://img.shields.io/github/v/release/poorants/engram?style=flat-square&label=release" alt="Latest release"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/poorants/engram?style=flat-square&label=go" alt="Go version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/poorants/engram?style=flat-square&label=license" alt="MIT license"></a>
</p>

---

> A session dies; the knowledge should not. The measure is not investigating the
> same question twice.

An agent puts what it concluded into a shared store, and the next session — a
different repo, a different machine, a different person — searches it and reads
it back. A file brain is limited by grep; a wiki is too heavy for an agent to
write to. This sits in between.

```text
brain_search  { "query": "why did we drop the vector channel" }

acme/shared/resources/search-ranking.md ¶ Search ranking > Why lexical only
  Measured with and without a vector channel: recall was the same and the
  failures were the same questions. The production image does not carry the
  extension, and the deployment stays one compose file.
```

The model gets the passage with its heading path — never a whole file to read
end to end.

## Why engram

- **Searched, not grepped.** Postgres full-text over chunks, with the heading path — an agent gets the passage, not a file to read end to end.
- **A graph, not a folder tree.** Bi-directional links, MOC hubs, and a lint that catches broken links, orphans and weakly-connected notes.
- **Nothing is lost.** Every document keeps a revision history with who changed it and why. There is no delete — the contract is *move to archives*.
- **Shared safely.** The store admits a list of owner groups; a document from a repo outside them is refused, so knowledge that should not be there cannot get in by accident.
- **Nothing on the machine.** A session reaches the brain through one remote MCP server with its own login; a person's machine holds no address, no token, no settings.

## Quick start

### 1. The store and its server — once, for everyone

One Linux or macOS host with Docker. Postgres, so not Windows.

```bash
git clone https://github.com/poorants/engram && cd engram/server
./setup.sh --owners <your-github-org>
```

It generates both secrets, writes `.env`, brings the compose stack up and waits
until it actually answers. Beside it runs `engram serve`, the remote MCP server:
it holds the store's credential and is its own OAuth server, so people log in
with Google and only the allow-list gets in. Settings and the systemd unit:
[docs/install.md](docs/install.md#engram-serve).

### 2. A person's machine — every person, every machine

```bash
claude mcp add --transport http --scope user --callback-port 33418 engram https://<host>/mcp
```

Then in Claude Code: `/mcp` → engram → **Authenticate**, and run

```
/mcp__engram__setup
```

The server hands the session a setup procedure with its own address written
in: it checks the registration, installs the plugin (the skill and the capture
hooks) and the `engram` binary the hooks run, and reports what it fixed.
Restart Claude Code afterwards.

> **Platforms.** A person's machine can be Linux, macOS or Windows. The store is
> Linux/macOS only. Details, SSH machines and upgrading from 0.11:
> [docs/install.md](docs/install.md).

## What it is made of

| | What | Who installs it |
|---|---|---|
| **[`server/`](server/)** | the store: FastAPI + Postgres 17, one compose file | once, on a machine everyone can reach |
| **`engram serve`** | the remote MCP server — the `brain_*` tools over HTTP, with its own OAuth | once, beside the store |
| **[`skills/engram/`](skills/engram/)** + hooks | the Claude Code plugin — the judgement, the workflows, and the capture loop | `/mcp__engram__setup`, per machine |
| **`engram`** on a person's machine | one static binary that runs the plugin's hooks and nothing else | `/mcp__engram__setup` ([`install.sh`](install.sh) / [`install.ps1`](install.ps1)) |

There is exactly **one surface** to the brain: the remote MCP server. No CLI,
no local MCP server, no local copy — a setting on a person's machine is a
setting that can be stale, and two surfaces mean two copies of the address rules
to drift apart. The hook binary decides whether to speak from the git origin
alone, with no settings file and no network.

## The tools an agent gets

| Tool | What it does |
|---|---|
| `brain_search` | the one ranking — returns chunks with their heading path, not whole files. Answers in tiers: snippets first, full chunks on request |
| `brain_get` | one document: body, outgoing links, backlinks, recent history |
| `brain_feedback` | say a document answered (or got in the way) — the ranking learns, and the next similar question finds it first |
| `brain_revisions` | who changed it, when, and why |
| `brain_integrity` | broken links, orphans, weak nodes |
| `brain_put` | save (create and update are one upsert; the previous body is kept) |
| `brain_patch` | change part of one — send the edit, not the document |
| `brain_move` | rename, reclassify, archive — the old path stays as an alias |

Every write is stamped with the logged-in person. The full contract is in
[`skills/engram/references/store.md`](skills/engram/references/store.md).

## Documentation

| | |
|---|---|
| [Installation](docs/install.md) | a person's machine, `engram serve`, upgrading from 0.11, building from source |
| [Concepts](docs/concepts.md) | how a document is addressed, PARA areas, links, the scope boundary |
| [Self-hosting the store](server/README.md) | configuration, seeding, backups |
| [Design decisions](docs/design.md) | what was chosen and what it cost |
| [Troubleshooting](docs/troubleshooting.md) | when the tools are missing, the login loops, or a write is refused |
| [Search bench](server/bench/README.md) | how ranking is measured and kept from drifting |

## Status

Extracted from three private repositories and made general, then measured
against its own bench before release. What was cut, what was kept, and what was
deliberately left out is written up in the example brain that ships with the
server: [`public-release.md`](server/bench/corpus/projects/public-release.md).

Not multi-tenant. One service on a LAN or a personal server.

## Contributing

Issues and pull requests are welcome — start with
[CONTRIBUTING.md](CONTRIBUTING.md). Ranking changes need a
[bench](server/bench/README.md) run in the PR; that is the one hard rule.

Several things are **decided** and have reasons written down in
[docs/design.md](docs/design.md) — no delete, no offline cache or write queue,
one ranking, lexical search by default, not multi-tenant. Arguments against them
are welcome; patches that quietly work around them are not.

Security: please report privately — see [SECURITY.md](SECURITY.md). Changes are
listed in [CHANGELOG.md](CHANGELOG.md).

## License

MIT — see [LICENSE](LICENSE).
