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
import urllib.parse
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import Depends, FastAPI, HTTPException, Query, Request
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse
from fastapi.templating import Jinja2Templates
from markdown_it import MarkdownIt
from psycopg_pool import ConnectionPool

sys.path.insert(0, str(Path(__file__).parent))
from ingest import (ALLOWED_OWNERS, PathRejected, ScopeDenied,  # noqa: E402
                    delete_doc, ensure_schema, import_docs, move_doc, rederive_all,
                    restore_doc, write_doc)
from patch import (PatchConflict, PatchRejected,  # noqa: E402
                   apply_edits, check_base, diff, normalize, sha256 as body_sha256)
from search import DSN, search  # noqa: E402

# -- authentication ----------------------------------------------------------
#
# One token, and it answers one question: may this caller use this store at all?
#
# It is deliberately NOT a permission system. There is no read token and no
# write token, no roles and no accounts — whoever holds ENGRAM_TOKEN can read
# everything and write everything. Splitting it would mean two secrets to
# distribute, rotate and lose, and a store whose whole model is "one shared
# credential per deployment" does not get safer by having two of them; it gets
# a second thing to be inconsistent about.
#
# What separates a caller who may write from one who may not is therefore not
# authorisation at all: it is whether that machine was given the token. A
# machine set up without one is read-only because it cannot authenticate for a
# write, not because it holds a lesser credential.
#
# What the token does NOT decide is which documents may enter the store. That
# is ENGRAM_OWNERS, and it is a different axis on purpose: authentication says
# who is admitted, the owner allow-list says what is admitted, and neither one
# can stand in for the other.
TOKEN = os.environ.get("ENGRAM_TOKEN", "").strip()

# Refusing to boot is the point. A store with no token cannot tell anyone
# apart, and the only two things it could do instead are both worse: serve
# everything to everyone, or refuse everything while appearing to run.
if not TOKEN:
    raise SystemExit(
        "engram: ENGRAM_TOKEN is not set — a store with no token cannot"
        " authenticate anyone. Generate one with `openssl rand -hex 24`, or"
        " run server/setup.sh, which does it for you.")

# Whether an unauthenticated caller may read.
#
# The default is closed. The store's own premise used to be that reads need no
# token — the owner allow-list is the boundary, and what must not be readable
# is never let in — and that holds exactly as long as the port is on a trusted
# network. It stops holding the moment the service is reachable from the
# internet, and it stops holding silently: nothing about a wide-open store
# looks wrong until the day it matters. A default that is only correct under a
# condition the software cannot check is not a safe default.
#
# ENGRAM_PUBLIC_READS=true is the deliberate opt-out for the deployment where
# everyone who can reach the port is already allowed to read everything.
PUBLIC_READS = os.environ.get("ENGRAM_PUBLIC_READS", "").strip().lower() in (
    "1", "true", "yes", "on")

# The cookie the viewer trades the token for. A browser cannot put a header on
# a plain navigation, so the alternatives are a cookie or the token in every
# URL — and a token in a URL lands in history, in bookmarks, in referrers and
# in every log along the way.
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


# -- the authentication gate -------------------------------------------------

# Reachable without authenticating, whatever ENGRAM_PUBLIC_READS says.
#
# /healthz is what the container's own healthcheck and setup.sh's wait loop
# call: gating it produces a store that reports itself permanently unhealthy
# and restarts forever. It says whether the service is up and how many
# documents exist, and nothing about what any of them contain.
#
# /login and /logout are how a browser authenticates in the first place.
UNAUTHENTICATED_PATHS = frozenset({"/healthz", "/login", "/logout"})


def presented_token(request: Request) -> str:
    """The credential this request carries, from either place a caller can put
    it: the header a program sets, or the cookie a browser was given at /login.

    One function, so there is exactly one answer to "is this caller
    authenticated" and no route can accidentally accept something the others
    reject."""
    header = request.headers.get("x-engram-token", "")
    if header:
        return header
    return request.cookies.get(SESSION_COOKIE, "")


def token_ok(supplied: str) -> bool:
    """compare_digest, not ==, so the comparison does not leak the token's
    length or its matching prefix through timing."""
    return bool(supplied) and secrets.compare_digest(supplied, TOKEN)


def authenticated(request: Request) -> bool:
    return token_ok(presented_token(request))


# How long a browser session lives without being used. It is a SLIDING window:
# every authenticated browser request re-issues the cookie with this much time
# again, so a browser that comes back within the window never logs in twice,
# and one that stays away this long does. That is how the sites people never
# remember logging into again behave, and the trade is deliberate: convenience
# is bought with a longer window on a lost laptop, and the way out is the same
# as everywhere else — rotate the credential, which invalidates every cookie at
# once because the cookie is bound to it.
SESSION_TTL = 60 * 60 * 24 * 30


