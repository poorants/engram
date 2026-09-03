# The store's HTTP surface

The service reads nothing but its database. Bodies arrive over HTTP and live in
Postgres, so there is no checkout on the server, no git, and no credentials.

## One token for the whole surface

`ENGRAM_TOKEN` is presented as the `X-Engram-Token` header, or as the session
cookie a browser is given at `/login`. It answers one question — may this caller
use this store at all — and it is not a permission system: holding it grants
reads and writes alike. See [[scope-boundary]] for the other axis, which is
about *what* may enter rather than *who* may connect.

**The store refuses to start without one.** A store that cannot tell anyone
apart has only two options left, and both are worse than not booting: serve
everything to everyone, or refuse everything while appearing healthy.

## Reads

| Endpoint | What it answers |
|---|---|
| `GET /api/search?q=` | the one ranking, as chunks with heading paths |
| `GET /api/doc/{path}` | one document — body, outgoing links, backlinks, history |
| `GET /api/revisions/{path}` | that document's change history |
| `GET /api/revision/{id}` | one revision's body as it stood |
| `GET /api/integrity` | broken links, orphans, weak nodes |
| `GET /api/scopes` | which owner groups are admitted, and what is present |
| `GET /api/export` | every live document, verbatim |

All of them need the token unless the deployment sets
`ENGRAM_PUBLIC_READS=true`, which serves reads to anyone who can reach the port.
That is sound on a network where everyone who can reach it is already allowed to
read everything, and it is not sound anywhere else — so it is off by default.

## Writes

`PUT /api/doc/{path}`, `POST /api/doc/{path}/move`, `DELETE /api/doc/{path}`,
`POST /api/doc/{path}/restore`, `POST /api/rederive` and `POST /api/index`
always need the token. `ENGRAM_PUBLIC_READS` does not affect them; opening reads
must not open writes as a side effect.

## Reachable without the token

`GET /healthz` — whether the database answers, and how many documents exist.
Gating it would make the container's own healthcheck fail forever.

`GET /login`, `POST /login`, `GET /logout` — how a browser authenticates.

## What each status code means

- `400` — the address broke the rules in [[document-addressing]], or the body was
  empty or contained a NUL byte. The request is malformed.
- `401` — the write token does not match.
- `403` — the owner group is not admitted. The request is fine; it must not go
  here. A caller treats this differently from every other error
  ([[client-exit-codes]]).
- `404` — no such document.
- `503` — the server has no ingest token, or cannot reach its database.

## healthz says nothing about the index being full

Healthy means the database answers. An empty index is a normal state for a
freshly deployed store that has not received its first document, and marking it
unhealthy makes the container look permanently sick and never recover.
