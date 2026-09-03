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
| `--tls [name]` | HTTPS via Let's Encrypt, for a host with a public IP — see [Reaching it from outside](#reaching-it-from-outside) |
| `--no-start` | write `.env` and stop |
| `--public-reads` | serve reads without a token — LAN only |
| `--force` | rewrite `.env` — **this rotates both secrets** |

`--force` on a store that has data in it will break every client's token and
leave the database password not matching the existing data directory. It is for
starting over, not for changing a setting; edit `.env` for that. The one
setting `setup.sh` does apply to an existing `.env` is `--tls`, because turning
it on later is the normal order of events and should not cost the secrets.

By hand, if you would rather:

```bash
cp .env.example .env      # fill in the two secrets and ENGRAM_OWNERS
docker compose up -d
curl -s localhost:8081/healthz
```

Open `http://<host>:8081` in a browser for the viewer — the same index and the
same ranking an agent gets. It asks for the token once and keeps a session
cookie that is renewed on every visit, so a browser that comes back within
thirty days is never asked again. Rotating the token ends every session.

## Configuration

| Variable | Meaning |
|---|---|
| `POSTGRES_PASSWORD` | database password — `openssl rand -hex 24` |
| `ENGRAM_TOKEN` | the store's one credential — `openssl rand -hex 24`. Required; the store will not start without it. |
| `ENGRAM_PUBLIC_READS` | serve reads to callers with no token (default `false`) |
| `ENGRAM_OWNERS` | comma-separated owner groups admitted. **Empty admits nothing.** |
| `ENGRAM_DATA` | where the database files live (default `./data`) |
| `ENGRAM_PORT` | published port (default 8081) |
| `ENGRAM_BIND` | address the port is published on (default `0.0.0.0`, every address). `127.0.0.1` when something in front terminates TLS |
| `ENGRAM_TZ` | zone revision timestamps display in (default UTC) |
| `COMPOSE_PROFILES` | `tls` adds the Caddy service — what `--tls` writes |
| `ENGRAM_DOMAIN` | the name Caddy serves and obtains a certificate for (tls profile only) |

`ENGRAM_OWNERS` is a list of GROUPS, not repos: a new repo under an admitted
group works with no change, and a repo outside one never does. It answers *what
may enter*. `ENGRAM_TOKEN` answers *who may connect*, and neither substitutes
for the other — see `bench/corpus/resources/scope-boundary.md`.

Exactly one port is published. The database publishes none; only the app reaches
it inside the compose network, and even bulk imports arrive over the app's port.

## Reaching it from outside

The default is plain HTTP on every address of the host. That is the right
default for the deployment it is usually run as — a trusted network, where
whoever can reach the port is already inside — and it is the only default that
*can* work everywhere, because a certificate is issued to a name that a public
CA can reach, and a store at `192.168.1.10` has none.

Whether the network in between is one to trust is your call, not the
software's. A home LAN usually is. An office LAN with a store that is one
person's, or anything that crosses the internet, usually is not. When it is not,
there are three paths, and which one is right is decided by where the host is.

### The host has a public IP — `--tls`

```bash
./setup.sh --owners <org> --tls              # https://<public-ip>.sslip.io
./setup.sh --owners <org> --tls kb.example.com   # a name you own that points here
```

This adds Caddy to the stack, which obtains a Let's Encrypt certificate and
renews it, publishes the store on 443 only, and redirects 80. The plaintext
port is re-bound to `127.0.0.1` so it is reachable from the host alone. No
domain is needed: [sslip.io](https://sslip.io) resolves `203-0-113-10.sslip.io`
to `203.0.113.10` for anyone, and Let's Encrypt issues for it like any other
name. The client address is then `https://<name>` with no port.

It checks before it writes anything that the public address is actually on one
of this machine's interfaces (behind NAT it is the router's, and no challenge
would ever arrive) and that 80 and 443 are free. Both failures are reported
with the path to take instead.

### The host already runs a reverse proxy

Do not use `--tls`; the ports are taken, and the answer is the proxy you have.
Bind the store to the address the proxy reaches it on, and add a site:

```bash
# .env
ENGRAM_BIND=127.0.0.1        # proxy on the host itself
ENGRAM_BIND=172.17.0.1       # proxy in a container: the docker bridge address
```

```caddyfile
kb.example.com {
    reverse_proxy 127.0.0.1:8081
}
```

```nginx
server {
    listen 443 ssl;
    server_name kb.example.com;
    location / {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

`X-Forwarded-Proto` is what tells the store the request arrived over HTTPS, so
that the session cookie is marked `Secure`. Caddy sets it by itself.

### The host has no public IP

A LAN host, a home server behind NAT, a laptop. No public CA can reach it, so
the choice is between staying on plain HTTP — which is what the default is for,
if the network is yours — and putting a private network in front.

**Tailscale** is the shortest path and what most self-hosters use. On the store
host:

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up                         # prints a login URL, once
sudo tailscale serve --bg 8081            # https://<host>.<tailnet>.ts.net
```

That gives every machine in your tailnet an `https://` address with a valid
certificate and WireGuard underneath, with no port opened and no domain. Set
`ENGRAM_BIND=127.0.0.1` so the plain port is not also on the LAN; `tailscale
serve` reaches it on loopback. Clients install Tailscale and use the `ts.net`
address as the store URL. It is a third-party account, which is why it is a
guide and not a flag — the store does not depend on it.

**A domain you own** works without a public IP through the DNS-01 challenge:
the CA checks a TXT record instead of connecting to the host. Caddy does this
with a DNS provider module, and the site block is the same as above. It needs
the domain, an API token for its DNS, and a Caddy build with the module, which
is more than this stack sets up for you.

Neither is required. A store on the network it serves, over plain HTTP with the
token, is the configuration this software is designed around.

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
