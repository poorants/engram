#!/usr/bin/env python3
"""Search API + viewer.

A person's search box and an agent's `brain_search` hit the **same
`/api/search`**. One ranking has to exist, or the order a person saw and the
order an agent received drift apart and nobody can reason about either — the
viewer page calls the same `search()` and only renders it differently.

Nothing but the database is read. Document bodies arrive over HTTP and live in
`docs.body`, so this service needs no checkout, no git, and no credentials.
"""
from __future__ import annotations

import gzip
import html
import json
import os
import re
import secrets
import sys
import time
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, Header, HTTPException, Query, Request
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse
from fastapi.templating import Jinja2Templates
from markdown_it import MarkdownIt
from psycopg_pool import ConnectionPool

sys.path.insert(0, str(Path(__file__).parent))
from ingest import (ALLOWED_OWNERS, PathRejected, ScopeDenied,  # noqa: E402
                    delete_doc, ensure_schema, import_docs, move_doc, rederive_all,
                    restore_doc, write_doc)
from search import DSN, search  # noqa: E402

INGEST_TOKEN = os.environ.get("ENGRAM_INGEST_TOKEN", "")

# Whether reading needs the token too.
#
# "open" is the original contract: the owner allow-list is the boundary, what
# must not be readable is never let in, and a store sits on a LAN or a personal
# server. That premise breaks the moment the port is reachable from the
# internet, and it breaks silently — nothing about a wide-open store looks
# wrong until the day it matters.
#
# "required" makes every read carry the same shared token the writes do. It is
# the same secret, not a second one: adding a read credential would mean a
# second thing to distribute, rotate and lose, for a store whose whole
# authorisation model is one shared token.
#
# The default stays "open" so an existing LAN deployment does not start
# refusing its own clients on upgrade. setup.sh writes "required" into every
# new .env, so new deployments are closed and old ones are left as they were.
READ_AUTH = os.environ.get("ENGRAM_READ_AUTH", "open").strip().lower()
if READ_AUTH not in ("open", "required"):
    print(f"engram: ENGRAM_READ_AUTH={READ_AUTH!r} is not 'open' or 'required'"
          " — refusing to guess, treating it as 'required'", file=sys.stderr)
    READ_AUTH = "required"
if READ_AUTH == "required" and not INGEST_TOKEN:
    # Failing to boot is the point. The alternative is a store that was asked
    # to require a token, has none, and therefore serves everything.
    raise SystemExit("engram: ENGRAM_READ_AUTH=required needs ENGRAM_INGEST_TOKEN")

# The cookie the viewer uses once a person has entered the token in a browser.
# A browser cannot send a header on a plain navigation, so the alternatives are
# a cookie or the token in every URL — and a token in a URL lands in history,
# in bookmarks, in referrers and in any log along the way.
SESSION_COOKIE = "engram_session"
# The timezone revision timestamps are rendered in. It is set explicitly rather
# than inherited, because "when did this change" must not quietly become wrong
# when the deployment method changes.
TZ = os.environ.get("ENGRAM_TZ", "UTC")
HERE = Path(__file__).parent


_tz_warned = False


def _configure(conn) -> None:
    """Pin the session timezone from the application itself. Relying on the
    server's or container's environment means a change in how it is deployed
    silently reverts to UTC, and a history timestamp is not a value that may be
    quietly wrong.

    set_config() is used rather than SET TIME ZONE because the latter takes only
    a literal — a bind parameter there is a syntax error, and since this runs on
    every pooled connection it takes the whole service down rather than failing
    once.

    An unusable zone name is reported LOUDLY and then left alone. It is an
    operator mistake worth seeing, but a typo in a display timezone must not stop
    the store from serving reads.
    """
    global _tz_warned
    try:
        with conn.cursor() as cur:
            cur.execute("SELECT set_config('TimeZone', %s, false)", (TZ,))
        conn.commit()
    except Exception as e:
        conn.rollback()
        if not _tz_warned:
            _tz_warned = True
            print(f"[config] ENGRAM_TZ={TZ!r} is not a timezone Postgres knows ({e}); "
                  f"timestamps will use the server default")


# check= matters: when the database container restarts, connections already in
# the pool stay there dead, and the one request that picks one up fails with
# AdminShutdown. Checking on checkout drops the dead ones quietly and the
# service survives a database restart.
pool = ConnectionPool(DSN, min_size=1, max_size=8, open=False,
                      check=ConnectionPool.check_connection, configure=_configure)
