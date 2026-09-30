# Troubleshooting

Start with `claude mcp list`. `engram` must be an **HTTP** server at
`https://<host>/mcp` and **connected**. Then run `/mcp__engram__setup` in a
session: it checks the registration, the plugin and the binary, and says which
of them needs you. Most problems are one of those three.

## The MCP tools do not appear in a session

- **Not registered, or registered per project.** Register it at user scope:
  `claude mcp add --transport http --scope user --callback-port 33418 engram https://<host>/mcp`.
- **Registered as a local command (`engram mcp`).** That is the 0.11-or-earlier
  client; the binary no longer implements it. `claude mcp remove engram -s user`,
  then add it as above.
- **"Needs authentication".** `/mcp` → engram → Authenticate.

## "Needs authentication" after a successful login

Claude Code remembers servers that answered 401 in
`~/.claude/mcp-needs-auth-cache.json` and may keep showing the state after the
login went through — including after a server-side fix. Remove the `engram`
entry from that file (or the file) and restart Claude Code.

## The login never completes on an SSH machine

The browser opens on your laptop, but the OAuth callback is sent to
`localhost:<port>` — the laptop's localhost, not the remote machine's where
Claude Code is listening. Fix the port and forward it:

```bash
# on the remote machine, once
claude mcp add --transport http --scope user --callback-port 33418 engram https://<host>/mcp
# from the laptop, while authenticating
ssh -L 33418:localhost:33418 <remote machine>
```

Then `/mcp` → engram → Authenticate.

## 403 at login: "… is not allowed on this store"

The Google account is not in the server's `ENGRAM_SERVE_ALLOW`. That list is the
operator's; add the email (or the Google subject id) there and restart
`engram serve`. Removing someone takes effect on their next call.

## 403 "this server answers to …" on every call

`/mcp` answers only to the issuer's host name (`ENGRAM_SERVE_ISSUER`) — a
DNS-rebinding guard. Behind a proxy that keeps the client's `Host` header
(`tailscale serve` does), the registered URL must use exactly the issuer's
host and port. A proxy that rewrites `Host` to something else gets this 403 too:
make it pass the original `Host`, or set the issuer to the name clients use.

Before 0.11.0's fix, the SDK's own guard refused any non-loopback `Host` behind
such a proxy, and Claude Code cached the failure as "needs auth" — see above.

## `engram serve` does not start

It refuses to start with anything missing rather than admit nobody or admit on
a guess, and the log line names the setting: `ENGRAM_SERVE_ISSUER` (an `https`
origin, no path), `ENGRAM_SERVE_KEY` (base64, ≥32 bytes), the Google client id
and secret, a non-empty `ENGRAM_SERVE_ALLOW`. An empty `ENGRAM_TOKEN` starts
with a warning: reads may work, every write is refused.

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

## The store could not be reached

Nothing was written. There is no queue: the write did not happen and will not
happen later. The tools report it on the spot.

1. Is the store up? On the server machine: `docker compose ps` and
   `curl -s localhost:8081/healthz`.
2. Does `engram serve` reach it? Its `--store` (default
   `http://127.0.0.1:8081`) must be the store's address from that host, and
   `journalctl -u engram-serve` shows each failure.

## The store refused this path's owner group (403)

This is not a bug and retrying will not help. The store is alive and declined
because the first segment of the path is not in its `ENGRAM_OWNERS`. The owner
comes from the working repo's `git remote get-url origin` — a repo cloned from a
fork resolves to the fork's owner.

- **The repo should be in the store.** Add its owner group to `ENGRAM_OWNERS` in
  `server/.env` and `docker compose up -d` to apply it. It is a list of groups,
  so this is a once-per-org change, not once-per-repo.
- **The repo should not be in the store.** Correct — that is the boundary
  working. Its knowledge stays out; there is no local brain to take it.

To keep the capture hooks quiet in such repos, set `ENGRAM_CAPTURE_OWNERS` to
the admitted owners.

## Writes fail but reads work

`engram serve` has no store token: `ENGRAM_TOKEN` is empty in its environment
file (it warns at start). Set it to the store's `ENGRAM_TOKEN` from
`server/.env` and restart the unit.

