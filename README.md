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

```console
$ engram search "why did we drop the vector channel"
{
  "results": [
    {
      "path": "acme/shared/resources/search-ranking.md",
      "heading": "Search ranking > Why lexical only",
      "chunk": "Measured with and without a vector channel: recall was the same
                and the failures were the same questions. The production image
                does not carry the extension, and the deployment stays one
                compose file."
    }
  ]
}
```

The CLI answers in JSON because its caller is usually a subprocess. In a session
the same query goes through `brain_search` and the model gets the passage with
its heading path — never a whole file to read end to end.

## Why engram

- **Searched, not grepped.** Postgres full-text over chunks, with the heading path — an agent gets the passage, not a file to read end to end.
- **A graph, not a folder tree.** Bi-directional links, MOC hubs, and a lint that catches broken links, orphans and weakly-connected notes.
- **Nothing is lost.** Every document keeps a revision history with who changed it and why. There is no delete — the contract is *move to archives*.
- **Shared safely.** The store admits a list of owner groups; a document from a repo outside them is refused, so knowledge that should not be there cannot get in by accident.
- **One static binary.** No toolchain, no runtime. MCP server and CLI in the same file.

## Quick start

Two scripts, and they set up two different kinds of machine.

### 1. The store — once, for everyone

One Linux or macOS host with Docker. Postgres, so not Windows.

```bash
git clone https://github.com/poorants/engram && cd engram/server
./setup.sh --owners <your-github-org>
```

It generates both secrets, writes `.env`, brings the compose stack up, waits
until it actually answers, and finishes by printing the exact client one-liner —
store address and store token already filled in — for you to hand out.

### 2. The client — every person, every machine

Binary, MCP server, skill and capture hooks, in one command. Paste what
`setup.sh` printed:

```bash
# Linux · macOS
curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh \
  | sh -s -- --store http://<host>:8081 --token <store token>
```

```powershell
# Windows
$env:ENGRAM_STORE_URL = 'http://<host>:8081'
$env:ENGRAM_TOKEN     = '<store token>'
irm https://raw.githubusercontent.com/poorants/engram/main/install.ps1 | iex
```

That installs the binary, designates the store, runs `store doctor`, registers
the `brain_*` MCP tools at user scope, and installs the skill and its hooks. Run
it with no arguments to install the binary only and wire the rest up later; add
`--no-claude` to skip the Claude Code half entirely.

`store doctor` is the check that matters: it proves the store answers **and**
that this machine can write to it. Those are two different facts, and a check
that proves only the first lets someone finish a setup read-only and discover it
at the end of a session, when a save fails.

> **Platforms.** The client — binary, CLI, MCP server, skill — runs on Linux,
> macOS and Windows. The store is Linux/macOS only; a Windows machine is a
> client of a store running elsewhere. Details in [docs/install.md](docs/install.md).

## What it is made of

Three deliverables in one repository. The layers are not collapsed, because each
one is a different kind of thing.

| | What | Who installs it |
|---|---|---|
| **`engram`** | one static binary: MCP server + CLI — the whole client | a person, per machine — [`install.sh`](install.sh) |
| **[`server/`](server/)** | the store: FastAPI + Postgres 17, one compose file | once, on a machine everyone can reach |
| **[`skills/engram/`](skills/engram/)** | the Claude Code skill — the judgement and the workflows | the plugin marketplace |

There is exactly **one** transport client with three surfaces over it: the MCP
tools for a model in a session, `engram <verb>` for a subprocess, and the skill's
Python wrapper. Two clients would mean two places to put the token, two default
authors, and two copies of the path rules to drift apart.

## The tools an agent gets

| Tool | What it does |
|---|---|
| `brain_search` | the one ranking — returns chunks with their heading path, not whole files |
| `brain_get` | one document: body, outgoing links, backlinks, recent history |
| `brain_revisions` | who changed it, when, and why |
| `brain_integrity` | broken links, orphans, weak nodes |
| `brain_put` | save (create and update are one upsert; the previous body is kept) |
| `brain_move` | rename, reclassify, archive — the old path stays as an alias |

Same six over the CLI as `engram search|get|revisions|integrity|put|move`.
Full reference: [docs/cli.md](docs/cli.md).

## Documentation

| | |
|---|---|
| [Installation](docs/install.md) | the two installers, every platform, upgrading, building from source |
| [Concepts](docs/concepts.md) | how a document is addressed, PARA areas, links, the scope boundary |
| [CLI & MCP reference](docs/cli.md) | every verb, flag, and exit code |
| [Self-hosting the store](server/README.md) | configuration, seeding, backups |
| [Design decisions](docs/design.md) | what was chosen and what it cost |
| [Troubleshooting](docs/troubleshooting.md) | when `store doctor` fails |
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