templates = Jinja2Templates(directory=str(HERE / "templates"))
md = MarkdownIt("commonmark", {"html": False, "linkify": True}).enable("table").enable("strikethrough")


@asynccontextmanager
async def lifespan(_: FastAPI):
    pool.open()
    # The canonical copy lives here, so the schema is ensured at BOOT rather
    # than at index time. It is idempotent and never touches existing data.
    try:
        with pool.connection() as conn:
            ensure_schema(conn)
    except Exception as e:            # a slow database gets another try on the first request
        print(f"[startup] could not ensure the schema (continuing): {e}")
    yield
    pool.close()


app = FastAPI(title="engram store", docs_url="/api/docs", redoc_url=None, lifespan=lifespan)


# -- read authorisation ------------------------------------------------------
# Enforced as middleware rather than per-route on purpose. A dependency has to
# be added to each route, and the failure mode of forgetting one is a route
# that serves everything to anyone — silent, and discovered by someone else.
# A default-deny gate with a named exception list fails the other way: forget
# to list a new public route and it stops working, loudly, for you.

# Open regardless of the setting. /healthz is what the container's own
# healthcheck and setup.sh's wait loop call, and gating it means a store that
# reports itself permanently unhealthy. It reveals whether the service is up
# and how many documents exist, and nothing about what they say.
PUBLIC_PATHS = frozenset({"/healthz", "/login", "/logout"})


def _authorised(request: Request) -> bool:
    token = request.headers.get("x-engram-token", "")
    if token and secrets.compare_digest(token, INGEST_TOKEN):
        return True
    cookie = request.cookies.get(SESSION_COOKIE, "")
    return bool(cookie) and secrets.compare_digest(cookie, INGEST_TOKEN)


@app.middleware("http")
async def read_gate(request: Request, call_next):
    if READ_AUTH == "required" and request.url.path not in PUBLIC_PATHS:
        if not _authorised(request):
            # A browser gets a page it can act on; a program gets JSON it can
            # branch on. Answering both with the same body means one of them
            # is parsing an error message meant for the other.
            wants_html = "text/html" in request.headers.get("accept", "")
            if wants_html and not request.url.path.startswith("/api/"):
                return templates.TemplateResponse(
                    request, "login.html",
                    {"error": "", "next": str(request.url.path)}, status_code=401)
            return JSONResponse({"detail": "this store requires a token to read"},
                                status_code=401)
    return await call_next(request)


@app.get("/login", response_class=HTMLResponse)
def login_form(request: Request, next: str = "/"):
    return templates.TemplateResponse(request, "login.html", {"error": "", "next": next})


@app.post("/login")
async def login(request: Request):
    form = await request.form()
    supplied = str(form.get("token", ""))
    nxt = str(form.get("next", "/")) or "/"
    # Only ever redirect somewhere on this site. An open redirect here would
    # turn the login page into a way to bounce people off a URL they trust.
    if not nxt.startswith("/") or nxt.startswith("//"):
        nxt = "/"
    if not (INGEST_TOKEN and secrets.compare_digest(supplied, INGEST_TOKEN)):
        return templates.TemplateResponse(
            request, "login.html",
            {"error": "That token was not accepted.", "next": nxt}, status_code=401)
    resp = RedirectResponse(nxt, status_code=303)
    resp.set_cookie(
        SESSION_COOKIE, INGEST_TOKEN,
        httponly=True,        # script on the page can never read it
        samesite="lax",       # not sent on cross-site POSTs
        max_age=60 * 60 * 24 * 30,
        # Only over HTTPS when the request arrived over HTTPS. Setting it
        # unconditionally would make the cookie undeliverable on the plain-HTTP
        # LAN deployment this is usually run as, and the login would appear to
        # succeed and then loop.
        secure=request.url.scheme == "https",
    )
    return resp


@app.get("/logout")
def logout():
    resp = RedirectResponse("/login", status_code=303)
    resp.delete_cookie(SESSION_COOKIE)
    return resp


# -- shared lookups ----------------------------------------------------------

_meta_cache: tuple[float, dict] = (0.0, {})


