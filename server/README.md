# engram server — the document store

FastAPI and Postgres 17. The deployment is one compose file: no systemd unit for
the service, no nginx, no virtualenv on the host.

**Linux or macOS, with Docker.** The store is not supported on Windows; Windows
machines are clients of a store running elsewhere. This is installed **once, for
everyone** — the per-person half is [`install.sh` / `install.ps1`](../docs/install.md)
at the repository root.

## Bring one up

```bash
./setup.sh --owners <your-github-org>
```

That generates both secrets, writes `.env`, brings the stack up, waits until it
actually answers, and prints the client install one-liner with the address and
the write token already filled in. Re-running it is safe: an existing `.env` is
kept, so it doubles as "bring the store back up".

| Option | |
|---|---|
| `--owners <a,b>` | the owner groups this store admits (asked for if omitted) |
| `--port <n>` | published port (default 8081) |
| `--tz <zone>` | zone revision timestamps display in (default UTC) |
| `--data <dir>` | where the database files live (default `./data`) |
| `--no-start` | write `.env` and stop |
| `--public-reads` | serve reads without a token — LAN only |
| `--force` | rewrite `.env` — **this rotates both secrets** |

`--force` on a store that has data in it will break every client's token and
leave the database password not matching the existing data directory. It is for
starting over, not for changing a setting; edit `.env` for that.

By hand, if you would rather:

```bash
cp .env.example .env      # fill in the two secrets and ENGRAM_OWNERS
docker compose up -d
curl -s localhost:8081/healthz
```

Open `http://<host>:8081` in a browser for the viewer — the same index and the
same ranking an agent gets.

## Configuration

| Variable | Meaning |
|---|---|
| `POSTGRES_PASSWORD` | database password — `openssl rand -hex 24` |
| `ENGRAM_TOKEN` | the store's one credential — `openssl rand -hex 24`. Required; the store will not start without it. |
| `ENGRAM_PUBLIC_READS` | serve reads to callers with no token (default `false`) |
| `ENGRAM_OWNERS` | comma-separated owner groups admitted. **Empty admits nothing.** |
| `ENGRAM_DATA` | where the database files live (default `./data`) |
| `ENGRAM_PORT` | published port (default 8081) |
| `ENGRAM_TZ` | zone revision timestamps display in (default UTC) |

`ENGRAM_OWNERS` is a list of GROUPS, not repos: a new repo under an admitted
group works with no change, and a repo outside one never does. It answers *what
may enter*. `ENGRAM_TOKEN` answers *who may connect*, and neither substitutes
for the other — see `bench/corpus/resources/scope-boundary.md`.

Exactly one port is published. The database publishes none; only the app reaches
it inside the compose network, and even bulk imports arrive over the app's port.

## Seeding it

```bash
python bin/import_tree.py ~/notes --owner acme --repo shared \
    --url http://localhost:8081 --token "$ENGRAM_TOKEN"
```

Each file becomes `<owner>/<repo>/<its path under the tree>`, so a tree laid out
as `projects/ areas/ resources/ archives/` keeps its PARA areas. If the tree is a
git repo, every document carries its last commit date, without which two hundred
imported documents all share the moment of the import.

The import is an upsert and never deletes. It is still not a thing to run out of
habit: the store is canonical and those files are a snapshot of an earlier
moment. The server refuses to overwrite a newer body with an older one and
reports it as `skipped_newer`.

## Backups

The canonical copy is `docs.body` and `revisions`, and there is no copy anywhere
else. `deploy/backup.sh` runs `pg_dump` inside the database container, verifies
the dump actually contains tables, and prunes old ones.

**The dump sits on the same disk.** That covers a document deleted by mistake and
covers nothing if the disk dies — add the off-machine copy as a second
`ExecStart` in `deploy/engram-backup.service`. Install the timer with:

```bash
sudo cp deploy/engram-backup.{service,timer} /etc/systemd/system/
sudo systemctl enable --now engram-backup.timer
```

## Layout

```
app/core.py      markdown -> chunks, lexemes, links; the address rules
app/ingest.py    canonical writes, history, moves, the owner allow-list
app/search.py    the one ranking: two channels fused with RRF
app/web.py       the HTTP API and the viewer
sql/schema.sql   idempotent; applied on every boot
bin/             seeding
bench/           the search bench and an example brain
tests/           rules that need no database
deploy/          backup script and systemd units
```

## Tests

```bash
pip install -r requirements-dev.txt
python -m pytest tests -q
```

They cover the parts that need no database: address rules, chunking, lexeme
construction, link extraction, and query construction. Everything else is
exercised by the bench against a live store — see `bench/README.md`.
