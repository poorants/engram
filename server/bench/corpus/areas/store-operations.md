# Running the store

The deployment is one compose file. No systemd unit for the service itself, no
nginx, no virtualenv.

## Configuration

| Variable | What it does |
|---|---|
| `POSTGRES_PASSWORD` | the database password; generate with `openssl rand -hex 24` |
| `ENGRAM_TOKEN` | the shared write token; without it every write is refused |
| `ENGRAM_OWNERS` | the owner groups admitted ([[scope-boundary]]); empty admits nothing |
| `ENGRAM_DATA` | where the database files live; defaults to `./data` |
| `ENGRAM_PORT` | the published port; defaults to 8081 |
| `ENGRAM_TZ` | the zone revision timestamps display in |

Exactly one port is published: the app's. The database publishes none — only the
app reaches it, inside the compose network. Even bulk imports arrive over that
one port.

## Things that are easy to get wrong

**Log growth.** Docker's default is unbounded. The limits are set per service
rather than daemon-wide, because a daemon-wide change would affect every other
container on the machine.

**Dead pooled connections.** When the database container restarts, connections
left in the pool stay there dead and the request that picks one up fails with
`AdminShutdown`. The pool checks a connection on checkout, so the dead ones are
dropped quietly and the service survives a database restart.

**The timezone.** It is pinned by the application on every connection, not
inherited from the environment. Inherited, it silently reverts to UTC when the
deployment method changes, and a history timestamp is not a value that may be
quietly wrong.

## Routine operations

- `docker compose logs -f --tail 100 app` — what the service is doing
- `curl -s localhost:8081/healthz` — whether it reaches its database
- `POST /api/rederive` — rebuild chunks and edges after changing the rules in
  [[chunking]] or [[linking-rules]]; bodies and history are untouched
- `deploy/backup.sh` — the daily dump ([[backup-and-recovery]])

The schema is applied on every boot and is idempotent, which is what keeps a
migration step out of the deployment sequence.