def meta(max_age: float = 30.0) -> dict:
    """These counters only move on a write, so there is no reason to read them
    on every request. Cache briefly and invalidate right after a bulk change."""
    global _meta_cache
    ts, cached = _meta_cache
    if cached and time.time() - ts < max_age:
        return cached
    try:
        with pool.connection() as conn, conn.cursor() as cur:
            cur.execute("SELECT k, v FROM meta")
            out = dict(cur.fetchall())
            # Counts are COUNTED, not stored. With writes arriving continuously
            # a stored number is guaranteed to be wrong at some moment; a
            # counted one is always right.
            cur.execute("SELECT count(*) FROM docs WHERE deleted_at IS NULL")
            out["docs"] = str(cur.fetchone()[0])
            cur.execute("SELECT count(*) FROM chunks")
            out["chunks"] = str(cur.fetchone()[0])
            cur.execute("SELECT count(*) FROM links WHERE dst IS NULL")
            out["broken_links"] = str(cur.fetchone()[0])
            cur.execute("SELECT max(updated_at) FROM docs WHERE deleted_at IS NULL")
            last = cur.fetchone()[0]
            out["updated_at"] = last.isoformat() if last else ""
    except Exception:
        return cached          # an empty or wobbling database must not blank the page
    _meta_cache = (time.time(), out)
    return out


def path_by_stem() -> dict[str, str]:
    """``[[name]]`` -> a real path. The indexer already resolved the edges, but
    rendering a body leaves only the name, so it is needed once more here."""
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT path FROM docs WHERE deleted_at IS NULL")
        out = {}
        for (p,) in cur.fetchall():
            out[Path(p).stem] = p
            out[p] = p
        return out


_WIKI = re.compile(r"\[\[([^\]|#]+)(?:\|([^\]]+))?\]\]")


def render_markdown(body: str, resolve: dict[str, str]) -> str:
    def sub(m: re.Match) -> str:
        name, label = m.group(1).strip(), (m.group(2) or "").strip()
        target = resolve.get(name.split("/")[-1]) or resolve.get(name)
        text = label or name
        if not target:
            # A broken link is never quietly turned into plain text. What is
            # visible is what gets fixed.
            return f"<span class='broken' title='no such document'>{html.escape(text)}</span>"
        return f"[{text}](/doc/{target})"
    return md.render(_WIKI.sub(sub, body))


_HL = re.compile(r"[가-힣]{2,}|[A-Za-z][A-Za-z0-9_.\-]{1,}|\d{2,}")
_EMPH = re.compile(r"\*\*|__(?=\S)|(?<=\S)__")


def highlight(text: str, q: str) -> str:
    """Mark the query terms in a snippet. ts_headline is not used because our
    tsvector holds direct lexemes that never went through a parser, and
    ts_headline would re-parse them.

    A snippet is the chunk body verbatim, so emphasis markers show through. That
    is noise to a reader, so it is stripped for display only — the body the API
    hands an agent is untouched."""
    terms = sorted({t for t in _HL.findall(q)}, key=len, reverse=True)
    out = html.escape(_EMPH.sub("", text))
    for t in terms[:12]:
        out = re.sub(f"({re.escape(html.escape(t))})", r"<mark>\1</mark>", out, flags=re.I)
    return out


# -- API — the one ranking people and agents share ---------------------------

@app.get("/api/scopes")
def api_scopes() -> dict:
    """What may come in, and what is in already."""
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT owner, repo, count(*) FROM docs WHERE deleted_at IS NULL"
                    " GROUP BY owner, repo ORDER BY count(*) DESC")
        present = [{"owner": r[0], "repo": r[1], "docs": r[2]} for r in cur.fetchall()]
    return {"allowed_owners": sorted(ALLOWED_OWNERS), "present": present}


@app.get("/healthz")
def healthz() -> JSONResponse:
    """Healthy = the database answers. An empty index is **not a fault** — a
    freshly deployed store that has not received its first document is a normal
    state, and marking it unhealthy makes the container look permanently sick."""
    try:
        with pool.connection() as conn, conn.cursor() as cur:
            cur.execute("SELECT to_regclass('public.docs') IS NOT NULL")
            indexed = cur.fetchone()[0]
            n = 0
            if indexed:
                # Soft-deleted documents are not counted. They are rows kept for
                # restore, not living documents, and the numbers must agree with
                # every other count.
                cur.execute("SELECT count(*) FROM docs WHERE deleted_at IS NULL")
                n = cur.fetchone()[0]
        return JSONResponse({"ok": True, "indexed": bool(indexed), "docs": n})
    except Exception as e:
        return JSONResponse({"ok": False, "error": str(e)}, status_code=503)