def set_session_cookie(resp, request: Request) -> None:
    """One place that knows what the session cookie looks like, so the login
    and the renewal cannot drift into issuing two different cookies."""
    resp.set_cookie(
        SESSION_COOKIE, TOKEN,
        httponly=True,        # script on the page can never read it
        samesite="lax",       # not sent on cross-site POSTs
        max_age=SESSION_TTL,
        # Only over HTTPS when the request arrived over HTTPS — which, behind a
        # TLS proxy, is what X-Forwarded-Proto says (uvicorn folds it into the
        # scheme). Setting it unconditionally would make the cookie
        # undeliverable on the plain-HTTP LAN deployment, and the login would
        # appear to succeed and then loop.
        secure=request.url.scheme == "https",
    )


def _authenticated_by_cookie(request: Request) -> bool:
    """True when the credential came in the cookie and not the header. Only a
    browser session is renewed; a program presenting the header has no session
    to extend and would be handed a Set-Cookie it never asked for."""
    return not request.headers.get("x-engram-token") and token_ok(
        request.cookies.get(SESSION_COOKIE, ""))


@app.middleware("http")
async def authentication(request: Request, call_next):
    """Default deny, with a named exception list.

    Middleware rather than a per-route dependency on purpose. A dependency has
    to be remembered on every route, and the failure mode of forgetting one is
    a route that serves everything to anyone — silent, and discovered by
    somebody else. Forgetting to add a genuinely public route to the list here
    fails the other way: it stops working, loudly, for whoever added it.

    Writes are gated here too, not only by require_auth on each write route.
    That is deliberate belt-and-braces: this gate is what makes an unlisted
    route closed by default, and the per-route check is what keeps writes
    closed even if this list ever grows an entry it should not have.
    """
    path = request.url.path
    if path not in UNAUTHENTICATED_PATHS:
        is_write = request.method not in ("GET", "HEAD", "OPTIONS")
        # Reads may be waved through when the deployment says so; writes never.
        needs_auth = is_write or not PUBLIC_READS
        if needs_auth and not authenticated(request):
            return _unauthenticated_response(request)
    response = await call_next(request)
    # Renew the browser session on use (see SESSION_TTL). Not on /logout, whose
    # whole point is to end it.
    if path != "/logout" and _authenticated_by_cookie(request):
        set_session_cookie(response, request)
    return response


def _unauthenticated_response(request: Request):
    """A browser gets a page it can act on; a program gets JSON it can branch
    on. One body for both means one of them is parsing an error written for the
    other."""
    is_api = request.url.path.startswith("/api/")
    wants_html = "text/html" in request.headers.get("accept", "")
    if wants_html and not is_api:
        return templates.TemplateResponse(
            request, "login.html",
            {"error": "", "next": request.url.path}, status_code=401)
    return JSONResponse(
        {"detail": "this store requires a token — send it as X-Engram-Token"},
        status_code=401)


@app.get("/login", response_class=HTMLResponse)
def login_form(request: Request, next: str = "/"):
    return templates.TemplateResponse(request, "login.html", {"error": "", "next": next})


@app.post("/login")
async def login(request: Request):
    # Parsed with the standard library rather than request.form(), which pulls
    # in python-multipart as a runtime dependency. This form is two fields of
    # urlencoded text; adding a dependency to the image for that is a poor
    # trade, and forgetting to add it makes the login page answer 500 -- which
    # is exactly how this was found.
    raw = (await request.body()).decode("utf-8", "replace")
    fields = urllib.parse.parse_qs(raw, keep_blank_values=True)
    supplied = (fields.get("token") or [""])[0]
    nxt = (fields.get("next") or ["/"])[0] or "/"
    # Only ever redirect somewhere on this site. An open redirect here would
    # turn the login page into a way to bounce people off a URL they trust.
    if not nxt.startswith("/") or nxt.startswith("//"):
        nxt = "/"
    if not (TOKEN and secrets.compare_digest(supplied, TOKEN)):
        return templates.TemplateResponse(
            request, "login.html",
            {"error": "That token was not accepted.", "next": nxt}, status_code=401)
    resp = RedirectResponse(nxt, status_code=303)
    set_session_cookie(resp, request)
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


def require_auth(request: Request) -> None:
    """Dependency for a route that must never be public.

    The middleware has already checked this for every route, so this is a
    second lock on the writes specifically. It does not depend on the
    exception list staying correct, which means a mistake there cannot turn a
    write route into an open one -- and writes are the only irreversible thing
    the store does.

    It accepts either carrier, like everything else here: a program's header or
    a browser's session cookie. One credential, one way of checking it.
    """
    if not authenticated(request):
        raise HTTPException(401, "invalid or missing token")


