# engram server — the document store

FastAPI and Postgres 17. The deployment is one compose file: no systemd unit for
the service, no nginx, no virtualenv on the host.

## Bring one up

```bash
cp .env.example .env      # fill in the two secrets and ENGRAM_OWNERS
docker compose up -d
curl -s localhost:8081/healthz
```

Then point a client at it:

```bash
engram store set http://<this host>:8081 --token "$ENGRAM_INGEST_TOKEN"
engram store doctor
```

Open `http://<host>:8081` in a browser for the viewer — the same index and the
same ranking an agent gets.

## Configuration

| Variable | Meaning |
|---|---|
| `POSTGRES_PASSWORD` | database password — `openssl rand -hex 24` |
| `ENGRAM_INGEST_TOKEN` | the shared write token — `openssl rand -hex 24` |
| `ENGRAM_OWNERS` | comma-separated owner groups admitted. **Empty admits nothing.** |
| `ENGRAM_DATA` | where the database files live (default `./data`) |
| `ENGRAM_PORT` | published port (default 8081) |
| `ENGRAM_TZ` | zone revision timestamps display in (default UTC) |

`ENGRAM_OWNERS` is the confidentiality boundary and it is a list of GROUPS, not
repos: a new repo under an admitted group works with no change, and a repo
outside one never does. Reads need no token, so what must not be readable is
never let in — see `bench/corpus/resources/scope-boundary.md`.

Exactly one port is published. The database publishes none; only the app reaches
it inside the compose network, and even bulk imports arrive over the app's port.

## Seeding it

```bash
python bin/import_tree.py ~/notes --owner acme --repo shared \
    --url http://localhost:8081 --token "$ENGRAM_INGEST_TOKEN"
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