@app.get("/api/search")
def api_search(q: str = Query(..., min_length=1),
               limit: int = Query(6, ge=1, le=50),
               archives: bool = False,
               repo: str = Query("", description="lift this repo (without excluding others)"),
               only_repo: list[str] = Query([], description="restrict to these repos"),
               only_owner: list[str] = Query([], description="restrict to these groups")) -> dict:
    """`repo` is a BONUS, `only_*` are FILTERS. Keeping them separate is the
    point — asking from one repo must not make another repo's existing answer
    disappear."""
    with pool.connection() as conn:
        hits = search(q, limit=limit, include_archives=archives, conn=conn,
                      boost_repo=repo or None, only_repos=list(only_repo) or None,
                      only_owners=list(only_owner) or None)
    m = meta()
    return {"q": q, "count": len(hits), "hits": [h.as_dict() for h in hits],
            "index": {"updated_at": m.get("updated_at", ""),
                      "docs": int(m.get("docs", 0) or 0),
                      "chunks": int(m.get("chunks", 0) or 0)}}


@app.get("/api/doc/{path:path}")
def api_doc(path: str) -> dict:
    d = fetch_doc(path)
    if not d:
        raise HTTPException(404, f"no such document: {path}")
    return d


def require_token(token: str) -> None:
    """Every write goes through the token. If no token is configured the store
    accepts **nothing** — defaulting to "allow anyone" means a deployment that
    forgot to configure it runs quietly wide open."""
    if not INGEST_TOKEN:
        raise HTTPException(503, "ENGRAM_INGEST_TOKEN is not set on this server")
    if not secrets.compare_digest(token, INGEST_TOKEN):
        raise HTTPException(401, "token mismatch")


@app.put("/api/doc/{path:path}")
async def api_put_doc(path: str, request: Request,
                      x_engram_token: str = Header(default="")) -> dict:
    """Save one document — a canonical write.

    The previous body is kept in revisions (the git log slot). An identical body
    changes nothing and answers status=unchanged.
    """
    require_token(x_engram_token)
    payload = await request.json()
    body = payload.get("body")
    if not isinstance(body, str) or not body.strip():
        raise HTTPException(400, "body is empty — an empty body never overwrites a document")
    if "\x00" in body:
        # A Postgres text column cannot hold NUL and the insert blows up as a
        # 500. The request is what is wrong, so this is a 400 — and it says how
        # to fix it.
        raise HTTPException(400, "body contains a NUL (0x00) byte — a text document cannot "
                                 "carry one; send it escaped (\\x00) instead")
    try:
        with pool.connection() as conn:
            return write_doc(conn, path, body, author=payload.get("author", ""),
                             note=payload.get("note", ""),
                             updated_at=payload.get("updated_at"))
    except PathRejected as e:
        # A malformed address is a **bad request**, hence 400. The rules live in
        # core.validate_path alone and the client mirrors that one copy.
        raise HTTPException(400, str(e))
    except ScopeDenied as e:
        # 403, not 400. The request is not malformed — this simply must not go
        # in here.
        raise HTTPException(403, str(e))


@app.delete("/api/doc/{path:path}")
def api_delete_doc(path: str, author: str = "", note: str = "",
                   x_engram_token: str = Header(default="")) -> dict:
    """Soft delete. The body survives in revisions, so restore brings it back."""
    require_token(x_engram_token)
    with pool.connection() as conn:
        return delete_doc(conn, path, author=author, note=note)


@app.post("/api/doc/{path:path}/move")
async def api_move_doc(path: str, request: Request,
                       x_engram_token: str = Header(default="")) -> dict:
    """Move a document. The old path stays as an alias so existing links reach it."""
    require_token(x_engram_token)
    payload = await request.json()
    to = (payload.get("to") or "").strip()
    if not to:
        raise HTTPException(400, "the destination path (to) is empty")
    try:
        with pool.connection() as conn:
            return move_doc(conn, path, to, author=payload.get("author", ""))
    except PathRejected as e:
        raise HTTPException(400, str(e))
    except ScopeDenied as e:
        raise HTTPException(403, str(e))


@app.post("/api/doc/{path:path}/restore")
def api_restore_doc(path: str, author: str = "",
                    x_engram_token: str = Header(default="")) -> dict:
    require_token(x_engram_token)
    with pool.connection() as conn:
        return restore_doc(conn, path, author=author)


