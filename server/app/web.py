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

import base64
import gzip
import hmac
import hashlib
import html
import json
import os
import re
import secrets
import sys
import time
import urllib.parse
from contextlib import asynccontextmanager
from datetime import datetime, timezone
from pathlib import Path

from fastapi import Depends, FastAPI, HTTPException, Query, Request
from fastapi.responses import FileResponse, HTMLResponse, JSONResponse, RedirectResponse
from fastapi.templating import Jinja2Templates
from markdown_it import MarkdownIt
from psycopg_pool import ConnectionPool

sys.path.insert(0, str(Path(__file__).parent))
from ingest import (ALLOWED_OWNERS, PathRejected, ScopeDenied,  # noqa: E402
                    delete_doc, ensure_schema, import_docs, move_doc, rederive_all,
                    restore_doc, write_doc)
from patch import (PatchConflict, PatchRejected,  # noqa: E402
                   apply_edits, check_base, diff, normalize, sha256 as body_sha256)
import feedback as fb  # noqa: E402
import usage  # noqa: E402
from search import DSN, query_lexemes, search, search_tier  # noqa: E402

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
STATIC = HERE / "static"

# The address of the remote MCP server (`engram serve`) machines register,
# shown on /setup. The store does not serve MCP itself and cannot discover
# where `engram serve` answers — it is usually a different port or a different
# host name behind a proxy — so the deployment says so. Unset, the setup page
# shows a placeholder and says what to fill in.
MCP_URL = os.environ.get("ENGRAM_MCP_URL", "").strip()
MCP_URL_PLACEHOLDER = "https://<host>/mcp"

# Google sign-in for the viewer, brokered by `engram serve`: the viewer sends
# the browser to <serve>/oauth/viewer, which runs the same Google login and
# allow-list the MCP clients go through and comes back to /auth/callback with
# a one-minute ticket signed with ENGRAM_VIEWER_KEY — the serve host's
# ENGRAM_SERVE_KEY. The viewer then keeps the person in a cookie it signs with
# the same key. Both unset: the viewer only knows the operator's token.
try:
    VIEWER_KEY = base64.b64decode(os.environ.get("ENGRAM_VIEWER_KEY", "").strip() or b"")
except ValueError:
    VIEWER_KEY = b""
GOOGLE_LOGIN = bool(MCP_URL) and len(VIEWER_KEY) >= 32
SERVE_ORIGIN = MCP_URL[: -len("/mcp")] if MCP_URL.endswith("/mcp") else MCP_URL.rstrip("/")
PERSON_COOKIE = "engram_person"


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
# app.css and app.js are requested with ?v=<content hash>, so they can be
# cached for a year and a deploy that changes them still reaches every browser.
templates.env.globals["asset_v"] = hashlib.sha256(
    (STATIC / "app.css").read_bytes() + (STATIC / "app.js").read_bytes()).hexdigest()[:10]
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
#
# /setup is the guide for connecting a machine. A person reads it BEFORE they
# can authenticate, so gating it would hide the instructions from exactly the
# reader they are for. It holds no brain data: the route renders it without
# the sidebar's repository list unless the caller could read that anyway.
UNAUTHENTICATED_PATHS = frozenset({"/healthz", "/login", "/logout", "/setup",
                                   "/auth/google", "/auth/callback"})

# Stylesheet, script and fonts. The login and setup pages need them before a
# browser has a session, and none of them says anything about what is stored.
UNAUTHENTICATED_PREFIXES = ("/static/",)


def is_unauthenticated_path(path: str) -> bool:
    return path in UNAUTHENTICATED_PATHS or path.startswith(UNAUTHENTICATED_PREFIXES)


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


# -- signed values shared with engram serve ---------------------------------
#
# The same shape serve's oauth.go seals with: base64url(JSON claims) "." base64url
# (HMAC-SHA256 over the first part), the claims carrying "typ" so a value
# issued as one kind can never be presented as another.

def _b64(b: bytes) -> str:
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode()


def _unb64(s: str) -> bytes:
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))


def seal(typ: str, claims: dict) -> str:
    payload = _b64(json.dumps({**claims, "typ": typ}, separators=(",", ":")).encode())
    sig = hmac.new(VIEWER_KEY, payload.encode(), hashlib.sha256).digest()
    return payload + "." + _b64(sig)


def unseal(typ: str, value: str) -> dict | None:
    if not VIEWER_KEY or "." not in (value or ""):
        return None
    payload, sig = value.split(".", 1)
    try:
        good = hmac.compare_digest(_unb64(sig), hmac.new(VIEWER_KEY, payload.encode(), hashlib.sha256).digest())
        claims = json.loads(_unb64(payload)) if good else None
    except (ValueError, TypeError):
        return None
    if not isinstance(claims, dict) or claims.get("typ") != typ:
        return None
    if time.time() > float(claims.get("exp", 0)):
        return None
    return claims