## `setup.sh` fails

**`docker compose v2 is required`** — you have the old `docker-compose` binary.
This project uses the v2 plugin (`docker compose`, no hyphen).

**`the store did not answer on port 8081 within 60s`** — the container came up
but the app did not. `docker compose logs app` from `server/`. The usual cause
is a port already in use; set `ENGRAM_PORT` in `.env` and bring it back up.

**`.env exists — keeping it`** — that is not a failure, it is the re-run path.
`--force` rewrites it, which **rotates both secrets**: `engram serve`'s
`ENGRAM_TOKEN` stops working and the database password no longer matches the existing data
directory. Almost never what you want on a store that has data in it.

**`no source of randomness`** — install `openssl`, or write `.env` by hand from
`.env.example`.

## `--tls`: the store is up but `https://` never answers

`setup.sh` waits two minutes for the certificate and then points at
`docker compose logs caddy`. What it says there is one of three things:

- **The challenge never arrived.** Port 80 is blocked — by `ufw`, or by the
  cloud provider's security group, which is separate from the host firewall
  and often closed by default. Open 80 and 443 inbound.
- **The name resolves elsewhere.** `dig +short <name>` should print this host's
  public IP. If you passed your own name, its DNS record is wrong or not yet
  propagated.
- **Rate limited.** Let's Encrypt allows five certificates a week for one
  name. Repeated `docker compose down -v` or deleting `data/caddy` re-issues
  each time; the certificate lives in `data/caddy`, keep it.

`setup.sh --tls` refuses up front on a host behind NAT (the public address is
not on any interface) and on a host with 80 or 443 in use. Both are the right
answer, not an obstacle: see "Reaching it from outside" in
[server/README.md](../server/README.md).

## The viewer accepts the token and then asks again

The store is behind a TLS proxy that does not send `X-Forwarded-Proto`. Without
it the store believes the request arrived over plain HTTP, and a session cookie
issued as such is refused by the browser on the `https://` page — so every page
is the login page. Add the header at the proxy (nginx:
`proxy_set_header X-Forwarded-Proto $scheme;`; Caddy sets it by itself).

## The capture hooks do not fire

The hook is `engram hook`, so the first question is whether the binary on `PATH`
is current:

```bash
engram version          # the latest release; re-run the installer if not
engram hook </dev/null  # must print nothing and exit 0
```

An older binary may speak from the old local settings, and one older than
v0.4.0 prints `unknown command "hook"` on every prompt — re-run `install.sh` /
`install.ps1`. If the version is fine: the hooks speak only in a repo with a
git `origin` (and, when `ENGRAM_CAPTURE_OWNERS` is set, only for those owners),
and `ENGRAM_CAPTURE_DISABLE=1` silences them.

This used to be the section about `python3`, and on Windows it was the answer
almost every time: `python3` is not a command there even where Python is
installed — the App Execution Alias of that name opens the Microsoft Store and
exits, so the hook failed silently. Nothing in engram needs an interpreter now.

## Search returns nothing useful

Ask it as a sentence, not as keywords — the ranking is built for questions.
Raise the `tier` before rephrasing: tier 1 is snippets, 2 full chunks, 3 adds
archives. `onlyRepos` is a filter, not a boost, and the usual reason an answer
that exists in another repo is not returned; `boostRepo` means "probably here"
rather than "only here". `brain_integrity` lists orphans — documents reachable
only by search.

## Restoring the store

The canonical copy is `docs.body` and `revisions` in Postgres — there is no copy
anywhere else. `server/deploy/backup.sh` dumps it with `pg_dump` inside the
database container and verifies the dump contains tables.

**The dump sits on the same disk.** That covers a document deleted by mistake
and covers nothing if the disk dies. Add the off-machine copy as a second
`ExecStart` in `server/deploy/engram-backup.service`.

## Still stuck

Open an issue with the output of `engram version` and `claude mcp list`, and
say which part — a person's machine, the store, or `engram serve` — you were
setting up:
https://github.com/poorants/engram/issues