@app.get("/api/revisions/{path:path}")
def api_revisions(path: str, limit: int = Query(20, ge=1, le=200)) -> dict:
    """How this document has changed. The slot git log used to fill."""
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT id, created_at, author, note, sha256, length(body)"
                    " FROM revisions WHERE path = %s"
                    " ORDER BY created_at DESC LIMIT %s", (path, limit))
        rows = cur.fetchall()
    return {"path": path, "count": len(rows), "revisions": [
        {"id": r[0], "at": r[1].isoformat(), "author": r[2], "note": r[3],
         "sha256": r[4][:12], "chars": r[5]} for r in rows]}


@app.get("/api/revision/{rev_id}")
def api_revision(rev_id: int) -> dict:
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT id, path, body, created_at, author, note"
                    " FROM revisions WHERE id = %s", (rev_id,))
        r = cur.fetchone()
    if not r:
        raise HTTPException(404, f"no such revision: {rev_id}")
    return {"id": r[0], "path": r[1], "body": r[2], "at": r[3].isoformat(),
            "author": r[4], "note": r[5]}


@app.post("/api/rederive")
def api_rederive(x_engram_token: str = Header(default="")) -> dict:
    """Rebuild only the derived data — bodies and history untouched. Run it
    after changing the chunking or link rules."""
    require_token(x_engram_token)
    global _meta_cache
    with pool.connection() as conn:
        res = rederive_all(conn)
    _meta_cache = (0.0, {})
    return res


@app.get("/api/integrity")
def api_integrity(limit: int = Query(50, ge=1, le=500)) -> dict:
    """Graph integrity — broken links, orphans, weak nodes.

    This is where a machine measures what engram's linking rules ask for. **The
    orphan check is the floor, not the goal** — a document hanging off a single
    MOC (a weak node) passes it while the graph stays a folder tree. Splitting
    `kind` into contextual links (wiki) and structural ones (md) is what makes
    that distinction computable at all.
    """
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("""
            SELECT d.path, l.dst_name, l.kind FROM links l JOIN docs d ON d.id = l.src
            WHERE l.dst IS NULL AND d.deleted_at IS NULL
            ORDER BY d.path, l.dst_name LIMIT %s""", (limit,))
        broken = [{"from": r[0], "to": r[1], "kind": r[2]} for r in cur.fetchall()]

        # Orphans: no inbound edge at all. Structural files (README = MOC) are
        # excluded, since they are the side that gives links.
        cur.execute("""
            SELECT d.path FROM docs d
            WHERE d.deleted_at IS NULL AND d.path NOT LIKE '%%/README.md'
              AND NOT EXISTS (SELECT 1 FROM links l JOIN docs s ON s.id = l.src
                              WHERE l.dst = d.id AND s.deleted_at IS NULL)
            ORDER BY d.path LIMIT %s""", (limit,))
        orphans = [r[0] for r in cur.fetchall()]

        # Weak nodes: every inbound edge is structural (a MOC). Connected, but
        # not woven in.
        cur.execute("""
            SELECT d.path FROM docs d
            WHERE d.deleted_at IS NULL AND d.path NOT LIKE '%%/README.md'
              AND EXISTS (SELECT 1 FROM links l WHERE l.dst = d.id)
              AND NOT EXISTS (SELECT 1 FROM links l JOIN docs s ON s.id = l.src
                              WHERE l.dst = d.id AND l.kind = 'wiki'
                                AND s.deleted_at IS NULL)
            ORDER BY d.path LIMIT %s""", (limit,))
        weak = [r[0] for r in cur.fetchall()]

        cur.execute("SELECT kind, count(*), count(*) FILTER (WHERE dst IS NULL)"
                    " FROM links GROUP BY kind")
        by_kind = {r[0]: {"total": r[1], "broken": r[2]} for r in cur.fetchall()}

        # Totals are counted separately from the lists. The lists are cut by
        # limit, so len() would report the limit as the total — a "you have seen
        # everything" signal that is false.
        cur.execute("""
            SELECT (SELECT count(*) FROM links l JOIN docs d ON d.id=l.src
                    WHERE l.dst IS NULL AND d.deleted_at IS NULL),
                   (SELECT count(*) FROM docs d WHERE d.deleted_at IS NULL
                      AND d.path NOT LIKE '%%/README.md'
                      AND NOT EXISTS (SELECT 1 FROM links l JOIN docs s ON s.id=l.src
                                      WHERE l.dst=d.id AND s.deleted_at IS NULL)),
                   (SELECT count(*) FROM docs d WHERE d.deleted_at IS NULL
                      AND d.path NOT LIKE '%%/README.md'
                      AND EXISTS (SELECT 1 FROM links l WHERE l.dst=d.id)
                      AND NOT EXISTS (SELECT 1 FROM links l JOIN docs s ON s.id=l.src
                                      WHERE l.dst=d.id AND l.kind='wiki'
                                        AND s.deleted_at IS NULL))""")
        n_broken, n_orphan, n_weak = cur.fetchone()
    return {"broken_links": broken, "orphans": orphans, "weak_nodes": weak,
            "truncated": len(broken) >= limit or len(orphans) >= limit,
            "counts": {"broken": n_broken, "orphans": n_orphan,
                       "weak": n_weak, "by_kind": by_kind}}


