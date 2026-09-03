# The store's HTTP surface

The service reads nothing but its database. Bodies arrive over HTTP and live in
Postgres, so there is no checkout on the server, no git, and no credentials.

## Reads need no token

| Endpoint | What it answers |
|---|---|
| `GET /api/search?q=` | the one ranking, as chunks with heading paths |
| `GET /api/doc/{path}` | one document — body, outgoing links, backlinks, history |
| `GET /api/revisions/{path}` | that document's change history |
| `GET /api/revision/{id}` | one revision's body as it stood |
| `GET /api/integrity` | broken links, orphans, weak nodes |
| `GET /api/scopes` | which owner groups are admitted, and what is present |
| `GET /api/export` | every live document, verbatim |
| `GET /healthz` | whether the database answers |

Reads being open is a deliberate trade. It is also exactly why the confidentiality
boundary cannot be a column — see [[scope-boundary]].

## Writes need the token

`PUT /api/doc/{path}`, `POST /api/doc/{path}/move`, `DELETE /api/doc/{path}`,
`POST /api/doc/{path}/restore` and `POST /api/rederive` all require the
`X-Engram-Token` header, matched against the server's `ENGRAM_INGEST_TOKEN`.

**If no token is configured on the server, every write is refused with 503.**
Defaulting to "allow anyone" would mean a deployment that forgot to configure it
runs quietly wide open, and nothing about its behaviour would reveal that.

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
