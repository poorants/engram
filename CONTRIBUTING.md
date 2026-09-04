# Contributing to engram

Thanks for looking. This document is short on ceremony and specific about the
two or three things that actually matter here.

## The shape of the repository

Three deliverables, and a change usually belongs to exactly one:

| Path | What | Language |
|---|---|---|
| `cmd/`, `pkg/`, `internal/` | the client — binary, CLI, MCP server | Go 1.25 |
| `server/` | the store — FastAPI + Postgres 17 | Python 3.12 |
| `skills/engram/` | the Claude Code skill and its references — prose only, no scripts | Markdown |

If a change spans two of them, say why in the PR. The layers are deliberately
not collapsed ([why](docs/design.md#the-three-layers-are-not-collapsed)), and a
change that has to touch both halves is usually a wire-format change, which is
the kind worth discussing before it is written.

## Getting set up

```bash
git clone https://github.com/poorants/engram && cd engram

make build test lint          # the client
make server-up                # a store on localhost:8081
```

For the server and the bench you also need Python:

```bash
pip install -r server/requirements-dev.txt
python -m pytest server/tests -q
```

`make server-up` runs `docker compose up -d --build` in `server/`. If you have
no `.env` yet, `server/setup.sh --owners acme` writes one for local work.

## Before you open a pull request

```bash
make lint test                                    # Go: gofmt, go vet, go test
python -m pytest server/tests -q                  # server unit tests, no database needed
jq -e . .claude-plugin/marketplace.json           # the plugin manifest must parse
```

CI runs exactly these, plus a cross-compile of every release target. Nothing in
CI needs a network or a database.

### If you touched ranking, run the bench

This is the one hard rule. Ranking is the product and it is the part that
degrades invisibly: a change that helps five questions and quietly breaks three
looks like an improvement from the inside.

```bash
make server-up
make bench-seed        # ENGRAM_INGEST_TOKEN must match server/.env
make bench
```

Paste the before and after numbers in the PR. A ranking change without them will
be asked for them, not merged on the reasoning alone. See
[`server/bench/README.md`](server/bench/README.md).

"Touched ranking" means `server/app/search.py`, `server/app/core.py` (chunking
and lexemes feed the index), or the schema's indexes. If you are not sure, run
it — it takes a couple of minutes.

## Style

**Go.** `gofmt` decides formatting; there is no second opinion. No new
dependencies without a reason in the PR — the deliverable is one static binary
with `CGO_ENABLED=0`, and that property is load-bearing for the installer.

**Python.** Server-side only. It targets 3.12 and the standard library plus what
is already in `requirements.txt`.

**The client ships no Python at all, and that is a rule rather than a
coincidence.** The skill's helpers and the capture-loop hooks used to be Python;
on Windows `python3` is not a command even where Python is installed, so the
hooks were silently dead on every Windows machine. Anything that runs on a
user's machine — a hook, a skill helper, an installer step — goes in the binary.

**Comments explain why, not what.** The existing code is written that way and it
is the house style: a comment that restates the line above it will be asked
about in review, and one that records the reasoning behind a non-obvious choice
will not.

## Things that are decided

These are not up for a drive-by PR. They have reasons written down in
[docs/design.md](docs/design.md), and if you want to change one, open an issue
and argue with the reason.

- **No `delete`.** The contract is *move to archives*.
- **No fallback cache and no write queue.** Unreachable means fail now.
- **One ranking** for the viewer and for `brain_search`.
- **Lexical search by default**, no vector extension in the production image.
- **Not multi-tenant.**
- **One transport client**, three surfaces over it.

Arguments against them are welcome. Patches that quietly work around them are
not.

## Commits and pull requests

One logical change per PR. Present-tense subject line, and a body that says why
if the why is not obvious from the diff.

There is no CLA and no commit-message convention to memorise. Releases are cut
by tagging `vX.Y.Z`, which triggers the release workflow; maintainers do that.

## Reporting things

- **Bugs and features:** [issues](https://github.com/poorants/engram/issues) —
  the templates ask for `engram version` and `engram store doctor` output, which
  is genuinely what gets asked for first otherwise.
- **Security:** do not open an issue. See [SECURITY.md](SECURITY.md).

## Code of conduct

By participating you agree to the
[Code of Conduct](CODE_OF_CONDUCT.md).
