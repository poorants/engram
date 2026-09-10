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

## Frozen builds — `-tags noupdate`

**When the user asks to install or update engram on a company / managed /
work machine, build the frozen variant. Do not run `engram update`, and do not
point them at `install.ps1` or `install.sh`.**

```bash
make build-frozen     # ./engram, self-update compiled out
```

Then replace the installed binary by plain copy (stop any running `engram mcp`
first, or Windows will refuse the write):

```bash
# Windows
Get-Process engram | Stop-Process -Force
Copy-Item .\engram.exe "$env:LOCALAPPDATA\engram\bin\engram.exe" -Force
Remove-Item "$env:LOCALAPPDATA\engram\bin\engram.exe.old*" -Force -EA SilentlyContinue
```

Verify: `engram version` prints a `+noupdate` suffix and returns in well under a
second. If it hangs for tens of seconds, the endpoint scanner is still holding
it — see below.

### Why

A managed endpoint's behaviour scanner suspends an unsigned binary that
downloads an executable, renames itself aside and runs what it just wrote. That
is step for step what `engram update` does, and step for step what a dropper
does. Observed on AhnLab V3: **new `engram.exe` processes were suspended before
the Go runtime started** (`Threads=1, CPU=0.00`) while already-running MCP
servers kept serving — so the store still worked and only the hooks silently
died. An allowlist entry is usually not the user's to grant, so the way out is a
binary that cannot rewrite itself.

`-tags noupdate` drops the version check, the download and the swap
([pkg/selfupdate/apply.go](pkg/selfupdate/apply.go),
[cmd/engram/selfupdate_cmd.go](cmd/engram/selfupdate_cmd.go)). Call sites need
no change: `newUpdateChecker` returns `Fetch: nil, Path: ""`, and the existing
nil guards in [cmd/engram/mcp.go](cmd/engram/mcp.go) and
[cmd/engram/cli.go](cmd/engram/cli.go) then make no network call and read no
cache. `engram update` still exists and explains that this build is frozen.

When adding code that reaches for a release, put it behind `//go:build
!noupdate` and give the frozen build a stub in
[cmd/engram/update_frozen.go](cmd/engram/update_frozen.go). Keep both variants
compiling: `go vet ./... && go vet -tags noupdate ./...`.

Full background, including the diagnosis, is in the brain at
`poorants/engram/resources/release-and-upgrade-traps.md`.

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

After releasing on a managed machine, reinstall the **frozen** build (above) —
`go build` output in the tree is not what the plugin's `engram hook` runs.

## Two tests fail on Windows, and always have

`pkg/config` `TestTokenIsNotWrittenIntoTheConfigFile` (mode 666, want 600) and
`pkg/selfupdate` `TestReplaceByRenameLeavesTheNewBinaryUnderTheOldName`
("must be executable") assert POSIX permission bits that Windows Go does not
have. Both fail on a clean checkout of `main`; CI is Linux, so it never sees
them. Do not chase them, and do not count them as a regression — confirm
against `HEAD` in a `git worktree` if unsure.

## Knowledge lives in the brain, not in files

This repo is wired to an engram store. Search it before grepping for anything
that is knowledge rather than code — decisions, conventions, traps, runbooks.
Record what is worth keeping with `brain_put` / `engram put`, never by writing
a Markdown file into the tree.
