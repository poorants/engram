# CLAUDE.md

Guidance for Claude Code working in this repo.

## Build

```bash
make build            # ./engram
make test             # go test ./...
make lint             # gofmt -l . && go vet ./...
```

No `make` on the machine (common in Git Bash on Windows)? The target is one line:

```bash
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty)" \
  -o engram ./cmd/engram
```

`CGO_ENABLED=0` is not optional — it is what makes the result a static binary
with nothing to install alongside it.

## What the binary is

`engram serve` (the remote MCP server, beside the store) and `engram hook` (the
plugin's capture-loop hook on a person's machine) — plus `version` and `help`.
There is no client CLI, no stdio MCP server, no local file brain and no
self-update: a session reaches the brain only through `engram serve`. Do not add
a client-side way to the store; the hook decides from the git origin alone and
must stay offline (it runs on every session start and every turn).

## Releasing — a patch goes straight to main

Do not hoard fixes in `[Unreleased]`. A release here is a tag and a CI build; it
costs nothing, and a fix that sits unreleased is a fix nobody has.

**Patch (`0.9.0` → `0.9.1`) — commit straight to `main`, no branch.** For a bug
fix, a wording fix, a doc fix: anything that adds no surface.

One commit, not two: write the CHANGELOG entry under a dated `## [0.9.1]`
heading (not `[Unreleased]`) as part of the fix, and tag it.

```bash
# on main, working tree clean
#   CHANGELOG.md: new "## [0.9.1] — <date>" section + its compare/ link
git commit -m "<what changed and why>"
git tag -a v0.9.1 -m "engram v0.9.1 — <one line>"
git push origin main && git push origin v0.9.1
```

A separate `Cut` commit earns its place only in the minor flow, where the
release date is not known until the branch merges.

**Minor (`0.9.x` → `0.10.0`) — branch, then `Merge: <topic>`.** For anything
that adds a flag, a verb, a hook, a build tag, or changes a contract. Branch,
push, `git merge --no-ff` with a `Merge: …` subject, then the same `Cut` commit
and tag on `main`.

Either way `Cut X.Y.Z` touches **only** `CHANGELOG.md` — move the entry out of
`[Unreleased]`, date it, and add its `compare/` link at the foot of the file.

**A change to `.claude-plugin/marketplace.json` is not carried by a binary
update.** Hooks and skills live in the plugin clone, so say so in the CHANGELOG
and tell the user to run it — this is the drift `0.6.0` warned about, and it is
how a machine ends up on a new binary with the old hooks:

```bash
claude plugin marketplace update engram
claude plugin update engram@engram   # applies after a Claude Code restart
```

A binary change reaches machines when they re-run the installer (or
`/mcp__engram__setup`) — there is no self-update; the serve host re-runs it
with `--dir /opt/engram-serve/bin` and restarts `engram-serve`. `go build` output in the tree
is not what the plugin's `engram hook` runs.

## Knowledge lives in the brain, not in files

This repo is wired to an engram store. Search it before grepping for anything
that is knowledge rather than code — decisions, conventions, traps, runbooks.
Record what is worth keeping with `brain_put` / `brain_patch`, never by writing
a Markdown file into the tree.
