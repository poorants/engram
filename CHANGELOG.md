# Changelog

Notable changes per release. Versions follow [semantic versioning](https://semver.org/);
until 1.0 the wire format and the CLI surface may change between minor versions,
and anything that does will say so here.

Releases are cut by tagging `vX.Y.Z`, which builds and publishes the binaries.

## [Unreleased]

### Added — `-tags noupdate`, for a machine that will not tolerate a self-updating binary

`engram update` downloads an executable, renames the running binary aside and
runs what it just wrote. That is the whole feature, and it is also, step for
step, what a dropper does. On a managed endpoint the behaviour scanner reaches
the second conclusion: observed on AhnLab V3, **every newly spawned
`engram.exe` was suspended at process creation, before the Go runtime started**
(`Threads=1, CPU=0.00`). Already-running MCP servers kept serving, so the store
still answered and documents still saved — only the capture hooks and every new
process silently hung, which reads as anything but an antivirus.

Turning the check off at runtime does not help: a scanner does not read
environment variables, and the download-and-replace code is still in the file.

- **`make build-frozen`** builds with `-tags noupdate`, which compiles out the
  version check, the download and the swap (`pkg/selfupdate/apply.go`,
  `cmd/engram/selfupdate_cmd.go`). The binary cannot rewrite itself because the
  code to do it is not in it. `engram version` carries a `+noupdate` suffix.
- **Call sites are unchanged.** The frozen `newUpdateChecker` returns
  `Fetch: nil, Path: ""`, and the nil guards that were already in `engram mcp`
  and `engram status` then make no network call and read no cache. No notice is
  ever composed.
- **`engram update` still exists** in a frozen build, and says that this build
  cannot update itself and how to replace it from outside. Dropping the verb
  would send the reader looking for a typo instead.

Signing the release is the real fix for an unsigned binary; a frozen build is
the one available to someone who cannot grant an allowlist entry on their own
machine. `docs/install.md` has the procedure and the diagnosis; `CLAUDE.md` is
new and tells a session to reach for this build on a work machine.

## [0.7.0] — 2026-09-10

**The viewer stops using search as a table of contents.** The home page's scope
and area cards linked to `/search?q=<name>`, which ranks documents by how much
they say that word. That is not the same question as "what is in this scope",
and the second one does not need a ranking at all — the path is already the
answer, and relevance ordering only hides the shape of the scope.

- **`/browse`** — every document in a scope (`?owner=&repo=`) or an area
  (`?area=`), grouped by PARA area and ordered by path. What one wants to know
  about a scope is "two projects, nineteen resources", and a ranked page never
  says that. The scope view offers "search inside this scope" for the other
  question; browsing and asking are different acts and now have different pages.
- **`/usage` is in the header.** Its only door was one link at the tail of the
  footer's statistics line, which is where a page goes to not be found.
  `/changes` deliberately stays out of the nav: the home page's "Recent changes
  · see all →" is already that door, and two doors to one room are each read
  half as often.

`/browse` is not logged as a call. Paging through a list is not a question, and
counting it as one would blur the numbers `/usage` exists to show — the tier-1
hit rate above all.

Server-only. The client binary is unchanged; a store is updated by redeploying
it (`server/setup.sh`, or compose up --build against this tree).

## [0.6.0] — 2026-09-09

### Added — search in tiers, and a ranking that learns what answered

A brain that keeps growing costs more to ask. Measured on a 255-document store,
one `brain_search` cost 1,500–2,400 tokens, and 35–40% of that was envelope —
`chunk_id`, `doc_id`, `owner`, `repo`, ranks — that a model never acts on. The
real drain was the loop around it: a first page with the answer third, the wrong
document fetched in full, the question asked again.

**Tiers.** `brain_search` and `engram search` now answer in a token budget.
Tier 1, the default, is up to four documents as 240-character snippets, one
chunk per document — enough to recognise the answer at ~380 tokens on the
bench (against ~1,500 for the old page) with the same recall. Tier 2 is up to
six full chunks; tier 3 is up to twelve documents, archives included, without
the repo boost. The result carries `next` — which tier to call and why — so a
caller raises the tier with the same question rather than rephrasing. Tier 1
that finds it costs a quarter of before; tier 1 then 2 costs about what one
call used to. `--tier 0` (and the API without `tier`) is the old page,
unchanged, for anything that predates this.

**Feedback.** The store now records what answered. `brain_feedback` and
`engram feedback <path>` vote a document useful (or noise); opening a hit with
`brain_get` after a search is recorded as a weaker implicit vote, without the
model doing anything — the MCP server remembers its last search and tells the
store. The viewer's search page has the two buttons, and a document page shows
its votes. Every search is logged with its hits (`searches`), so a vote is
attributed to the question that found the document.

Votes reach the ranking two ways. A **remembered channel** brings back a
document voted useful for a question that shares at least half of this
question's lexemes — it is a channel, so it can surface what the text
channels ranked low, and it is gated on the question, so it cannot surface a
popular document for an unrelated one. A **usefulness bonus**, log-saturated
at ten net votes and decaying with a 90-day half-life, breaks ties among what
the text already found; its ceiling is the distance between ranks 1 and 4 in
one channel, because a first cut at 1/(RRF_K+4) was measured to put one voted
document first for six unrelated questions. Neither is a filter: a document
must still match the question to appear.

The bench gates all of it: `eval_index.py --tier N` measures each tier's page
and wire tokens against its own pass mark, and `--vote-gold` votes the gold
answer for half the questions and checks the other half did not move (the
lock-in check). On the corpus: voted questions went from 9/17 to 17/17 at
rank 1, unvoted ones did not change.

### Added — `engram update`

The update notice used to name a curl one-liner; now it names a verb the
binary implements. `engram update` picks the release asset for this platform,
verifies it against `SHA256SUMS`, unpacks it, swaps it in by rename (on
Windows the running file moves to `engram.exe.old`, as the installer already
did) and runs the result once to prove it answers. Settings, token and store
are untouched; a running MCP server keeps the old file until its session
ends. `--check` reports, `--version` pins, and a development build is not
replaced without `--force` — the latest release may be older than it.

Both installers now treat an already-registered MCP server as success on a
re-run. Windows PowerShell 5.1 had been turning `claude mcp add`'s
"already exists" on stderr into a terminating error, killing an upgrade
right after the binary was replaced.

### Added — `engram usage`: what a session cost, and whether tier 1 was enough

Every API call now leaves one row in `calls` — the tool, what it was about,
and the bytes and estimated tokens that crossed the wire (the response for a
read, the body for a write). The MCP server mints a session id per process
and sends it as `X-Engram-Session` on every call (it logs the id to stderr at
start); the CLI passes `ENGRAM_SESSION` when it has one. The search log
learned the session too, so "tier 1, then tier 2 for the same question in the
same session" can be counted — that is the tier-1 miss.

`engram usage [--days N] [--session id]`, `GET /api/usage` and the viewer's
`/usage` page report per session: calls, tokens, searches / reads / votes /
writes, and the tier-1 hit rate; plus the questions asked in more than one
session, which are documents nobody wrote yet. Nothing here is fed back into
a session on its own — telling a session what it has spent would cost tokens
on every call to save tokens on some.

Wire format: a search called with `tier` answers compact hits (`path`,
`heading_path`, `score`, `snippet` or `body`) plus `search_id`, `candidates`
and `next`; the untiered answer gains `search_id`, `mem_rank` and `utility`
per hit and is otherwise what it was. New: `POST /api/feedback`,
`GET /api/doc/{path}?search=<id>`. Schema: two new tables, `searches` and
`feedback`, created on boot like the rest.


## [0.5.0] — 2026-09-04

### Fixed — upgrading on Windows while engram is running

`install.ps1` could not replace a binary that was in use, which on Windows is
the normal case rather than the edge one: the engram being upgraded is usually
the MCP server the person's editor is running at that moment. The install died
with

```
Move-Item: 파일이 이미 있으므로 만들 수 없습니다.
Cannot create a file when that file already exists.
```

after the new binary had already downloaded — an error that reads like a stale
leftover and is really a lock. The machine kept the old version, and the
`.new` file sat next to it as the only trace.

The replacement was already meant to be a rename, for exactly this reason, but
it renamed the wrong file: it moved the *new* binary onto the live name, and
`-Force` has to delete that destination first, which is the one thing a lock
forbids. Windows does allow renaming a running executable — the process keeps
its handle and keeps working — so the old binary now moves aside and the new one
takes the name it vacated. If that second move fails, the old binary is put back:
a failed upgrade costs the new version, never the working one.

The parked copy is deleted on the next run, since a process still serving from
it will refuse to go on this one. This is Windows-only; `install.sh` was never
affected, because a Unix `mv` over a running binary replaces the directory entry
and leaves the running inode alone.

### Added — the binary says when it is out of date

Distribution is by GitHub release, so a fix merged to main reaches a machine
only when someone re-runs the installer, and the signal to do that was a person
saying so. The failure is silent by construction: the machine keeps working and
simply lacks whatever was added, surfacing as a tool that is not there in a
session — where the binary's version is the last thing anyone suspects.

The check now runs itself, in the shape oh-my-zsh uses:

- **It never delays startup.** The read path is a cache read — instant, offline
  — and the network call is a background refresh (at most daily) whose answer
  the *next* session sees. Measured cold-cache `engram status`: 48 ms, silent;
  the run after it carries the notice.
- **It is silent when it cannot know.** No network, no releases, a `dev` build,
  an unparsable tag — all say nothing. A build stamped `v0.4.0-2-gbde191b` by
  `git describe` compares as its tag, so a source build of a release is not
  nagged to install the release it already contains.
- **The notice carries the remedy**, because "a new version exists" without the
  command to get it only moves the work to the reader.

Where it appears is decided by the transport: stdout is protocol framing and
stderr is a log nobody opens, so the notice goes into `initialize.instructions`
(the model reads it and can offer to run the update), onto the first tool result
of a session (once, never per call), to stderr, and to `engram status`.

- `engram status` grows `--live` to fetch now instead of reading the cache.
- `ENGRAM_UPDATE_CHECK=0` turns it off.

The shape is borrowed from `parataxis-mcp`'s `pkg/selfupdate`, which solved the
same problem for an internally-distributed binary.


## [0.4.0] — 2026-09-04

### Added — partial writes (`brain_patch`, `engram patch`)

A put prices an edit by the size of the DOCUMENT. Fixing one link in a
9,000-character guide meant sending 9,000 characters back, and the commonest
edit in a brain is exactly that shape: one line, in each of several documents.
Five such fixes moved about 40,000 characters to change under twenty lines.

`PATCH /api/doc/{path}` takes addressed edits instead of a body. It is **not a
second write path**: the edits are applied to the stored body in memory and the
result goes through the same `write_doc`, so one patch is one revision holding
the whole previous body, and aliases, scope refusal and re-indexing are
unchanged. What is saved is the transfer, not the history.

The risk a partial write introduces is the one an upsert cannot have — landing
somewhere the caller did not look — so the safety model is three independent
layers, and the independence is the point:

- **Addressing (where).** A line range, a section (a heading and everything
  under it, up to the next heading of the same or shallower depth), or an anchor
  (an exact substring). An address that matches twice is **refused with the
  candidates listed, never resolved by taking the first match.**
- **Verification (what you expected).** `expect` is the literal current text of
  the addressed range, compared character for character. Matching CHOOSES a
  range; this PROVES it. It is text rather than a hash so a caller that cannot
  run a hash function can still supply it, and its size is proportional to the
  edit rather than the document. Required for a line range, which by itself
  proves nothing about what is on it.
- **Concurrency (which version).** `base_sha256`, the hash `brain_get` now
  returns alongside the body. It is the only layer that catches an edit correct
  about its own range and wrong about the document, because someone else changed
  the rest of it in between.

There is deliberately **no fuzz**. `patch(1)` applies a hunk at an offset when
the context nearly matches, and "nearly" is precisely how an edit lands in the
neighbouring section. Every mismatch here refuses and reports what is actually
there, so the caller re-aims instead of guessing. Nothing is ever partially
applied: a batch resolves every edit against the document as read (the LSP rule
— sequential resolution silently shifts coordinates), refuses overlaps, and
writes all of it or none.

- `brain_patch` is the MCP tool; `engram patch` is the single-edit CLI
  (`--section` / `--anchor` / `--lines`, `--expect-file`, `--base`,
  `--dry-run` for a unified diff).
- `brain_get` and `GET /api/doc/{path}` now return `sha256`.
- A 409 means the document disagrees with the request — re-read and re-aim
  rather than retrying unchanged. A 400 means the call itself is malformed.

### Upgrading

The plugin now requires **`engram` v0.4.0 or newer**: the skill routes every
edit of an existing document through `brain_patch`, and an older binary does not
serve that tool. Nothing else changes — the settings file, the token, the store
and a designated file brain are all untouched.

## [0.3.0] — 2026-09-04

### Changed — the client has no dependencies at all

The skill's Python helpers and the capture-loop hooks are gone. Everything they
did is in the `engram` binary, and the plugin now ships prose and a hook command
and nothing else.

The reason is not tidiness. **On Windows the capture hooks never ran.** The hook
command was `python3 …/brain_reflect.py`, and `python3` is not a command on
Windows even where Python is installed — the App Execution Alias of that name
opens the Microsoft Store and exits. The hooks failed silently on every Windows
machine, which is the worst way for a dependency to be missing: it is present on
the maintainer's machine and absent where nobody is looking.

- **The hooks are `engram hook`.** One command for both `UserPromptSubmit` and
  `Stop` — Claude Code puts `hook_event_name` in the payload. Same wrap-up
  phrases, same `Stop` loop guard and cooldown, same env knobs
  (`ENGRAM_CAPTURE_DISABLE`, `ENGRAM_CAPTURE_COOLDOWN_MIN`,
  `ENGRAM_CAPTURE_PHRASES`). It still never fails a session: every path exits 0,
  now including a panic.
- **New file-brain commands**, replacing the scripts one for one:
  `engram resolve` (was `workspace.py resolve`), `engram brain show|set|unset`
  (was `list` / `set-brain` / `unset-brain`), `engram link`, `engram init`
  (was `init.py`), `engram lint` (was `engram_lint.py`), `engram weave` (was
  `weave_candidates.py`). `lint` and `weave` produce byte-identical JSON to the
  scripts they replace.
- **`store.py` is gone entirely.** Its two jobs moved into the binary: the
  human-readable output every verb now prints by default, and the
  403 → local-file-brain fallback that `engram put` performs itself.

### Changed — CLI output and one exit code

- **Every command prints for a person by default and takes `--json` for a
  machine.** Raw JSON was the only format before, and a session that reads it
  pays for every field it did not need. Anything parsing `engram <verb>` output
  must add `--json`.
- **`engram put` writes a scope-refused document to the local file brain itself**
  and exits **0**, reporting where it landed — the document did land, and a
  non-zero exit for that reads as a failure to both a shell and a model. Exit
  `3` now means the store refused AND no file brain took it, which is the only
  case a caller has to act on. Exit `4` (store unreachable) is unchanged and
  still writes nothing anywhere: an outage read as a refusal puts a document in a
  file nobody reads while everyone believes it was recorded.

### Added

- `pkg/workspace` — brain resolution (store first, then the designated file
  brain, then a local `brain/`), and the `CLAUDE.md` pointer block.
- `pkg/vault` — the file-brain link graph: the integrity lint, the weave finder,
  PARA init, and the refused-document write.
- CI checks that every hook command in the plugin manifest is a verb the binary
  actually implements, and that `skills/engram/scripts/` stays gone. An unknown
  hook verb is not a quiet failure — it puts an error in front of the user on
  every single prompt.

### Upgrading

The plugin now requires **`engram` v0.3.0 or newer**. Update the binary
(`install.sh` / `install.ps1`) before or with the plugin; an older binary does
not know `hook` and will complain on every prompt. Nothing else changes — the
settings file, the token and the store are untouched, and a designated file
brain is read exactly where it was.

## [0.2.0] — 2026-09-04

### Added
- `install.ps1` — the Windows client installer. The client (binary, CLI, MCP
  server, skill) now supports Linux, macOS and Windows; the store remains
  Linux/macOS only, because it is Postgres in Docker.
- `windows/arm64` release target.
- `server/setup.sh` — brings a store up in one command: generates both secrets,
  writes `.env`, starts the compose stack, waits until it answers, and prints
  the client install one-liner with the address and token filled in.
- Both client installers now finish the whole client setup, not just the binary:
  `--store` / `--token` designate the store and run `store doctor`, and the
  skill, its capture hooks and the `brain_*` MCP tools are registered with
  Claude Code at user scope. `--no-claude` installs the binary alone.
- Reads require the token. The store has one credential, `ENGRAM_TOKEN`, and it
  authorises reads and writes alike; the viewer asks for it once and keeps a
  session cookie. `ENGRAM_PUBLIC_READS=true` (`setup.sh --public-reads`) opens
  reads deliberately, for a network where everyone who can reach the port may
  already read everything.
- `setup.sh --tls [name]` — HTTPS for a store on a host with a public IP: Caddy
  with a Let's Encrypt certificate, `<public-ip>.sslip.io` as the name when
  none is given, the plaintext port re-bound to loopback. The installer checks
  that the host actually has the public address and that 80/443 are free
  before writing anything. `ENGRAM_BIND` for hosts that already run a reverse
  proxy; a guide for hosts with no public IP (Tailscale, DNS-01).
- The viewer's session is renewed on every visit, so a browser that comes back
  within thirty days is never asked for the token again. Rotating the token
  ends every session.
- `docs/` — install, concepts, CLI and MCP reference, design decisions,
  troubleshooting.
- `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, issue and pull request
  templates.

### Changed
- `ENGRAM_INGEST_TOKEN` is `ENGRAM_TOKEN`, and `ENGRAM_READ_AUTH` is gone: there
  is one credential and reads are closed unless `ENGRAM_PUBLIC_READS` says
  otherwise. A `.env` with the old names does not start.

### Fixed
- `engram store set <url>` without `--token` no longer reports a machine that
  already has a token as having none. It reached the store unauthenticated,
  took the 401, dropped the cached owner list, and printed `token: not set —
  Re-run with --token` — so re-running `store set` to change only the address
  or the byline read as if it had just discarded the credential. It never had;
  the token file is not written unless `--token` is given. The command now
  probes with the token already configured (`--token`, else `ENGRAM_TOKEN`,
  else `store.token`) and reports `token: kept` and where it came from.
- `make lint` no longer fails on every run under `dash` (Debian/Ubuntu `/bin/sh`).
  The gofmt check ended in `| (! read)`, and `read` with no variable is a
  bashism — the recipe died with `read: arg count` whatever gofmt found, so the
  target reported nothing useful in either direction.

## [0.1.1] — 2026-09-03

### Added
- The release workflow installs what it just built, on every OS the install
  instructions claim, so a broken asset name or archive layout fails the release
  rather than the first person to follow the README.

## [0.1.0] — 2026-09-03

First public release. Extracted from three private repositories, made general,
and measured against its own bench.

- One static binary: MCP server + CLI + store setup, `CGO_ENABLED=0`, no runtime.
- Six tools — `brain_search`, `brain_get`, `brain_revisions`, `brain_integrity`,
  `brain_put`, `brain_move` — over the CLI and over MCP, one transport client
  underneath.
- The store: FastAPI + Postgres 17 in one compose file, with the viewer served
  from the same ranking the tools get.
- PARA addressing derived from the git remote, with an owner allow-list as the
  confidentiality boundary.
- Revision history per document, aliases that keep links alive across moves, and
  no delete.
- The Claude Code skill, its references and the capture hooks.

[Unreleased]: https://github.com/poorants/engram/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/poorants/engram/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/poorants/engram/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/poorants/engram/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/poorants/engram/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/poorants/engram/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/poorants/engram/releases/tag/v0.1.0