@app.get("/api/export")
def api_export() -> dict:
    """The whole store, bodies verbatim.

    This is **the last way a person gets their text out if the store dies**, and
    it is worth keeping quite apart from backups (which are pg_dump, and which
    also carry the revisions this does not). There is deliberately no route back
    in from an export: restoring by re-importing an old dump overwrites edits
    made in the store since, so the reverse direction is always wrong.

    Soft-deleted documents are not exported. This is the brain that is alive.
    """
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT path, body, updated_at, title, owner, repo, area"
                    " FROM docs WHERE deleted_at IS NULL ORDER BY path")
        docs = [{"path": r[0], "body": r[1],
                 "updated_at": r[2].isoformat() if r[2] else None,
                 "title": r[3], "owner": r[4], "repo": r[5], "area": r[6]}
                for r in cur.fetchall()]
    m = meta()
    return {"count": len(docs), "exported_at": m.get("updated_at", ""), "docs": docs}


@app.post("/api/index")
async def api_index(request: Request,
                    x_engram_token: str = Header(default="")) -> dict:
    """Bulk import — seed the store from a tree of markdown files.

    **Upsert only.** An earlier design rebuilt the whole index (DROP -> CREATE),
    which was safe while the canonical copy was in files. Now that it is here,
    that would be knowledge deletion, not re-indexing. Documents missing from
    the payload are left alone, and nothing born here is touched.
    """
    require_token(x_engram_token)

    # Starlette does not decompress a request body (only responses, via
    # middleware). A few megabytes of markdown compress to a fraction of that,
    # so the client may gzip and this unpacks it.
    raw = await request.body()
    if request.headers.get("content-encoding", "").lower() == "gzip":
        raw = gzip.decompress(raw)
    payload = json.loads(raw)
    docs = payload.get("docs") or []
    if not docs:
        raise HTTPException(400, "docs is empty — an empty import never overwrites the index")
    global _meta_cache
    with pool.connection() as conn:
        res = import_docs(conn, docs, note=payload.get("note", ""))
    _meta_cache = (0.0, {})        # it just changed — the next read hits the database
    return res


# -- viewer ------------------------------------------------------------------

def fetch_doc(path: str) -> dict | None:
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT id, path, title, area, updated_at, chars, body, owner, repo"
                    " FROM docs WHERE path = %s AND deleted_at IS NULL", (path,))
        row = cur.fetchone()
        if not row:
            return None
        doc_id = row[0]
        cur.execute("SELECT l.dst_name, d.path FROM links l"
                    " LEFT JOIN docs d ON d.id = l.dst WHERE l.src = %s"
                    " ORDER BY l.dst_name", (doc_id,))
        outgoing = [{"name": n, "path": p} for n, p in cur.fetchall()]
        cur.execute("SELECT DISTINCT d.path, d.title FROM links l"
                    " JOIN docs d ON d.id = l.src"
                    " WHERE l.dst = %s AND d.deleted_at IS NULL"
                    " ORDER BY d.title", (doc_id,))
        backlinks = [{"path": p, "title": t} for p, t in cur.fetchall()]
        # History. Claiming to replace git and then giving people nowhere to
        # look is not replacing it.
        cur.execute("SELECT id, created_at, author, note, length(body)"
                    " FROM revisions WHERE path = %s"
                    " ORDER BY created_at DESC LIMIT 20", (path,))
        revs = [{"id": r[0], "at": r[1].strftime("%Y-%m-%d %H:%M"), "author": r[2],
                 "note": r[3], "chars": r[4]} for r in cur.fetchall()]
    return dict(id=doc_id, path=row[1], title=row[2], area=row[3],
                updated_at=row[4].strftime("%Y-%m-%d %H:%M") if row[4] else None,
                chars=row[5], body=row[6], owner=row[7], repo=row[8],
                outgoing=outgoing, backlinks=backlinks, revisions=revs)


