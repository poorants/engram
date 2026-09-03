# Troubleshooting

Start with `engram store doctor`. It separates the two facts that matter — the
store answers, and this machine can write to it — and most problems are one of
them failing.

If `doctor` passes and something still misbehaves, `engram store show` prints
where every setting actually came from (file, environment, or default) without
touching the network. Environment beats file, so a stale `ENGRAM_STORE_URL` in a
shell profile explains a surprising number of "I changed the config and nothing
happened".

## `engram: command not found`

The installer put it in `~/.local/bin`, which is not on your `PATH`.

```bash
export PATH="$HOME/.local/bin:$PATH"    # add to ~/.bashrc or ~/.zshrc
```

On Windows the installer adds `%LOCALAPPDATA%\engram\bin` to your user `PATH`,
but an already-open terminal keeps the environment it started with — open a new
one.

## macOS: "cannot be opened because the developer cannot be verified"

The binaries are not notarized.

```bash
xattr -d com.apple.quarantine ~/.local/bin/engram
```

## Exit code 4 — the store could not be reached

Nothing was written. There is no queue: the write did not happen and will not
happen later.

1. Is the store up? On the server machine: `docker compose ps` and
   `curl -s localhost:8081/healthz`.
2. Is it reachable from here? `curl -s http://<host>:8081/healthz` from the
   client machine. A store bound on a laptop behind a firewall answers on
   `localhost` and nowhere else.
3. Is the address right? `engram store show`. A trailing path or an `https://`
   in front of a server that only speaks HTTP both land here.

## Exit code 3 — the store refused this path's owner group

This is not a bug and retrying will not help. The store is alive and declined
because the first segment of the path is not in its `ENGRAM_OWNERS`.

```bash
engram scope        # what owner/repo this directory resolves to
```

Then one of two things is true:

- **The repo should be in the store.** Add its owner group to `ENGRAM_OWNERS` in
  `server/.env` and `docker compose up -d` to apply it. It is a list of groups,
  so this is a once-per-org change, not once-per-repo.
- **The repo should not be in the store.** Correct — that is the boundary
  working. The document goes to a local file brain instead.

If `engram scope` reports something you did not expect, check `git remote -v`.
The coordinates come from the origin remote and nowhere else; a repo cloned from
a fork resolves to the fork's owner.

## Writes fail but reads work

The machine is set up read-only: `store set` was run without `--token`.

```bash
engram store set http://<host>:8081 --token <write token>
engram store doctor
```

The token lives in `~/.claude/engram/store.token` at mode `0600`, deliberately
not in `config.json` — that is a file people open and paste from. If you have
lost the token it is not recoverable from the server; it is in the server's
`.env` as `ENGRAM_INGEST_TOKEN`.

## `setup.sh` fails

**`docker compose v2 is required`** — you have the old `docker-compose` binary.
This project uses the v2 plugin (`docker compose`, no hyphen).

**`the store did not answer on port 8081 within 60s`** — the container came up
but the app did not. `docker compose logs app` from `server/`. The usual cause
is a port already in use; set `ENGRAM_PORT` in `.env` and bring it back up.

**`.env exists — keeping it`** — that is not a failure, it is the re-run path.
`--force` rewrites it, which **rotates both secrets**: every client's token
stops working and the database password no longer matches the existing data
directory. Almost never what you want on a store that has data in it.

**`no source of randomness`** — install `openssl`, or write `.env` by hand from
`.env.example`.

## The skill's scripts fail

The skill's helpers and hooks need `python3` on `PATH`. The binary and the MCP
server do not — those are the same static Go binary with no runtime.

The skill and the binary read one config file, resolved as `$ENGRAM_CONFIG_DIR`,
else `$CLAUDE_CONFIG_DIR`, else `~/.claude`. If one half works and the other
does not, check that both processes see the same value for those.

## The MCP tools do not appear in a session

```bash
claude mcp list                    # is engram registered, and in which scope?
engram mcp </dev/null              # does the server start at all?
```

`claude mcp add engram -- engram mcp` registers it for the current project only.
Use `--scope user` for every project. The MCP server inherits the machine's
designation — it takes no store URL or token of its own — so a session in a
directory with no store configured has the tools but nothing to talk to.

## Search returns nothing useful

Ask it as a sentence, not as keywords — the ranking is built for questions.
Then check that the corpus is actually there:

```bash
engram search "<something you know is written down>" --only-repo <repo>
engram integrity        # orphans are reachable only by search
```

Archived documents are excluded by default; add `--archives`. And
`--only-repo`/`--only-owner` are filters, not boosts: they are the usual reason
an answer that exists in another repo is not returned. Use `--boost-repo` when
you mean "probably here" rather than "only here".

## Restoring the store

The canonical copy is `docs.body` and `revisions` in Postgres — there is no copy
anywhere else. `server/deploy/backup.sh` dumps it with `pg_dump` inside the
database container and verifies the dump contains tables.

**The dump sits on the same disk.** That covers a document deleted by mistake
and covers nothing if the disk dies. Add the off-machine copy as a second
`ExecStart` in `server/deploy/engram-backup.service`.

## Still stuck

Open an issue with the output of `engram version` and `engram store doctor`, and
say which of the three parts — client, store, skill — you were setting up:
https://github.com/poorants/engram/issues