@app.put("/api/doc/{path:path}")
async def api_put_doc(path: str, request: Request,
                      _: None = Depends(require_auth)) -> dict:
    """Save one document — a canonical write.

    The previous body is kept in revisions (the git log slot). An identical body
    changes nothing and answers status=unchanged.
    """
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


@app.patch("/api/doc/{path:path}")
async def api_patch_doc(path: str, request: Request,
                        _: None = Depends(require_auth)) -> dict:
    """Change PART of a document — the same canonical write, addressed narrowly.

    This exists because a whole-body upsert prices an edit by the size of the
    document rather than the size of the change, and the commonest edit in this
    brain is one line in each of several documents.

    It is not a second write path. The edits are applied to the stored body in
    memory and the RESULT goes through ``write_doc`` — so one patch is one
    revision holding the whole previous body, and aliases, scope refusal and
    re-indexing behave exactly as they do for a put. What is saved is the
    transfer, not the history.

    The safety argument lives in ``patch.py``: an address that matches twice is
    refused rather than guessed, ``expect`` proves the addressed range really
    holds what the caller thinks, and ``base_sha256`` proves they read the
    version they are editing. A failure writes nothing at all — there is no
    partial application.
    """
    payload = await request.json()
    edits = payload.get("edits")
    note = payload.get("note", "")
    if not isinstance(note, str) or not note.strip():
        raise HTTPException(400, "note is empty — say in one line why this revision "
                                 "exists (it is the commit message of the history)")
    current = fetch_doc(path)
    if not current:
        raise HTTPException(404, f"no such document: {path} — patch changes an existing "
                                 "document; create one with PUT")
    before = normalize(current["body"])
    try:
        check_base(before, payload.get("base_sha256"))
        applied = apply_edits(before, edits)
    except PatchRejected as e:
        raise HTTPException(400, str(e))
    except PatchConflict as e:
        # 409, not 400: the call is well formed and the DOCUMENT disagrees with
        # it. Fixing it means re-reading, not rewriting the arguments — and a
        # caller that retries a 400 unchanged would loop forever here.
        raise HTTPException(409, str(e))

    after = applied.body
    if after == before:
        return {"path": path, "status": "unchanged", "doc_id": current["id"],
                "sha256": body_sha256(after), "edits": applied.edits}

    if payload.get("dry_run"):
        return {"path": path, "status": "dry_run", "doc_id": current["id"],
                "edits": applied.edits, "chars": len(after),
                "sha256": body_sha256(after),
                "diff": diff(before, after, path),
                "warning": "Call again without dry_run to write."}

    try:
        with pool.connection() as conn:
            result = write_doc(conn, path, after, author=payload.get("author", ""),
                               note=note)
    except PathRejected as e:
        raise HTTPException(400, str(e))
    except ScopeDenied as e:
        raise HTTPException(403, str(e))
    result["edits"] = applied.edits
    result["sha256"] = body_sha256(after)
    result["chars"] = len(after)
    return result


@app.delete("/api/doc/{path:path}")
def api_delete_doc(path: str, author: str = "", note: str = "",
                   _: None = Depends(require_auth)) -> dict:
    """Soft delete. The body survives in revisions, so restore brings it back."""
    with pool.connection() as conn:
        return delete_doc(conn, path, author=author, note=note)


@app.post("/api/doc/{path:path}/move")
async def api_move_doc(path: str, request: Request,
                       _: None = Depends(require_auth)) -> dict:
    """Move a document. The old path stays as an alias so existing links reach it."""
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
                    _: None = Depends(require_auth)) -> dict:
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
def api_rederive(_: None = Depends(require_auth)) -> dict:
    """Rebuild only the derived data — bodies and history untouched. Run it
    after changing the chunking or link rules."""
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
                    _: None = Depends(require_auth)) -> dict:
    """Bulk import — seed the store from a tree of markdown files.

    **Upsert only.** An earlier design rebuilt the whole index (DROP -> CREATE),
    which was safe while the canonical copy was in files. Now that it is here,
    that would be knowledge deletion, not re-indexing. Documents missing from
    the payload are left alone, and nothing born here is touched.
    """
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
        cur.execute("SELECT id, path, title, area, updated_at, chars, body, owner, repo,"
                    " sha256 FROM docs WHERE path = %s AND deleted_at IS NULL", (path,))
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
                # The hash a partial write sends back as base_sha256. Handing it
                # out with the body is what lets a caller prove it edited the
                # version it actually read.
                sha256=row[9],
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