@app.get("/", response_class=HTMLResponse)
def home(request: Request):
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT path, title, area, updated_at FROM docs"
                    " WHERE deleted_at IS NULL"
                    " ORDER BY updated_at DESC NULLS LAST, path LIMIT 24")
        recent = cur.fetchall()
        cur.execute("SELECT area, count(*) FROM docs WHERE deleted_at IS NULL"
                    " GROUP BY area ORDER BY count(*) DESC")
        areas = cur.fetchall()
        cur.execute("SELECT owner, repo, count(*) FROM docs WHERE deleted_at IS NULL"
                    " GROUP BY owner, repo ORDER BY count(*) DESC")
        scopes = cur.fetchall()
    return templates.TemplateResponse(request, "home.html", {
        "recent": recent, "areas": areas, "scopes": scopes, "meta": meta(), "q": ""})


@app.get("/search", response_class=HTMLResponse)
def search_page(request: Request, q: str = "", archives: bool = False,
                only_repo: list[str] = Query([]), limit: int = Query(20, ge=1, le=50)):
    hits = []
    if q.strip():
        with pool.connection() as conn:
            hits = search(q, limit=limit, include_archives=archives, conn=conn,
                          only_repos=list(only_repo) or None)
    return templates.TemplateResponse(request, "search.html", {
        "q": q, "hits": hits, "archives": archives, "only": only_repo,
        "highlight": highlight, "meta": meta()})


@app.get("/rev/{rev_id}", response_class=HTMLResponse)
def rev_page(request: Request, rev_id: int):
    """One revision's body AS IT WAS. Deciding whether to roll back means
    actually reading it."""
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT id, path, body, created_at, author, note"
                    " FROM revisions WHERE id = %s", (rev_id,))
        r = cur.fetchone()
    if not r:
        raise HTTPException(404, f"no such revision: {rev_id}")
    d = {"id": r[0], "path": r[1], "title": f"revision {r[0]}",
         "area": "revision", "owner": "", "repo": "",
         "updated_at": r[3].strftime("%Y-%m-%d %H:%M"), "chars": len(r[2]),
         "body": r[2], "author": r[4], "note": r[5],
         "outgoing": [], "backlinks": [], "revisions": [], "is_revision": True}
    d["html"] = render_markdown(r[2], path_by_stem())
    return templates.TemplateResponse(request, "doc.html",
                                      {"doc": d, "q": "", "meta": meta()})


@app.get("/changes", response_class=HTMLResponse)
def changes_page(request: Request, limit: int = Query(80, ge=1, le=300)):
    """Every recent write in the store — document updates and revisions on one
    timeline."""
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("""
            SELECT at, kind, path, title, note, author, rev_id FROM (
              SELECT d.updated_at AS at, 'doc' AS kind, d.path, d.title,
                     '' AS note, '' AS author, NULL::bigint AS rev_id
              FROM docs d WHERE d.deleted_at IS NULL
              UNION ALL
              SELECT r.created_at, 'rev', r.path,
                     COALESCE(d.title, r.path), r.note, r.author, r.id
              FROM revisions r LEFT JOIN docs d ON d.id = r.doc_id
            ) x ORDER BY at DESC LIMIT %s""", (limit,))
        rows = [{"at": r[0].strftime("%Y-%m-%d %H:%M"), "kind": r[1], "path": r[2],
                 "title": r[3], "note": r[4], "author": r[5], "rev_id": r[6]}
                for r in cur.fetchall()]
    return templates.TemplateResponse(request, "changes.html",
                                      {"rows": rows, "q": "", "meta": meta()})


@app.get("/doc/{path:path}", response_class=HTMLResponse)
def doc_page(request: Request, path: str):
    d = fetch_doc(path)
    if not d:
        raise HTTPException(404, f"no such document: {path}")
    d["html"] = render_markdown(d["body"], path_by_stem())
    return templates.TemplateResponse(request, "doc.html",
                                      {"doc": d, "q": "", "meta": meta()})