def person(request: Request) -> dict | None:
    """The Google-signed-in person this browser holds a session for, if any."""
    return unseal("vsess", request.cookies.get(PERSON_COOKIE, ""))


def authenticated(request: Request) -> bool:
    return token_ok(presented_token(request)) or person(request) is not None


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


def set_person_cookie(resp, request: Request, who: dict) -> None:
    """The Google session: who, signed, sliding like the token session."""
    resp.set_cookie(
        PERSON_COOKIE,
        seal("vsess", {"email": who.get("email", ""), "sub": who.get("sub", ""),
                       "exp": int(time.time()) + SESSION_TTL}),
        httponly=True, samesite="lax", max_age=SESSION_TTL,
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
    if not is_unauthenticated_path(path):
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
    if path not in ("/logout", "/auth/callback") and (who := person(request)):
        set_person_cookie(response, request, who)
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


def _local(nxt: str) -> str:
    """Only ever redirect somewhere on this site — an open redirect on a login
    page bounces people off a URL they trust."""
    return nxt if nxt.startswith("/") and not nxt.startswith("//") else "/"


@app.get("/login", response_class=HTMLResponse)
def login_form(request: Request, next: str = "/"):
    return templates.TemplateResponse(request, "login.html", {"error": "", "next": next})


@app.get("/auth/google")
def auth_google(request: Request, next: str = "/"):
    if not GOOGLE_LOGIN:
        raise HTTPException(404, "Google sign-in is not configured (ENGRAM_MCP_URL, ENGRAM_VIEWER_KEY)")
    back = str(request.base_url).rstrip("/") + "/auth/callback?" + urllib.parse.urlencode({"next": _local(next)})
    return RedirectResponse(SERVE_ORIGIN + "/oauth/viewer?" + urllib.parse.urlencode({"return": back}), status_code=303)


# Tickets already exchanged. One minute is their whole life, so the set stays tiny.
_spent_tickets: dict[str, float] = {}


@app.get("/auth/callback")
def auth_callback(request: Request, ticket: str = "", next: str = "/"):
    claims = unseal("viewer", ticket)
    now = time.time()
    for k in [k for k, t in _spent_tickets.items() if t < now]:
        del _spent_tickets[k]
    if not claims or claims.get("jti") in _spent_tickets:
        return templates.TemplateResponse(request, "login.html", {
            "error": "That sign-in expired or was already used — try again.", "next": _local(next)},
            status_code=401)
    _spent_tickets[claims.get("jti", "")] = float(claims.get("exp", now))
    resp = RedirectResponse(_local(next), status_code=303)
    set_person_cookie(resp, request, claims)
    return resp


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
    resp.delete_cookie(PERSON_COOKIE)
    return resp


# -- static assets -----------------------------------------------------------

@app.get("/static/{name:path}", include_in_schema=False)
def static_file(name: str):
    """app.css, app.js and the Pretendard font files.

    A route rather than a StaticFiles mount so the one thing that matters —
    nothing outside static/ is ever served — is written out here, where the
    gate's exception for /static/ can be checked against it."""
    f = (STATIC / name).resolve()
    if not f.is_relative_to(STATIC.resolve()) or not f.is_file():
        raise HTTPException(404)
    # The slim image's mimetypes does not know woff2 and would say
    # octet-stream, which some browsers refuse for a font.
    kind = "font/woff2" if f.suffix == ".woff2" else None
    return FileResponse(f, media_type=kind,
                        headers={"Cache-Control": "public, max-age=31536000, immutable"})


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


PARA_ORDER = ["projects", "areas", "resources", "archives"]


def para_rank(area: str) -> tuple[int, str]:
    """PARA order, then anything else by name. Ordering by count would move
    every entry around the sidebar as documents arrive."""
    return (PARA_ORDER.index(area) if area in PARA_ORDER else len(PARA_ORDER), area)


_rail_cache: tuple[float, dict] = (0.0, {})


def rail(max_age: float = 30.0) -> dict:
    """What the sidebar lists on every page: the repositories and the PARA
    areas, with counts. Every page draws it, so it is cached like meta()."""
    global _rail_cache
    ts, cached = _rail_cache
    if cached and time.time() - ts < max_age:
        return cached
    try:
        with pool.connection() as conn, conn.cursor() as cur:
            cur.execute("SELECT area, count(*) FROM docs WHERE deleted_at IS NULL"
                        " GROUP BY area")
            areas = sorted(cur.fetchall(), key=lambda r: para_rank(r[0]))
            # Most recently written first, as GitHub orders "Top repositories":
            # the scope someone is working in is the one they want next.
            cur.execute("SELECT owner, repo, count(*), max(updated_at) FROM docs"
                        " WHERE deleted_at IS NULL GROUP BY owner, repo"
                        " ORDER BY max(updated_at) DESC NULLS LAST, owner, repo")
            repos = [{"owner": o, "repo": r, "docs": n, "updated": u}
                     for o, r, n, u in cur.fetchall()]
    except Exception:
        return cached or {"areas": [], "repos": []}
    out = {"areas": areas, "repos": repos}
    _rail_cache = (time.time(), out)
    return out


def signed_in(request: Request) -> bool:
    """Whether this browser holds a session — the header shows a sign-out
    button only then."""
    return token_ok(request.cookies.get(SESSION_COOKIE, "")) or person(request) is not None


def signed_in_as(request: Request) -> str:
    """Who the header says is signed in: the Google email, or "operator" for
    the token session."""
    who = person(request)
    if who:
        return who.get("email", "")
    return "operator" if token_ok(request.cookies.get(SESSION_COOKIE, "")) else ""


def may_read(request: Request) -> bool:
    """Whether this caller would be let through the gate for a read. Pages the
    gate leaves open (/setup) use it to decide whether brain data — the
    sidebar's repository list, the footer's counts — may appear on them."""
    return PUBLIC_READS or authenticated(request)


def _display_zone():
    try:
        from zoneinfo import ZoneInfo
        return ZoneInfo(TZ)
    except Exception:
        return timezone.utc


def ago(value) -> str:
    """'3 hours ago', the way GitHub labels a time. The exact time goes in the
    element's title; this is for scanning a list."""
    if not value:
        return ""
    if isinstance(value, str):
        try:
            value = datetime.fromisoformat(value)
        except ValueError:
            return value
    if value.tzinfo is None:
        # A naive value was formatted from the database session, which runs
        # in ENGRAM_TZ (see _configure).
        value = value.replace(tzinfo=_display_zone())
    secs = (datetime.now(timezone.utc) - value).total_seconds()
    if secs < 45:
        return "just now"
    for unit, size in (("year", 31536000), ("month", 2592000), ("day", 86400),
                       ("hour", 3600), ("minute", 60)):
        n = int(secs // size)
        if n >= 1:
            if unit == "day" and n == 1:
                return "yesterday"
            return f"{n} {unit}{'' if n == 1 else 's'} ago"
    return "just now"


def stamp(value) -> str:
    """The full timestamp, for a title attribute."""
    if not value:
        return ""
    if isinstance(value, str):
        return value[:16].replace("T", " ")
    return value.strftime("%Y-%m-%d %H:%M")


def hue(name: str) -> int:
    """A stable colour slot (0-7) for an owner's letter avatar."""
    return int(hashlib.md5((name or "").encode()).hexdigest()[:4], 16) % 8


templates.env.globals.update(rail=rail, signed_in=signed_in, signed_in_as=signed_in_as,
                             google_login=GOOGLE_LOGIN, para_rank=para_rank)
templates.env.filters.update(ago=ago, stamp=stamp, hue=hue)


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


# Code — fenced blocks and inline spans — where a [[name]] is literal text,
# as it is on GitHub.
_CODE = re.compile(r"(^```.*?^```[^\n]*$|^~~~.*?^~~~[^\n]*$|`[^`\n]+`)", re.S | re.M)
_TASK = re.compile(r"<li>(<p>)?\[([ xX])\] ")
_BROKEN = re.compile("(\\d+)")


def render_markdown(body: str, resolve: dict[str, str]) -> str:
    broken: list[str] = []

    def sub(m: re.Match) -> str:
        name, label = m.group(1).strip(), (m.group(2) or "").strip()
        target = resolve.get(name.split("/")[-1]) or resolve.get(name)
        text = label or name
        if not target:
            # A broken link is never quietly turned into plain text. What is
            # visible is what gets fixed. The renderer escapes raw HTML, so the
            # span goes in as a private-use marker and is swapped in after.
            broken.append(text)
            return f"{len(broken) - 1}"
        return f"[{text}](/doc/{target})"

    parts = _CODE.split(body)
    body = "".join(p if i % 2 else _WIKI.sub(sub, p) for i, p in enumerate(parts))
    out = _BROKEN.sub(lambda m: ("<span class='broken' title='no such document'>"
                                 f"{html.escape(broken[int(m.group(1))])}</span>"),
                      md.render(body))
    # Task lists, drawn as GitHub draws them: a disabled checkbox, not "[x]".
    return _TASK.sub(lambda m: (f"<li class='task'>{m.group(1) or ''}"
                                f"<input type='checkbox' disabled"
                                f"{' checked' if m.group(2) != ' ' else ''}> "), out)


_HEAD = re.compile(r"<h([1-6])>(.*?)</h\1>", re.S)


def slugify(text: str) -> str:
    """GitHub's heading anchors: lower case, punctuation dropped, spaces to
    hyphens. Hangul and other letters survive, so a Korean heading keeps a
    readable anchor."""
    s = re.sub(r"[^\w\- ]", "", text.strip().lower())
    return re.sub(r" ", "-", s) or "section"


def with_toc(body_html: str) -> tuple[str, list[dict]]:
    """Give every heading an id and a hover anchor, and return the h2/h3
    outline for the sidebar. Repeated headings get -1, -2 suffixes, as on
    GitHub, so each one is still addressable."""
    toc: list[dict] = []
    seen: dict[str, int] = {}

    def sub(m: re.Match) -> str:
        level, inner = int(m.group(1)), m.group(2)
        text = html.unescape(re.sub(r"<[^>]+>", "", inner))
        base = slugify(text)
        n = seen.get(base, 0)
        seen[base] = n + 1
        hid = base if n == 0 else f"{base}-{n}"
        if level in (2, 3):
            toc.append({"id": hid, "level": level, "text": text})
        return (f'<h{level} id="{hid}"><a class="anchor" href="#{hid}" aria-hidden="true">'
                f'#</a>{inner}</h{level}>')
    return _HEAD.sub(sub, body_html), toc


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


# The session a call belongs to, as the client declared it. The MCP server
# mints one per process (one editor session); the CLI passes ENGRAM_SESSION
# when it has one. Nothing is inferred from addresses or timing: a session is
# a claim the client makes, and an unclaimed call is simply unattributed.
SESSION_HEADER = "x-engram-session"


def session_of(request: Request) -> str:
    return request.headers.get(SESSION_HEADER, "")[:64]


def log_call(request: Request, tool: str, ref: str, payload, author: str = "") -> None:
    """Record what a call cost (usage.py). Never fails the call it records."""
    try:
        with pool.connection() as conn:
            usage.log_call(conn, session_of(request), tool, ref, payload, author)
    except Exception as e:
        print(f"[usage] could not log the call: {e}")


def log_search(conn, q: str, author: str, tier: int, hits: list[dict],
               session: str = "") -> int | None:
    """Record the search for feedback to attach to. A failure here must never
    fail the search — the log is a means, the answer is the end — so it is
    reported and swallowed, and the caller gets no search_id."""
    try:
        return fb.log_search(conn, q, query_lexemes(q), author, tier, hits, session)
    except Exception as e:
        try:
            conn.rollback()
        except Exception:
            pass
        print(f"[feedback] could not log the search: {e}")
        return None


@app.get("/api/search")
def api_search(request: Request,
               q: str = Query(..., min_length=1),
               limit: int | None = Query(None, ge=1, le=50),
               archives: bool | None = None,
               tier: int | None = Query(None, ge=1, le=3,
                                        description="the token budget: 1 snippets, 2 full chunks, 3 wide"),
               view: str = Query("", pattern="^(full|compact)?$",
                                 description="compact drops the envelope; default compact with a tier, full without"),
               author: str = Query("", description="who is asking — recorded with the search, so a vote can be attributed"),
               repo: str = Query("", description="lift this repo (without excluding others)"),
               only_repo: list[str] = Query([], description="restrict to these repos"),
               only_owner: list[str] = Query([], description="restrict to these groups")) -> dict:
    """`repo` is a BONUS, `only_*` are FILTERS. Keeping them separate is the
    point — asking from one repo must not make another repo's existing answer
    disappear.

    With `tier` the tier's budget applies (search.TIERS) and the hits come back
    compact: path, heading_path, score and a snippet (tier 2: the chunk body).
    Without it the call is what it always was — six full hits — so a client
    that predates tiers sees nothing change. Every call is logged and answers
    with a `search_id`, the handle a later vote names.
    """
    m = meta()
    index = {"updated_at": m.get("updated_at", ""),
             "docs": int(m.get("docs", 0) or 0),
             "chunks": int(m.get("chunks", 0) or 0)}
    compact = view == "compact" or (not view and tier is not None)
    with pool.connection() as conn:
        if tier is None:
            hits = search(q, limit=limit or 6, include_archives=bool(archives), conn=conn,
                          boost_repo=repo or None, only_repos=list(only_repo) or None,
                          only_owners=list(only_owner) or None)
            as_seen = [h.as_dict() for h in hits]
            sid = log_search(conn, q, author, 0, as_seen, session_of(request))
            payload = [h.compact(q, 0) for h in hits] if compact else as_seen
            result = {"q": q, "search_id": sid, "count": len(hits), "hits": payload,
                      "index": index}
            log_call(request, "search", q, result, author)
            return result
        res = search_tier(q, tier, conn=conn, boost_repo=repo or None,
                          only_repos=list(only_repo) or None,
                          only_owners=list(only_owner) or None,
                          limit=limit, include_archives=archives)
        sid = log_search(conn, q, author, tier, [h.as_dict() for h in res.hits],
                         session_of(request))
    payload = res.payload(q) if compact else [h.as_dict() for h in res.hits]
    result = {"q": q, "tier": tier, "search_id": sid, "count": len(res.hits),
              "candidates": res.candidates, "hits": payload, "next": res.next,
              "index": index}
    log_call(request, "search", q, result, author)
    return result


@app.get("/api/doc/{path:path}")
def api_doc(path: str, request: Request,
            search: int | None = Query(None, description="the search_id that led here"),
            author: str = Query("")) -> dict:
    """One document. Naming the `search` that led here records an implicit
    `opened` vote (feedback.py): the reader did not vote, but it did read, and
    that is the one signal that costs nobody a call."""
    d = fetch_doc(path)
    if not d:
        raise HTTPException(404, f"no such document: {path}")
    if search is not None:
        try:
            with pool.connection() as conn:
                if fb.search_exists(conn, search):
                    chunk = fb.chunk_from_search(conn, search, d["id"])
                    fb.record(conn, d["id"], "opened", author=author, search_id=search,
                              chunk_id=chunk)
                    d["feedback"] = fb.summary(conn, d["id"])
        except Exception as e:
            print(f"[feedback] could not record the open: {e}")
    log_call(request, "doc", path, d, author)
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


@app.post("/api/feedback")
async def api_feedback(request: Request, _: None = Depends(require_auth)) -> dict:
    """Vote on documents: `kind` useful | noise | opened, for one `path` or a
    list of `paths`. `search_id` (from the search that showed them) is what
    makes a vote teach the ranking about the QUESTION, not only the document —
    send it whenever there is one. A vote of a kind a person has already cast
    on a document today answers `already` rather than counting twice.
    """
    payload = await request.json()
    kind = (payload.get("kind") or "").strip()
    if kind not in fb.KINDS:
        raise HTTPException(400, f"kind must be one of {sorted(fb.KINDS)}")
    paths = payload.get("paths")
    if not paths:
        paths = [payload.get("path")] if payload.get("path") else []
    paths = [p.strip() for p in paths if isinstance(p, str) and p.strip()]
    if not paths:
        raise HTTPException(400, "path (or paths) is empty — say which document the vote is about")
    author = (payload.get("author") or "").strip()
    note = (payload.get("note") or "").strip()
    search_id = payload.get("search_id")
    results = []
    with pool.connection() as conn:
        if search_id is not None and not fb.search_exists(conn, int(search_id)):
            # An unknown search is not an error worth failing a vote over, but
            # it is worth saying: the vote lands on the document alone.
            search_id = None
            results.append({"warning": "search_id unknown — votes recorded without it"})
        with conn.cursor() as cur:
            for p in paths:
                cur.execute("SELECT id FROM docs WHERE path = %s AND deleted_at IS NULL", (p,))
                row = cur.fetchone()
                if not row:
                    results.append({"path": p, "status": "no such document"})
                    continue
                doc_id = row[0]
                chunk = fb.chunk_from_search(conn, int(search_id), doc_id) if search_id else None
                status = fb.record(conn, doc_id, kind, author=author, note=note,
                                   search_id=int(search_id) if search_id else None,
                                   chunk_id=chunk)
                results.append({"path": p, "status": status, **fb.summary(conn, doc_id)})
    result = {"kind": kind, "search_id": search_id, "results": results}
    log_call(request, "feedback", ", ".join(paths), result, author)
    return result


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
            result = write_doc(conn, path, body, author=payload.get("author", ""),
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
    # What a put costs is the body that was sent, not the receipt.
    log_call(request, "put", path, body, payload.get("author", ""))
    return result


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
    log_call(request, "patch", path, edits, payload.get("author", ""))
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
            result = move_doc(conn, path, to, author=payload.get("author", ""))
    except PathRejected as e:
        raise HTTPException(400, str(e))
    except ScopeDenied as e:
        raise HTTPException(403, str(e))
    log_call(request, "move", path + " -> " + to, result, payload.get("author", ""))
    return result


@app.post("/api/doc/{path:path}/restore")
def api_restore_doc(path: str, author: str = "",
                    _: None = Depends(require_auth)) -> dict:
    with pool.connection() as conn:
        return restore_doc(conn, path, author=author)


@app.get("/api/revisions/{path:path}")
def api_revisions(path: str, request: Request, limit: int = Query(20, ge=1, le=200)) -> dict:
    """How this document has changed. The slot git log used to fill."""
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT id, created_at, author, note, sha256, length(body)"
                    " FROM revisions WHERE path = %s"
                    " ORDER BY created_at DESC LIMIT %s", (path, limit))
        rows = cur.fetchall()
    result = {"path": path, "count": len(rows), "revisions": [
        {"id": r[0], "at": r[1].isoformat(), "author": r[2], "note": r[3],
         "sha256": r[4][:12], "chars": r[5]} for r in rows]}
    log_call(request, "revisions", path, result)
    return result


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
def api_integrity(request: Request, limit: int = Query(50, ge=1, le=500)) -> dict:
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
    result = {"broken_links": broken, "orphans": orphans, "weak_nodes": weak,
              "truncated": len(broken) >= limit or len(orphans) >= limit,
              "counts": {"broken": n_broken, "orphans": n_orphan,
                         "weak": n_weak, "by_kind": by_kind}}
    log_call(request, "integrity", "", result)
    return result


@app.get("/api/usage")
def api_usage(days: int = Query(7, ge=1, le=365),
              session: str = Query("", description="one session only"),
              limit: int = Query(30, ge=1, le=500)) -> dict:
    """What sessions cost, and whether the tiers paid off — see usage.py.
    Read-only and never fed back into a session by itself."""
    with pool.connection() as conn:
        return usage.report(conn, days=days, session=session or None, limit=limit)


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
        votes = fb.summary(conn, doc_id)
    return dict(id=doc_id, path=row[1], title=row[2], area=row[3],
                updated_at=row[4].strftime("%Y-%m-%d %H:%M") if row[4] else None,
                chars=row[5], body=row[6], owner=row[7], repo=row[8],
                # The hash a partial write sends back as base_sha256. Handing it
                # out with the body is what lets a caller prove it edited the
                # version it actually read.
                sha256=row[9],
                outgoing=outgoing, backlinks=backlinks, revisions=revs,
                # What the document has earned in searches. Shown so a person
                # can see why it ranks where it does.
                feedback=votes)


def recent_changes(limit: int) -> list[dict]:
    """Every recent write — document updates and revisions on one timeline.

    A revision row is a change: its author and note say who changed the
    document and why, and its body is the document as it stood just BEFORE
    that change. A document row is the document's latest state."""
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("""
            SELECT at, kind, path, title, note, author, rev_id, owner, repo, area FROM (
              SELECT d.updated_at AS at, 'doc' AS kind, d.path, d.title,
                     '' AS note, '' AS author, NULL::bigint AS rev_id,
                     d.owner, d.repo, d.area
              FROM docs d WHERE d.deleted_at IS NULL
              UNION ALL
              SELECT r.created_at, 'rev', r.path,
                     COALESCE(d.title, r.path), r.note, r.author, r.id,
                     COALESCE(d.owner, ''), COALESCE(d.repo, ''), COALESCE(d.area, '')
              FROM revisions r LEFT JOIN docs d ON d.id = r.doc_id
            ) x WHERE at IS NOT NULL ORDER BY at DESC LIMIT %s""", (limit,))
        return [{"dt": r[0], "at": r[0].strftime("%Y-%m-%d %H:%M"),
                 "day": r[0].strftime("%Y-%m-%d"), "kind": r[1], "path": r[2],
                 "title": r[3], "note": r[4], "author": r[5], "rev_id": r[6],
                 "owner": r[7], "repo": r[8], "area": r[9]}
                for r in cur.fetchall()]


def page(request: Request, name: str, ctx: dict, status_code: int = 200):
    """Render a viewer page. Every page gets the same frame variables, so the
    header and sidebar never depend on which route happened to render them."""
    base = {"q": "", "nav": "", "show_brain": True}
    base.update(ctx)
    if "meta" not in base:
        base["meta"] = meta()
    return templates.TemplateResponse(request, name, base, status_code=status_code)


@app.get("/", response_class=HTMLResponse)
def home(request: Request):
    """The dashboard: how big the brain is, what changed, and whether it is
    being used — the three questions someone opening it usually has."""
    feed = recent_changes(15)
    week = None
    with pool.connection() as conn:
        try:
            week = usage.report(conn, days=7)
        except Exception as e:           # a side panel; never fail the page for it
            conn.rollback()
            print(f"[home] usage report failed: {e}")
    r = rail()
    return page(request, "home.html", {
        "feed": feed, "week": week, "areas": r["areas"], "repos": r["repos"],
        "nav": "home"})


@app.get("/search", response_class=HTMLResponse)
def search_page(request: Request, q: str = "", archives: bool = False,
                only_repo: list[str] = Query([]), limit: int = Query(20, ge=1, le=50)):
    hits, sid = [], None
    if q.strip():
        with pool.connection() as conn:
            hits = search(q, limit=limit, include_archives=archives, conn=conn,
                          only_repos=list(only_repo) or None)
            # Logged like an agent's search, so a person's vote on this page
            # teaches the ranking the same way.
            sid = log_search(conn, q, "viewer", 0, [h.as_dict() for h in hits])
    # The filter pane lists repository NAMES: that is what only_repo filters
    # on, and two owners' repos of one name are one choice there.
    names: dict[str, int] = {}
    for rp in rail()["repos"]:
        names[rp["repo"]] = names.get(rp["repo"], 0) + rp["docs"]
    return page(request, "search.html", {
        "q": q, "hits": hits, "archives": archives, "only": only_repo,
        "repo_names": sorted(names.items(), key=lambda kv: (-kv[1], kv[0])),
        "search_id": sid, "highlight": highlight, "nav": "search"})


@app.get("/rev/{rev_id}", response_class=HTMLResponse)
def rev_page(request: Request, rev_id: int):
    """One revision's body AS IT WAS. Deciding whether to roll back means
    actually reading it."""
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT r.id, r.path, r.body, r.created_at, r.author, r.note,"
                    " COALESCE(d.owner, ''), COALESCE(d.repo, ''), COALESCE(d.area, '')"
                    " FROM revisions r LEFT JOIN docs d ON d.id = r.doc_id"
                    " WHERE r.id = %s", (rev_id,))
        r = cur.fetchone()
    if not r:
        raise HTTPException(404, f"no such revision: {rev_id}")
    d = {"id": r[0], "path": r[1], "title": f"revision {r[0]}",
         "area": r[8], "owner": r[6], "repo": r[7],
         "updated_at": r[3].strftime("%Y-%m-%d %H:%M"), "chars": len(r[2]),
         "body": r[2], "author": r[4], "note": r[5],
         "outgoing": [], "backlinks": [], "revisions": [], "is_revision": True}
    d["html"], d["toc"] = with_toc(render_markdown(r[2], path_by_stem()))
    return page(request, "doc.html", {"doc": d, "nav": "changes"})


@app.get("/changes", response_class=HTMLResponse)
def changes_page(request: Request, limit: int = Query(80, ge=1, le=300)):
    """Every recent write in the store, as a timeline grouped by day."""
    rows = recent_changes(limit)
    days: list[tuple[str, list]] = []
    for r in rows:
        if not days or days[-1][0] != r["day"]:
            days.append((r["day"], []))
        days[-1][1].append(r)
    return page(request, "changes.html", {"days": days, "count": len(rows),
                                          "limit": limit, "nav": "changes"})


def build_tree(rows: list[dict], strip: str) -> dict:
    """Nest documents by the folders in their paths, below `strip` (the
    ``owner/repo/`` prefix). The address already IS a tree; drawing it as one
    is what makes a repository's shape visible at a glance."""
    root = {"name": "", "dirs": {}, "files": [], "count": 0}
    for d in rows:
        rel = d["path"][len(strip):] if strip and d["path"].startswith(strip) else d["path"]
        parts = rel.split("/")
        node = root
        node["count"] += 1
        for part in parts[:-1]:
            node = node["dirs"].setdefault(
                part, {"name": part, "dirs": {}, "files": [], "count": 0})
            node["count"] += 1
        node["files"].append(dict(d, fname=parts[-1]))

    def finish(node: dict, depth: int) -> dict:
        dirs = sorted(node["dirs"].values(),
                      key=lambda n: para_rank(n["name"]) if depth == 0 else (0, n["name"]))
        return {"name": node["name"], "count": node["count"], "depth": depth,
                "dirs": [finish(n, depth + 1) for n in dirs],
                "files": sorted(node["files"], key=lambda f: f["fname"])}
    return finish(root, 0)


@app.get("/browse", response_class=HTMLResponse)
def browse_page(request: Request, owner: str = Query(""), repo: str = Query(""),
                area: str = Query("")):
    """Every document in one scope, by path — no ranking involved.

    The question here is "what is in this repo", and the path is already the
    answer; a ranking over it only hides the shape of the scope. So this does
    not go through search, and it is not logged as a call — paging through a
    list is not a question, and counting it as one would blur the numbers
    /usage exists to show.

    Three shapes, by what is named:
    - nothing, or only an owner: the repository index;
    - a repo (optionally an area tab): that repository as a file tree, with
      its hub README rendered underneath, as GitHub shows a repository;
    - an area alone: that PARA area across every repository.
    """
    if repo and not owner:
        # A repo named without its owner. When the name is unambiguous, go to
        # the one repository it means, so the page has its full address.
        owners = {r["owner"] for r in rail()["repos"] if r["repo"] == repo}
        if len(owners) == 1:
            qs = urllib.parse.urlencode({"owner": owners.pop(), "repo": repo,
                                         **({"area": area} if area else {})})
            return RedirectResponse(f"/browse?{qs}", status_code=307)

    if not repo and not area:
        repos = [r for r in rail()["repos"] if not owner or r["owner"] == owner]
        with pool.connection() as conn, conn.cursor() as cur:
            cur.execute("SELECT owner, repo, area, count(*) FROM docs WHERE deleted_at IS NULL"
                        " GROUP BY owner, repo, area")
            split: dict[tuple, list] = {}
            for o, rp, a, n in cur.fetchall():
                split.setdefault((o, rp), []).append((a, n))
        # dict(r, ...) copies: the rows belong to the cached rail.
        rows = [dict(r, areas=sorted(split.get((r["owner"], r["repo"]), []),
                                     key=lambda t: para_rank(t[0])))
                for r in repos]
        by_owner: dict[str, list] = {}
        for r in sorted(rows, key=lambda r: r["owner"]):
            by_owner.setdefault(r["owner"], []).append(r)
        return page(request, "browse.html", {
            "mode": "index", "owner": owner, "repo": "", "area": "",
            "by_owner": list(by_owner.items()),
            "total": sum(r["docs"] for r in rows), "nav": "browse"})

    where, params = ["deleted_at IS NULL"], {}
    for col, val in (("owner", owner), ("repo", repo), ("area", area)):
        if val:
            where.append(f"{col} = %({col})s")
            params[col] = val
    tabs, readme, latest = [], None, None
    with pool.connection() as conn, conn.cursor() as cur:
        cur.execute("SELECT owner, repo, area, path, title, updated_at FROM docs"
                    " WHERE " + " AND ".join(where) + " ORDER BY path", params)
        rows = [{"owner": o, "repo": rp, "area": a, "path": p, "title": t or p,
                 "updated": u}
                for o, rp, a, p, t, u in cur.fetchall()]
        if repo:
            scope = {"owner": owner, "repo": repo}
            by_owner_sql = " AND d.owner = %(owner)s" if owner else ""
            # Tab counts are for the whole repository, so choosing one tab
            # does not make the others' numbers disappear.
            cur.execute("SELECT d.area, count(*) FROM docs d WHERE d.deleted_at IS NULL"
                        " AND d.repo = %(repo)s" + by_owner_sql + " GROUP BY d.area", scope)
            tabs = sorted(cur.fetchall(), key=lambda t: para_rank(t[0] or "root"))
            if owner:
                cur.execute("SELECT body FROM docs WHERE path = %s AND deleted_at IS NULL",
                            (f"{owner}/{repo}/README.md",))
                hit = cur.fetchone()
                readme = hit[0] if hit else None
            # The newest change in the repository — GitHub's latest-commit bar.
            cur.execute("SELECT r.author, r.note, r.created_at, d.title, d.path"
                        " FROM revisions r JOIN docs d ON d.id = r.doc_id"
                        " WHERE d.deleted_at IS NULL AND d.repo = %(repo)s" + by_owner_sql +
                        " ORDER BY r.created_at DESC LIMIT 1", scope)
            hit = cur.fetchone()
            if hit:
                latest = {"author": hit[0], "note": hit[1], "at": hit[2],
                          "title": hit[3], "path": hit[4]}

    readme_html = with_toc(render_markdown(readme, path_by_stem()))[0] if readme else None
    if repo:
        groups = [((owner, repo), build_tree(rows, f"{owner}/{repo}/" if owner else ""))]
    else:
        # An area across repositories: one box per repository.
        per: dict[tuple, list] = {}
        for d in rows:
            per.setdefault((d["owner"], d["repo"]), []).append(d)
        groups = [(k, build_tree(v, f"{k[0]}/{k[1]}/")) for k, v in per.items()]
    return page(request, "browse.html", {
        "mode": "repo" if repo else "area", "owner": owner, "repo": repo, "area": area,
        "groups": groups, "tabs": tabs, "total": len(rows), "readme_html": readme_html,
        "latest": latest, "nav": "browse"})


@app.get("/usage", response_class=HTMLResponse)
def usage_page(request: Request, days: int = Query(7, ge=1, le=365),
               session: str = Query(""), tab: str = Query("activity")):
    """Two tabs over one log. `activity` asks whether the brain is used at all
    and where that is going; `sessions` is the ledger — what each session cost.

    Naming a session pins the ledger, so it forces that tab: the link that got
    the reader here is a row in it, and answering with a chart of every session
    would be answering a question they did not ask.
    """
    if tab not in ("activity", "sessions"):
        tab = "activity"
    if session:
        tab = "sessions"
    with pool.connection() as conn:
        rep = usage.report(conn, days=days, session=session or None)
        act = usage.activity(conn) if tab == "activity" else {"empty": True}
    return page(request, "usage.html", {"nav": "usage", "u": rep, "act": act, "tab": tab,
                                        "days": days, "session": session})


@app.get("/setup", response_class=HTMLResponse)
def setup_page(request: Request):
    """How a machine connects to this brain: remote MCP, OAuth, the plugin.

    Open to everyone (see UNAUTHENTICATED_PATHS) because it is read before the
    reader can authenticate. It carries no brain data of its own, and the
    frame's sidebar and counts appear only for a caller who could read them
    anyway."""
    show = may_read(request)
    return page(request, "setup.html", {
        "nav": "setup", "show_brain": show, "meta": meta() if show else {},
        "mcp_url": MCP_URL or MCP_URL_PLACEHOLDER, "mcp_url_set": bool(MCP_URL)})


@app.get("/doc/{path:path}", response_class=HTMLResponse)
def doc_page(request: Request, path: str):
    d = fetch_doc(path)
    if not d:
        raise HTTPException(404, f"no such document: {path}")
    d["html"], d["toc"] = with_toc(render_markdown(d["body"], path_by_stem()))
    return page(request, "doc.html", {"doc": d, "nav": "doc"})
